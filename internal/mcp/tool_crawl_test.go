package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/engine"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/fetcher"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/urlutil"
	gomcp "github.com/mark3labs/mcp-go/mcp"
)

func setupTestDB(t *testing.T) *storage.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func callTool(t *testing.T, s *Server, args map[string]any) *gomcp.CallToolResult {
	t.Helper()
	req := gomcp.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleCrawlSite(context.Background(), req)
	if err != nil {
		t.Fatalf("handleCrawlSite returned error: %v", err)
	}
	return result
}

func TestCrawlSite_CreatesJob(t *testing.T) {
	db := setupTestDB(t)
	cfg := config.DefaultConfig()

	s := NewServer(ServerConfig{
		DB:     db,
		Config: &cfg,
	})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"url":  "example.com",
		"urls": []any{"www.example.com/about"},
	}

	result, err := s.handleCrawlSite(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}

	// Parse result to get job ID
	var res crawlSiteResult
	for _, content := range result.Content {
		if tc, ok := content.(gomcp.TextContent); ok {
			if err := json.Unmarshal([]byte(tc.Text), &res); err != nil {
				t.Fatalf("parsing result: %v", err)
			}
		}
	}

	if res.JobID == "" {
		t.Fatal("expected non-empty job ID")
	}
	if res.Status != "queued" {
		t.Errorf("expected status %q, got %q", "queued", res.Status)
	}
	if res.ResourceLink == "" {
		t.Error("expected non-empty resource link")
	}

	// Verify job exists in DB
	job, err := db.GetJob(res.JobID)
	if err != nil {
		t.Fatalf("getting job from DB: %v", err)
	}
	if job.Type != "crawl" {
		t.Errorf("expected job type %q, got %q", "crawl", job.Type)
	}
	var seeds []string
	if err := json.Unmarshal([]byte(job.SeedURLs), &seeds); err != nil {
		t.Fatalf("parsing seed URLs: %v", err)
	}
	wantSeeds := []string{"https://example.com", "https://www.example.com/about"}
	if fmt.Sprint(seeds) != fmt.Sprint(wantSeeds) {
		t.Fatalf("seed URLs = %v, want %v", seeds, wantSeeds)
	}
}

func TestCrawlSite_RequiresURL(t *testing.T) {
	db := setupTestDB(t)
	s := NewServer(ServerConfig{DB: db})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := s.handleCrawlSite(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error result for missing URL")
	}
}

func TestCrawlSite_InvalidURL(t *testing.T) {
	db := setupTestDB(t)
	s := NewServer(ServerConfig{DB: db})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"url": "ftp://example.com",
	}

	result, err := s.handleCrawlSite(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error result for invalid URL")
	}
}

func TestCrawlSite_InvalidScopeMode(t *testing.T) {
	db := setupTestDB(t)
	s := NewServer(ServerConfig{DB: db})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"url":       "https://example.com",
		"scopeMode": "invalid_mode",
	}

	result, err := s.handleCrawlSite(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error result for invalid scope mode")
	}
}

func TestCrawlSiteViaHTTPForwardsCrawlSettings(t *testing.T) {
	var got map[string]any
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/crawl" {
			t.Fatalf("path = %q, want /api/crawl", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, "{\"jobId\":\"job-http\",\"status\":\"queued\"}")
	}))
	defer api.Close()
	t.Setenv("SEO_CRAWLER_HTTP_API", api.URL)
	defer os.Unsetenv("SEO_CRAWLER_HTTP_API")

	s := NewServer(ServerConfig{})
	result := callTool(t, s, map[string]any{
		"url":           "https://example.com",
		"urls":          []any{"https://docs.example.com/start"},
		"scopeMode":     "allowlist",
		"allowedHosts":  []any{"example.com", "docs.example.com"},
		"maxPages":      float64(42),
		"maxDepth":      float64(4),
		"renderMode":    "hybrid",
		"respectRobots": false,
		"dryRun":        true,
	})
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}

	want := map[string]any{
		"url":           "https://example.com",
		"scopeMode":     "allowlist",
		"maxPages":      float64(42),
		"maxDepth":      float64(4),
		"renderMode":    "hybrid",
		"respectRobots": false,
		"dryRun":        true,
	}
	for key, expected := range want {
		if got[key] != expected {
			t.Fatalf("%s = %#v, want %#v; body=%#v", key, got[key], expected, got)
		}
	}
	urls, ok := got["urls"].([]any)
	if !ok || len(urls) != 1 || urls[0] != "https://docs.example.com/start" {
		t.Fatalf("urls = %#v", got["urls"])
	}
	allowedHosts, ok := got["allowedHosts"].([]any)
	if !ok || len(allowedHosts) != 2 || allowedHosts[1] != "docs.example.com" {
		t.Fatalf("allowedHosts = %#v", got["allowedHosts"])
	}
}

