package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/dto"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/ssrf"
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

func normalizeCrawlURL(rawURL string) (string, *url.URL, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", nil, fmt.Errorf("url is required")
	}

	if !strings.Contains(trimmed, "://") {
		if looksLikeUnsupportedScheme(trimmed) {
			return "", nil, fmt.Errorf("invalid URL %q: enter a domain or http(s) URL", rawURL)
		}
		trimmed = "https://" + strings.TrimLeft(trimmed, "/")
	}

	parsed, err := url.ParseRequestURI(trimmed)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return "", nil, fmt.Errorf("invalid URL %q: enter a domain or http(s) URL", rawURL)
	}
	return trimmed, parsed, nil
}

func looksLikeUnsupportedScheme(value string) bool {
	colon := strings.Index(value, ":")
	if colon <= 0 {
		return false
	}
	if sep := strings.IndexAny(value, "/?#"); sep >= 0 && sep < colon {
		return false
	}

	hostPart := value[:colon]
	rest := value[colon+1:]
	if sep := strings.IndexAny(rest, "/?#"); sep >= 0 {
		rest = rest[:sep]
	}
	if (strings.Contains(hostPart, ".") || strings.EqualFold(hostPart, "localhost")) && rest != "" {
		if _, err := strconv.Atoi(rest); err == nil {
			return false
		}
	}
	return true
}

