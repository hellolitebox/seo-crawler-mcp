package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/dto"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

// writeJSON encodes v as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// handleCrawl handles POST /api/crawl.
func (s *Server) handleCrawl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body struct {
		URL        string `json:"url"`
		MaxPages   int    `json:"maxPages"`
		RenderMode string `json:"renderMode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if body.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}

	parsed, err := url.ParseRequestURI(body.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid URL %q: must be http or https", body.URL))
		return
	}

	maxPages := 500
	if body.MaxPages > 0 {
		maxPages = body.MaxPages
		if maxPages > 100000 {
			maxPages = 100000
		}
	}

	renderMode := "static"
	if body.RenderMode != "" {
		switch body.RenderMode {
		case "static", "browser", "hybrid", "auto":
			if body.RenderMode == "auto" {
				renderMode = "static"
			} else {
				renderMode = body.RenderMode
			}
		default:
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid renderMode %q", body.RenderMode))
			return
		}
	}

	if s.db == nil {
		writeError(w, http.StatusInternalServerError, "database unavailable")
		return
	}

	// Rate limit check.
	maxJobsPerHour := 20
	if s.config != nil && s.config.MaxJobsPerHour > 0 {
		maxJobsPerHour = s.config.MaxJobsPerHour
	}
	hourAgo := time.Now().Add(-1 * time.Hour)
	recentJobs, err := s.db.CountJobsCreatedSince(hourAgo)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("checking job rate limit: %v", err))
		return
	}
	if recentJobs >= maxJobsPerHour {
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf("rate limit: max %d jobs per hour", maxJobsPerHour))
		return
	}

	// Concurrent crawl limit.
	maxConcurrent := 3
	if s.config != nil {
		maxConcurrent = s.config.MaxConcurrentCrawls
	}
	activeCount, err := s.db.CountActiveJobs("crawl")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("checking active jobs: %v", err))
		return
	}
	if activeCount >= maxConcurrent {
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf("concurrent crawl limit reached (%d/%d active)", activeCount, maxConcurrent))
		return
	}

	crawlConfig := map[string]any{
		"scopeMode":     "registrable_domain",
		"allowedHosts":  []string{},
		"maxPages":      maxPages,
		"maxDepth":      50,
		"renderMode":    renderMode,
		"respectRobots": true,
		"dryRun":        false,
	}
	configJSON, err := json.Marshal(crawlConfig)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("marshalling config: %v", err))
		return
	}

	seedURLs := []string{body.URL}
	seedJSON, err := json.Marshal(seedURLs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("marshalling seed URLs: %v", err))
		return
	}

	job, err := s.db.CreateJob("crawl", string(configJSON), string(seedJSON))
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("creating job: %v", err))
		return
	}

	if s.engine != nil {
		go func() {
			_ = s.engine.RunCrawl(context.Background(), job.ID)
		}()
	}

	writeJSON(w, http.StatusAccepted, map[string]string{
		"jobId":  job.ID,
		"status": "running",
	})
}

// V2 wrappers extract jobId from the Go 1.22+ ServeMux path parameters.
func (s *Server) handleJobStatusV2(w http.ResponseWriter, r *http.Request) {
	s.handleJobStatus(w, r, r.PathValue("id"))
}
func (s *Server) handleJobCancelV2(w http.ResponseWriter, r *http.Request) {
	s.handleJobCancel(w, r, r.PathValue("id"))
}
func (s *Server) handleJobReportV2(w http.ResponseWriter, r *http.Request) {
	s.handleJobReport(w, r, r.PathValue("id"))
}
func (s *Server) handleJobActivityV2(w http.ResponseWriter, r *http.Request) {
	s.handleJobActivity(w, r, r.PathValue("id"))
}

// handleJobStatus handles GET /api/jobs/:jobId.
func (s *Server) handleJobStatus(w http.ResponseWriter, r *http.Request, jobID string) {
	if s.db == nil {
		writeError(w, http.StatusInternalServerError, "database unavailable")
		return
	}

	job, err := s.db.GetJob(jobID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("job %q not found", jobID))
		return
	}

	result := map[string]any{
		"jobId":          job.ID,
		"status":         job.Status,
		"pagesCrawled":   job.PagesCrawled,
		"urlsDiscovered": job.URLsDiscovered,
		"issuesFound":    job.IssuesFound,
		"createdAt":      job.CreatedAt,
	}

	if job.StartedAt.Valid {
		result["startedAt"] = job.StartedAt.String
	}
	if job.FinishedAt.Valid {
		result["finishedAt"] = job.FinishedAt.String
	}
	if job.Error.Valid {
		result["error"] = job.Error.String
	}

	urlCounts, err := s.db.CountURLsByStatus(jobID)
	if err == nil {
		result["urlsByStatus"] = urlCounts
	}

	issueCounts, err := s.db.CountIssuesByType(jobID)
	if err == nil {
		result["issuesByType"] = issueCounts
	}

	writeJSON(w, http.StatusOK, result)
}

// handleJobCancel handles DELETE /api/jobs/:jobId.
// If the job is running/queued, it is cancelled. Otherwise, the job and all
// its related data (URLs, fetches, pages, issues, edges, events, etc.) are
// purged from the DB via ON DELETE CASCADE.
func (s *Server) handleJobCancel(w http.ResponseWriter, r *http.Request, jobID string) {
	if s.db == nil {
		writeError(w, http.StatusInternalServerError, "database unavailable")
		return
	}

	job, err := s.db.GetJob(jobID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("job %q not found", jobID))
		return
	}

	if job.Status == "running" || job.Status == "queued" {
		if err := s.db.UpdateJobStatus(jobID, "cancelling"); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("cancelling job: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"jobId": jobID, "status": "cancelling"})
		return
	}

	// Completed/failed/cancelled job: purge it.
	if _, err := s.db.Exec(`DELETE FROM crawl_jobs WHERE id = ?`, jobID); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("deleting job: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"jobId": jobID, "status": "deleted"})
}

// urlLookup returns a dto.URLLookup backed by the database.
func (s *Server) urlLookup() dto.URLLookup {
	return func(id int64) string {
		u, err := s.db.GetURL(id)
		if err != nil || u == nil {
			return fmt.Sprintf("url:%d", id)
		}
		return u.NormalizedURL
	}
}

// handleJobReport handles GET /api/jobs/:jobId/report.
func (s *Server) handleJobReport(w http.ResponseWriter, r *http.Request, jobID string) {
	if s.db == nil {
		writeError(w, http.StatusInternalServerError, "database unavailable")
		return
	}

	// Verify job exists.
	_, err := s.db.GetJob(jobID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("job %q not found", jobID))
		return
	}

	summary, err := s.db.GetCrawlSummary(jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("getting summary: %v", err))
		return
	}

	lookup := s.urlLookup()

	// Pages
	pagesResult, err := s.db.QueryPages(jobID, storage.QueryFilter{}, "", 10000)
	var pageDTOs []dto.PageDTO
	if err == nil {
		pageDTOs = make([]dto.PageDTO, 0, len(pagesResult.Results))
		for _, p := range pagesResult.Results {
			pageDTOs = append(pageDTOs, dto.PageFromStorage(p, lookup))
		}
	} else {
		pageDTOs = []dto.PageDTO{}
	}

	// Issues
	issuesResult, err := s.db.QueryIssues(jobID, storage.QueryFilter{}, "", 5000)
	var issueDTOs []dto.IssueDTO
	if err == nil {
		issueDTOs = make([]dto.IssueDTO, 0, len(issuesResult.Results))
		for _, i := range issuesResult.Results {
			issueDTOs = append(issueDTOs, dto.IssueFromStorage(i, lookup))
		}
	} else {
		issueDTOs = []dto.IssueDTO{}
	}

	report := map[string]any{
		"summary":           summary,
		"pages":             pageDTOs,
		"issues":            issueDTOs,
		"external_links":    []any{},
		"response_codes":    []any{},
		"robots_directives": []any{},
		"sitemap_entries":   []any{},
		"urls":              []any{},
		"internal_edges":    []any{},
		"assets":            []any{},
		"asset_references":  []any{},
		"redirect_hops":     []any{},
		"llms_findings":     []any{},
		"crawl_events":      []any{},
		"psi_audits":        []any{},
		"axe_audits":        []any{},
		"security":          []any{},
	}

	writeJSON(w, http.StatusOK, report)
}

// handleJobActivity returns recent fetch activity for a job (live log feed).
func (s *Server) handleJobActivity(w http.ResponseWriter, r *http.Request, jobID string) {
	if s.db == nil {
		writeError(w, http.StatusInternalServerError, "database unavailable")
		return
	}

	limit := 30
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 200 {
		limit = n
	}

	rows, err := s.db.Query(`
		SELECT u.normalized_url, f.status_code, f.ttfb_ms, f.fetched_at, f.render_mode, f.error
		FROM fetches f
		JOIN urls u ON u.id = f.requested_url_id
		WHERE f.job_id = ?
		ORDER BY f.id DESC
		LIMIT ?
	`, jobID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("querying activity: %v", err))
		return
	}
	defer rows.Close()

	type activityRow struct {
		Kind       string  `json:"kind"`
		URL        string  `json:"url,omitempty"`
		StatusCode int     `json:"statusCode,omitempty"`
		TTFBMs     int64   `json:"ttfbMs,omitempty"`
		FetchedAt  string  `json:"fetchedAt"`
		RenderMode string  `json:"renderMode,omitempty"`
		Error      *string `json:"error,omitempty"`
		Phase      string  `json:"phase,omitempty"`
		Message    string  `json:"message,omitempty"`
	}

	out := []activityRow{}
	for rows.Next() {
		var ar activityRow
		ar.Kind = "fetch"
		var errStr sql.NullString
		var renderMode sql.NullString
		if scanErr := rows.Scan(&ar.URL, &ar.StatusCode, &ar.TTFBMs, &ar.FetchedAt, &renderMode, &errStr); scanErr != nil {
			continue
		}
		if errStr.Valid {
			s := errStr.String
			ar.Error = &s
		}
		if renderMode.Valid {
			ar.RenderMode = renderMode.String
		}
		out = append(out, ar)
	}

	// Also include phase events (post-crawl markers so the log doesn't look stuck)
	eventRows, eventErr := s.db.Query(`
		SELECT timestamp, details_json
		FROM crawl_events
		WHERE job_id = ? AND event_type = 'phase'
		ORDER BY id DESC
		LIMIT 30
	`, jobID)
	if eventErr == nil {
		defer eventRows.Close()
		for eventRows.Next() {
			var ts string
			var detailsJSON sql.NullString
			if scanErr := eventRows.Scan(&ts, &detailsJSON); scanErr != nil {
				continue
			}
			row := activityRow{Kind: "phase", FetchedAt: ts}
			if detailsJSON.Valid {
				var d struct {
					Phase   string `json:"phase"`
					Message string `json:"message"`
				}
				json.Unmarshal([]byte(detailsJSON.String), &d)
				row.Phase = d.Phase
				row.Message = d.Message
			}
			out = append(out, row)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"activity": out,
	})
}

// handleJobsList returns a paginated list of crawl jobs (most recent first).
// Used by the UI to render the "Reports" history tab.
// Query params: ?limit=N (1..200, default 50), ?offset=N (default 0).
func (s *Server) handleJobsList(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusInternalServerError, "database unavailable")
		return
	}

	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 200 {
		limit = n
	}
	offset := 0
	if n, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && n >= 0 {
		offset = n
	}

	total, err := s.db.CountJobs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("counting jobs: %v", err))
		return
	}

	jobs, err := s.db.ListJobsPaginated(limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("listing jobs: %v", err))
		return
	}

	// Build a map of jobID -> actual issue count from the issues table.
	// The job row's issues_found counter doesn't always reflect post-crawl phases
	// (text quality, axe audits, etc.), so query the source of truth.
	actualIssues := map[string]int{}
	if rows, qErr := s.db.Query(`SELECT job_id, COUNT(*) FROM issues GROUP BY job_id`); qErr == nil {
		defer rows.Close()
		for rows.Next() {
			var jid string
			var c int
			if scanErr := rows.Scan(&jid, &c); scanErr == nil {
				actualIssues[jid] = c
			}
		}
	}

	type jobRow struct {
		JobID          string  `json:"jobId"`
		Status         string  `json:"status"`
		SeedURLs       string  `json:"seedUrls"`
		PagesCrawled   int     `json:"pagesCrawled"`
		IssuesFound    int     `json:"issuesFound"`
		URLsDiscovered int     `json:"urlsDiscovered"`
		CreatedAt      string  `json:"createdAt"`
		FinishedAt     *string `json:"finishedAt,omitempty"`
	}

	out := make([]jobRow, 0, len(jobs))
	for _, j := range jobs {
		issues := j.IssuesFound
		if a, ok := actualIssues[j.ID]; ok && a > issues {
			issues = a
		}
		row := jobRow{
			JobID:          j.ID,
			Status:         j.Status,
			SeedURLs:       j.SeedURLs,
			PagesCrawled:   j.PagesCrawled,
			IssuesFound:    issues,
			URLsDiscovered: j.URLsDiscovered,
			CreatedAt:      j.CreatedAt,
		}
		if j.FinishedAt.Valid {
			fa := j.FinishedAt.String
			row.FinishedAt = &fa
		}
		out = append(out, row)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"jobs":   out,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// handleJobStreamV2 is a Server-Sent Events endpoint that pushes live job
// updates to the client. Replaces client-side polling for active crawls.
//
// Events emitted (each on its own line, format `event: <name>\ndata: <json>\n\n`):
//   - status:   periodic full status snapshot (pagesCrawled, issuesFound, urlsByStatus, etc.)
//   - activity: incremental fetch + phase events since the last tick
//   - done:     terminal event when status is completed/failed/cancelled (then connection closes)
//   - error:    any internal error during streaming (then connection closes)
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

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx-style buffering

	send := func(event string, payload any) bool {
		body, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	ctx := r.Context()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()

	var lastFetchID int64 = 0
	var lastEventID int64 = 0

	// Send initial status + activity so the client doesn't have to wait 1s.
	if status, ok := s.buildJobStatus(jobID); ok {
		send("status", status)
	}
	lastFetchID, lastEventID = s.streamActivityDelta(send, jobID, lastFetchID, lastEventID)

	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			// SSE comment line keeps proxies/load balancers from closing idle connections.
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case <-ticker.C:
			status, ok := s.buildJobStatus(jobID)
			if !ok {
				send("error", map[string]string{"error": "job vanished"})
				return
			}
			send("status", status)
			lastFetchID, lastEventID = s.streamActivityDelta(send, jobID, lastFetchID, lastEventID)

			st, _ := status["status"].(string)
			if st == "completed" || st == "done" || st == "failed" || st == "cancelled" {
				send("done", map[string]string{"status": st})
				return
			}
		}
	}
}

// buildJobStatus returns the same shape as GET /api/jobs/{id}, used by SSE.
func (s *Server) buildJobStatus(jobID string) (map[string]any, bool) {
	job, err := s.db.GetJob(jobID)
	if err != nil {
		return nil, false
	}
	result := map[string]any{
		"jobId":          job.ID,
		"status":         job.Status,
		"pagesCrawled":   job.PagesCrawled,
		"urlsDiscovered": job.URLsDiscovered,
		"issuesFound":    job.IssuesFound,
		"createdAt":      job.CreatedAt,
	}
	if job.StartedAt.Valid {
		result["startedAt"] = job.StartedAt.String
	}
	if job.FinishedAt.Valid {
		result["finishedAt"] = job.FinishedAt.String
	}
	if job.Error.Valid {
		result["error"] = job.Error.String
	}
	if c, err := s.db.CountURLsByStatus(jobID); err == nil {
		result["urlsByStatus"] = c
	}
	if c, err := s.db.CountIssuesByType(jobID); err == nil {
		result["issuesByType"] = c
	}
	return result, true
}

// streamActivityDelta sends new fetches + phase events since the last seen IDs.
// Returns the new lastFetchID + lastEventID watermarks.
func (s *Server) streamActivityDelta(send func(string, any) bool, jobID string, lastFetchID, lastEventID int64) (int64, int64) {
	// New fetches
	rows, err := s.db.Query(`
		SELECT f.id, u.normalized_url, f.status_code, f.ttfb_ms, f.fetched_at, f.render_mode, f.error
		FROM fetches f
		JOIN urls u ON u.id = f.requested_url_id
		WHERE f.job_id = ? AND f.id > ?
		ORDER BY f.id ASC
		LIMIT 200
	`, jobID, lastFetchID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id int64
			var url string
			var statusCode int
			var ttfb int64
			var fetchedAt string
			var renderMode sql.NullString
			var errStr sql.NullString
			if err := rows.Scan(&id, &url, &statusCode, &ttfb, &fetchedAt, &renderMode, &errStr); err != nil {
				continue
			}
			row := map[string]any{
				"kind":       "fetch",
				"url":        url,
				"statusCode": statusCode,
				"ttfbMs":     ttfb,
				"fetchedAt":  fetchedAt,
			}
			if renderMode.Valid {
				row["renderMode"] = renderMode.String
			}
			if errStr.Valid {
				row["error"] = errStr.String
			}
			send("activity", row)
			if id > lastFetchID {
				lastFetchID = id
			}
		}
	}

	// New phase events
	eventRows, err := s.db.Query(`
		SELECT id, timestamp, details_json
		FROM crawl_events
		WHERE job_id = ? AND event_type = 'phase' AND id > ?
		ORDER BY id ASC
		LIMIT 50
	`, jobID, lastEventID)
	if err == nil {
		defer eventRows.Close()
		for eventRows.Next() {
			var id int64
			var ts string
			var detailsJSON sql.NullString
			if err := eventRows.Scan(&id, &ts, &detailsJSON); err != nil {
				continue
			}
			row := map[string]any{
				"kind":      "phase",
				"fetchedAt": ts,
			}
			if detailsJSON.Valid {
				var d struct {
					Phase   string `json:"phase"`
					Message string `json:"message"`
				}
				json.Unmarshal([]byte(detailsJSON.String), &d)
				row["phase"] = d.Phase
				row["message"] = d.Message
			}
			send("activity", row)
			if id > lastEventID {
				lastEventID = id
			}
		}
	}

	return lastFetchID, lastEventID
}
