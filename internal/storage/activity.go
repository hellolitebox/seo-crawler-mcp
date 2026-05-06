package storage

import (
	"database/sql"
	"fmt"
)

// ActivityFetch is a single fetch row exposed via the activity feed.
type ActivityFetch struct {
	ID         int64
	URL        string
	StatusCode int
	TTFBMs     int64
	FetchedAt  string
	RenderMode sql.NullString
	Error      sql.NullString
}

// ActivityPhaseEvent is a phase-transition event exposed via the activity feed.
type ActivityPhaseEvent struct {
	ID          int64
	Timestamp   string
	DetailsJSON sql.NullString
}

// GetFetchesSince returns fetches with id > sinceID for a job, oldest first,
// capped at limit. Used by both /activity and /stream to build incremental feeds.
func (db *DB) GetFetchesSince(jobID string, sinceID int64, limit int) ([]ActivityFetch, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.Query(`
		SELECT f.id, u.normalized_url, f.status_code, f.ttfb_ms, f.fetched_at, f.render_mode, f.error
		FROM fetches f
		JOIN urls u ON u.id = f.requested_url_id
		WHERE f.job_id = ? AND f.id > ?
		ORDER BY f.id ASC
		LIMIT ?
	`, jobID, sinceID, limit)
	if err != nil {
		return nil, fmt.Errorf("querying fetches since %d: %w", sinceID, err)
	}
	defer rows.Close()

	var out []ActivityFetch
	for rows.Next() {
		var f ActivityFetch
		if err := rows.Scan(&f.ID, &f.URL, &f.StatusCode, &f.TTFBMs, &f.FetchedAt, &f.RenderMode, &f.Error); err != nil {
			return nil, fmt.Errorf("scanning fetch row: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetPhaseEventsSince returns phase events with id > sinceID for a job,
// oldest first, capped at limit.
func (db *DB) GetPhaseEventsSince(jobID string, sinceID int64, limit int) ([]ActivityPhaseEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.Query(`
		SELECT id, timestamp, details_json
		FROM crawl_events
		WHERE job_id = ? AND event_type = 'phase' AND id > ?
		ORDER BY id ASC
		LIMIT ?
	`, jobID, sinceID, limit)
	if err != nil {
		return nil, fmt.Errorf("querying phase events since %d: %w", sinceID, err)
	}
	defer rows.Close()

	var out []ActivityPhaseEvent
	for rows.Next() {
		var ev ActivityPhaseEvent
		if err := rows.Scan(&ev.ID, &ev.Timestamp, &ev.DetailsJSON); err != nil {
			return nil, fmt.Errorf("scanning event row: %w", err)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// LatestFetchID returns the highest fetch id for a job, or 0 if none.
func (db *DB) LatestFetchID(jobID string) (int64, error) {
	var id sql.NullInt64
	err := db.QueryRow(`SELECT MAX(id) FROM fetches WHERE job_id = ?`, jobID).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id.Int64, nil
}

// LatestPhaseEventID returns the highest phase-event id for a job, or 0 if none.
func (db *DB) LatestPhaseEventID(jobID string) (int64, error) {
	var id sql.NullInt64
	err := db.QueryRow(`SELECT MAX(id) FROM crawl_events WHERE job_id = ? AND event_type = 'phase'`, jobID).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id.Int64, nil
}
