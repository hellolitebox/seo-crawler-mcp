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
func (db *DB) ListJobsPaginated(limit, offset int) ([]CrawlJob, error) {
	query := `SELECT ` + jobColumns + ` FROM crawl_jobs ORDER BY created_at DESC`
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

// CountJobs returns the total number of jobs in the database.
func (db *DB) CountJobs() (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM crawl_jobs`).Scan(&count)
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
