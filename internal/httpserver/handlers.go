package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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

// handleJobs dispatches GET /api/jobs/:jobId, GET /api/jobs/:jobId/report, and DELETE /api/jobs/:jobId.
func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	// Strip "/api/jobs/" prefix.
	path := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	// path is now either "<jobId>" or "<jobId>/report"
	parts := strings.SplitN(path, "/", 2)
	jobID := parts[0]
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "jobId is required")
		return
	}

	if len(parts) == 2 && parts[1] == "report" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleJobReport(w, r, jobID)
		return
	}

	if len(parts) == 2 && parts[1] == "activity" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleJobActivity(w, r, jobID)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleJobStatus(w, r, jobID)
	case http.MethodDelete:
		s.handleJobCancel(w, r, jobID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
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

	if job.Status != "running" && job.Status != "queued" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("job %q has status %q, cannot cancel", jobID, job.Status))
		return
	}

	if err := s.db.UpdateJobStatus(jobID, "cancelling"); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("cancelling job: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"jobId": jobID, "status": "cancelling"})
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
	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
		if limit < 1 || limit > 200 {
			limit = 30
		}
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

// handleJobsList returns a list of all crawl jobs (most recent first).
// Used by the UI to render the "Reports" history tab.
func (s *Server) handleJobsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.db == nil {
		writeError(w, http.StatusInternalServerError, "database unavailable")
		return
	}

	jobs, err := s.db.ListJobs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("listing jobs: %v", err))
		return
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
		row := jobRow{
			JobID:          j.ID,
			Status:         j.Status,
			SeedURLs:       j.SeedURLs,
			PagesCrawled:   j.PagesCrawled,
			IssuesFound:    j.IssuesFound,
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
		"jobs": out,
	})
}
