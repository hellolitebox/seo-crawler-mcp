package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/engine"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/fetcher"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/urlutil"
)

// newTestServer creates a Server backed by a temp-file SQLite DB.
// Returns the server, an http test handler, and a cleanup func.
func newTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	srv := New(db, nil, nil)
	return srv, srv.Handler()
}

func newTestCrawlServer(t *testing.T, rawURL string, requestTimeout time.Duration) (*Server, http.Handler) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := config.DefaultConfig()
	cfg.MaxConcurrentCrawls = 1
	cfg.GlobalConcurrency = 1
	cfg.MaxPages = 1
	cfg.MaxDepth = 0
	cfg.RequestTimeout = requestTimeout
	cfg.RenderMode = config.RenderModeStatic
	cfg.RespectRobots = false
	cfg.SSRFProtection = false
	f := fetcher.New(fetcher.Options{UserAgent: "test-crawler/1.0", Timeout: requestTimeout, MaxResponseBody: 1 << 20, MaxDecompressedBody: 2 << 20, MaxRedirectHops: 3})
	eng := engine.New(engine.EngineConfig{
		DB:           db,
		Fetcher:      f,
		RateLimiter:  fetcher.NewRateLimiter(1),
		ScopeChecker: scopeCheckerForTestURL(t, rawURL),
		Config:       &cfg,
	})
	srv := &Server{
		db:      db,
		engine:  eng,
		config:  &cfg,
		allowed: []string{},
		limiter: newRateLimiter(10, time.Hour),
		purger:  newPurgeWorker(db),
		queue:   make(chan struct{}, 1),
		running: map[string]context.CancelCauseFunc{},
		rootCtx: context.Background(),
	}
	return srv, srv.Handler()
}

// seedJob inserts a completed crawl job with the given URL.
func seedJob(t *testing.T, db *storage.DB, jobID, seedURL, status string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO crawl_jobs (id, type, status, config_json, seed_urls)
		VALUES (?, 'spider', ?, '{}', ?)
	`, jobID, status, `["`+seedURL+`"]`)
	if err != nil {
		t.Fatalf("seeding job: %v", err)
	}
}

func TestHealth(t *testing.T) {
	_, h := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("expected ok=true, got %v", body)
	}
}

func TestJobPageBundlePopulatesInSitemap(t *testing.T) {
	srv, h := newTestServer(t)
	jobID := "job-page-sitemap"
	pageURL := "https://example.com/in-sitemap"
	seedJob(t, srv.db, jobID, "https://example.com", "completed")
	result, err := srv.db.Exec("INSERT INTO urls (job_id, normalized_url, host, status, discovered_via) VALUES (?, ?, 'example.com', 'fetched', 'crawl')", jobID, pageURL)
	if err != nil {
		t.Fatalf("seeding url: %v", err)
	}
	urlID, _ := result.LastInsertId()
	fetchResult, err := srv.db.Exec("INSERT INTO fetches (job_id, fetch_seq, requested_url_id, status_code, content_type) VALUES (?, 1, ?, 200, 'text/html')", jobID, urlID)
	if err != nil {
		t.Fatalf("seeding fetch: %v", err)
	}
	fetchID, _ := fetchResult.LastInsertId()
	if _, err := srv.db.Exec("INSERT INTO pages (job_id, url_id, fetch_id, depth, indexability_state) VALUES (?, ?, ?, 1, 'indexable')", jobID, urlID, fetchID); err != nil {
		t.Fatalf("seeding page: %v", err)
	}
	if _, err := srv.db.Exec("INSERT INTO sitemap_entries (job_id, url, source_sitemap_url, source_host) VALUES (?, ?, 'https://example.com/sitemap.xml', 'example.com')", jobID, pageURL); err != nil {
		t.Fatalf("seeding sitemap entry: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+jobID+"/page?url="+url.QueryEscape(pageURL), nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Page struct {
			InSitemap bool `json:"inSitemap"`
		} `json:"page"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !payload.Page.InSitemap {
		t.Fatal("page.inSitemap = false, want true")
	}
}

