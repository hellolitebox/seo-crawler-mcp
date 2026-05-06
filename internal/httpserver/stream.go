package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

const (
	streamTickInterval      = 1 * time.Second
	streamKeepaliveInterval = 20 * time.Second
	streamFetchBatch        = 200
	streamEventBatch        = 50
)

// streamSession is a single SSE connection's state.
// One session per connected client per job. Fields are mutated only inside
// the session's run loop, so no locking is needed.
type streamSession struct {
	w       http.ResponseWriter
	flusher http.Flusher
	ctx     context.Context

	db    *storage.DB
	jobID string

	lastFetchID int64
	lastEventID int64

	// Cached count payloads. Recomputed only when their respective
	// watermark moves (a new fetch / new event arrived).
	cachedURLsByStatus map[string]int
	cachedIssuesByType map[string]int
	urlsCountStale     bool
	issuesCountStale   bool
}

// newStreamSession constructs a session and writes the SSE response headers.
// Returns nil if the response writer doesn't support flushing.
func newStreamSession(w http.ResponseWriter, r *http.Request, db *storage.DB, jobID string) *streamSession {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	return &streamSession{
		w:                w,
		flusher:          flusher,
		ctx:              r.Context(),
		db:               db,
		jobID:            jobID,
		urlsCountStale:   true,
		issuesCountStale: true,
	}
}

// send writes a single SSE event. Returns false if the underlying writer fails
// (typically because the client disconnected).
func (s *streamSession) send(event string, payload any) bool {
	body, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, body); err != nil {
		return false
	}
	s.flusher.Flush()
	return true
}

// run drives the per-tick send loop until the client disconnects or the job
// reaches a terminal status.
func (s *streamSession) run() {
	// Initial snapshot so the client doesn't have to wait a full tick.
	s.sendStatus()
	s.sendActivityDelta()

	tick := time.NewTicker(streamTickInterval)
	defer tick.Stop()
	keepalive := time.NewTicker(streamKeepaliveInterval)
	defer keepalive.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-keepalive.C:
			// SSE comment line keeps proxies/load balancers from closing idle connections.
			if _, err := fmt.Fprintf(s.w, ": keepalive\n\n"); err != nil {
				return
			}
			s.flusher.Flush()
		case <-tick.C:
			if !s.sendStatus() {
				return
			}
			s.sendActivityDelta()
			if s.terminal() {
				return
			}
		}
	}
}

// terminal checks whether the job has reached a terminal status. If so, sends
// a `done` event so the client can close cleanly.
func (s *streamSession) terminal() bool {
	job, err := s.db.GetJob(s.jobID)
	if err != nil {
		s.send("error", map[string]string{"error": "job vanished"})
		return true
	}
	switch job.Status {
	case "completed", "done", "failed", "cancelled":
		s.send("done", map[string]string{"status": job.Status})
		return true
	}
	return false
}

// sendStatus emits a `status` event with the current job snapshot.
// Returns false on a write failure (client disconnected).
func (s *streamSession) sendStatus() bool {
	snap, ok := s.buildStatusSnapshot()
	if !ok {
		s.send("error", map[string]string{"error": "job vanished"})
		return false
	}
	return s.send("status", snap)
}

// buildStatusSnapshot reads the job row and (cached) count maps into the shape
// returned by GET /api/jobs/{id}.
func (s *streamSession) buildStatusSnapshot() (map[string]any, bool) {
	job, err := s.db.GetJob(s.jobID)
	if err != nil {
		return nil, false
	}
	out := map[string]any{
		"jobId":          job.ID,
		"status":         job.Status,
		"pagesCrawled":   job.PagesCrawled,
		"urlsDiscovered": job.URLsDiscovered,
		"issuesFound":    job.IssuesFound,
		"createdAt":      job.CreatedAt,
	}
	if job.StartedAt.Valid {
		out["startedAt"] = job.StartedAt.String
	}
	if job.FinishedAt.Valid {
		out["finishedAt"] = job.FinishedAt.String
	}
	if job.Error.Valid {
		out["error"] = job.Error.String
	}
	out["urlsByStatus"] = s.urlsByStatus()
	out["issuesByType"] = s.issuesByType()
	return out, true
}

// urlsByStatus returns a cached urlsByStatus map, recomputing only when a new
// fetch has arrived since the last computation.
func (s *streamSession) urlsByStatus() map[string]int {
	if s.urlsCountStale || s.cachedURLsByStatus == nil {
		if c, err := s.db.CountURLsByStatus(s.jobID); err == nil {
			s.cachedURLsByStatus = c
		}
		s.urlsCountStale = false
	}
	return s.cachedURLsByStatus
}

// issuesByType returns a cached issuesByType map, recomputing only when a new
// fetch has arrived (issues are emitted alongside fetches).
func (s *streamSession) issuesByType() map[string]int {
	if s.issuesCountStale || s.cachedIssuesByType == nil {
		if c, err := s.db.CountIssuesByType(s.jobID); err == nil {
			s.cachedIssuesByType = c
		}
		s.issuesCountStale = false
	}
	return s.cachedIssuesByType
}

// sendActivityDelta queries new fetches + phase events since the last seen
// watermarks and sends one `activity` event per row. Marks count caches stale
// when new rows arrive so the next status snapshot recomputes them.
func (s *streamSession) sendActivityDelta() {
	fetches, err := s.db.GetFetchesSince(s.jobID, s.lastFetchID, streamFetchBatch)
	if err == nil && len(fetches) > 0 {
		s.urlsCountStale = true
		s.issuesCountStale = true
		for _, f := range fetches {
			row := map[string]any{
				"kind":       "fetch",
				"url":        f.URL,
				"statusCode": f.StatusCode,
				"ttfbMs":     f.TTFBMs,
				"fetchedAt":  f.FetchedAt,
			}
			if f.RenderMode.Valid {
				row["renderMode"] = f.RenderMode.String
			}
			if f.Error.Valid {
				row["error"] = f.Error.String
			}
			s.send("activity", row)
			if f.ID > s.lastFetchID {
				s.lastFetchID = f.ID
			}
		}
	}

	events, err := s.db.GetPhaseEventsSince(s.jobID, s.lastEventID, streamEventBatch)
	if err == nil {
		for _, ev := range events {
			row := map[string]any{
				"kind":      "phase",
				"fetchedAt": ev.Timestamp,
			}
			if ev.DetailsJSON.Valid {
				var d struct {
					Phase   string `json:"phase"`
					Message string `json:"message"`
				}
				_ = json.Unmarshal([]byte(ev.DetailsJSON.String), &d)
				row["phase"] = d.Phase
				row["message"] = d.Message
			}
			s.send("activity", row)
			if ev.ID > s.lastEventID {
				s.lastEventID = ev.ID
			}
		}
	}
}

// handleJobStreamV2 is the SSE endpoint. It validates the job exists, opens a
// session, and runs it until completion or client disconnect.
//
// Events emitted:
//   - status:   periodic full snapshot (pagesCrawled, issuesFound, urlsByStatus, ...)
//   - activity: incremental fetch + phase events since last tick
//   - done:     terminal state reached (completed/failed/cancelled)
//   - error:    internal error during streaming
func (s *Server) handleJobStreamV2(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "jobId is required")
		return
	}
	if s.db == nil {
		writeError(w, http.StatusInternalServerError, "database unavailable")
		return
	}
	if _, err := s.db.GetJob(jobID); err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("job %q not found", jobID))
		return
	}

	session := newStreamSession(w, r, s.db, jobID)
	if session == nil {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	session.run()
}
