package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/parser"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/robots"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/sitemap"
	gomcp "github.com/mark3labs/mcp-go/mcp"
)

// crawlSiteArgs holds parsed arguments for crawl_site.
type crawlSiteArgs struct {
	URL               string   `json:"url"`
	URLs              []string `json:"urls,omitempty"`
	ScopeMode         string   `json:"scopeMode,omitempty"`
	AllowedHosts      []string `json:"allowedHosts,omitempty"`
	MaxPages          int      `json:"maxPages,omitempty"`
	MaxDepth          int      `json:"maxDepth,omitempty"`
	MaxDiscoveredURLs int      `json:"maxDiscoveredUrls,omitempty"`
	MaxOnboardedHosts int      `json:"maxOnboardedHosts,omitempty"`
	MaxCrawlDuration  string   `json:"maxCrawlDuration,omitempty"`
	RenderMode        string   `json:"renderMode,omitempty"`
	PSIMaxPages       *int     `json:"psiMaxPages,omitempty"`
	AxeMaxPages       *int     `json:"axeMaxPages,omitempty"`
	RespectRobots     *bool    `json:"respectRobots,omitempty"`
	DryRun            bool     `json:"dryRun,omitempty"`
}

// crawlSiteResult is returned from crawl_site.
type crawlSiteResult struct {
	JobID        string `json:"jobId"`
	Status       string `json:"status"`
	ResourceLink string `json:"resourceLink"`
}

// crawlStatusResult is returned from crawl_status.
type crawlStatusResult struct {
	JobID          string         `json:"jobId"`
	Status         string         `json:"status"`
	Type           string         `json:"type"`
	CreatedAt      string         `json:"createdAt"`
	StartedAt      string         `json:"startedAt,omitempty"`
	FinishedAt     string         `json:"finishedAt,omitempty"`
	Error          string         `json:"error,omitempty"`
	PagesCrawled   int            `json:"pagesCrawled"`
	URLsDiscovered int            `json:"urlsDiscovered"`
	IssuesFound    int            `json:"issuesFound"`
	URLsByStatus   map[string]int `json:"urlsByStatus,omitempty"`
	IssuesByType   map[string]int `json:"issuesByType,omitempty"`
}

func (s *Server) registerRun(jobID string, cancel context.CancelCauseFunc) {
	s.runMu.Lock()
	s.running[jobID] = cancel
	s.runMu.Unlock()
}

func (s *Server) unregisterRun(jobID string) {
	s.runMu.Lock()
	delete(s.running, jobID)
	s.runMu.Unlock()
}

func (s *Server) cancelRun(jobID string, cause error) bool {
	s.runMu.Lock()
	cancel := s.running[jobID]
	s.runMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel(cause)
	return true
}