func TestJobPageBundleDeduplicatesSitemapEntries(t *testing.T) {
	srv, h := newTestServer(t)
	jobID := "job-page-sitemap-dedupe"
	pageURL := "https://www.example.com/page"
	seedJob(t, srv.db, jobID, "https://example.com", "completed")
	result, err := srv.db.Exec("INSERT INTO urls (job_id, normalized_url, host, status, discovered_via) VALUES (?, ?, 'example.com', 'fetched', 'crawl')", jobID, pageURL)
	if err != nil {
		t.Fatalf("seeding url: %v", err)
	}
	urlID, _ := result.LastInsertId()
	fetchResult, err := srv.db.Exec("INSERT INTO fetches (job_id, fetch_seq, requested_url_id, status_code, content_type) VALUES (?, 1, ?, 200, 'text/html')", jobID, urlID)
	if err != nil {
		t.Fatalf("seeding fetch: %v", err)
	}
	fetchID, _ := fetchResult.LastInsertId()
	if _, err := srv.db.Exec("INSERT INTO pages (job_id, url_id, fetch_id, depth, indexability_state) VALUES (?, ?, ?, 1, 'indexable')", jobID, urlID, fetchID); err != nil {
		t.Fatalf("seeding page: %v", err)
	}
	for _, row := range []struct {
		url    string
		source string
	}{
		{"https://example.com/page", "https://example.com/sitemap.xml"},
		{"http://www.example.com/page/", "http://www.example.com/sitemap.xml"},
		{"https://www.example.com/page", "https://www.example.com/sitemap.xml"},
	} {
		if _, err := srv.db.Exec("INSERT INTO sitemap_entries (job_id, url, source_sitemap_url, source_host) VALUES (?, ?, ?, 'example.com')", jobID, row.url, row.source); err != nil {
			t.Fatalf("seeding sitemap entry %q: %v", row.url, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+jobID+"/page?url="+url.QueryEscape(pageURL), nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		SitemapEntries []struct {
			URL string `json:"url"`
		} `json:"sitemap_entries"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(payload.SitemapEntries) != 1 {
		t.Fatalf("sitemap_entries len = %d, want 1: %+v", len(payload.SitemapEntries), payload.SitemapEntries)
	}
}

func TestJobPageBundleImagesExcludeScriptChunks(t *testing.T) {
	srv, h := newTestServer(t)
	jobID := "job-page-images"
	pageURL := "https://example.com/page"
	imageURL := "https://example.com/hero.webp"
	scriptURL := "https://example.com/_next/static/chunks/app.js"
	seedJob(t, srv.db, jobID, "https://example.com", "completed")

	insertURL := func(rawURL, host string) int64 {
		t.Helper()
		result, err := srv.db.Exec(
			"INSERT INTO urls (job_id, normalized_url, host, status, discovered_via) VALUES (?, ?, ?, 'fetched', 'crawl')",
			jobID, rawURL, host,
		)
		if err != nil {
			t.Fatalf("seeding url %s: %v", rawURL, err)
		}
		id, _ := result.LastInsertId()
		return id
	}
	pageURLID := insertURL(pageURL, "example.com")
	imageURLID := insertURL(imageURL, "example.com")
	scriptURLID := insertURL(scriptURL, "example.com")
	fetchResult, err := srv.db.Exec("INSERT INTO fetches (job_id, fetch_seq, requested_url_id, status_code, content_type) VALUES (?, 1, ?, 200, 'text/html')", jobID, pageURLID)
	if err != nil {
		t.Fatalf("seeding fetch: %v", err)
	}
	fetchID, _ := fetchResult.LastInsertId()
	if _, err := srv.db.Exec("INSERT INTO pages (job_id, url_id, fetch_id, depth, indexability_state) VALUES (?, ?, ?, 1, 'indexable')", jobID, pageURLID, fetchID); err != nil {
		t.Fatalf("seeding page: %v", err)
	}
	if _, err := srv.db.Exec("INSERT INTO assets (job_id, url_id, content_type, status_code) VALUES (?, ?, 'image/webp', 200), (?, ?, 'application/javascript', 200)", jobID, imageURLID, jobID, scriptURLID); err != nil {
		t.Fatalf("seeding assets: %v", err)
	}
	if _, err := srv.db.Exec("INSERT INTO asset_references (job_id, asset_url_id, source_page_url_id, reference_type) VALUES (?, ?, ?, 'img_src'), (?, ?, ?, 'script_src')", jobID, imageURLID, pageURLID, jobID, scriptURLID, pageURLID); err != nil {
		t.Fatalf("seeding asset references: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+jobID+"/page?url="+url.QueryEscape(pageURL), nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		AssetReferences []struct {
			AssetURL      string `json:"asset_url"`
			ReferenceType string `json:"reference_type"`
		} `json:"asset_references"`
		Assets []struct {
			URL         string `json:"url"`
			ContentType string `json:"content_type"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(payload.AssetReferences) != 1 {
		t.Fatalf("asset_references len = %d, want 1: %+v", len(payload.AssetReferences), payload.AssetReferences)
	}
	if payload.AssetReferences[0].AssetURL != imageURL || payload.AssetReferences[0].ReferenceType != "img_src" {
		t.Fatalf("asset reference = %+v, want img_src hero", payload.AssetReferences[0])
	}
	if len(payload.Assets) != 1 || payload.Assets[0].URL != imageURL || payload.Assets[0].ContentType != "image/webp" {
		t.Fatalf("assets = %+v, want only image asset", payload.Assets)
	}
}

func TestJobAssetsIncludesSourcePageReferences(t *testing.T) {
	srv, h := newTestServer(t)
	jobID := "job-assets-references"
	pageURL := "https://example.com/page"
	imageURL := "https://example.com/hero.webp"
	seedJob(t, srv.db, jobID, "https://example.com", "completed")

	insertURL := func(rawURL string) int64 {
		t.Helper()
		result, err := srv.db.Exec(
			"INSERT INTO urls (job_id, normalized_url, host, status, discovered_via) VALUES (?, ?, 'example.com', 'fetched', 'crawl')",
			jobID, rawURL,
		)
		if err != nil {
			t.Fatalf("seeding url %s: %v", rawURL, err)
		}
		id, _ := result.LastInsertId()
		return id
	}
	pageURLID := insertURL(pageURL)
	imageURLID := insertURL(imageURL)
	if _, err := srv.db.Exec("INSERT INTO assets (job_id, url_id, content_type, status_code) VALUES (?, ?, 'image/webp', 200)", jobID, imageURLID); err != nil {
		t.Fatalf("seeding asset: %v", err)
	}
	if _, err := srv.db.Exec("INSERT INTO asset_references (job_id, asset_url_id, source_page_url_id, reference_type, natural_width, natural_height, rendered_width, rendered_height) VALUES (?, ?, ?, 'img_src', 1600, 900, 390, 219)", jobID, imageURLID, pageURLID); err != nil {
		t.Fatalf("seeding asset reference: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+jobID+"/assets?content_type=image/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Results []struct {
			URL            string `json:"url"`
			ReferenceCount int    `json:"referenceCount"`
			References     []struct {
				PageURL       string `json:"pageUrl"`
				ReferenceType string `json:"referenceType"`
				NaturalWidth  *int64 `json:"naturalWidth"`
				RenderedWidth *int64 `json:"renderedWidth"`
			} `json:"references"`
		} `json:"results"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(payload.Results) != 1 {
		t.Fatalf("results len = %d, want 1: %+v", len(payload.Results), payload.Results)
	}
	asset := payload.Results[0]
	if asset.URL != imageURL || asset.ReferenceCount != 1 || len(asset.References) != 1 {
		t.Fatalf("asset references = %+v, want one reference for image", asset)
	}
	ref := asset.References[0]
	if ref.PageURL != pageURL || ref.ReferenceType != "img_src" {
		t.Fatalf("reference = %+v, want source page img_src", ref)
	}
	if ref.NaturalWidth == nil || *ref.NaturalWidth != 1600 || ref.RenderedWidth == nil || *ref.RenderedWidth != 390 {
		t.Fatalf("reference dimensions = %+v, want natural/rendered widths", ref)
	}
}

func TestHandleCrawlWithoutEngine(t *testing.T) {
	_, handler := newTestServer(t)

	body := bytes.NewBufferString(`{"url":"https://example.com","maxPages":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/crawl", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusServiceUnavailable, w.Body.String())
	}
}

func TestNormalizeCrawlURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain domain", in: "pipapou.com", want: "https://pipapou.com"},
		{name: "plain domain with path", in: "www.pipapou.com/wizard", want: "https://www.pipapou.com/wizard"},
		{name: "plain domain with port", in: "example.com:8443/path", want: "https://example.com:8443/path"},
		{name: "explicit https", in: "https://example.com/path", want: "https://example.com/path"},
		{name: "explicit http", in: "http://example.com", want: "http://example.com"},
		{name: "trims whitespace", in: "  pipapou.com  ", want: "https://pipapou.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, parsed, err := normalizeCrawlURL(tt.in)
			if err != nil {
				t.Fatalf("normalizeCrawlURL(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("normalizeCrawlURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if parsed.Hostname() == "" {
				t.Fatalf("normalized URL %q has empty hostname", got)
			}
		})
	}
}

func TestNormalizeCrawlURLRejectsInvalidSchemes(t *testing.T) {
	for _, rawURL := range []string{"ftp://example.com", "javascript:alert(1)", "mailto:test@example.com"} {
		t.Run(rawURL, func(t *testing.T) {
			if got, _, err := normalizeCrawlURL(rawURL); err == nil {
				t.Fatalf("normalizeCrawlURL(%q) = %q, want error", rawURL, got)
			}
		})
	}
}

func TestHandleCrawlAutoRenderModeQueuesHybrid(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	activeJob, err := db.CreateJob("crawl", `{}`, `["https://busy.example"]`)
	if err != nil {
		t.Fatalf("creating active job: %v", err)
	}
	if err := db.UpdateJobStarted(activeJob.ID); err != nil {
		t.Fatalf("marking active job started: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.SSRFProtection = false
	srv := New(db, &engine.Engine{}, &cfg)
	handler := srv.Handler()

	body := bytes.NewBufferString(`{"url":"pipapou.com","maxPages":10,"renderMode":"auto"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/crawl", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST /api/crawl status = %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	job, err := db.GetJob(resp["jobId"])
	if err != nil {
		t.Fatalf("getting queued job: %v", err)
	}
	var crawlConfig map[string]any
	if err := json.Unmarshal([]byte(job.ConfigJSON), &crawlConfig); err != nil {
		t.Fatalf("parsing crawl config: %v", err)
	}
	if crawlConfig["renderMode"] != "hybrid" {
		t.Fatalf("renderMode = %v, want hybrid", crawlConfig["renderMode"])
	}
	var seeds []string
	if err := json.Unmarshal([]byte(job.SeedURLs), &seeds); err != nil {
		t.Fatalf("parsing seeds: %v", err)
	}
	if fmt.Sprint(seeds) != fmt.Sprint([]string{"https://pipapou.com"}) {
		t.Fatalf("seeds = %v, want normalized https seed", seeds)
	}
}

func TestHandleCrawlHonorsConfigAndRequestSettings(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	activeJob, err := db.CreateJob("crawl", "{}", "[\"https://busy.example\"]")
	if err != nil {
		t.Fatalf("creating active job: %v", err)
	}
	if err := db.UpdateJobStarted(activeJob.ID); err != nil {
		t.Fatalf("marking active job started: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.SSRFProtection = false
	cfg.MaxConcurrentCrawls = 1
	cfg.MaxPages = 123
	cfg.MaxDepth = 7
	cfg.ScopeMode = config.ScopeModeExactHost
	cfg.RespectRobots = false
	srv := New(db, &engine.Engine{}, &cfg)
	handler := srv.Handler()

	body := bytes.NewBufferString("{\"url\":\"example.com\",\"urls\":[\"example.com/docs\"],\"scopeMode\":\"allowlist\",\"allowedHosts\":[\"example.com\",\"docs.example.com\"],\"maxPages\":10,\"maxDepth\":3,\"renderMode\":\"static\",\"respectRobots\":true,\"dryRun\":true}")
	req := httptest.NewRequest(http.MethodPost, "/api/crawl", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST /api/crawl status = %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	job, err := db.GetJob(resp["jobId"])
	if err != nil {
		t.Fatalf("getting queued job: %v", err)
	}
	var crawlConfig map[string]any
	if err := json.Unmarshal([]byte(job.ConfigJSON), &crawlConfig); err != nil {
		t.Fatalf("parsing crawl config: %v", err)
	}
	assertConfig := map[string]any{
		"scopeMode":     "allowlist",
		"maxPages":      float64(10),
		"maxDepth":      float64(3),
		"renderMode":    "static",
		"respectRobots": true,
		"dryRun":        true,
	}
	for key, want := range assertConfig {
		if got := crawlConfig[key]; got != want {
			t.Fatalf("%s = %#v, want %#v", key, got, want)
		}
	}
	allowedHosts, ok := crawlConfig["allowedHosts"].([]any)
	if !ok || len(allowedHosts) != 2 || allowedHosts[1] != "docs.example.com" {
		t.Fatalf("allowedHosts = %#v, want request hosts", crawlConfig["allowedHosts"])
	}
	var seeds []string
	if err := json.Unmarshal([]byte(job.SeedURLs), &seeds); err != nil {
		t.Fatalf("parsing seeds: %v", err)
	}
	wantSeeds := []string{"https://example.com", "https://example.com/docs"}
	if fmt.Sprint(seeds) != fmt.Sprint(wantSeeds) {
		t.Fatalf("seeds = %v, want %v", seeds, wantSeeds)
	}
}

func TestMaxConcurrentCrawlsUsesConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MaxConcurrentCrawls = 2
	s := New(nil, nil, &cfg)
	if got := s.maxConcurrentCrawls(); got != 2 {
		t.Fatalf("maxConcurrentCrawls() = %d, want 2", got)
	}
}

func TestJobsList_EmptyDb(t *testing.T) {
	_, h := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Jobs   []any `json:"jobs"`
		Total  int   `json:"total"`
		Limit  int   `json:"limit"`
		Offset int   `json:"offset"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if resp.Total != 0 {
		t.Fatalf("expected total=0, got %d", resp.Total)
	}
	if resp.Limit != 50 {
		t.Fatalf("expected default limit=50, got %d", resp.Limit)
	}
}

func TestJobsList_Pagination(t *testing.T) {
	srv, h := newTestServer(t)

	// Insert 5 completed jobs
	for i := 0; i < 5; i++ {
		seedJob(t, srv.db, string(rune('a'+i)), "https://example.com", "completed")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/jobs?limit=2&offset=1", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Jobs   []map[string]any `json:"jobs"`
		Total  int              `json:"total"`
		Limit  int              `json:"limit"`
		Offset int              `json:"offset"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if resp.Total != 5 {
		t.Fatalf("expected total=5, got %d", resp.Total)
	}
	if len(resp.Jobs) != 2 {
		t.Fatalf("expected 2 jobs returned, got %d", len(resp.Jobs))
	}
	if resp.Limit != 2 || resp.Offset != 1 {
		t.Fatalf("expected limit=2, offset=1, got %d/%d", resp.Limit, resp.Offset)
	}
}

func TestJobsList_LimitClamps(t *testing.T) {
	_, h := newTestServer(t)

	// limit=999 should clamp to default since it's out of range (>200)
	req := httptest.NewRequest(http.MethodGet, "/api/jobs?limit=999", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	var resp struct{ Limit int }
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Limit != 50 {
		t.Fatalf("expected limit clamped to 50, got %d", resp.Limit)
	}
}

func TestJobStatus_NotFound(t *testing.T) {
	_, h := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/does-not-exist", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestJobStatus_Found(t *testing.T) {
	srv, h := newTestServer(t)
	seedJob(t, srv.db, "job-1", "https://example.com", "completed")

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/job-1", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if resp["jobId"] != "job-1" {
		t.Fatalf("expected jobId=job-1, got %v", resp["jobId"])
	}
	if resp["status"] != "completed" {
		t.Fatalf("expected status=completed, got %v", resp["status"])
	}
}

func TestPagesAPIDefaultsTo2xxContentPages(t *testing.T) {
	srv, h := newTestServer(t)
	job, err := srv.db.CreateJob("crawl", "{}", "[\"https://example.com/\"]")
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	seedPage := func(rawURL string, statusCode int, seq int) {
		t.Helper()
		urlID, err := srv.db.UpsertURL(job.ID, rawURL, "example.com", "fetched", true, "link")
		if err != nil {
			t.Fatalf("upserting URL %s: %v", rawURL, err)
		}
		fetchID, err := srv.db.InsertFetch(storage.FetchInput{
			JobID:          job.ID,
			FetchSeq:       seq,
			RequestedURLID: urlID,
			StatusCode:     statusCode,
			ContentType:    "text/html",
			HTTPMethod:     "GET",
			FetchKind:      "full",
			RenderMode:     "static",
		})
		if err != nil {
			t.Fatalf("inserting fetch for %s: %v", rawURL, err)
		}
		title := fmt.Sprintf("HTTP %d", statusCode)
		if _, err := srv.db.InsertPage(storage.PageInput{
			JobID:             job.ID,
			URLID:             urlID,
			FetchID:           fetchID,
			Depth:             0,
			Title:             &title,
			IndexabilityState: "indexable",
		}); err != nil {
			t.Fatalf("inserting page for %s: %v", rawURL, err)
		}
	}
	seedPage("https://example.com/", http.StatusOK, 1)
	seedPage("https://example.com/missing", http.StatusNotFound, 2)

	type pageDTO struct {
		URL        string
		StatusCode int
	}
	type pageResp struct {
		Results    []pageDTO
		TotalCount int
	}

	assertOnlyOK := func(path string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body=%s", path, rr.Code, rr.Body.String())
		}
		var resp struct {
			Pages      pageResp
			Results    []pageDTO
			TotalCount int
		}
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decoding %s: %v", path, err)
		}
		pages := resp.Results
		totalCount := resp.TotalCount
		if len(pages) == 0 && len(resp.Pages.Results) > 0 {
			pages = resp.Pages.Results
			totalCount = resp.Pages.TotalCount
		}
		if totalCount != 1 || len(pages) != 1 {
			t.Fatalf("GET %s returned total=%d len=%d, want one 2xx page", path, totalCount, len(pages))
		}
		if pages[0].StatusCode != http.StatusOK || pages[0].URL != "https://example.com/" {
			t.Fatalf("GET %s page = %+v, want only the 200 page", path, pages[0])
		}
	}
	assertOnlyOK("/api/jobs/" + job.ID + "/report")
	assertOnlyOK("/api/jobs/" + job.ID + "/pages")

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+job.ID+"/pages?status_code_family=4xx", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET 4xx pages status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var fourXX pageResp
	if err := json.NewDecoder(rr.Body).Decode(&fourXX); err != nil {
		t.Fatalf("decoding 4xx pages: %v", err)
	}
	if fourXX.TotalCount != 1 || len(fourXX.Results) != 1 || fourXX.Results[0].StatusCode != http.StatusNotFound {
		t.Fatalf("4xx pages response = %+v, want the explicit 404 page", fourXX)
	}
}

func TestJobDelete_TombstonesWithoutPurgingByDefault(t *testing.T) {
	t.Setenv("SEO_CRAWLER_PURGE_ON_DELETE", "")
	srv, h := newTestServer(t)
	seedJob(t, srv.db, "job-purge", "https://example.com", "completed")

	req := httptest.NewRequest(http.MethodDelete, "/api/jobs/job-purge", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["status"] != "deleted" {
		t.Fatalf("expected status=deleted, got %v", resp)
	}

	job, err := srv.db.GetJob("job-purge")
	if err != nil {
		t.Fatalf("job should be tombstoned, not purged: %v", err)
	}
	if job.Status != "deleting" {
		t.Fatalf("expected tombstone status deleting, got %q", job.Status)
	}
}

func TestJobDelete_TombstoneHidesFromList(t *testing.T) {
	srv, h := newTestServer(t)
	seedJob(t, srv.db, "alive", "https://a.com", "completed")
	seedJob(t, srv.db, "deleting", "https://b.com", "completed")

	// Trigger DELETE on the second one.
	req := httptest.NewRequest(http.MethodDelete, "/api/jobs/deleting", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE: expected 200, got %d", rr.Code)
	}

	// The list should immediately exclude tombstoned jobs, even before the
	// background purge finishes.
	listReq := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	listRR := httptest.NewRecorder()
	h.ServeHTTP(listRR, listReq)
	var listResp struct {
		Jobs  []map[string]any `json:"jobs"`
		Total int              `json:"total"`
	}
	json.NewDecoder(listRR.Body).Decode(&listResp)
	if listResp.Total != 1 {
		t.Fatalf("expected 1 job in list, got %d", listResp.Total)
	}
	for _, j := range listResp.Jobs {
		if j["jobId"] == "deleting" {
			t.Fatal("'deleting' job should not appear in list")
		}
	}
}

func TestJobDelete_CancelsRunning(t *testing.T) {
	srv, h := newTestServer(t)
	seedJob(t, srv.db, "job-running", "https://example.com", "running")

	req := httptest.NewRequest(http.MethodDelete, "/api/jobs/job-running", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["status"] != "cancelling" {
		t.Fatalf("expected status=cancelling, got %v", resp)
	}

	// Job should still exist but with 'cancelling' status
	job, err := srv.db.GetJob("job-running")
	if err != nil {
		t.Fatalf("expected job to still exist, got: %v", err)
	}
	if job.Status != "cancelling" {
		t.Fatalf("expected status=cancelling, got %s", job.Status)
	}
}

func TestCrawlImmediateStartCompletesFromQueuedJob(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><head><title>OK</title></head><body><h1>OK</h1></body></html>`)
	}))
	defer ts.Close()

	srv, h := newTestCrawlServer(t, ts.URL, 2*time.Second)

	body := bytes.NewBufferString(fmt.Sprintf(`{"url":%q,"maxPages":1,"renderMode":"static"}`, ts.URL+"/"))
	req := httptest.NewRequest(http.MethodPost, "/api/crawl", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST /api/crawl status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	job := waitForJobStatus(t, srv.db, resp["jobId"], 3*time.Second, "completed", "failed", "cancelled")
	if job.Status != "completed" {
		t.Fatalf("immediate crawl final status = %q, error=%v; want completed", job.Status, job.Error)
	}
}

func TestJobDelete_CancelledQueuedRegisteredRunWins(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>should not run</title></head><body>ok</body></html>`)
	}))
	defer ts.Close()

	srv, h := newTestCrawlServer(t, ts.URL, 2*time.Second)

	job, err := srv.db.CreateJob("crawl", `{}`, fmt.Sprintf(`[%q]`, ts.URL+"/"))
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	ctx, cancel, ok := srv.registerRun(job.ID)
	if !ok {
		t.Fatal("registerRun returned !ok")
	}
	defer srv.unregisterRun(job.ID)
	defer cancel(nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/jobs/"+job.ID, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, body=%s", rr.Code, rr.Body.String())
	}
	select {
	case <-ctx.Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("DELETE of queued-but-registered job did not cancel registered run context")
	}

	err = srv.engine.RunCrawl(ctx, job.ID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunCrawl error = %v, want context.Canceled", err)
	}
	updated, err := srv.db.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if updated.Status != "cancelled" {
		t.Fatalf("job status after queued cancellation race = %q, want cancelled", updated.Status)
	}
}

func TestJobDelete_RunningCrawlStopsEngineAndFinishesCancelled(t *testing.T) {
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

	srv, h := newTestCrawlServer(t, ts.URL, 5*time.Second)

	body := bytes.NewBufferString(fmt.Sprintf(`{"url":%q,"maxPages":1,"renderMode":"static"}`, ts.URL+"/"))
	req := httptest.NewRequest(http.MethodPost, "/api/crawl", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("POST /api/crawl status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("crawl did not reach slow handler")
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/jobs/"+resp["jobId"], nil)
	delRR := httptest.NewRecorder()
	h.ServeHTTP(delRR, delReq)
	if delRR.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, body=%s", delRR.Code, delRR.Body.String())
	}

	job := waitForJobStatus(t, srv.db, resp["jobId"], 3*time.Second, "cancelled", "completed", "failed")
	if job.Status != "cancelled" {
		t.Fatalf("running cancellation final status = %q, error=%v; want cancelled", job.Status, job.Error)
	}
}

func waitForJobStatus(t *testing.T, db *storage.DB, jobID string, timeout time.Duration, statuses ...string) *storage.CrawlJob {
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

func scopeCheckerForTestURL(t *testing.T, rawURL string) *urlutil.ScopeChecker {
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

func TestJobDelete_NotFound(t *testing.T) {
	_, h := newTestServer(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/jobs/ghost", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestJobActivity_Empty(t *testing.T) {
	srv, h := newTestServer(t)
	seedJob(t, srv.db, "job-act", "https://example.com", "running")

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/job-act/activity", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp struct {
		Activity []any `json:"activity"`
	}
	json.NewDecoder(rr.Body).Decode(&resp)
	// Empty is fine; just ensure shape is correct.
	if resp.Activity == nil {
		t.Fatalf("expected activity array (possibly empty), got nil")
	}
}

func TestJobActivity_MergesSortsAndTrims(t *testing.T) {
	srv, h := newTestServer(t)
	seedJob(t, srv.db, "job-act-order", "https://example.com", "running")

	insertURL := func(raw string) int64 {
		res, err := srv.db.Exec(`
			INSERT INTO urls (job_id, normalized_url, host, status, is_internal, discovered_via)
			VALUES ('job-act-order', ?, 'example.com', 'fetched', 1, 'test')
		`, raw)
		if err != nil {
			t.Fatalf("inserting url %s: %v", raw, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("getting url id: %v", err)
		}
		return id
	}

	urlID1 := insertURL("https://example.com/old")
	fetchID1, err := srv.db.InsertFetch(storage.FetchInput{
		JobID:          "job-act-order",
		FetchSeq:       1,
		RequestedURLID: urlID1,
		StatusCode:     200,
		TTFBMs:         40,
	})
	if err != nil {
		t.Fatalf("inserting older fetch: %v", err)
	}
	if _, err := srv.db.Exec(`UPDATE fetches SET fetched_at = '2026-05-17T10:00:00.000Z' WHERE id = ?`, fetchID1); err != nil {
		t.Fatalf("dating older fetch: %v", err)
	}

	urlID2 := insertURL("https://example.com/new")
	fetchID2, err := srv.db.InsertFetch(storage.FetchInput{
		JobID:          "job-act-order",
		FetchSeq:       2,
		RequestedURLID: urlID2,
		StatusCode:     204,
		TTFBMs:         20,
	})
	if err != nil {
		t.Fatalf("inserting newer fetch: %v", err)
	}
	if _, err := srv.db.Exec(`UPDATE fetches SET fetched_at = '2026-05-17T10:02:00.000Z' WHERE id = ?`, fetchID2); err != nil {
		t.Fatalf("dating newer fetch: %v", err)
	}

	details := `{"phase":"asset_checks","message":"Checking assets"}`
	eventID, err := srv.db.InsertEvent("job-act-order", "phase", &details, nil)
	if err != nil {
		t.Fatalf("inserting phase: %v", err)
	}
	if _, err := srv.db.Exec(`UPDATE crawl_events SET timestamp = '2026-05-17T10:03:00.000Z' WHERE id = ?`, eventID); err != nil {
		t.Fatalf("dating phase: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/job-act-order/activity?limit=2", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Activity []struct {
			Kind      string `json:"kind"`
			ID        int64  `json:"id"`
			URL       string `json:"url"`
			FetchedAt string `json:"fetchedAt"`
			Phase     string `json:"phase"`
		} `json:"activity"`
		LatestFetchID int64 `json:"latestFetchId"`
		LatestEventID int64 `json:"latestEventId"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(resp.Activity) != 2 {
		t.Fatalf("expected 2 activity rows, got %d: %+v", len(resp.Activity), resp.Activity)
	}
	if resp.Activity[0].Kind != "phase" || resp.Activity[0].ID != eventID || resp.Activity[0].Phase != "asset_checks" {
		t.Fatalf("expected newest phase first, got %+v", resp.Activity[0])
	}
	if resp.Activity[1].Kind != "fetch" || resp.Activity[1].ID != fetchID2 || resp.Activity[1].URL != "https://example.com/new" {
		t.Fatalf("expected newest fetch second, got %+v", resp.Activity[1])
	}
	if resp.LatestFetchID != fetchID2 || resp.LatestEventID != eventID {
		t.Fatalf("expected latest watermarks fetch=%d event=%d, got fetch=%d event=%d", fetchID2, eventID, resp.LatestFetchID, resp.LatestEventID)
	}
}

func TestCORS_AllowedOrigin(t *testing.T) {
	_, h := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "https://seo-crawler-report.vercel.app")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://seo-crawler-report.vercel.app" {
		t.Fatalf("expected allowed origin echoed, got %q", got)
	}
}

func TestCORS_DeniedOrigin(t *testing.T) {
	_, h := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no ACAO header for denied origin, got %q", got)
	}
}

func TestCORS_OptionsPreflight(t *testing.T) {
	_, h := newTestServer(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/crawl", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Fatalf("expected ACAM header on preflight")
	}
}
