package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// jobColumns is the canonical SELECT list for crawl_jobs (13 columns).
const jobColumns = `id, type, status, config_json, seed_urls, created_at,
	started_at, finished_at, error, pages_crawled, urls_discovered,
	issues_found, ttl_expires_at`

// scanJob scans a row into a CrawlJob using the jobColumns order.
func scanJob(sc interface{ Scan(...any) error }) (CrawlJob, error) {
	var job CrawlJob
	err := sc.Scan(
		&job.ID, &job.Type, &job.Status, &job.ConfigJSON, &job.SeedURLs,
		&job.CreatedAt, &job.StartedAt, &job.FinishedAt, &job.Error,
		&job.PagesCrawled, &job.URLsDiscovered, &job.IssuesFound,
		&job.TTLExpiresAt,
	)
	return job, err
}

// CreateJob inserts a new crawl job and returns the populated struct.
func (db *DB) CreateJob(jobType, configJSON, seedURLsJSON string) (*CrawlJob, error) {
	id := uuid.New().String()

	_, err := db.Exec(
		`INSERT INTO crawl_jobs (id, type, config_json, seed_urls)
		 VALUES (?, ?, ?, ?)`,
		id, jobType, configJSON, seedURLsJSON,
	)
	if err != nil {
		return nil, fmt.Errorf("creating job %q: %w", id, err)
	}

	return db.GetJob(id)
}

// GetJob retrieves a crawl job by ID.
func (db *DB) GetJob(id string) (*CrawlJob, error) {
	row := db.QueryRow(
		`SELECT `+jobColumns+` FROM crawl_jobs WHERE id = ?`, id,
	)

	job, err := scanJob(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("job %q not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("scanning job %q: %w", id, err)
	}

	return &job, nil
}

// UpdateJobStatus changes a job's status.
func (db *DB) UpdateJobStatus(id, status string) error {
	result, err := db.Exec(
		`UPDATE crawl_jobs SET status = ? WHERE id = ?`,
		status, id,
	)
	if err != nil {
		return fmt.Errorf("updating status for job %q: %w", id, err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected for job %q: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("job %q not found", id)
	}

	return nil
}

// UpdateJobStarted sets a job to running with the current timestamp.
func (db *DB) UpdateJobStarted(id string) error {
	result, err := db.Exec(
		`UPDATE crawl_jobs SET status = 'running', started_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		id,
	)
	if err != nil {
		return fmt.Errorf("starting job %q: %w", id, err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected for job %q: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("job %q not found", id)
	}

	return nil
}

// UpdateJobFinished marks a job as finished with the given status.
func (db *DB) UpdateJobFinished(id, status string, errMsg *string) error {
	result, err := db.Exec(
		`UPDATE crawl_jobs SET status = ?, finished_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), error = ? WHERE id = ?`,
		status, errMsg, id,
	)
	if err != nil {
		return fmt.Errorf("finishing job %q: %w", id, err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected for job %q: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("job %q not found", id)
	}

	return nil
}

// UpdateJobCounters sets the crawl progress counters on a job.
func (db *DB) UpdateJobCounters(id string, pagesCrawled, urlsDiscovered, issuesFound int) error {
	result, err := db.Exec(
		`UPDATE crawl_jobs
		 SET pages_crawled = ?, urls_discovered = ?, issues_found = ?
		 WHERE id = ?`,
		pagesCrawled, urlsDiscovered, issuesFound, id,
	)
	if err != nil {
		return fmt.Errorf("updating counters for job %q: %w", id, err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected for job %q: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("job %q not found", id)
	}

	return nil
}

// ListJobs returns all crawl jobs ordered by creation time descending.
// Prefer ListJobsPaginated for HTTP handlers that serve large tables.
func (db *DB) ListJobs() ([]CrawlJob, error) {
	return db.ListJobsPaginated(-1, 0)
}

// ListJobsPaginated returns crawl jobs ordered by creation time descending
// with optional LIMIT/OFFSET. Pass limit=-1 to mean "no limit".
// Jobs in 'deleting' status (tombstoned, async purge in flight) are excluded.
func (db *DB) ListJobsPaginated(limit, offset int) ([]CrawlJob, error) {
	query := `SELECT ` + jobColumns + ` FROM crawl_jobs WHERE status != 'deleting' ORDER BY created_at DESC`
	var args []any
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = []any{limit, offset}
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing jobs: %w", err)
	}
	defer rows.Close()

	jobs := []CrawlJob{}
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning job row: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating job rows: %w", err)
	}

	return jobs, nil
}

// PurgeJob deletes a job and all its dependent rows from every child table.
// We bypass cascade-delete (which is O(n) per child) and instead disable FK
// enforcement, delete each child table by job_id directly (constant-time per
// table thanks to indexes), then re-enable FKs. On a 200K-row job this turns
// a multi-minute lock into a few seconds.
//
// Caller is responsible for running this off the request thread — it can
// still hold the (single) DB connection for tens of seconds on huge jobs.
func (db *DB) PurgeJob(jobID string) error {
	// Order doesn't matter when FK enforcement is off. Tables that reference
	// crawl_jobs(id) directly:
	tables := []string{
		"asset_references",
		"assets",
		"axe_audits",
		"canonical_clusters",
		"crawl_events",
		"duplicate_clusters",
		"edges",
		"fetches",
		"global_issues",
		"issues",
		"llms_findings",
		"markdown_negotiation",
		"pages",
		"psi_audits",
		"redirect_hops",
		"response_codes_summary",
		"robots_directives",
		"security",
		"sitemap_entries",
		"text_quality_findings",
		"url_groups",
		"urls",
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() // no-op if Commit succeeds

	if _, err := tx.Exec(`PRAGMA defer_foreign_keys = ON`); err != nil {
		return fmt.Errorf("defer fks: %w", err)
	}

	for _, t := range tables {
		// Skip tables that don't exist (older schemas). Use a single query that
		// silently no-ops on a missing table by checking sqlite_master first.
		var exists int
		if err := tx.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, t,
		).Scan(&exists); err != nil || exists == 0 {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM `+t+` WHERE job_id = ?`, jobID); err != nil {
			return fmt.Errorf("delete from %s: %w", t, err)
		}
	}

	if _, err := tx.Exec(`DELETE FROM crawl_jobs WHERE id = ?`, jobID); err != nil {
		return fmt.Errorf("delete crawl_jobs row: %w", err)
	}

	return tx.Commit()
}