func (s *Server) handleCrawlSite(ctx context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	args := req.GetArguments()

	rawURL, ok := args["url"].(string)
	if !ok || rawURL == "" {
		return gomcp.NewToolResultError("parameter \"url\" is required"), nil
	}

	normalizedURL, _, err := normalizeToolURL(rawURL)
	if err != nil {
		return gomcp.NewToolResultError(err.Error()), nil
	}

	// Parse optional scope mode and validate
	scopeMode := "registrable_domain"
	if sm, ok := args["scopeMode"].(string); ok && sm != "" {
		switch sm {
		case "registrable_domain", "exact_host", "allowlist":
			scopeMode = sm
		default:
			return gomcp.NewToolResultError(fmt.Sprintf("invalid scopeMode %q", sm)), nil
		}
	}

	// Parse optional numeric params
	maxPages := 10000
	if mp, ok := args["maxPages"].(float64); ok && mp > 0 {
		maxPages = int(mp)
	}
	const maxPagesLimit = 100000
	if maxPages > maxPagesLimit {
		maxPages = maxPagesLimit
	}

	maxDepth := 50
	if md, ok := args["maxDepth"].(float64); ok && md > 0 {
		maxDepth = int(md)
	}
	maxDiscoveredURLs := 100000
	if md, ok := args["maxDiscoveredUrls"].(float64); ok && md > 0 {
		maxDiscoveredURLs = int(md)
	}
	maxOnboardedHosts := 50
	if mh, ok := args["maxOnboardedHosts"].(float64); ok && mh > 0 {
		maxOnboardedHosts = int(mh)
	}
	maxCrawlDuration := "30m0s"
	if d, ok := args["maxCrawlDuration"].(string); ok && d != "" {
		if _, err := time.ParseDuration(d); err != nil {
			return gomcp.NewToolResultError(fmt.Sprintf("invalid maxCrawlDuration %q", d)), nil
		}
		maxCrawlDuration = d
	}
	var psiMaxPages *int
	if n, ok := args["psiMaxPages"].(float64); ok {
		if n < 0 {
			return gomcp.NewToolResultError("psiMaxPages must be >= 0"), nil
		}
		v := int(n)
		psiMaxPages = &v
	}
	var axeMaxPages *int
	if n, ok := args["axeMaxPages"].(float64); ok {
		if n < 0 {
			return gomcp.NewToolResultError("axeMaxPages must be >= 0"), nil
		}
		v := int(n)
		axeMaxPages = &v
	}

	renderMode := "static"
	if rm, ok := args["renderMode"].(string); ok && rm != "" {
		switch rm {
		case "static", "browser", "hybrid":
			renderMode = rm
		default:
			return gomcp.NewToolResultError(fmt.Sprintf("invalid renderMode %q", rm)), nil
		}
	}

	respectRobots := true
	if rr, ok := args["respectRobots"].(bool); ok {
		respectRobots = rr
	}

	dryRun := false
	if dr, ok := args["dryRun"].(bool); ok {
		dryRun = dr
	}

	// Collect additional URLs
	var additionalURLs []string
	if rawURLs, ok := args["urls"].([]any); ok {
		for _, u := range rawURLs {
			if us, ok := u.(string); ok {
				normalized, _, err := normalizeToolURL(us)
				if err != nil {
					return gomcp.NewToolResultError(err.Error()), nil
				}
				additionalURLs = append(additionalURLs, normalized)
			}
		}
	}

	var allowedHosts []string
	if rawHosts, ok := args["allowedHosts"].([]any); ok {
		for _, h := range rawHosts {
			if hs, ok := h.(string); ok {
				allowedHosts = append(allowedHosts, hs)
			}
		}
	}

	// If SEO_CRAWLER_HTTP_API is set, delegate to the remote HTTP server.
	if httpAPI := os.Getenv("SEO_CRAWLER_HTTP_API"); httpAPI != "" {
		return s.crawlSiteViaHTTP(ctx, crawlSiteArgs{
			URL:               normalizedURL,
			URLs:              additionalURLs,
			ScopeMode:         scopeMode,
			AllowedHosts:      allowedHosts,
			MaxPages:          maxPages,
			MaxDepth:          maxDepth,
			MaxDiscoveredURLs: maxDiscoveredURLs,
			MaxOnboardedHosts: maxOnboardedHosts,
			MaxCrawlDuration:  maxCrawlDuration,
			RenderMode:        renderMode,
			PSIMaxPages:       psiMaxPages,
			AxeMaxPages:       axeMaxPages,
			RespectRobots:     &respectRobots,
			DryRun:            dryRun,
		})
	}

	if s.db == nil {
		return gomcp.NewToolResultError("server not configured: database unavailable"), nil
	}

	// Job guard: check hourly rate limit
	maxJobsPerHour := 20
	if s.config != nil && s.config.MaxJobsPerHour > 0 {
		maxJobsPerHour = s.config.MaxJobsPerHour
	}
	hourAgo := time.Now().Add(-1 * time.Hour)
	recentJobs, err := s.db.CountJobsCreatedSince(hourAgo)
	if err != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("checking job rate limit: %v", err)), nil
	}
	if recentJobs >= maxJobsPerHour {
		return gomcp.NewToolResultError(fmt.Sprintf("rate limit: max %d jobs per hour (currently %d)", maxJobsPerHour, recentJobs)), nil
	}

	// Job guard: check concurrent crawl limit
	maxConcurrent := 3
	if s.config != nil {
		maxConcurrent = s.config.MaxConcurrentCrawls
	}
	activeCount, err := s.db.CountActiveJobs("crawl")
	if err != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("checking active jobs: %v", err)), nil
	}
	if activeCount >= maxConcurrent {
		return gomcp.NewToolResultError(fmt.Sprintf("concurrent crawl limit reached (%d/%d active)", activeCount, maxConcurrent)), nil
	}

	// Build config JSON
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
		"dryRun":            dryRun,
	}
	if psiMaxPages != nil {
		crawlConfig["psiMaxPages"] = *psiMaxPages
	}
	if axeMaxPages != nil {
		crawlConfig["axeMaxPages"] = *axeMaxPages
	}
	configJSON, err := json.Marshal(crawlConfig)
	if err != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("marshalling config: %v", err)), nil
	}

	// Build seed URLs list
	seedURLs := []string{normalizedURL}
	seedURLs = append(seedURLs, additionalURLs...)
	seedJSON, err := json.Marshal(seedURLs)
	if err != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("marshalling seed URLs: %v", err)), nil
	}

	job, err := s.db.CreateJob("crawl", string(configJSON), string(seedJSON))
	if err != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("creating job: %v", err)), nil
	}

	// Notify clients that the resource list changed (new job created)
	s.mcpServer.SendNotificationToAllClients(gomcp.MethodNotificationResourcesListChanged, nil)

	// Log the crawl start event
	s.logInfo(ctx, fmt.Sprintf("Created crawl job %s for %s (maxPages=%d, scope=%s)", job.ID, normalizedURL, maxPages, scopeMode))

	if dryRun {
		// Discovery-only: fetch seeds, parse links, check robots/sitemaps.
		return s.runDryRun(ctx, job.ID, seedURLs)
	}

	// Start crawl in background.
	if s.engine != nil {
		go func() {
			ctx, cancel := context.WithCancelCause(context.Background())
			s.registerRun(job.ID, cancel)
			defer s.unregisterRun(job.ID)
			defer cancel(nil)
			defer func() {
				if r := recover(); r != nil {
					msg := fmt.Sprintf("panic: %v", r)
					_ = s.db.UpdateJobFinished(job.ID, "failed", &msg)
				}
			}()
			if err := s.engine.RunCrawl(ctx, job.ID); err != nil && ctx.Err() == nil {
				msg := err.Error()
				_ = s.db.UpdateJobFinished(job.ID, "failed", &msg)
			}
			// Notify clients when crawl completes
			s.mcpServer.SendNotificationToAllClients(gomcp.MethodNotificationResourceUpdated, map[string]any{
				"uri": fmt.Sprintf("seo-crawler://jobs/%s", job.ID),
			})
		}()
	}

	result := crawlSiteResult{
		JobID:        job.ID,
		Status:       job.Status,
		ResourceLink: fmt.Sprintf("seo-crawler://jobs/%s", job.ID),
	}

	return gomcp.NewToolResultJSON(result)
}