func TestCrawlSite_JobGuard(t *testing.T) {
	db := setupTestDB(t)
	cfg := config.DefaultConfig()
	cfg.MaxConcurrentCrawls = 1

	s := NewServer(ServerConfig{
		DB:     db,
		Config: &cfg,
	})

	// Create first job (will be queued)
	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"url": "https://example.com",
	}
	result, err := s.handleCrawlSite(context.Background(), req)
	if err != nil {
		t.Fatalf("first crawl: %v", err)
	}
	if result.IsError {
		t.Fatalf("first crawl should succeed")
	}

	// Second job should be blocked
	req2 := gomcp.CallToolRequest{}
	req2.Params.Arguments = map[string]any{
		"url": "https://other.com",
	}
	result2, err := s.handleCrawlSite(context.Background(), req2)
	if err != nil {
		t.Fatalf("second crawl: %v", err)
	}

	if !result2.IsError {
		t.Error("expected error: concurrent crawl limit should be reached")
	}
}

func TestCrawlStatus_ReturnsCounters(t *testing.T) {
	db := setupTestDB(t)
	cfg := config.DefaultConfig()

	s := NewServer(ServerConfig{
		DB:     db,
		Config: &cfg,
	})

	// Create a job directly
	job, err := db.CreateJob("crawl", `{}`, `["https://example.com"]`)
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	// Update counters
	if err := db.UpdateJobCounters(job.ID, 42, 100, 5); err != nil {
		t.Fatalf("updating counters: %v", err)
	}

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"jobId": job.ID,
	}

	result, err := s.handleCrawlStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.IsError {
		t.Fatalf("expected success, got error")
	}

	var status crawlStatusResult
	for _, content := range result.Content {
		if tc, ok := content.(gomcp.TextContent); ok {
			if err := json.Unmarshal([]byte(tc.Text), &status); err != nil {
				t.Fatalf("parsing status: %v", err)
			}
		}
	}

	if status.PagesCrawled != 42 {
		t.Errorf("expected 42 pages crawled, got %d", status.PagesCrawled)
	}
	if status.URLsDiscovered != 100 {
		t.Errorf("expected 100 URLs discovered, got %d", status.URLsDiscovered)
	}
	if status.IssuesFound != 5 {
		t.Errorf("expected 5 issues found, got %d", status.IssuesFound)
	}
}

func TestCrawlStatus_JobNotFound(t *testing.T) {
	db := setupTestDB(t)
	s := NewServer(ServerConfig{DB: db})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"jobId": "nonexistent-id",
	}

	result, err := s.handleCrawlStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error for nonexistent job")
	}
}

func TestCancelCrawl_TransitionsStatus(t *testing.T) {
	db := setupTestDB(t)
	s := NewServer(ServerConfig{DB: db})

	// Create and start a job
	job, err := db.CreateJob("crawl", `{}`, `["https://example.com"]`)
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}
	if err := db.UpdateJobStarted(job.ID); err != nil {
		t.Fatalf("starting job: %v", err)
	}

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"jobId": job.ID,
	}

	result, err := s.handleCancelCrawl(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.IsError {
		t.Fatalf("expected success, got error")
	}

	// Verify status in DB
	updated, err := db.GetJob(job.ID)
	if err != nil {
		t.Fatalf("getting updated job: %v", err)
	}
	if updated.Status != "cancelling" {
		t.Errorf("expected status %q, got %q", "cancelling", updated.Status)
	}
}

func TestCancelCrawl_QueuedWithoutEngineFinishesCancelled(t *testing.T) {
	db := setupTestDB(t)
	s := NewServer(ServerConfig{DB: db})

	job, err := db.CreateJob("crawl", `{}`, `["https://example.com"]`)
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"jobId": job.ID}

	result, err := s.handleCancelCrawl(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}

	updated, err := db.GetJob(job.ID)
	if err != nil {
		t.Fatalf("getting updated job: %v", err)
	}
	if updated.Status != "cancelled" {
		t.Fatalf("expected queued job without engine to finish cancelled, got %q", updated.Status)
	}
	if !updated.FinishedAt.Valid {
		t.Fatal("expected cancelled queued job to have finished_at set")
	}
}

