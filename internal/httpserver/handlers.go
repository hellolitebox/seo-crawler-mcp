package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/dto"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/ssrf"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/urlutil"
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

func writePagedOffsetJSON[T any](w http.ResponseWriter, results []T, totalCount, limit, offset int) {
	var nextOffset *int
	if offset+len(results) < totalCount {
		n := offset + len(results)
		nextOffset = &n
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results":    results,
		"totalCount": totalCount,
		"nextOffset": nextOffset,
		"limit":      limit,
		"offset":     offset,
	})
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
		URL               string   `json:"url"`
		URLs              []string `json:"urls"`
		ScopeMode         string   `json:"scopeMode"`
		AllowedHosts      []string `json:"allowedHosts"`
		MaxPages          int      `json:"maxPages"`
		MaxDepth          int      `json:"maxDepth"`
		MaxDiscoveredURLs int      `json:"maxDiscoveredUrls"`
		MaxOnboardedHosts int      `json:"maxOnboardedHosts"`
		MaxCrawlDuration  string   `json:"maxCrawlDuration"`
		RenderMode        string   `json:"renderMode"`
		RespectRobots     *bool    `json:"respectRobots"`
		DryRun            bool     `json:"dryRun"`
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
	seedURLs := []string{body.URL}
	for _, rawSeed := range body.URLs {
		normalizedSeed, parsedSeed, seedErr := normalizeCrawlURL(rawSeed)
		if seedErr != nil {
			writeError(w, http.StatusBadRequest, seedErr.Error())
			return
		}
		if s.config == nil || s.config.SSRFProtection {
			allowPrivate := false
			if s.config != nil {
				allowPrivate = s.config.AllowPrivateNetworks
			}
			guard := ssrf.NewGuard(allowPrivate)
			if err := guard.ValidateURL(normalizedSeed); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if err := guard.ValidateHost(parsedSeed.Hostname()); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		seedURLs = append(seedURLs, normalizedSeed)
	}
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
	if s.config != nil && s.config.MaxPages > 0 {
		maxPages = s.config.MaxPages
	}
	if body.MaxPages > 0 {
		maxPages = body.MaxPages
		if maxPages > 100000 {
			maxPages = 100000
		}
	}

	maxDepth := 50
	if s.config != nil && s.config.MaxDepth > 0 {
		maxDepth = s.config.MaxDepth
	}
	if body.MaxDepth > 0 {
		maxDepth = body.MaxDepth
	}
	maxDiscoveredURLs := 100000
	if s.config != nil && s.config.MaxDiscoveredURLs > 0 {
		maxDiscoveredURLs = s.config.MaxDiscoveredURLs
	}
	if body.MaxDiscoveredURLs > 0 {
		maxDiscoveredURLs = body.MaxDiscoveredURLs
	}
	maxOnboardedHosts := 50
	if s.config != nil && s.config.MaxOnboardedHosts > 0 {
		maxOnboardedHosts = s.config.MaxOnboardedHosts
	}
	if body.MaxOnboardedHosts > 0 {
		maxOnboardedHosts = body.MaxOnboardedHosts
	}
	maxCrawlDuration := "30m0s"
	if s.config != nil && s.config.MaxCrawlDuration > 0 {
		maxCrawlDuration = s.config.MaxCrawlDuration.String()
	}
	if body.MaxCrawlDuration != "" {
		if _, err := time.ParseDuration(body.MaxCrawlDuration); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid maxCrawlDuration %q", body.MaxCrawlDuration))
			return
		}
		maxCrawlDuration = body.MaxCrawlDuration
	}

	scopeMode := "registrable_domain"
	if s.config != nil && s.config.ScopeMode != "" {
		scopeMode = string(s.config.ScopeMode)
	}
	if body.ScopeMode != "" {
		switch body.ScopeMode {
		case "registrable_domain", "exact_host", "allowlist":
			scopeMode = body.ScopeMode
		default:
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid scopeMode %q", body.ScopeMode))
			return
		}
	}

	allowedHosts := []string{}
	if s.config != nil && len(s.config.AllowedHosts) > 0 {
		allowedHosts = append(allowedHosts, s.config.AllowedHosts...)
	}
	if len(body.AllowedHosts) > 0 {
		allowedHosts = append([]string{}, body.AllowedHosts...)
	}

	respectRobots := true
	if s.config != nil {
		respectRobots = s.config.RespectRobots
	}
	if body.RespectRobots != nil {
		respectRobots = *body.RespectRobots
	}

	renderMode := "static"
	if s.config != nil && s.config.RenderMode != "" {
		renderMode = string(s.config.RenderMode)
	}
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
		"scopeMode":         scopeMode,
		"allowedHosts":      allowedHosts,
		"maxPages":          maxPages,
		"maxDepth":          maxDepth,
		"maxDiscoveredUrls": maxDiscoveredURLs,
		"maxOnboardedHosts": maxOnboardedHosts,
		"maxCrawlDuration":  maxCrawlDuration,
		"renderMode":        renderMode,
		"respectRobots":     respectRobots,
		"dryRun":            body.DryRun,
	}
	configJSON, err := json.Marshal(crawlConfig)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("marshalling config: %v", err))
		return
	}

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
func (s *Server) handleJobEdgesV2(w http.ResponseWriter, r *http.Request) {
	s.handleJobEdges(w, r, r.PathValue("id"))
}
func (s *Server) handleJobResponseCodesV2(w http.ResponseWriter, r *http.Request) {
	s.handleJobResponseCodes(w, r, r.PathValue("id"))
}
func (s *Server) handleJobAssetsV2(w http.ResponseWriter, r *http.Request) {
	s.handleJobAssets(w, r, r.PathValue("id"))
}
func (s *Server) handleJobSitemapEntriesV2(w http.ResponseWriter, r *http.Request) {
	s.handleJobSitemapEntries(w, r, r.PathValue("id"))
}
func (s *Server) handleJobSecurityV2(w http.ResponseWriter, r *http.Request) {
	s.handleJobSecurity(w, r, r.PathValue("id"))
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
	sitemapKeys := loadSitemapURLKeys(r.Context(), s.db, jobID)

	// Pages are an SEO content inventory. Error/redirect responses are still
	// reported through fetches, response codes, links, and issues, but their HTML
	// body is usually a shared error template rather than content for that URL.
	pagesResult, err := s.db.QueryPagesOffset(jobID, storage.QueryFilter{StatusCodeFamily: "2xx"}, pagesLimit, pagesOffset)
	var pageDTOs []dto.PageDTO
	var pagesTotalCount int
	if err == nil {
		pagesTotalCount = pagesResult.TotalCount
		pageDTOs = make([]dto.PageDTO, 0, len(pagesResult.Results))
		for _, p := range pagesResult.Results {
			pageDTO := dto.PageFromStorage(p, lookup)
			pageDTO.InSitemap = urlIsInSitemap(pageDTO.URL, sitemapKeys)
			pageDTOs = append(pageDTOs, pageDTO)
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

	statusCodeFamily := r.URL.Query().Get("status_code_family")
	if statusCodeFamily == "" {
		statusCodeFamily = "2xx"
	}

	filter := storage.QueryFilter{
		URLPattern:       r.URL.Query().Get("url_pattern"),
		URLGroup:         r.URL.Query().Get("url_group"),
		StatusCodeFamily: statusCodeFamily,
		Indexability:     r.URL.Query().Get("indexability"),
		IssuePresence:    r.URL.Query().Get("issue_presence"),
		SortBy:           r.URL.Query().Get("sort_by"),
		SortDir:          r.URL.Query().Get("sort_dir"),
	}

	result, err := s.db.QueryPagesOffset(jobID, filter, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("querying pages: %v", err))
		return
	}

	lookup := s.urlLookup()
	sitemapKeys := loadSitemapURLKeys(r.Context(), s.db, jobID)
	pageDTOs := make([]dto.PageDTO, 0, len(result.Results))
	for _, p := range result.Results {
		pageDTO := dto.PageFromStorage(p, lookup)
		pageDTO.InSitemap = urlIsInSitemap(pageDTO.URL, sitemapKeys)
		pageDTOs = append(pageDTOs, pageDTO)
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
		SortBy:     r.URL.Query().Get("sort_by"),
		SortDir:    r.URL.Query().Get("sort_dir"),
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

// handleJobEdges handles GET /api/jobs/:jobId/edges.
func (s *Server) handleJobEdges(w http.ResponseWriter, r *http.Request, jobID string) {
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
		RelationType: r.URL.Query().Get("relation_type"),
		URLPattern:   r.URL.Query().Get("url_pattern"),
		TargetDomain: r.URL.Query().Get("target_domain"),
		SortBy:       r.URL.Query().Get("sort_by"),
		SortDir:      r.URL.Query().Get("sort_dir"),
	}
	if raw := r.URL.Query().Get("internal"); raw != "" {
		isInternal := raw == "1" || strings.EqualFold(raw, "true")
		filter.IsInternal = &isInternal
	}
	result, err := s.db.QueryEdgesOffset(jobID, filter, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("querying edges: %v", err))
		return
	}
	lookup := s.urlLookup()
	edgeDTOs := make([]dto.EdgeDTO, 0, len(result.Results))
	for _, edge := range result.Results {
		edgeDTOs = append(edgeDTOs, dto.EdgeFromStorage(edge, lookup))
	}
	writePagedOffsetJSON(w, edgeDTOs, result.TotalCount, limit, offset)
}

// handleJobResponseCodes handles GET /api/jobs/:jobId/response-codes.
func (s *Server) handleJobResponseCodes(w http.ResponseWriter, r *http.Request, jobID string) {
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
		StatusCodeFamily: r.URL.Query().Get("status_code_family"),
		ContentType:      r.URL.Query().Get("content_type"),
		URLPattern:       r.URL.Query().Get("url_pattern"),
		SortBy:           r.URL.Query().Get("sort_by"),
		SortDir:          r.URL.Query().Get("sort_dir"),
	}
	result, err := s.db.QueryResponseCodesOffset(jobID, filter, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("querying response codes: %v", err))
		return
	}
	lookup := s.urlLookup()
	fetchDTOs := make([]dto.FetchDTO, 0, len(result.Results))
	for _, fetch := range result.Results {
		fetchDTOs = append(fetchDTOs, dto.FetchFromStorage(fetch, lookup))
	}
	writePagedOffsetJSON(w, fetchDTOs, result.TotalCount, limit, offset)
}

