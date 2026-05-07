package storage

import (
	"fmt"
	"strconv"
	"strings"
)

// IssueInput holds parameters for InsertIssue.
type IssueInput struct {
	JobID       string
	URLID       *int64
	IssueType   string
	Severity    string
	Scope       string
	DetailsJSON *string
}

// issueColumns is the canonical SELECT list for issues.
const issueColumns = `id, job_id, url_id, issue_type, severity, scope, details_json`

// scanIssue scans a row into an Issue using the issueColumns order.
func scanIssue(sc interface{ Scan(...any) error }) (Issue, error) {
	var i Issue
	err := sc.Scan(
		&i.ID, &i.JobID, &i.URLID, &i.IssueType,
		&i.Severity, &i.Scope, &i.DetailsJSON,
	)
	return i, err
}

// InsertIssue creates a new issue record and returns its ID.
func (db *DB) InsertIssue(input IssueInput) (int64, error) {
	result, err := db.Exec(
		`INSERT INTO issues (job_id, url_id, issue_type, severity, scope, details_json)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		input.JobID, input.URLID, input.IssueType, input.Severity, input.Scope, input.DetailsJSON,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting issue %q for job %q: %w", input.IssueType, input.JobID, err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert ID for issue: %w", err)
	}

	return id, nil
}

// InsertIssuesBatch inserts many issues in a single transaction. Vastly
// faster than calling InsertIssue in a loop because it acquires the (single)
// SQLite write connection once instead of once per row, and uses one prepared
// statement reused across rows.
//
// Use this from any post-crawl phase that emits per-page issues (markdown
// negotiation, text quality, axe audits, ...) so the API stays responsive
// while a 3K-row insert runs.
func (db *DB) InsertIssuesBatch(inputs []IssueInput) error {
	if len(inputs) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin batch: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`INSERT INTO issues (job_id, url_id, issue_type, severity, scope, details_json)
		 VALUES (?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("prepare batch: %w", err)
	}
	defer stmt.Close()

	for _, in := range inputs {
		if _, err := stmt.Exec(in.JobID, in.URLID, in.IssueType, in.Severity, in.Scope, in.DetailsJSON); err != nil {
			return fmt.Errorf("insert issue %q: %w", in.IssueType, err)
		}
	}
	return tx.Commit()
}

// GetIssuesByJob returns issues for a job with cursor pagination.
func (db *DB) GetIssuesByJob(jobID string, limit int, cursor string) ([]Issue, error) {
	var cursorID int64
	if cursor != "" {
		var parseErr error
		cursorID, parseErr = strconv.ParseInt(cursor, 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("parsing cursor %q: %w", cursor, parseErr)
		}
	}

	rows, err := db.Query(
		`SELECT `+issueColumns+` FROM issues
		 WHERE job_id = ? AND id > ?
		 ORDER BY id ASC LIMIT ?`,
		jobID, cursorID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("querying issues for job %q: %w", jobID, err)
	}
	defer rows.Close()

	issues := []Issue{}
	for rows.Next() {
		i, scanErr := scanIssue(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scanning issue row: %w", scanErr)
		}
		issues = append(issues, i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating issue rows: %w", err)
	}

	return issues, nil
}

// GetIssuesByURL returns issues for a specific URL within a job.
func (db *DB) GetIssuesByURL(jobID string, urlID int64) ([]Issue, error) {
	rows, err := db.Query(
		`SELECT `+issueColumns+` FROM issues
		 WHERE job_id = ? AND url_id = ?
		 ORDER BY id ASC`,
		jobID, urlID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying issues for URL %d in job %q: %w", urlID, jobID, err)
	}
	defer rows.Close()

	issues := []Issue{}
	for rows.Next() {
		i, scanErr := scanIssue(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scanning issue row: %w", scanErr)
		}
		issues = append(issues, i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating issue rows: %w", err)
	}

	return issues, nil
}

// CountIssuesByJobs returns issue counts for the supplied job IDs only.
// It is intentionally scoped to a visible page of jobs so report-list APIs
// don't scan the entire historical issues table on every request.
func (db *DB) CountIssuesByJobs(jobIDs []string) (map[string]int, error) {
	counts := map[string]int{}
	if len(jobIDs) == 0 {
		return counts, nil
	}

	placeholders := make([]string, len(jobIDs))
	args := make([]any, len(jobIDs))
	for i, id := range jobIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	rows, err := db.Query(
		`SELECT job_id, COUNT(*) FROM issues WHERE job_id IN (`+strings.Join(placeholders, ",")+`) GROUP BY job_id`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("counting issues for jobs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var jobID string
		var count int
		if err := rows.Scan(&jobID, &count); err != nil {
			return nil, fmt.Errorf("scanning issue count: %w", err)
		}
		counts[jobID] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating issue counts: %w", err)
	}

	return counts, nil
}

// CountIssuesByType returns a map of issue_type → count for a job.
func (db *DB) CountIssuesByType(jobID string) (map[string]int, error) {
	rows, err := db.Query(
		`SELECT issue_type, COUNT(*) FROM issues WHERE job_id = ? GROUP BY issue_type`,
		jobID,
	)
	if err != nil {
		return nil, fmt.Errorf("counting issues by type for job %q: %w", jobID, err)
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var issueType string
		var count int
		if err := rows.Scan(&issueType, &count); err != nil {
			return nil, fmt.Errorf("scanning issue type count: %w", err)
		}
		counts[issueType] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating issue type counts: %w", err)
	}

	return counts, nil
}

// CountIssuesBySeverity returns a map of severity → count for a job.
func (db *DB) CountIssuesBySeverity(jobID string) (map[string]int, error) {
	rows, err := db.Query(
		`SELECT severity, COUNT(*) FROM issues WHERE job_id = ? GROUP BY severity`,
		jobID,
	)
	if err != nil {
		return nil, fmt.Errorf("counting issues by severity for job %q: %w", jobID, err)
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var severity string
		var count int
		if err := rows.Scan(&severity, &count); err != nil {
			return nil, fmt.Errorf("scanning issue severity count: %w", err)
		}
		counts[severity] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating issue severity counts: %w", err)
	}

	return counts, nil
}
