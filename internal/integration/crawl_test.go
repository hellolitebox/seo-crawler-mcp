package integration

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/engine"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/fetcher"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/ssrf"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/urlutil"
)

func TestFullCrawl(t *testing.T) {
	// 1. Start fixture site
	site := NewFixtureSite()
	defer site.Close()

	t.Logf("fixture site at %s", site.URL)

	// 2. Open temp DB
	dbPath := filepath.Join(t.TempDir(), "integration.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	// 3. Configure crawl
	cfg := config.DefaultConfig()
	cfg.MaxPages = 50
	cfg.MaxDepth = 10
	cfg.GlobalConcurrency = 4
	cfg.PerHostConcurrency = 2
	cfg.RequestTimeout = 10 * time.Second
	cfg.RenderMode = config.RenderModeStatic
	cfg.AllowPrivateNetworks = true
	cfg.SSRFProtection = false
	cfg.ThinContentThreshold = 200

	// 4. Build engine dependencies
	guard := ssrf.NewGuard(true)
	f := fetcher.New(fetcher.Options{
		UserAgent:           "integration-test/1.0",
		Timeout:             10 * time.Second,
		MaxResponseBody:     5 * 1024 * 1024,
		MaxDecompressedBody: 20 * 1024 * 1024,
		MaxRedirectHops:     10,
		Retries:             0,
		SSRFGuard:           nil, // disabled for localhost
	})
	rl := fetcher.NewRateLimiter(cfg.PerHostConcurrency)

	tsURL, err := url.Parse(site.URL)
	if err != nil {
		t.Fatalf("parsing site URL: %v", err)
	}
	sc, err := urlutil.NewScopeChecker("exact_host", tsURL.Hostname(), nil)
	if err != nil {
		t.Fatalf("creating scope checker: %v", err)
	}

	eng := engine.New(engine.EngineConfig{
		DB:           db,
		Fetcher:      f,
		RateLimiter:  rl,
		ScopeChecker: sc,
		SSRFGuard:    guard,
		Config:       &cfg,
	})

	// 5. Create job
	seedURLs, _ := json.Marshal([]string{site.URL + "/"})
	job, err := db.CreateJob("crawl", "{}", string(seedURLs))
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	// 6. Run crawl
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	err = eng.RunCrawl(ctx, job.ID)
	if err != nil {
		t.Fatalf("RunCrawl failed: %v", err)
	}

	// ============================================================
	// 7. Verify results
	// ============================================================

	// --- Job status ---
	job, err = db.GetJob(job.ID)
	if err != nil {
		t.Fatalf("getting job: %v", err)
	}
	if job.Status != "completed" {
		t.Errorf("job status = %q, want completed (error: %v)", job.Status, job.Error)
	}
	t.Logf("pages_crawled=%d urls_discovered=%d issues_found=%d",
		job.PagesCrawled, job.URLsDiscovered, job.IssuesFound)

	// We expect at least 12 HTML pages crawled (homepage, about, contact, blog,
	// blog/post-1..5, products, products/widget, products/widget-pro, gallery).
	// /old-page and /redirect-step are redirects (not pages), /broken-link is 404.
	if job.PagesCrawled < 12 {
		t.Errorf("pages_crawled = %d, want >= 12", job.PagesCrawled)
	}

	// --- URL status counts ---
	urlCounts, err := db.CountURLsByStatus(job.ID)
	if err != nil {
		t.Fatalf("CountURLsByStatus: %v", err)
	}
	t.Logf("URL status counts: %v", urlCounts)
	fetched := urlCounts["fetched"]
	if fetched < 10 {
		t.Errorf("fetched URLs = %d, want >= 10", fetched)
	}

	// --- Issues ---
	issuesByType, err := db.CountIssuesByType(job.ID)
	if err != nil {
		t.Fatalf("CountIssuesByType: %v", err)
	}
	t.Logf("issues by type: %v", issuesByType)

	// missing_title: blog/post-4 has no <title>
	if issuesByType["missing_title"] < 1 {
		t.Errorf("expected missing_title issues, got %d", issuesByType["missing_title"])
	}

	// thin_content: blog/post-3 has < 200 words
	if issuesByType["thin_content"] < 1 {
		t.Errorf("expected thin_content issues, got %d", issuesByType["thin_content"])
	}

	// multiple_h1: blog/post-5 has 3 H1 tags
	if issuesByType["multiple_h1"] < 1 {
		t.Errorf("expected multiple_h1 issues, got %d", issuesByType["multiple_h1"])
	}

	// redirect_chain: /old-page → /redirect-step → /about (2 hops)
	if issuesByType["redirect_chain"] < 1 {
		t.Errorf("expected redirect_chain issues, got %d", issuesByType["redirect_chain"])
	}

	// missing_alt_attribute: gallery has 3 images without alt
	if issuesByType["missing_alt_attribute"] < 1 {
		t.Errorf("expected missing_alt_attribute issues, got %d", issuesByType["missing_alt_attribute"])
	}

	// missing_description: blog/post-4 has no meta description
	if issuesByType["missing_description"] < 1 {
		t.Errorf("expected missing_description issues, got %d", issuesByType["missing_description"])
	}

	// --- Edges ---
	edgeCount, err := db.CountEdges(job.ID)
	if err != nil {
		t.Fatalf("CountEdges: %v", err)
	}
	t.Logf("total edges: %d", edgeCount)
	if edgeCount < 20 {
		t.Errorf("edge count = %d, want >= 20", edgeCount)
	}

	// --- Crawl Summary ---
	summary, err := db.GetCrawlSummary(job.ID)
	if err != nil {
		t.Fatalf("GetCrawlSummary: %v", err)
	}
	t.Logf("summary: totalPages=%d totalURLs=%d totalIssues=%d crawlDuration=%.2fs",
		summary.TotalPages, summary.TotalURLs, summary.TotalIssues, summary.CrawlDuration)

	if summary.TotalPages < 1 {
		t.Errorf("summary.TotalPages = %d, want > 0", summary.TotalPages)
	}
	if summary.TotalURLs < 1 {
		t.Errorf("summary.TotalURLs = %d, want > 0", summary.TotalURLs)
	}
	if summary.TotalIssues < 1 {
		t.Errorf("summary.TotalIssues = %d, want > 0", summary.TotalIssues)
	}

	// --- Verify sitemap entries were discovered ---
	var sitemapCount int
	err = db.QueryRow("SELECT COUNT(*) FROM sitemap_entries WHERE job_id = ?", job.ID).Scan(&sitemapCount)
	if err != nil {
		t.Fatalf("counting sitemap entries: %v", err)
	}
	t.Logf("sitemap entries discovered: %d", sitemapCount)
	if sitemapCount == 0 {
		t.Error("expected sitemap entries to be discovered via host onboarding, got 0")
	}

	// --- Verify robots directives were stored ---
	var robotsCount int
	err = db.QueryRow("SELECT COUNT(*) FROM robots_directives WHERE job_id = ?", job.ID).Scan(&robotsCount)
	if err != nil {
		t.Fatalf("counting robots directives: %v", err)
	}
	t.Logf("robots directives stored: %d", robotsCount)
	if robotsCount == 0 {
		t.Error("expected robots directives to be stored via host onboarding, got 0")
	}

	// --- Verify specific pages exist (including orphan /hidden-page from sitemap) ---
	expectedPaths := []string{
		"/", "/about", "/contact", "/blog",
		"/blog/post-1", "/blog/post-2", "/blog/post-3", "/blog/post-4", "/blog/post-5",
		"/products", "/products/widget", "/products/widget-pro",
		"/gallery",
		"/hidden-page", // orphan page only discoverable via sitemap
	}
	for _, path := range expectedPaths {
		fullURL := site.URL + path
		normalized, normErr := urlutil.Normalize(fullURL)
		if normErr != nil {
			t.Errorf("normalizing %q: %v", fullURL, normErr)
			continue
		}
		urlRec, urlErr := db.GetURLByNormalized(job.ID, normalized)
		if urlErr != nil {
			t.Errorf("URL %q not found in DB: %v", path, urlErr)
			continue
		}
		if urlRec.Status != "fetched" {
			t.Errorf("URL %q status = %q, want fetched", path, urlRec.Status)
		}
	}

	// --- Verify structured data detected on post-1 ---
	post1URL := site.URL + "/blog/post-1"
	post1Norm, _ := urlutil.Normalize(post1URL)
	post1Rec, err := db.GetURLByNormalized(job.ID, post1Norm)
	if err != nil {
		t.Fatalf("post-1 URL not found: %v", err)
	}
	post1Page, err := db.GetPageByURL(job.ID, post1Rec.ID)
	if err != nil {
		t.Fatalf("post-1 page not found: %v", err)
	}
	if !post1Page.JSONLDRaw.Valid || post1Page.JSONLDRaw.String == "" || post1Page.JSONLDRaw.String == "[]" {
		t.Error("post-1 should have JSON-LD structured data")
	}

	// --- Verify post-4 has missing_title issue ---
	post4URL := site.URL + "/blog/post-4"
	post4Norm, _ := urlutil.Normalize(post4URL)
	post4Rec, err := db.GetURLByNormalized(job.ID, post4Norm)
	if err != nil {
		t.Fatalf("post-4 URL not found: %v", err)
	}
	post4Page, err := db.GetPageByURL(job.ID, post4Rec.ID)
	if err != nil {
		t.Fatalf("post-4 page not found: %v", err)
	}
	if post4Page.Title.Valid && post4Page.Title.String != "" {
		t.Errorf("post-4 title = %q, expected empty (missing title)", post4Page.Title.String)
	}

	// --- Verify gallery has missing_alt images ---
	galleryURL := site.URL + "/gallery"
	galleryNorm, _ := urlutil.Normalize(galleryURL)
	galleryRec, err := db.GetURLByNormalized(job.ID, galleryNorm)
	if err != nil {
		t.Fatalf("gallery URL not found: %v", err)
	}
	galleryPage, err := db.GetPageByURL(job.ID, galleryRec.ID)
	if err != nil {
		t.Fatalf("gallery page not found: %v", err)
	}
	if !galleryPage.ImagesJSON.Valid || galleryPage.ImagesJSON.String == "[]" {
		t.Error("gallery should have images")
	}

	// --- Verify post-5 has multiple H1 ---
	post5URL := site.URL + "/blog/post-5"
	post5Norm, _ := urlutil.Normalize(post5URL)
	post5Rec, err := db.GetURLByNormalized(job.ID, post5Norm)
	if err != nil {
		t.Fatalf("post-5 URL not found: %v", err)
	}
	post5Page, err := db.GetPageByURL(job.ID, post5Rec.ID)
	if err != nil {
		t.Fatalf("post-5 page not found: %v", err)
	}
	if !post5Page.H1JSON.Valid {
		t.Error("post-5 H1 JSON should be set")
	} else {
		var h1s []string
		if jsonErr := json.Unmarshal([]byte(post5Page.H1JSON.String), &h1s); jsonErr != nil {
			t.Errorf("parsing post-5 H1 JSON: %v", jsonErr)
		} else if len(h1s) < 2 {
			t.Errorf("post-5 H1 count = %d, want >= 2", len(h1s))
		}
	}

	// --- Verify image assets were HEAD-checked ---
	assets, assetsErr := db.GetAssetsByJob(job.ID, 100)
	if assetsErr != nil {
		t.Fatalf("querying assets: %v", assetsErr)
	}
	if len(assets) < 3 {
		t.Errorf("expected at least 3 image assets, got %d", len(assets))
	}
	// Check that at least one has a 200 status and image content type
	foundOK := false
	for _, a := range assets {
		if a.StatusCode.Valid && a.StatusCode.Int64 == 200 &&
			a.ContentType.Valid && (
			a.ContentType.String == "image/jpeg" ||
				a.ContentType.String == "image/png" ||
				a.ContentType.String == "image/gif") {
			foundOK = true
			break
		}
	}
	if !foundOK {
		t.Error("expected at least one image asset with 200 status and image/* content-type")
	}

	// Verify asset_references link images to the gallery page
	refRows, refErr := db.Query(
		`SELECT COUNT(*) FROM asset_references WHERE job_id = ? AND source_page_url_id = ?`,
		job.ID, galleryRec.ID,
	)
	if refErr != nil {
		t.Fatalf("querying asset_references: %v", refErr)
	}
	defer refRows.Close()
	var refCount int
	if refRows.Next() {
		refRows.Scan(&refCount)
	}
	if refCount < 3 {
		t.Errorf("expected at least 3 asset references from gallery page, got %d", refCount)
	}

	t.Log("integration test completed successfully")
}