// handleJobAssets handles GET /api/jobs/:jobId/assets.
func (s *Server) handleJobAssets(w http.ResponseWriter, r *http.Request, jobID string) {
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
		URLPattern:       r.URL.Query().Get("url_pattern"),
		ContentType:      r.URL.Query().Get("content_type"),
		StatusCodeFamily: r.URL.Query().Get("status_code_family"),
		SortBy:           r.URL.Query().Get("sort_by"),
		SortDir:          r.URL.Query().Get("sort_dir"),
	}
	result, err := s.db.QueryAssetsOffset(jobID, filter, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("querying assets: %v", err))
		return
	}
	lookup := s.urlLookup()
	assetRows := make([]map[string]any, 0, len(result.Results))
	for _, asset := range result.Results {
		assetRows = append(assetRows, map[string]any{
			"id":              asset.ID,
			"jobId":           asset.JobID,
			"urlId":           asset.URLID,
			"url":             lookup(asset.URLID),
			"contentType":     nullString(asset.ContentType),
			"contentEncoding": nullString(asset.ContentEncoding),
			"cacheControl":    nullString(asset.CacheControl),
			"transferSize":    nullInt64(asset.TransferSize),
			"decodedSize":     nullInt64(asset.DecodedSize),
			"statusCode":      nullInt64(asset.StatusCode),
			"contentLength":   nullInt64(asset.ContentLength),
		})
	}
	writePagedOffsetJSON(w, assetRows, result.TotalCount, limit, offset)
}

