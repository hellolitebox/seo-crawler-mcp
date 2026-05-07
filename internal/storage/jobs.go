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
		`UPDATE crawl_jobs SET status = 'running', started_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ? AND status = 'queued'`,
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
		job, getErr := db.GetJob(id)
		if getErr != nil {
			return fmt.Errorf("job %q not found", id)
		}
		return fmt.Errorf("job %q has status %q, expected queued", id, job.Status)
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

// purgeChunkSize is how many rows are deleted per table per transaction.
// Each chunk is intentionally small because this process uses a single SQLite
// connection. Large chunks make background purges monopolize the connection and
// stall read endpoints like /api/jobs.
const purgeChunkSize = 500

// purgeChunkYield is the tiny pause between chunks that lets normal API reads
// acquire the DB connection while a large background purge is draining.
const purgeChunkYield = 10 * time.Millisecond

// purgeTables lists tables that reference crawl_jobs(id), ordered from leaves
// to parents so chunked deletes satisfy immediate SQLite FK checks. Keep tables
// that reference fetches/urls before fetches/urls, and cluster members before
// their cluster parent tables.
var purgeTables = []string{
	"asset_references",
	"canonical_cluster_members",
	"duplicate_cluster_members",
	"redirect_hops",
	"pages",
	"edges",
	"assets",
	"issues",
	"duplicate_clusters",
	"canonical_clusters",
	"fetches",
	"axe_audits",
	"crawl_events",
	"global_issues",
	"llms_findings",
	"markdown_negotiation",
	"psi_audits",
	"response_codes_summary",
	"robots_directives",
	"security",
	"sitemap_entries",
	"text_quality_findings",
	"url_pattern_groups",
	"url_groups",
	"urls",
}

// PurgeJob deletes a job and all its dependent rows. Designed to coexist with
// normal API traffic on the same SQLite connection: each table is drained in
// chunks of `purgeChunkSize` inside its own transaction, so other queries can
// interleave between chunks instead of waiting for the full purge.
//
// Total time on a 200K-row job: tens of seconds, but no single statement holds
// the write connection for more than ~100 ms.
func (db *DB) PurgeJob(jobID string) error {
	for _, t := range purgeTables {
		var exists int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, t,
		).Scan(&exists); err != nil || exists == 0 {
			continue
		}
		if err := db.purgeTableChunked(t, jobID); err != nil {
			return fmt.Errorf("purging %s: %w", t, err)
		}
	}

	// Final delete of the parent row — small, single statement.
	if _, err := db.Exec(`DELETE FROM crawl_jobs WHERE id = ?`, jobID); err != nil {
		return fmt.Errorf("deleting parent row: %w", err)
	}
	return nil
}

// purgeTableChunked deletes all rows for `jobID` in `table` in chunks of
// purgeChunkSize, releasing the connection between chunks.
func (db *DB) purgeTableChunked(table, jobID string) error {
	for {
		res, err := db.Exec(
			// SQLite doesn't support DELETE ... LIMIT without a build flag, so we
			// use rowid-in-subquery, which it does support.
			`DELETE FROM `+table+` WHERE rowid IN (
				SELECT rowid FROM `+table+` WHERE job_id = ? LIMIT ?
			)`, jobID, purgeChunkSize,
		)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n < purgeChunkSize {
			return nil // last chunk
		}
		time.Sleep(purgeChunkYield)
	}
}

// MarkOrphanedJobsFailed transitions any jobs left in 'running' or
// 'cancelling' state into 'failed'. Called on server startup so jobs that
// were in-flight when a previous process died don't appear stuck forever in
// the UI. Jobs in 'queued' state are intentionally left alone — the queue
// worker will pick them up after restart so deploys don't wipe the queue.
// Returns the number of jobs updated.
func (db *DB) MarkOrphanedJobsFailed(reason string) (int, error) {
	res, err := db.Exec(`
		UPDATE crawl_jobs
		SET status = 'failed',
		    finished_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
		    error = ?
		WHERE status IN ('running', 'cancelling')
	`, reason)
	if err != nil {
		return 0, fmt.Errorf("marking orphaned jobs failed: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ResumePendingPurges returns the IDs of jobs left in 'deleting' status (a
// previous process died mid-purge). The HTTP layer enqueues these on the
// purge worker on startup so they don't stay tombstoned forever.
func (db *DB) ResumePendingPurges() ([]string, error) {
	rows, err := db.Query(`SELECT id FROM crawl_jobs WHERE status = 'deleting'`)
	if err != nil {
		return nil, fmt.Errorf("querying pending purges: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
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

// CountRunningJobs returns the number of jobs with status 'running' (NOT
// 'queued') for the given job type. Used by the queue worker to decide
// whether a slot is free before promoting the next queued job — counting
// queued ones here would deadlock the queue (it would always look full).
func (db *DB) CountRunningJobs(jobType string) (int, error) {
	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM crawl_jobs
		 WHERE type = ? AND status = 'running'`,
		jobType,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting running jobs for type %q: %w", jobType, err)
	}

	return count, nil
}

// NextQueuedJob returns the oldest job with status="queued", or nil if none.
// Used by the queue worker to pick up the next job to run.
func (db *DB) NextQueuedJob() (*CrawlJob, error) {
	row := db.QueryRow(
		`SELECT ` + jobColumns + ` FROM crawl_jobs WHERE status = 'queued' ORDER BY created_at ASC LIMIT 1`,
	)
	job, err := scanJob(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning next queued job: %w", err)
	}
	return &job, nil
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