// MarkOrphanedJobsFailed transitions any jobs left in 'running' or 'queued'
// state into 'failed'. Called on server startup so jobs that were in-flight
// when a previous process died don't appear stuck forever in the UI.
// Returns the number of jobs updated.
func (db *DB) MarkOrphanedJobsFailed(reason string) (int, error) {
	res, err := db.Exec(`
		UPDATE crawl_jobs
		SET status = 'failed',
		    finished_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
		    error = ?
		WHERE status IN ('running', 'queued', 'cancelling')
	`, reason)
	if err != nil {
		return 0, fmt.Errorf("marking orphaned jobs failed: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// CountJobs returns the total number of jobs (excluding tombstoned 'deleting').
func (db *DB) CountJobs() (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM crawl_jobs WHERE status != 'deleting'`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting jobs: %w", err)
	}
	return count, nil
}

// CountActiveJobs returns the number of jobs with status 'queued' or 'running'
// for the given job type.
func (db *DB) CountActiveJobs(jobType string) (int, error) {
	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM crawl_jobs
		 WHERE type = ? AND status IN ('queued', 'running')`,
		jobType,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting active jobs for type %q: %w", jobType, err)
	}

	return count, nil
}

// CountJobsCreatedSince returns the number of jobs created at or after the
// given time. It counts all job types.
func (db *DB) CountJobsCreatedSince(since time.Time) (int, error) {
	sinceStr := since.UTC().Format("2006-01-02T15:04:05.000Z")
	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM crawl_jobs WHERE created_at >= ?`,
		sinceStr,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting jobs since %s: %w", sinceStr, err)
	}
	return count, nil
}

// CreateJobWithTTL inserts a new job and sets its ttl_expires_at field.
func (db *DB) CreateJobWithTTL(jobType, configJSON, seedURLsJSON string, ttl time.Duration) (*CrawlJob, error) {
	id := uuid.New().String()
	ttlStr := time.Now().UTC().Add(ttl).Format("2006-01-02T15:04:05.000Z")

	_, err := db.Exec(
		`INSERT INTO crawl_jobs (id, type, config_json, seed_urls, ttl_expires_at)
		 VALUES (?, ?, ?, ?, ?)`,
		id, jobType, configJSON, seedURLsJSON, ttlStr,
	)
	if err != nil {
		return nil, fmt.Errorf("creating job %q with TTL: %w", id, err)
	}

	return db.GetJob(id)
}

// PurgeExpiredAnalyzeJobs deletes analyze jobs whose TTL has expired.
func (db *DB) PurgeExpiredAnalyzeJobs() (int64, error) {
	result, err := db.Exec(
		`DELETE FROM crawl_jobs
		 WHERE type = 'analyze' AND ttl_expires_at IS NOT NULL
		   AND ttl_expires_at < strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`,
	)
	if err != nil {
		return 0, fmt.Errorf("purging expired analyze jobs: %w", err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("checking purge count: %w", err)
	}

	return count, nil
}