// handleJobSitemapEntries handles GET /api/jobs/:jobId/sitemap-entries.
func (s *Server) handleJobSitemapEntries(w http.ResponseWriter, r *http.Request, jobID string) {
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
		URLPattern:       r.URL.Query().Get("url_pattern"),
		StatusCodeFamily: r.URL.Query().Get("status"),
		SortBy:           r.URL.Query().Get("sort_by"),
		SortDir:          r.URL.Query().Get("sort_dir"),
	}
	result, err := s.db.QuerySitemapEntriesOffset(jobID, filter, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("querying sitemap entries: %v", err))
		return
	}
	rows := make([]map[string]any, 0, len(result.Results))
	for _, entry := range result.Results {
		rows = append(rows, map[string]any{
			"id":                   entry.ID,
			"jobId":                entry.JobID,
			"url":                  entry.URL,
			"sourceSitemapUrl":     entry.SourceSitemapURL,
			"sourceHost":           entry.SourceHost,
			"lastmod":              nullString(entry.Lastmod),
			"changefreq":           nullString(entry.Changefreq),
			"priority":             nullFloat64(entry.Priority),
			"reconciliationStatus": entry.ReconciliationStatus,
		})
	}
	writePagedOffsetJSON(w, rows, result.TotalCount, limit, offset)
}

