package materialize

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

func testDB(t *testing.T) *storage.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", dbPath, err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbPath)
	})
	return db
}

func seedJob(t *testing.T, db *storage.DB, jobID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO crawl_jobs (id, status) VALUES (?, 'completed')`, jobID)
	if err != nil {
		t.Fatalf("seeding job: %v", err)
	}
}

func seedURL(t *testing.T, db *storage.DB, jobID, normalizedURL, host string) int64 {
	t.Helper()
	result, err := db.Exec(`INSERT INTO urls (job_id, normalized_url, host, status, discovered_via) VALUES (?, ?, ?, 'fetched', 'seed')`,
		jobID, normalizedURL, host)
	if err != nil {
		t.Fatalf("seeding URL %q: %v", normalizedURL, err)
	}
	id, _ := result.LastInsertId()
	return id
}

func seedFetch(t *testing.T, db *storage.DB, jobID string, fetchSeq int, urlID int64) int64 {
	t.Helper()
	result, err := db.Exec(`INSERT INTO fetches (job_id, fetch_seq, requested_url_id, status_code) VALUES (?, ?, ?, 200)`,
		jobID, fetchSeq, urlID)
	if err != nil {
		t.Fatalf("seeding fetch: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func TestMaterializeCanonicalClusters(t *testing.T) {
	db := testDB(t)
	jobID := "job-canon-cluster"
	seedJob(t, db, jobID)

	canonical := "https://example.com/canonical"

	u1 := seedURL(t, db, jobID, "https://example.com/a", "example.com")
	u2 := seedURL(t, db, jobID, "https://example.com/b", "example.com")
	u3 := seedURL(t, db, jobID, "https://example.com/c", "example.com")

	f1 := seedFetch(t, db, jobID, 1, u1)
	f2 := seedFetch(t, db, jobID, 2, u2)
	f3 := seedFetch(t, db, jobID, 3, u3)

	// All three pages point to the same canonical
	for _, pair := range []struct {
		urlID   int64
		fetchID int64
	}{{u1, f1}, {u2, f2}, {u3, f3}} {
		_, err := db.Exec(`INSERT INTO pages (job_id, url_id, fetch_id, depth, canonical_url) VALUES (?, ?, ?, 1, ?)`,
			jobID, pair.urlID, pair.fetchID, canonical)
		if err != nil {
			t.Fatalf("seeding page: %v", err)
		}
	}

	if err := Materialize(db, jobID); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	// Verify cluster
	var clusterCount int
	db.QueryRow(`SELECT COUNT(*) FROM canonical_clusters WHERE job_id = ?`, jobID).Scan(&clusterCount)
	if clusterCount != 1 {
		t.Errorf("expected 1 canonical cluster, got %d", clusterCount)
	}

	var memberCount int
	db.QueryRow(`SELECT member_count FROM canonical_clusters WHERE job_id = ?`, jobID).Scan(&memberCount)
	if memberCount != 3 {
		t.Errorf("expected 3 members in cluster, got %d", memberCount)
	}

	var actualMembers int
	db.QueryRow(`SELECT COUNT(*) FROM canonical_cluster_members WHERE job_id = ?`, jobID).Scan(&actualMembers)
	if actualMembers != 3 {
		t.Errorf("expected 3 cluster member rows, got %d", actualMembers)
	}
}

func TestMaterializeDuplicateClusters(t *testing.T) {
	db := testDB(t)
	jobID := "job-dup-cluster"
	seedJob(t, db, jobID)

	u1 := seedURL(t, db, jobID, "https://example.com/a", "example.com")
	u2 := seedURL(t, db, jobID, "https://example.com/b", "example.com")

	f1 := seedFetch(t, db, jobID, 1, u1)
	f2 := seedFetch(t, db, jobID, 2, u2)

	// Two pages with the same content_hash
	hash := "abc123"
	_, err := db.Exec(`INSERT INTO pages (job_id, url_id, fetch_id, depth, content_hash, title, meta_description) VALUES (?, ?, ?, 1, ?, 'Unique 1', 'Desc 1')`,
		jobID, u1, f1, hash)
	if err != nil {
		t.Fatalf("seeding page 1: %v", err)
	}
	_, err = db.Exec(`INSERT INTO pages (job_id, url_id, fetch_id, depth, content_hash, title, meta_description) VALUES (?, ?, ?, 1, ?, 'Unique 2', 'Desc 2')`,
		jobID, u2, f2, hash)
	if err != nil {
		t.Fatalf("seeding page 2: %v", err)
	}

	if err := Materialize(db, jobID); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	// Verify: should have 1 content duplicate cluster
	var contentClusters int
	db.QueryRow(`SELECT COUNT(*) FROM duplicate_clusters WHERE job_id = ? AND cluster_type = 'content'`, jobID).Scan(&contentClusters)
	if contentClusters != 1 {
		t.Errorf("expected 1 content duplicate cluster, got %d", contentClusters)
	}

	var members int
	db.QueryRow(`SELECT COUNT(*) FROM duplicate_cluster_members WHERE job_id = ?`, jobID).Scan(&members)
	// 2 members for content cluster (titles and descriptions are unique)
	if members != 2 {
		t.Errorf("expected 2 duplicate cluster members, got %d", members)
	}
}

func TestMaterializeRollsBackOnLaterFailure(t *testing.T) {
	db := testDB(t)
	jobID := "job-materialize-rollback"
	seedJob(t, db, jobID)

	// Existing rollup data should survive if a later materialization phase fails.
	_, err := db.Exec(`
		INSERT INTO canonical_clusters (job_id, cluster_url, member_count, is_self_referencing)
		VALUES (?, 'https://example.com/old', 2, 0)
	`, jobID)
	if err != nil {
		t.Fatalf("seeding existing canonical cluster: %v", err)
	}

	u1 := seedURL(t, db, jobID, "https://example.com/a", "example.com")
	u2 := seedURL(t, db, jobID, "https://example.com/b", "example.com")
	f1 := seedFetch(t, db, jobID, 1, u1)
	f2 := seedFetch(t, db, jobID, 2, u2)
	for _, pair := range []struct {
		urlID   int64
		fetchID int64
	}{{u1, f1}, {u2, f2}} {
		_, err := db.Exec(`INSERT INTO pages (job_id, url_id, fetch_id, depth, canonical_url) VALUES (?, ?, ?, 1, 'https://example.com/new')`,
			jobID, pair.urlID, pair.fetchID)
		if err != nil {
			t.Fatalf("seeding page: %v", err)
		}
	}

	if _, err := db.Exec(`DROP TABLE duplicate_clusters`); err != nil {
		t.Fatalf("dropping duplicate_clusters: %v", err)
	}

	if err := Materialize(db, jobID); err == nil {
		t.Fatal("Materialize succeeded after dropping duplicate_clusters, want error")
	}

	var oldCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM canonical_clusters WHERE job_id = ? AND cluster_url = 'https://example.com/old'`, jobID).Scan(&oldCount); err != nil {
		t.Fatalf("counting old canonical clusters: %v", err)
	}
	if oldCount != 1 {
		t.Fatalf("old canonical cluster count = %d, want 1 after rollback", oldCount)
	}
}