func (s *Server) handleCrawlStatus(ctx context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	args := req.GetArguments()

	jobID, ok := args["jobId"].(string)
	if !ok || jobID == "" {
		return gomcp.NewToolResultError("parameter \"jobId\" is required"), nil
	}

	if s.db == nil {
		return gomcp.NewToolResultError("server not configured: database unavailable"), nil
	}

	job, err := s.db.GetJob(jobID)
	if err != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("job %q: %v", jobID, err)), nil
	}

	result := crawlStatusResult{
		JobID:          job.ID,
		Status:         job.Status,
		Type:           job.Type,
		CreatedAt:      job.CreatedAt,
		PagesCrawled:   job.PagesCrawled,
		URLsDiscovered: job.URLsDiscovered,
		IssuesFound:    job.IssuesFound,
	}

	if job.StartedAt.Valid {
		result.StartedAt = job.StartedAt.String
	}
	if job.FinishedAt.Valid {
		result.FinishedAt = job.FinishedAt.String
	}
	if job.Error.Valid {
		result.Error = job.Error.String
	}

	// URL counts by status
	urlCounts, err := s.db.CountURLsByStatus(jobID)
	if err == nil {
		result.URLsByStatus = urlCounts
	}

	// Issue counts by type
	issueCounts, err := s.db.CountIssuesByType(jobID)
	if err == nil {
		result.IssuesByType = issueCounts
	}

	return gomcp.NewToolResultJSON(result)
}

// dryRunResult is returned when dryRun is true.
type dryRunResult struct {
	JobID             string            `json:"jobId"`
	Status            string            `json:"status"`
	EstimatedURLs     int               `json:"estimatedUrls"`
	HostsDiscovered   []string          `json:"hostsDiscovered"`
	SitemapEntryCount int               `json:"sitemapEntryCount"`
	SeedIssues        []dryRunSeedIssue `json:"seedIssues"`
}

type dryRunSeedIssue struct {
	URL   string `json:"url"`
	Issue string `json:"issue"`
}