// handleJobSecurity handles GET /api/jobs/:jobId/security.
func (s *Server) handleJobSecurity(w http.ResponseWriter, r *http.Request, jobID string) {
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
	rows, total, err := querySecurityHeadersOffset(
		r.Context(),
		s.db,
		jobID,
		r.URL.Query().Get("url_pattern"),
		r.URL.Query().Get("status_code_family"),
		r.URL.Query().Get("sort_by"),
		r.URL.Query().Get("sort_dir"),
		limit,
		offset,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("querying security headers: %v", err))
		return
	}
	writePagedOffsetJSON(w, rows, total, limit, offset)
}

func querySecurityHeadersOffset(
	ctx context.Context,
	db *storage.DB,
	jobID string,
	urlPattern string,
	statusCodeFamily string,
	sortBy string,
	sortDir string,
	limit int,
	offset int,
) ([]map[string]any, int, error) {
	var filterClause strings.Builder
	filterArgs := []any{}
	if urlPattern != "" {
		filterClause.WriteString(" AND u.normalized_url LIKE ?")
		filterArgs = append(filterArgs, "%"+urlPattern+"%")
	}
	if statusCodeFamily != "" {
		lo, hi, err := httpStatusFamilyRange(statusCodeFamily)
		if err != nil {
			return nil, 0, err
		}
		filterClause.WriteString(" AND f.status_code >= ? AND f.status_code <= ?")
		filterArgs = append(filterArgs, lo, hi)
	}

	countArgs := append([]any{jobID}, filterArgs...)
	countSQL := "SELECT COUNT(*) FROM fetches f JOIN urls u ON u.id = f.requested_url_id WHERE f.job_id = ?" + filterClause.String()
	var totalCount int
	if err := db.QueryRowContext(ctx, countSQL, countArgs...).Scan(&totalCount); err != nil {
		return nil, 0, err
	}

	orderExpr := "f.id"
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case "url":
		orderExpr = "u.normalized_url"
	case "status":
		orderExpr = "f.status_code"
	case "missing":
		orderExpr = "missing_count"
	}
	direction := "ASC"
	if strings.EqualFold(sortDir, "desc") {
		direction = "DESC"
	}

	selectSQL := `
		SELECT u.normalized_url, f.status_code, f.response_headers_json,
		       ((CASE WHEN lower(COALESCE(f.response_headers_json, '')) LIKE '%strict-transport-security%' THEN 0 ELSE 1 END) +
		        (CASE WHEN lower(COALESCE(f.response_headers_json, '')) LIKE '%content-security-policy%' THEN 0 ELSE 1 END) +
		        (CASE WHEN lower(COALESCE(f.response_headers_json, '')) LIKE '%x-content-type-options%' THEN 0 ELSE 1 END) +
		        (CASE WHEN lower(COALESCE(f.response_headers_json, '')) LIKE '%x-frame-options%' THEN 0 ELSE 1 END) +
		        (CASE WHEN lower(COALESCE(f.response_headers_json, '')) LIKE '%referrer-policy%' THEN 0 ELSE 1 END) +
		        (CASE WHEN lower(COALESCE(f.response_headers_json, '')) LIKE '%x-xss-protection%' THEN 0 ELSE 1 END) +
		        (CASE WHEN lower(COALESCE(f.response_headers_json, '')) LIKE '%permissions-policy%' THEN 0 ELSE 1 END)) AS missing_count
		FROM fetches f JOIN urls u ON u.id = f.requested_url_id
		WHERE f.job_id = ?` + filterClause.String() +
		" ORDER BY " + orderExpr + " " + direction + ", f.id ASC LIMIT ? OFFSET ?"
	queryArgs := append(append([]any{jobID}, filterArgs...), limit, offset)
	queryRows, err := db.QueryContext(ctx, selectSQL, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer queryRows.Close()

	results := []map[string]any{}
	for queryRows.Next() {
		var (
			urlValue    string
			statusCode  sql.NullInt64
			headersJSON sql.NullString
			missing     int
		)
		if err := queryRows.Scan(&urlValue, &statusCode, &headersJSON, &missing); err != nil {
			return nil, 0, err
		}
		results = append(results, map[string]any{
			"url":          urlValue,
			"statusCode":   nullInt64(statusCode),
			"headers":      buildSecurityHeaderSnapshot(headersJSON),
			"missingCount": missing,
		})
	}
	if err := queryRows.Err(); err != nil {
		return nil, 0, err
	}
	return results, totalCount, nil
}

