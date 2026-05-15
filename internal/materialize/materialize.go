// Package materialize runs post-crawl materialization of aggregated views
// into denormalized tables for efficient querying.
package materialize

import (
	"fmt"
	"strconv"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

// Materialize runs post-crawl materialization for a completed job.
func Materialize(db *storage.DB, jobID string) error {
	if err := materializeCanonicalClusters(db, jobID); err != nil {
		return fmt.Errorf("materializing canonical clusters for job %q: %w", jobID, err)
	}
	if err := materializeDuplicateClusters(db, jobID); err != nil {
		return fmt.Errorf("materializing duplicate clusters for job %q: %w", jobID, err)
	}
	return nil
}

func materializeCanonicalClusters(db *storage.DB, jobID string) error {
	// Clear existing clusters for this job
	if _, err := db.Exec(`DELETE FROM canonical_cluster_members WHERE job_id = ?`, jobID); err != nil {
		return fmt.Errorf("clearing canonical cluster members: %w", err)
	}
	if _, err := db.Exec(`DELETE FROM canonical_clusters WHERE job_id = ?`, jobID); err != nil {
		return fmt.Errorf("clearing canonical clusters: %w", err)
	}

	// Find groups of pages sharing the same canonical URL
	rows, err := db.Query(`
		SELECT p.canonical_url, COUNT(*) as cnt, GROUP_CONCAT(p.url_id) as url_ids
		FROM pages p
		WHERE p.job_id = ? AND p.canonical_url IS NOT NULL AND p.canonical_url != ''
		GROUP BY p.canonical_url
		HAVING cnt >= 2
	`, jobID)
	if err != nil {
		return fmt.Errorf("querying canonical groups: %w", err)
	}
	defer rows.Close()

	type cluster struct {
		canonicalURL string
		count        int
		urlIDs       string
	}
	clusters := []cluster{}
	for rows.Next() {
		var c cluster
		if err := rows.Scan(&c.canonicalURL, &c.count, &c.urlIDs); err != nil {
			return fmt.Errorf("scanning canonical group: %w", err)
		}
		clusters = append(clusters, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, c := range clusters {
		// Check if the canonical URL itself is one of the members (self-referencing)
		var isSelfRef int
		db.QueryRow(`SELECT COUNT(*) FROM urls WHERE job_id = ? AND normalized_url = ?`,
			jobID, c.canonicalURL).Scan(&isSelfRef)

		// Get target status code if crawled
		var targetStatusCode *int
		var sc int
		err := db.QueryRow(`
			SELECT f.status_code FROM urls u
			JOIN fetches f ON f.requested_url_id = u.id AND f.job_id = u.job_id
			WHERE u.job_id = ? AND u.normalized_url = ? AND f.status_code IS NOT NULL
			ORDER BY f.fetch_seq DESC LIMIT 1
		`, jobID, c.canonicalURL).Scan(&sc)
		if err == nil {
			targetStatusCode = &sc
		}

		result, err := db.Exec(`
			INSERT INTO canonical_clusters (job_id, cluster_url, member_count, target_status_code, is_self_referencing)
			VALUES (?, ?, ?, ?, ?)
		`, jobID, c.canonicalURL, c.count, targetStatusCode, isSelfRef > 0)
		if err != nil {
			return fmt.Errorf("inserting canonical cluster for %q: %w", c.canonicalURL, err)
		}

		clusterID, _ := result.LastInsertId()

		// Insert members
		urlIDs := splitIDs(c.urlIDs)
		for _, urlID := range urlIDs {
			_, err := db.Exec(`INSERT INTO canonical_cluster_members (cluster_id, url_id, job_id) VALUES (?, ?, ?)`,
				clusterID, urlID, jobID)
			if err != nil {
				return fmt.Errorf("inserting canonical cluster member: %w", err)
			}
		}
	}
	return nil
}

func materializeDuplicateClusters(db *storage.DB, jobID string) error {
	// Clear existing clusters for this job
	if _, err := db.Exec(`DELETE FROM duplicate_cluster_members WHERE job_id = ?`, jobID); err != nil {
		return fmt.Errorf("clearing duplicate cluster members: %w", err)
	}
	if _, err := db.Exec(`DELETE FROM duplicate_clusters WHERE job_id = ?`, jobID); err != nil {
		return fmt.Errorf("clearing duplicate clusters: %w", err)
	}

	// Three types of duplicate detection
	types := []struct {
		clusterType string
		query       string
	}{
		{
			clusterType: "content",
			query: `
				SELECT p.content_hash, GROUP_CONCAT(p.url_id), GROUP_CONCAT(f.fetch_seq), COUNT(*)
				FROM pages p
				JOIN fetches f ON f.id = p.fetch_id
				WHERE p.job_id = ? AND p.content_hash IS NOT NULL AND p.content_hash != ''
				GROUP BY p.content_hash HAVING COUNT(*) > 1
			`,
		},
		{
			clusterType: "title",
			query: `
				SELECT p.title, GROUP_CONCAT(p.url_id), GROUP_CONCAT(f.fetch_seq), COUNT(*)
				FROM pages p
				JOIN fetches f ON f.id = p.fetch_id
				WHERE p.job_id = ? AND p.title IS NOT NULL AND p.title != ''
				GROUP BY p.title HAVING COUNT(*) > 1
			`,
		},
		{
			clusterType: "description",
			query: `
				SELECT p.meta_description, GROUP_CONCAT(p.url_id), GROUP_CONCAT(f.fetch_seq), COUNT(*)
				FROM pages p
				JOIN fetches f ON f.id = p.fetch_id
				WHERE p.job_id = ? AND p.meta_description IS NOT NULL AND p.meta_description != ''
				GROUP BY p.meta_description HAVING COUNT(*) > 1
			`,
		},
	}

	for _, dt := range types {
		rows, err := db.Query(dt.query, jobID)
		if err != nil {
			return fmt.Errorf("querying duplicate %s: %w", dt.clusterType, err)
		}

		type dupGroup struct {
			hashValue string
			urlIDs    string
			fetchSeqs string
			count     int
		}
		groups := []dupGroup{}
		for rows.Next() {
			var g dupGroup
			if err := rows.Scan(&g.hashValue, &g.urlIDs, &g.fetchSeqs, &g.count); err != nil {
				rows.Close()
				return fmt.Errorf("scanning duplicate %s: %w", dt.clusterType, err)
			}
			groups = append(groups, g)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, g := range groups {
			urlIDs := splitIDs(g.urlIDs)
			fetchSeqs := splitIDs(g.fetchSeqs)

			// First URL by fetch_seq is the canonical
			firstIdx := 0
			minSeq := int64(1<<63 - 1)
			for i, seq := range fetchSeqs {
				if seq < minSeq {
					minSeq = seq
					firstIdx = i
				}
			}

			result, err := db.Exec(`
				INSERT INTO duplicate_clusters (job_id, cluster_type, hash_value, first_url_id, member_count)
				VALUES (?, ?, ?, ?, ?)
			`, jobID, dt.clusterType, g.hashValue, urlIDs[firstIdx], g.count)
			if err != nil {
				return fmt.Errorf("inserting duplicate cluster: %w", err)
			}

			clusterID, _ := result.LastInsertId()

			for i, urlID := range urlIDs {
				var fetchSeq int64
				if i < len(fetchSeqs) {
					fetchSeq = fetchSeqs[i]
				}
				_, err := db.Exec(`INSERT INTO duplicate_cluster_members (cluster_id, url_id, job_id, fetch_seq) VALUES (?, ?, ?, ?)`,
					clusterID, urlID, jobID, fetchSeq)
				if err != nil {
					return fmt.Errorf("inserting duplicate cluster member: %w", err)
				}
			}
		}
	}
	return nil
}

// splitIDs splits a comma-separated string of integers into a slice.
// Preserves slice length on parse failure (appends 0) to keep parallel
// slices like urlIDs/fetchSeqs aligned.
func splitIDs(s string) []int64 {
	parts := splitComma(s)
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		id, _ := strconv.ParseInt(p, 10, 64)
		ids = append(ids, id)
	}
	return ids
}

func splitComma(s string) []string {
	if s == "" {
		return nil
	}
	result := []string{}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	return result
}