func (s *Server) runDryRun(ctx context.Context, jobID string, seedURLs []string) (*gomcp.CallToolResult, error) {
	result := dryRunResult{
		JobID:           jobID,
		Status:          "completed",
		HostsDiscovered: []string{},
		SeedIssues:      []dryRunSeedIssue{},
	}

	hostsSeen := map[string]bool{}
	allLinks := map[string]bool{}

	for _, seedURL := range seedURLs {
		parsed, err := url.Parse(seedURL)
		if err != nil {
			result.SeedIssues = append(result.SeedIssues, dryRunSeedIssue{URL: seedURL, Issue: fmt.Sprintf("invalid URL: %v", err)})
			continue
		}
		hostsSeen[parsed.Hostname()] = true

		if s.fetcher == nil {
			result.SeedIssues = append(result.SeedIssues, dryRunSeedIssue{URL: seedURL, Issue: "fetcher unavailable"})
			continue
		}

		fetchResult, err := s.fetcher.FetchContext(ctx, seedURL)
		if err != nil {
			result.SeedIssues = append(result.SeedIssues, dryRunSeedIssue{URL: seedURL, Issue: fmt.Sprintf("fetch error: %v", err)})
			continue
		}
		if fetchResult.StatusCode >= 400 {
			result.SeedIssues = append(result.SeedIssues, dryRunSeedIssue{URL: seedURL, Issue: fmt.Sprintf("HTTP %d", fetchResult.StatusCode)})
			continue
		}

		// Parse HTML to extract links.
		parseResult, parseErr := parser.ParseHTML(fetchResult.Body, fetchResult.FinalURL, fetchResult.ResponseHeaders)
		if parseErr != nil {
			result.SeedIssues = append(result.SeedIssues, dryRunSeedIssue{URL: seedURL, Issue: fmt.Sprintf("parse error: %v", parseErr)})
			continue
		}

		for _, link := range parseResult.Links {
			allLinks[link.URL] = true
			if lp, lpErr := url.Parse(link.URL); lpErr == nil && lp.Hostname() != "" {
				hostsSeen[lp.Hostname()] = true
			}
		}
	}

	// Fetch robots.txt and sitemaps for discovered hosts.
	var client *http.Client
	if s.fetcher != nil {
		client = s.fetcher.SafeClient()
	} else {
		client = &http.Client{}
	}

	sitemapTotal := 0
	for host := range hostsSeen {
		// Fetch robots.txt.
		robotsURL := fmt.Sprintf("https://%s/robots.txt", host)
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, nil)
		if reqErr != nil {
			continue
		}
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == 200 {
			bodyBytes := make([]byte, 0, 32*1024)
			buf := make([]byte, 4096)
			for {
				n, readErr := resp.Body.Read(buf)
				if n > 0 {
					bodyBytes = append(bodyBytes, buf[:n]...)
				}
				if readErr != nil {
					break
				}
				if len(bodyBytes) > 512*1024 {
					break
				}
			}
			resp.Body.Close()

			rf, parseErr := robots.Parse(string(bodyBytes))
			if parseErr == nil {
				for _, smURL := range rf.Sitemaps {
					entries, _, smErr := sitemap.FetchAndParseContext(ctx, smURL, 10000, client)
					if smErr == nil {
						sitemapTotal += len(entries)
						for _, e := range entries {
							allLinks[e.Loc] = true
						}
					}
				}
			}
		} else if resp != nil {
			resp.Body.Close()
		}
	}

	result.EstimatedURLs = len(allLinks)
	result.SitemapEntryCount = sitemapTotal
	for host := range hostsSeen {
		result.HostsDiscovered = append(result.HostsDiscovered, host)
	}

	// Mark job completed.
	if s.db != nil {
		_ = s.db.UpdateJobFinished(jobID, "completed", nil)
	}

	return gomcp.NewToolResultJSON(result)
}

