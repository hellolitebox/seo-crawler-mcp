package storage

import (
	"database/sql"
	"fmt"
)

// AgentReadinessCheckInput holds parameters for UpsertAgentReadinessCheck.
type AgentReadinessCheckInput struct {
	JobID               string
	Category            string
	CheckKey            string
	Status              string
	Score               int
	TargetURL           string
	Endpoint            string
	Method              string
	RequestHeadersJSON  *string
	ResponseStatus      *int
	ResponseHeadersJSON *string
	EvidenceJSON        string
	Recommendation      *string
	ResourcesJSON       string
}

const agentReadinessColumns = `id, job_id, category, check_key, status, score,
	target_url, endpoint, method, request_headers_json, response_status,
	response_headers_json, evidence_json, recommendation, resources_json, checked_at`

func scanAgentReadinessCheck(sc interface{ Scan(...any) error }) (AgentReadinessCheck, error) {
	var c AgentReadinessCheck
	err := sc.Scan(
		&c.ID, &c.JobID, &c.Category, &c.CheckKey, &c.Status, &c.Score,
		&c.TargetURL, &c.Endpoint, &c.Method, &c.RequestHeadersJSON, &c.ResponseStatus,
		&c.ResponseHeadersJSON, &c.EvidenceJSON, &c.Recommendation, &c.ResourcesJSON, &c.CheckedAt,
	)
	return c, err
}

// UpsertAgentReadinessCheck inserts or updates an agent-readiness result.
func (db *DB) UpsertAgentReadinessCheck(input AgentReadinessCheckInput) (int64, error) {
	method := input.Method
	if method == "" {
		method = "GET"
	}
	evidenceJSON := input.EvidenceJSON
	if evidenceJSON == "" {
		evidenceJSON = "{}"
	}
	resourcesJSON := input.ResourcesJSON
	if resourcesJSON == "" {
		resourcesJSON = "[]"
	}

	_, err := db.Exec(
		`INSERT INTO agent_readiness_checks (
			job_id, category, check_key, status, score, target_url, endpoint, method,
			request_headers_json, response_status, response_headers_json,
			evidence_json, recommendation, resources_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id, category, check_key, target_url) DO UPDATE SET
			status = excluded.status,
			score = excluded.score,
			endpoint = excluded.endpoint,
			method = excluded.method,
			request_headers_json = excluded.request_headers_json,
			response_status = excluded.response_status,
			response_headers_json = excluded.response_headers_json,
			evidence_json = excluded.evidence_json,
			recommendation = excluded.recommendation,
			resources_json = excluded.resources_json,
			checked_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`,
		input.JobID, input.Category, input.CheckKey, input.Status, input.Score,
		input.TargetURL, input.Endpoint, method, input.RequestHeadersJSON,
		nullableInt(input.ResponseStatus), input.ResponseHeadersJSON, evidenceJSON,
		input.Recommendation, resourcesJSON,
	)
	if err != nil {
		return 0, fmt.Errorf("upserting agent readiness check %q for job %q: %w", input.CheckKey, input.JobID, err)
	}

	var id int64
	err = db.QueryRow(
		`SELECT id FROM agent_readiness_checks
		 WHERE job_id = ? AND category = ? AND check_key = ? AND target_url = ?`,
		input.JobID, input.Category, input.CheckKey, input.TargetURL,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("getting agent readiness check %q for job %q: %w", input.CheckKey, input.JobID, err)
	}
	return id, nil
}

// GetAgentReadinessChecksByJob returns all agent-readiness checks for a job.
func (db *DB) GetAgentReadinessChecksByJob(jobID string) ([]AgentReadinessCheck, error) {
	rows, err := db.Query(
		`SELECT `+agentReadinessColumns+` FROM agent_readiness_checks
		 WHERE job_id = ?
		 ORDER BY category ASC, check_key ASC, target_url ASC`,
		jobID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying agent readiness checks for job %q: %w", jobID, err)
	}
	defer rows.Close()

	checks := []AgentReadinessCheck{}
	for rows.Next() {
		c, scanErr := scanAgentReadinessCheck(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scanning agent readiness check row: %w", scanErr)
		}
		checks = append(checks, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating agent readiness check rows: %w", err)
	}
	return checks, nil
}

func nullableInt(v *int) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}