// handleCrawl handles POST /api/crawl.
func (s *Server) handleCrawl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var body struct {
		URL        string `json:"url"`
		MaxPages   int    `json:"maxPages"`
		RenderMode string `json:"renderMode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	normalizedURL, parsed, err := normalizeCrawlURL(body.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	body.URL = normalizedURL
	if s.config == nil || s.config.SSRFProtection {
		allowPrivate := false
		if s.config != nil {
			allowPrivate = s.config.AllowPrivateNetworks
		}
		guard := ssrf.NewGuard(allowPrivate)
		if err := guard.ValidateURL(body.URL); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := guard.ValidateHost(parsed.Hostname()); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
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
				renderMode = "hybrid"
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
	if s.engine == nil {
		writeError(w, http.StatusServiceUnavailable, "crawler engine unavailable")
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

	// Concurrent crawl limit. Lowered to 1 because two large crawls (e.g. two
	// 3K-page sites) compete for the single SQLite write connection during
	// post-processing and starve every other API request. Override via
	// s.config.MaxConcurrentCrawls if you really want more.
	maxConcurrent := s.maxConcurrentCrawls()

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

	// Serialize the count→create→decide block so two concurrent POSTs can't
	// both observe activeCount<maxConcurrent and both launch crawls. Without
	// this lock the concurrency limit is silently violated under load.
	s.crawlMu.Lock()
	activeCount, err := s.db.CountActiveJobs("crawl")
	if err != nil {
		s.crawlMu.Unlock()
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("checking active jobs: %v", err))
		return
	}

	job, err := s.db.CreateJob("crawl", string(configJSON), string(seedJSON))
	if err != nil {
		s.crawlMu.Unlock()
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("creating job: %v", err))
		return
	}

	runNow := activeCount < maxConcurrent
	if runNow {
		if err := s.db.UpdateJobStarted(job.ID); err != nil {
			s.crawlMu.Unlock()
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("starting job: %v", err))
			return
		}
	}
	s.crawlMu.Unlock()

	if !runNow {
		// All slots are occupied. Job is left in 'queued' status. We do NOT
		// signal the queue worker here — the slot is busy by definition, and
		// the running crawl will signal once it completes.
		writeJSON(w, http.StatusAccepted, map[string]string{
			"jobId":  job.ID,
			"status": "queued",
		})
		return
	}

	// Slot free — start immediately. runCrawlJob owns panic recovery and the
	// post-completion queue signal so we don't have to here.
	go s.runCrawlJob(job.ID)

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
func (s *Server) handleJobPagesV2(w http.ResponseWriter, r *http.Request) {
	s.handleJobPages(w, r, r.PathValue("id"))
}
func (s *Server) handleJobIssuesV2(w http.ResponseWriter, r *http.Request) {
	s.handleJobIssues(w, r, r.PathValue("id"))
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
//
// SECURITY: this endpoint has no authentication. The deployment
// contract is "expose only behind a proxy that authenticates the
// caller" — same model as the per-IP rate limiter and SSE cap, both
// of which trust upstream-supplied client identity. Direct internet
// exposure lets any caller delete any job they can name. If you need
// auth in-process, wire a token check here (see the Bearer-token
// option in CLAUDE.md / docs).
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

	// Queued jobs have no engine goroutine running them, so transitioning
	// to 'cancelling' would strand them there forever. Mark cancelled
	// directly so the queue worker / UI both stop seeing them.
	if job.Status == "queued" {
		if err := s.db.UpdateJobStatus(jobID, "cancelled"); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("cancelling queued job: %v", err))
			return
		}
		s.cancelRun(jobID, context.Canceled)
		writeJSON(w, http.StatusOK, map[string]string{"jobId": jobID, "status": "cancelled"})
		return
	}

	if job.Status == "running" || job.Status == "cancelling" {
		if err := s.db.UpdateJobStatus(jobID, "cancelling"); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("cancelling job: %v", err))
			return
		}
		s.cancelRun(jobID, context.Canceled)
		writeJSON(w, http.StatusOK, map[string]string{"jobId": jobID, "status": "cancelling"})
		return
	}

	// Completed/failed/cancelled job: tombstone it so the UI hides it
	// immediately. Do not purge during normal interactive traffic: purging large
	// jobs shares the single SQLite connection and can block POST /api/crawl and
	// status reads long enough for the UI to look hung. Physical cleanup is a
	// maintenance operation, enabled explicitly with SEO_CRAWLER_PURGE_ON_DELETE=1.
	if err := s.db.UpdateJobStatus(jobID, "deleting"); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("marking deleted: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"jobId": jobID, "status": "deleted"})

	if os.Getenv("SEO_CRAWLER_PURGE_ON_DELETE") == "1" {
		s.purger.enqueue(jobID)
	}
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

// clampInt returns n clamped to [minVal, maxVal].
func clampInt(n, minVal, maxVal int) int {
	if n < minVal {
		return minVal
	}
	if n > maxVal {
		return maxVal
	}
	return n
}

// parsePaginationParam parses an int query param with a default and max.
func parsePaginationParam(r *http.Request, key string, def, max int) int {
	n := def
	if v, err := strconv.Atoi(r.URL.Query().Get(key)); err == nil && v > 0 {
		n = clampInt(v, 1, max)
	}
	return n
}

// parseOffsetParam parses a non-negative int query param with default 0.
func parseOffsetParam(r *http.Request, key string) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(key)); err == nil && v >= 0 {
		return v
	}
	return 0
}

// handleJobReport handles GET /api/jobs/:jobId/report.
// Accepts query params: pages_limit (default 100, max 200), pages_offset (default 0),
// issues_limit (default 200, max 500), issues_offset (default 0).
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

	// Pagination params
	pagesLimit := parsePaginationParam(r, "pages_limit", 100, 200)
	pagesOffset := parseOffsetParam(r, "pages_offset")
	issuesLimit := parsePaginationParam(r, "issues_limit", 200, 500)
	issuesOffset := parseOffsetParam(r, "issues_offset")

	lookup := s.urlLookup()

	// Pages — paginated
	pagesResult, err := s.db.QueryPagesOffset(jobID, storage.QueryFilter{}, pagesLimit, pagesOffset)
	var pageDTOs []dto.PageDTO
	var pagesTotalCount int
	if err == nil {
		pagesTotalCount = pagesResult.TotalCount
		pageDTOs = make([]dto.PageDTO, 0, len(pagesResult.Results))
		for _, p := range pagesResult.Results {
			pageDTOs = append(pageDTOs, dto.PageFromStorage(p, lookup))
		}
	} else {
		pageDTOs = []dto.PageDTO{}
	}

	// Issues — paginated
	issuesResult, err := s.db.QueryIssuesOffset(jobID, storage.QueryFilter{}, issuesLimit, issuesOffset)
	var issueDTOs []dto.IssueDTO
	var issuesTotalCount int
	if err == nil {
		issuesTotalCount = issuesResult.TotalCount
		issueDTOs = make([]dto.IssueDTO, 0, len(issuesResult.Results))
		for _, i := range issuesResult.Results {
			issueDTOs = append(issueDTOs, dto.IssueFromStorage(i, lookup))
		}
	} else {
		issueDTOs = []dto.IssueDTO{}
	}

	// Compute next offsets (nil means no more pages).
	var pagesNextOffset *int
	if pagesOffset+len(pageDTOs) < pagesTotalCount {
		n := pagesOffset + len(pageDTOs)
		pagesNextOffset = &n
	}
	var issuesNextOffset *int
	if issuesOffset+len(issueDTOs) < issuesTotalCount {
		n := issuesOffset + len(issueDTOs)
		issuesNextOffset = &n
	}

	report := loadReportExtras(r.Context(), s.db, jobID)
	report["summary"] = summary
	report["pages"] = map[string]any{
		"results":    pageDTOs,
		"totalCount": pagesTotalCount,
		"nextOffset": pagesNextOffset,
		"limit":      pagesLimit,
		"offset":     pagesOffset,
	}
	report["issues"] = map[string]any{
		"results":    issueDTOs,
		"totalCount": issuesTotalCount,
		"nextOffset": issuesNextOffset,
		"limit":      issuesLimit,
		"offset":     issuesOffset,
	}

	writeJSON(w, http.StatusOK, report)
}

// handleJobPages handles GET /api/jobs/:jobId/pages?limit=100&offset=0
func (s *Server) handleJobPages(w http.ResponseWriter, r *http.Request, jobID string) {
	if s.db == nil {
		writeError(w, http.StatusInternalServerError, "database unavailable")
		return
	}
	_, err := s.db.GetJob(jobID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("job %q not found", jobID))
		return
	}

	limit := parsePaginationParam(r, "limit", 100, 200)
	offset := parseOffsetParam(r, "offset")

	filter := storage.QueryFilter{
		URLPattern:       r.URL.Query().Get("url_pattern"),
		URLGroup:         r.URL.Query().Get("url_group"),
		StatusCodeFamily: r.URL.Query().Get("status_code_family"),
	}

	result, err := s.db.QueryPagesOffset(jobID, filter, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("querying pages: %v", err))
		return
	}

	lookup := s.urlLookup()
	pageDTOs := make([]dto.PageDTO, 0, len(result.Results))
	for _, p := range result.Results {
		pageDTOs = append(pageDTOs, dto.PageFromStorage(p, lookup))
	}

	var nextOffset *int
	if offset+len(pageDTOs) < result.TotalCount {
		n := offset + len(pageDTOs)
		nextOffset = &n
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"results":    pageDTOs,
		"totalCount": result.TotalCount,
		"nextOffset": nextOffset,
		"limit":      limit,
		"offset":     offset,
	})
}

// handleJobIssues handles GET /api/jobs/:jobId/issues?limit=100&offset=0&issue_type=&severity=
func (s *Server) handleJobIssues(w http.ResponseWriter, r *http.Request, jobID string) {
	if s.db == nil {
		writeError(w, http.StatusInternalServerError, "database unavailable")
		return
	}
	_, err := s.db.GetJob(jobID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("job %q not found", jobID))
		return
	}

	limit := parsePaginationParam(r, "limit", 100, 500)
	offset := parseOffsetParam(r, "offset")

	filter := storage.QueryFilter{
		IssueType:  r.URL.Query().Get("issue_type"),
		Severity:   r.URL.Query().Get("severity"),
		URLPattern: r.URL.Query().Get("url_pattern"),
	}

	result, err := s.db.QueryIssuesOffset(jobID, filter, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("querying issues: %v", err))
		return
	}

	lookup := s.urlLookup()
	issueDTOs := make([]dto.IssueDTO, 0, len(result.Results))
	for _, i := range result.Results {
		issueDTOs = append(issueDTOs, dto.IssueFromStorage(i, lookup))
	}

	var nextOffset *int
	if offset+len(issueDTOs) < result.TotalCount {
		n := offset + len(issueDTOs)
		nextOffset = &n
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"results":    issueDTOs,
		"totalCount": result.TotalCount,
		"nextOffset": nextOffset,
		"limit":      limit,
		"offset":     offset,
	})
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

	// Build a map of visible jobID -> actual issue count from the issues table.
	// The job row's issues_found counter doesn't always reflect post-crawl phases
	// (text quality, axe audits, etc.), so query the source of truth, but only
	// for the page of jobs being returned. A global GROUP BY over historical
	// issues makes the All Reports tab slow once old crawls accumulate.
	jobIDs := make([]string, 0, len(jobs))
	for _, j := range jobs {
		jobIDs = append(jobIDs, j.ID)
	}
	actualIssues, err := s.db.CountIssuesByJobs(jobIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("counting visible job issues: %v", err))
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