func httpStatusFamilyRange(family string) (int, int, error) {
	switch strings.ToLower(strings.TrimSpace(family)) {
	case "", "all":
		return 0, 999, nil
	case "2xx":
		return 200, 299, nil
	case "3xx":
		return 300, 399, nil
	case "4xx":
		return 400, 499, nil
	case "5xx":
		return 500, 599, nil
	default:
		return 0, 0, fmt.Errorf("invalid status_code_family %q", family)
	}
}

func loadSitemapURLKeys(ctx context.Context, db *storage.DB, jobID string) map[string]bool {
	keys := map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT url FROM sitemap_entries WHERE job_id = ?`, jobID)
	if err != nil {
		return keys
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		for _, key := range sitemapURLLookupKeys(raw) {
			keys[key] = true
		}
	}
	return keys
}

func urlIsInSitemap(raw string, keys map[string]bool) bool {
	for _, key := range sitemapURLLookupKeys(raw) {
		if keys[key] {
			return true
		}
	}
	return false
}

func sitemapURLLookupKeys(raw string) []string {
	keySet := map[string]bool{}
	trimmed := strings.TrimSpace(raw)
	if trimmed != "" {
		keySet[trimmed] = true
		if pageKey, err := urlutil.PageIdentityKey(trimmed); err == nil && pageKey != "" {
			keySet[pageKey] = true
		}
	}
	keys := make([]string, 0, len(keySet))
	for key := range keySet {
		keys = append(keys, key)
	}
	return keys
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
		SELECT f.id, u.normalized_url, f.status_code, f.ttfb_ms, f.fetched_at, f.render_mode, f.error
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
		ID         int64   `json:"id,omitempty"`
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
	var latestFetchID int64
	var latestEventID int64
	for rows.Next() {
		var ar activityRow
		ar.Kind = "fetch"
		var errStr sql.NullString
		var renderMode sql.NullString
		if scanErr := rows.Scan(&ar.ID, &ar.URL, &ar.StatusCode, &ar.TTFBMs, &ar.FetchedAt, &renderMode, &errStr); scanErr != nil {
			continue
		}
		if errStr.Valid {
			s := errStr.String
			ar.Error = &s
		}
		if renderMode.Valid {
			ar.RenderMode = renderMode.String
		}
		if ar.ID > latestFetchID {
			latestFetchID = ar.ID
		}
		out = append(out, ar)
	}

	// Also include phase events (post-crawl markers so the log doesn't look stuck)
	eventRows, eventErr := s.db.Query(`
		SELECT id, timestamp, details_json
		FROM crawl_events
		WHERE job_id = ? AND event_type = 'phase'
		ORDER BY id DESC
		LIMIT ?
	`, jobID, limit)
	if eventErr == nil {
		defer eventRows.Close()
		for eventRows.Next() {
			var id int64
			var ts string
			var detailsJSON sql.NullString
			if scanErr := eventRows.Scan(&id, &ts, &detailsJSON); scanErr != nil {
				continue
			}
			row := activityRow{Kind: "phase", ID: id, FetchedAt: ts}
			if row.ID > latestEventID {
				latestEventID = row.ID
			}
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
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].FetchedAt == out[j].FetchedAt {
			if out[i].Kind == out[j].Kind {
				return out[i].ID > out[j].ID
			}
			return out[i].Kind < out[j].Kind
		}
		return out[i].FetchedAt > out[j].FetchedAt
	})
	if len(out) > limit {
		out = out[:limit]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"activity":      out,
		"latestFetchId": latestFetchID,
		"latestEventId": latestEventID,
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