func TestCancelCrawl_RunningEngineFinishesCancelled(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
		default:
			close(started)
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>slow</title></head><body>slow</body></html>`)
	}))
	defer ts.Close()
	defer close(release)

	db := setupTestDB(t)
	cfg := config.DefaultConfig()
	cfg.MaxConcurrentCrawls = 1
	cfg.GlobalConcurrency = 1
	cfg.MaxPages = 1
	cfg.MaxDepth = 0
	cfg.RequestTimeout = 5 * time.Second
	cfg.RenderMode = config.RenderModeStatic
	cfg.RespectRobots = false
	cfg.SSRFProtection = false
	f := fetcher.New(fetcher.Options{UserAgent: "test-crawler/1.0", Timeout: 5 * time.Second, MaxResponseBody: 1 << 20, MaxDecompressedBody: 2 << 20, MaxRedirectHops: 3})
	s := NewServer(ServerConfig{
		DB:      db,
		Config:  &cfg,
		Fetcher: f,
		Engine:  engine.New(engine.EngineConfig{DB: db, Fetcher: f, RateLimiter: fetcher.NewRateLimiter(1), ScopeChecker: mcpScopeCheckerForTestURL(t, ts.URL), Config: &cfg}),
	})

	result := callTool(t, s, map[string]any{"url": ts.URL + "/", "maxPages": float64(1), "renderMode": "static"})
	if result.IsError {
		t.Fatalf("crawl_site returned error: %v", result.Content)
	}
	var res crawlSiteResult
	for _, content := range result.Content {
		if tc, ok := content.(gomcp.TextContent); ok {
			if err := json.Unmarshal([]byte(tc.Text), &res); err != nil {
				t.Fatalf("parsing crawl result: %v", err)
			}
		}
	}
	if res.JobID == "" {
		t.Fatal("expected job id")
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("crawl did not reach slow handler")
	}

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"jobId": res.JobID}
	cancelResult, err := s.handleCancelCrawl(context.Background(), req)
	if err != nil {
		t.Fatalf("handleCancelCrawl: %v", err)
	}
	if cancelResult.IsError {
		t.Fatalf("cancel_crawl returned error: %v", cancelResult.Content)
	}

	job := waitForMCPJobStatus(t, db, res.JobID, 3*time.Second, "cancelled", "completed", "failed")
	if job.Status != "cancelled" {
		t.Fatalf("MCP cancellation final status = %q, error=%v; want cancelled", job.Status, job.Error)
	}
}

func waitForMCPJobStatus(t *testing.T, db *storage.DB, jobID string, timeout time.Duration, statuses ...string) *storage.CrawlJob {
	t.Helper()
	want := map[string]bool{}
	for _, status := range statuses {
		want[status] = true
	}
	deadline := time.Now().Add(timeout)
	var last *storage.CrawlJob
	for time.Now().Before(deadline) {
		job, err := db.GetJob(jobID)
		if err == nil {
			last = job
			if want[job.Status] {
				return job
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if last != nil {
		t.Fatalf("timed out waiting for job %s statuses %v; last status=%q error=%v", jobID, statuses, last.Status, last.Error)
	}
	t.Fatalf("timed out waiting for job %s statuses %v; job not found", jobID, statuses)
	return nil
}

func mcpScopeCheckerForTestURL(t *testing.T, rawURL string) *urlutil.ScopeChecker {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parsing test URL: %v", err)
	}
	sc, err := urlutil.NewScopeChecker("exact_host", parsed.Hostname(), nil)
	if err != nil {
		t.Fatalf("creating scope checker: %v", err)
	}
	return sc
}

func TestCancelCrawl_RejectsCompletedJob(t *testing.T) {
	db := setupTestDB(t)
	s := NewServer(ServerConfig{DB: db})

	job, err := db.CreateJob("crawl", `{}`, `["https://example.com"]`)
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}
	if err := db.UpdateJobFinished(job.ID, "completed", nil); err != nil {
		t.Fatalf("finishing job: %v", err)
	}

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"jobId": job.ID,
	}

	result, err := s.handleCancelCrawl(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error: cannot cancel completed job")
	}
}

func TestCrawlSite_NilDB(t *testing.T) {
	s := NewServer(ServerConfig{})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"url": "https://example.com",
	}

	result, err := s.handleCrawlSite(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected error when DB is nil")
	}
}