func (s *Server) handleCancelCrawl(ctx context.Context, req gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
	args := req.GetArguments()

	jobID, ok := args["jobId"].(string)
	if !ok || jobID == "" {
		return gomcp.NewToolResultError("parameter \"jobId\" is required"), nil
	}

	if s.db == nil {
		return gomcp.NewToolResultError("server not configured: database unavailable"), nil
	}

	job, err := s.db.GetJob(jobID)
	if err != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("job %q: %v", jobID, err)), nil
	}

	// Only running/queued jobs can be cancelled.
	if job.Status != "running" && job.Status != "queued" && job.Status != "cancelling" {
		return gomcp.NewToolResultError(fmt.Sprintf("job %q has status %q, only running or queued jobs can be cancelled", jobID, job.Status)), nil
	}

	status := "cancelling"
	if job.Status == "queued" {
		// A queued MCP job may or may not already have a background goroutine
		// registered. If a run exists, cancel it and let Engine.RunCrawl observe
		// the cancelling state. If no run exists (for example, no engine is
		// configured), finish the job as cancelled immediately instead of
		// stranding it in cancelling forever.
		if s.cancelRun(jobID, context.Canceled) {
			if err := s.db.UpdateJobStatus(jobID, "cancelling"); err != nil {
				return gomcp.NewToolResultError(fmt.Sprintf("cancelling job %q: %v", jobID, err)), nil
			}
		} else {
			msg := context.Canceled.Error()
			if err := s.db.UpdateJobFinished(jobID, "cancelled", &msg); err != nil {
				return gomcp.NewToolResultError(fmt.Sprintf("cancelling queued job %q: %v", jobID, err)), nil
			}
			status = "cancelled"
		}
	} else {
		if err := s.db.UpdateJobStatus(jobID, "cancelling"); err != nil {
			return gomcp.NewToolResultError(fmt.Sprintf("cancelling job %q: %v", jobID, err)), nil
		}
		s.cancelRun(jobID, context.Canceled)
	}

	// Notify clients that the job resource was updated
	s.mcpServer.SendNotificationToAllClients(gomcp.MethodNotificationResourceUpdated, map[string]any{
		"uri": fmt.Sprintf("seo-crawler://jobs/%s", jobID),
	})

	// Log the cancellation event
	s.logInfo(ctx, fmt.Sprintf("Cancelling crawl job %s", jobID))

	result := map[string]string{
		"jobId":  jobID,
		"status": status,
	}
	return gomcp.NewToolResultJSON(result)
}

// crawlSiteViaHTTP delegates a crawl_site call to the remote HTTP API configured
// via the SEO_CRAWLER_HTTP_API environment variable. This makes the local MCP
// server a thin client, forwarding crawl requests to the cloud instance.
func (s *Server) crawlSiteViaHTTP(
	ctx context.Context,
	args crawlSiteArgs,
) (*gomcp.CallToolResult, error) {
	httpAPI := os.Getenv("SEO_CRAWLER_HTTP_API")

	body := map[string]any{
		"url":               args.URL,
		"scopeMode":         args.ScopeMode,
		"allowedHosts":      args.AllowedHosts,
		"maxPages":          args.MaxPages,
		"maxDepth":          args.MaxDepth,
		"maxDiscoveredUrls": args.MaxDiscoveredURLs,
		"maxOnboardedHosts": args.MaxOnboardedHosts,
		"maxCrawlDuration":  args.MaxCrawlDuration,
		"renderMode":        args.RenderMode,
		"respectRobots":     true,
		"dryRun":            args.DryRun,
	}
	if args.PSIMaxPages != nil {
		body["psiMaxPages"] = *args.PSIMaxPages
	}
	if args.AxeMaxPages != nil {
		body["axeMaxPages"] = *args.AxeMaxPages
	}
	if args.RespectRobots != nil {
		body["respectRobots"] = *args.RespectRobots
	}
	if len(args.URLs) > 0 {
		body["urls"] = args.URLs
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("marshalling request body: %v", err)), nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpAPI+"/api/crawl", bytes.NewReader(bodyBytes))
	if err != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("creating HTTP request: %v", err)), nil
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("calling HTTP API at %s: %v", httpAPI, err)), nil
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(respBody, &errResp)
		msg := errResp.Error
		if msg == "" {
			msg = string(respBody)
		}
		return gomcp.NewToolResultError(fmt.Sprintf("HTTP API returned %d: %s", resp.StatusCode, msg)), nil
	}

	var apiResp struct {
		JobID  string `json:"jobId"`
		Status string `json:"status"`
		Error  string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return gomcp.NewToolResultError(fmt.Sprintf("decoding HTTP API response: %v", err)), nil
	}
	if apiResp.Error != "" {
		return gomcp.NewToolResultError(apiResp.Error), nil
	}

	out, _ := json.Marshal(crawlSiteResult{
		JobID:        apiResp.JobID,
		Status:       apiResp.Status,
		ResourceLink: fmt.Sprintf("seo-crawler://jobs/%s", apiResp.JobID),
	})
	return gomcp.NewToolResultText(string(out)), nil
}
