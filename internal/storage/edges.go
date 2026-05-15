package storage

import (
	"fmt"
	"strconv"
)

// EdgeInput holds parameters for InsertEdge.
type EdgeInput struct {
	JobID                 string
	SourceURLID           int64
	NormalizedTargetURLID int64
	SourceKind            string
	RelationType          string
	RelFlagsJSON          *string
	DiscoveryMode         string
	AnchorText            *string
	IsInternal            bool
	DeclaredTargetURL     string
	FinalTargetURLID      *int64
	TargetStatusCode      *int
}

// edgeColumns is the canonical SELECT list for edges.
const edgeColumns = `id, job_id, source_url_id, normalized_target_url_id,
	source_kind, relation_type, rel_flags_json, discovery_mode,
	anchor_text, is_internal, declared_target_url,
	final_target_url_id, target_status_code`

// scanEdge scans a row into an Edge using the edgeColumns order.
func scanEdge(sc interface{ Scan(...any) error }) (Edge, error) {
	var e Edge
	var isInternal int
	err := sc.Scan(
		&e.ID, &e.JobID, &e.SourceURLID, &e.NormalizedTargetURLID,
		&e.SourceKind, &e.RelationType, &e.RelFlagsJSON, &e.DiscoveryMode,
		&e.AnchorText, &isInternal, &e.DeclaredTargetURL,
		&e.FinalTargetURLID, &e.TargetStatusCode,
	)
	e.IsInternal = isInternal == 1
	return e, err
}

// InsertEdge creates a new edge record and returns its ID.
func (db *DB) InsertEdge(input EdgeInput) (int64, error) {
	result, err := db.Exec(
		`INSERT INTO edges (job_id, source_url_id, normalized_target_url_id,
			source_kind, relation_type, rel_flags_json, discovery_mode,
			anchor_text, is_internal, declared_target_url,
			final_target_url_id, target_status_code)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.JobID, input.SourceURLID, input.NormalizedTargetURLID,
		input.SourceKind, input.RelationType, input.RelFlagsJSON, input.DiscoveryMode,
		input.AnchorText, boolToInt(input.IsInternal), input.DeclaredTargetURL,
		input.FinalTargetURLID, input.TargetStatusCode,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting edge from URL %d in job %q: %w", input.SourceURLID, input.JobID, err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting last insert ID for edge: %w", err)
	}

	return id, nil
}

// GetEdgesBySource returns edges originating from a source URL with cursor pagination.
func (db *DB) GetEdgesBySource(jobID string, sourceURLID int64, limit int, cursor string) ([]Edge, error) {
	var cursorID int64
	if cursor != "" {
		var parseErr error
		cursorID, parseErr = strconv.ParseInt(cursor, 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("parsing cursor %q: %w", cursor, parseErr)
		}
	}

	rows, err := db.Query(
		`SELECT `+edgeColumns+` FROM edges
		 WHERE job_id = ? AND source_url_id = ? AND id > ?
		 ORDER BY id ASC LIMIT ?`,
		jobID, sourceURLID, cursorID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("querying edges by source URL %d in job %q: %w", sourceURLID, jobID, err)
	}
	defer rows.Close()

	edges := []Edge{}
	for rows.Next() {
		e, scanErr := scanEdge(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scanning edge row: %w", scanErr)
		}
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating edge rows: %w", err)
	}

	return edges, nil
}

// GetEdgesByTarget returns edges pointing to a target URL with cursor pagination.
func (db *DB) GetEdgesByTarget(jobID string, targetURLID int64, limit int, cursor string) ([]Edge, error) {
	var cursorID int64
	if cursor != "" {
		var parseErr error
		cursorID, parseErr = strconv.ParseInt(cursor, 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("parsing cursor %q: %w", cursor, parseErr)
		}
	}

	rows, err := db.Query(
		`SELECT `+edgeColumns+` FROM edges
		 WHERE job_id = ? AND normalized_target_url_id = ? AND id > ?
		 ORDER BY id ASC LIMIT ?`,
		jobID, targetURLID, cursorID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("querying edges by target URL %d in job %q: %w", targetURLID, jobID, err)
	}
	defer rows.Close()

	edges := []Edge{}
	for rows.Next() {
		e, scanErr := scanEdge(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scanning edge row: %w", scanErr)
		}
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating edge rows: %w", err)
	}

	return edges, nil
}

// CountEdges returns the total number of edges in a job.
func (db *DB) CountEdges(jobID string) (int, error) {
	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM edges WHERE job_id = ?`, jobID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting edges for job %q: %w", jobID, err)
	}

	return count, nil
}


