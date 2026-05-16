package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/crawl"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/fetcher"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/issues"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/parser"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/ssrf"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/urlutil"
)

// testSite serves a 5-page HTML site for integration testing.
func testSite() http.Handler {
	pages := map[string]string{
		"/": `<!DOCTYPE html><html><head><title>Home</title>
			<meta name="description" content="This is the home page of our test website for SEO crawling purposes."></head>
			<body><h1>Home</h1>
			<p>Welcome to our test website. This content is designed to be long enough to avoid thin content warnings during testing. We need enough words to pass the threshold configured in the test configuration.</p>
			<a href="/about">About</a>
			<a href="/blog">Blog</a></body></html>`,

		"/about": `<!DOCTYPE html><html><head><title>About Us</title>
			<meta name="description" content="Learn more about our company and what we do in this about page section."></head>
			<body><h1>About</h1>
			<p>This is the about page. It contains information about our company and team. We have many interesting things to share with you about our mission and values.</p>
			<a href="/contact">Contact</a></body></html>`,

		"/blog": `<!DOCTYPE html><html><head><title>Blog</title>
			<meta name="description" content="Read our latest blog posts about technology, development, and SEO optimization."></head>
			<body><h1>Blog</h1>
			<p>Welcome to our blog section. Here you will find many articles about various topics including technology, development, and search engine optimization best practices.</p>
			<a href="/blog/post1">First Post</a></body></html>`,

		"/contact": `<!DOCTYPE html><html><head><title>Contact Us</title>
			<meta name="description" content="Get in touch with our team through this contact page for any questions or inquiries."></head>
			<body><h1>Contact</h1>
			<p>You can reach us at our office. We are available Monday through Friday and will respond to all inquiries within one business day of receipt.</p></body></html>`,

		"/blog/post1": `<!DOCTYPE html><html><head><title>First Post</title>
			<meta name="description" content="Read our first blog post about getting started with web development and optimization."></head>
			<body><h1>First Post</h1>
			<p>This is the first blog post. It covers many interesting topics about web development and search engine optimization techniques and strategies.</p>
			<a href="/blog">Back to Blog</a></body></html>`,
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html, ok := pages[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, html)
	})
}

func TestRunCrawlIntegration(t *testing.T) {
	// Start test server
	ts := httptest.NewServer(testSite())
	defer ts.Close()

	// Setup database
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	// Create config
	cfg := config.DefaultConfig()
	cfg.GlobalConcurrency = 2
	cfg.MaxPages = 100
	cfg.MaxDepth = 10
	cfg.RequestTimeout = 5 * time.Second
	cfg.AllowPrivateNetworks = true
	cfg.SSRFProtection = false
	cfg.ThinContentThreshold = 10 // Lower threshold for test
	cfg.MaxQueryVariantsPerPath = 50

	// Setup dependencies
	guard := ssrf.NewGuard(true) // allow private networks for test server
	f := fetcher.New(fetcher.Options{
		UserAgent:           "test-crawler/1.0",
		Timeout:             5 * time.Second,
		MaxResponseBody:     5 * 1024 * 1024,
		MaxDecompressedBody: 20 * 1024 * 1024,
		MaxRedirectHops:     10,
		Retries:             0,
		AllowInsecureTLS:    false,
		SSRFGuard:           nil, // disable SSRF for test
	})
	rl := fetcher.NewRateLimiter(cfg.PerHostConcurrency)

	// Create scope checker for test server host
	// ScopeChecker.IsInScope uses url.Hostname() which strips port,
	// so we must pass just the hostname.
	tsURL, _ := url.Parse(ts.URL)
	sc, err := urlutil.NewScopeChecker("exact_host", tsURL.Hostname(), nil)
	if err != nil {
		t.Fatalf("creating scope checker: %v", err)
	}

	// Create job
	seedURLs, _ := json.Marshal([]string{ts.URL + "/"})
	job, err := db.CreateJob("crawl", "{}", string(seedURLs))
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	// Create and run engine
	eng := New(EngineConfig{
		DB:           db,
		Fetcher:      f,
		RateLimiter:  rl,
		ScopeChecker: sc,
		SSRFGuard:    guard,
		Config:       &cfg,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := eng.RunCrawl(ctx, job.ID); err != nil {
		t.Fatalf("RunCrawl: %v", err)
	}

	// Verify job status
	job, err = db.GetJob(job.ID)
	if err != nil {
		t.Fatalf("getting job: %v", err)
	}
	if job.Status != "completed" {
		t.Errorf("job status = %q, want completed", job.Status)
	}
	if !job.StartedAt.Valid {
		t.Error("started_at should be set")
	}
	if !job.FinishedAt.Valid {
		t.Error("finished_at should be set")
	}

	// Verify pages crawled
	if job.PagesCrawled < 5 {
		t.Errorf("pages_crawled = %d, want >= 5", job.PagesCrawled)
	}

	// Verify urls discovered
	if job.URLsDiscovered < 5 {
		t.Errorf("urls_discovered = %d, want >= 5", job.URLsDiscovered)
	}

	// Verify all 5 pages exist with correct depths
	expectedPages := map[string]int{
		ts.URL + "/":           0,
		ts.URL + "/about":      1,
		ts.URL + "/blog":       1,
		ts.URL + "/contact":    2,
		ts.URL + "/blog/post1": 2,
	}

	for pageURL, expectedDepth := range expectedPages {
		normalized, err := urlutil.Normalize(pageURL)
		if err != nil {
			t.Errorf("normalizing %q: %v", pageURL, err)
			continue
		}
		urlRec, err := db.GetURLByNormalized(job.ID, normalized)
		if err != nil {
			t.Errorf("URL %q not found: %v", pageURL, err)
			continue
		}

		page, err := db.GetPageByURL(job.ID, urlRec.ID)
		if err != nil {
			t.Errorf("page for %q not found: %v", pageURL, err)
			continue
		}

		if int(page.Depth) != expectedDepth {
			t.Errorf("page %q depth = %d, want %d", pageURL, page.Depth, expectedDepth)
		}
	}

	// Verify edges exist
	// / links to /about and /blog, so source URL for / should have outbound edges
	rootNorm, _ := urlutil.Normalize(ts.URL + "/")
	rootURL, err := db.GetURLByNormalized(job.ID, rootNorm)
	if err != nil {
		t.Fatalf("root URL not found: %v", err)
	}

	edges, err := db.GetEdgesBySource(job.ID, rootURL.ID, 100, "")
	if err != nil {
		t.Fatalf("getting edges: %v", err)
	}
	if len(edges) < 2 {
		t.Errorf("root page edges = %d, want >= 2", len(edges))
	}

	// Verify no duplicate fetches (each URL fetched exactly once)
	var fetchCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM fetches WHERE job_id = ?`, job.ID).Scan(&fetchCount)
	if err != nil {
		t.Fatalf("counting fetches: %v", err)
	}
	if fetchCount != 5 {
		t.Errorf("fetch count = %d, want 5", fetchCount)
	}
}

func TestAuditHTTPToHTTPSRedirectsChecksEveryDiscoveredHost(t *testing.T) {
	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://127.0.0.1:1/", http.StatusMovedPermanently)
	}))
	defer redirectServer.Close()
	noRedirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>still http</body></html>")
	}))
	defer noRedirectServer.Close()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	f := fetcher.New(fetcher.Options{
		UserAgent:           "test-crawler/1.0",
		Timeout:             500 * time.Millisecond,
		MaxResponseBody:     1024,
		MaxDecompressedBody: 1024,
		MaxRedirectHops:     5,
	})
	eng := New(EngineConfig{DB: db, Fetcher: f})

	job, err := db.CreateJob("crawl", "{}", `["https://example.com/"]`)
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}
	for _, serverURL := range []string{redirectServer.URL, noRedirectServer.URL} {
		parsed, err := url.Parse(serverURL)
		if err != nil {
			t.Fatalf("parsing test server URL: %v", err)
		}
		httpsVariant := "https://" + parsed.Host + "/"
		if _, err := db.UpsertURL(job.ID, httpsVariant, parsed.Hostname(), "fetched", true, "seed"); err != nil {
			t.Fatalf("seeding URL: %v", err)
		}
	}

	eng.auditHTTPToHTTPSRedirects(context.Background(), job.ID)

	var auditFetches int
	if err := db.QueryRow(`SELECT COUNT(*) FROM fetches WHERE job_id = ? AND fetch_kind = 'http_https_audit'`, job.ID).Scan(&auditFetches); err != nil {
		t.Fatalf("counting audit fetches: %v", err)
	}
	if auditFetches != 2 {
		t.Fatalf("audit fetches = %d, want 2", auditFetches)
	}

	redirectParsed, _ := url.Parse(redirectServer.URL)
	var redirectHops int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM redirect_hops
		WHERE job_id = ? AND from_url = ? AND to_url LIKE 'https://%'
	`, job.ID, "http://"+redirectParsed.Host+"/").Scan(&redirectHops); err != nil {
		t.Fatalf("counting redirect hops: %v", err)
	}
	if redirectHops != 1 {
		t.Fatalf("redirect audit hops = %d, want 1", redirectHops)
	}

	noRedirectParsed, _ := url.Parse(noRedirectServer.URL)
	var noRedirectURLCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM urls
		WHERE job_id = ? AND normalized_url = ?
	`, job.ID, "http://"+noRedirectParsed.Host+"/").Scan(&noRedirectURLCount); err != nil {
		t.Fatalf("counting no-redirect audit URL: %v", err)
	}
	if noRedirectURLCount != 1 {
		t.Fatalf("no-redirect audit URL rows = %d, want 1", noRedirectURLCount)
	}
}

func TestRunCrawlRebuildsScopeCheckerForEachJob(t *testing.T) {
	// Regression for production bug: a long-lived HTTP server reuses one Engine
	// across jobs. The first crawl's scope checker must not reject the next
	// crawl when the seed host changes.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><title>OK</title><meta name="description" content="Enough description for the test page."></head><body><h1>OK</h1><p>Enough body copy for the crawler test.</p></body></html>`)
	}))
	defer ts.Close()

	parsed, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parsing test URL: %v", err)
	}
	localhostURL := "http://localhost"
	if parsed.Port() != "" {
		localhostURL += ":" + parsed.Port()
	}

	dbPath := filepath.Join(t.TempDir(), "scope-reset.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	cfg := config.DefaultConfig()
	cfg.ScopeMode = config.ScopeModeExactHost
	cfg.GlobalConcurrency = 1
	cfg.MaxPages = 10
	cfg.AllowPrivateNetworks = true
	cfg.SSRFProtection = false
	cfg.RequestTimeout = 5 * time.Second
	cfg.ThinContentThreshold = 1

	eng := New(EngineConfig{
		DB: db,
		Fetcher: fetcher.New(fetcher.Options{
			UserAgent:           "test-crawler/1.0",
			Timeout:             5 * time.Second,
			MaxResponseBody:     5 * 1024 * 1024,
			MaxDecompressedBody: 20 * 1024 * 1024,
			MaxRedirectHops:     10,
		}),
		RateLimiter: fetcher.NewRateLimiter(cfg.PerHostConcurrency),
		Config:      &cfg,
	})

	run := func(rawURL string) storage.CrawlJob {
		seedURLs, _ := json.Marshal([]string{rawURL + "/"})
		job, err := db.CreateJob("crawl", `{}`, string(seedURLs))
		if err != nil {
			t.Fatalf("creating job for %s: %v", rawURL, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := eng.RunCrawl(ctx, job.ID); err != nil {
			t.Fatalf("RunCrawl for %s: %v", rawURL, err)
		}
		got, err := db.GetJob(job.ID)
		if err != nil {
			t.Fatalf("getting job for %s: %v", rawURL, err)
		}
		return *got
	}

	first := run(localhostURL)
	if first.PagesCrawled == 0 {
		t.Fatalf("first crawl pages_crawled = 0, want > 0")
	}

	second := run(ts.URL)
	if second.PagesCrawled == 0 {
		t.Fatalf("second crawl pages_crawled = 0; scope checker from first host leaked into second job")
	}
}

func TestRunCrawlHonorsMaxPagesWhenFrontierIsLarge(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/" {
			fmt.Fprint(w, `<!doctype html><html><head><title>Home</title><meta name="description" content="Home description for max pages test."></head><body>`)
			for i := 0; i < 50; i++ {
				fmt.Fprintf(w, `<a href="/p%d">Page %d</a>`, i, i)
			}
			fmt.Fprint(w, `</body></html>`)
			return
		}
		fmt.Fprintf(w, `<!doctype html><html><head><title>%s</title><meta name="description" content="Page description for max pages test."></head><body><p>Body copy.</p></body></html>`, r.URL.Path)
	}))
	defer ts.Close()

	dbPath := filepath.Join(t.TempDir(), "max-pages.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	cfg := config.DefaultConfig()
	cfg.GlobalConcurrency = 8
	cfg.MaxPages = 100
	cfg.MaxDepth = 10
	cfg.AllowPrivateNetworks = true
	cfg.SSRFProtection = false
	cfg.RequestTimeout = 5 * time.Second
	cfg.ThinContentThreshold = 1

	tsURL, _ := url.Parse(ts.URL)
	sc, err := urlutil.NewScopeChecker("exact_host", tsURL.Hostname(), nil)
	if err != nil {
		t.Fatalf("creating scope checker: %v", err)
	}

	seedURLs, _ := json.Marshal([]string{ts.URL + "/"})
	job, err := db.CreateJob("crawl", `{"maxPages":5}`, string(seedURLs))
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	eng := New(EngineConfig{
		DB: db,
		Fetcher: fetcher.New(fetcher.Options{
			UserAgent:           "test-crawler/1.0",
			Timeout:             5 * time.Second,
			MaxResponseBody:     5 * 1024 * 1024,
			MaxDecompressedBody: 20 * 1024 * 1024,
			MaxRedirectHops:     10,
		}),
		RateLimiter:  fetcher.NewRateLimiter(cfg.PerHostConcurrency),
		ScopeChecker: sc,
		Config:       &cfg,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := eng.RunCrawl(ctx, job.ID); err != nil {
		t.Fatalf("RunCrawl: %v", err)
	}

	got, err := db.GetJob(job.ID)
	if err != nil {
		t.Fatalf("getting job: %v", err)
	}
	if got.PagesCrawled > 5 {
		t.Fatalf("pages_crawled = %d, want <= 5", got.PagesCrawled)
	}
	if got.PagesCrawled != 5 {
		t.Fatalf("pages_crawled = %d, want crawl to fill maxPages limit 5", got.PagesCrawled)
	}
}

func TestRunCrawlUsesJobRenderModeConfig(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>http://%s/hidden</loc></url></urlset>`, r.Host)
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!doctype html><html><head><title>Home</title><meta name="description" content="Home description for render mode test."></head><body><p>Home.</p></body></html>`)
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!doctype html><html><head><title>Hidden</title><meta name="description" content="Hidden description for render mode test."></head><body><p>Hidden.</p></body></html>`)
		}
	}))
	defer ts.Close()

	dbPath := filepath.Join(t.TempDir(), "render-mode.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	cfg := config.DefaultConfig()
	cfg.GlobalConcurrency = 1
	cfg.MaxPages = 10
	cfg.MaxDepth = 10
	cfg.RenderMode = config.RenderModeHybrid
	cfg.AllowPrivateNetworks = true
	cfg.SSRFProtection = false
	cfg.RequestTimeout = 5 * time.Second
	cfg.ThinContentThreshold = 1

	tsURL, _ := url.Parse(ts.URL)
	sc, err := urlutil.NewScopeChecker("exact_host", tsURL.Hostname(), nil)
	if err != nil {
		t.Fatalf("creating scope checker: %v", err)
	}

	seedURLs, _ := json.Marshal([]string{ts.URL + "/"})
	job, err := db.CreateJob("crawl", `{"renderMode":"static","maxPages":1}`, string(seedURLs))
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	eng := New(EngineConfig{
		DB: db,
		Fetcher: fetcher.New(fetcher.Options{
			UserAgent:           "test-crawler/1.0",
			Timeout:             5 * time.Second,
			MaxResponseBody:     5 * 1024 * 1024,
			MaxDecompressedBody: 20 * 1024 * 1024,
			MaxRedirectHops:     10,
		}),
		RateLimiter:  fetcher.NewRateLimiter(cfg.PerHostConcurrency),
		ScopeChecker: sc,
		Config:       &cfg,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := eng.RunCrawl(ctx, job.ID); err != nil {
		t.Fatalf("RunCrawl: %v", err)
	}

	var sitemapGapEvents int
	if err := db.QueryRow(`SELECT COUNT(*) FROM crawl_events WHERE job_id = ? AND event_type = 'sitemap_gap'`, job.ID).Scan(&sitemapGapEvents); err != nil {
		t.Fatalf("querying sitemap gap events: %v", err)
	}
	if sitemapGapEvents != 0 {
		t.Fatalf("sitemap_gap events = %d, want 0 for job renderMode=static", sitemapGapEvents)
	}
}

func TestRunCrawlCancellation(t *testing.T) {
	// All pages are slow so cancellation always triggers
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Slow</title></head><body>slow</body></html>`)
	}))
	defer ts.Close()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	cfg := config.DefaultConfig()
	cfg.GlobalConcurrency = 1
	cfg.AllowPrivateNetworks = true
	cfg.SSRFProtection = false
	cfg.RequestTimeout = 30 * time.Second

	f := fetcher.New(fetcher.Options{
		UserAgent:           "test-crawler/1.0",
		Timeout:             30 * time.Second,
		MaxResponseBody:     5 * 1024 * 1024,
		MaxDecompressedBody: 20 * 1024 * 1024,
		MaxRedirectHops:     10,
	})
	rl := fetcher.NewRateLimiter(cfg.PerHostConcurrency)

	tsURL, _ := url.Parse(ts.URL)
	sc, _ := urlutil.NewScopeChecker("exact_host", tsURL.Hostname(), nil)

	seedURLs, _ := json.Marshal([]string{ts.URL + "/"})
	job, _ := db.CreateJob("crawl", "{}", string(seedURLs))

	eng := New(EngineConfig{
		DB:           db,
		Fetcher:      f,
		RateLimiter:  rl,
		ScopeChecker: sc,
		Config:       &cfg,
	})

	// Use a short request timeout so fetcher doesn't block too long
	cfg.RequestTimeout = 200 * time.Millisecond
	f2 := fetcher.New(fetcher.Options{
		UserAgent:           "test-crawler/1.0",
		Timeout:             200 * time.Millisecond,
		MaxResponseBody:     5 * 1024 * 1024,
		MaxDecompressedBody: 20 * 1024 * 1024,
		MaxRedirectHops:     10,
	})
	eng.fetcher = f2

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = eng.RunCrawl(ctx, job.ID)
	if err == nil {
		t.Error("expected error from cancelled crawl")
	}

	job, _ = db.GetJob(job.ID)
	if job.Status != "cancelled" && job.Status != "completed" {
		t.Errorf("job status = %q, want cancelled or completed", job.Status)
	}
}

func TestPersistItemDoesNotDoNetworkHEADInsideTransaction(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "persist-no-head.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	job, err := db.CreateJob("crawl", `{}`, `["https://example.com/"]`)
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}
	urlID, err := db.UpsertURL(job.ID, "https://example.com/", "example.com", "fetched", true, "seed")
	if err != nil {
		t.Fatalf("upserting url: %v", err)
	}

	headStarted := make(chan struct{}, 1)
	slowExternal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			headStarted <- struct{}{}
			time.Sleep(500 * time.Millisecond)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer slowExternal.Close()

	eng := &Engine{
		db:      db,
		fetcher: fetcher.New(fetcher.Options{Timeout: time.Second}),
	}

	started := time.Now()
	err = eng.persistItem(context.Background(), job.ID, persistItem{
		fetchSeq: 1,
		parseResult: parseResult{
			fetchResult: fetchResult{
				urlID:    urlID,
				url:      "https://example.com/",
				host:     "example.com",
				depth:    0,
				fetchSeq: 1,
				result: &fetcher.FetchResult{
					RequestedURL: "https://example.com/",
					FinalURL:     "https://example.com/",
					StatusCode:   http.StatusOK,
					ContentType:  "text/html",
				},
			},
			edges: []crawl.DiscoveredEdge{{
				SourceURLID:         urlID,
				DeclaredTargetURL:   slowExternal.URL,
				NormalizedTargetURL: slowExternal.URL,
				SourceKind:          "html",
				RelationType:        "canonical",
				DiscoveryMode:       "static",
				IsInternal:          false,
			}},
		},
	})
	if err != nil {
		t.Fatalf("persistItem: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("persistItem took %v; likely performed network I/O while holding DB transaction", elapsed)
	}
	select {
	case <-headStarted:
		t.Fatal("persistItem should not issue external HEAD requests")
	default:
	}
}

func TestPersistItemStoresPageUnderNormalizedFinalURL(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "persist-final-url.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	job, err := db.CreateJob("crawl", "{}", "[\"https://example.com/page\"]")
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}
	requestedID, err := db.UpsertURL(job.ID, "https://example.com/page", "example.com", "fetched", true, "seed")
	if err != nil {
		t.Fatalf("upserting requested URL: %v", err)
	}

	scope, err := urlutil.NewScopeChecker("exact_host", "www.example.com", nil)
	if err != nil {
		t.Fatalf("creating scope checker: %v", err)
	}
	eng := &Engine{
		db:           db,
		scopeChecker: scope,
		fetcher:      fetcher.New(fetcher.Options{Timeout: time.Second}),
	}

	body := []byte("<!doctype html><html><head><title>Final</title></head><body>" +
		`<h1>Final</h1><a href="/next">Next</a><img src="/logo.png" alt="Logo">` +
		"</body></html>")
	page, err := parser.ParseHTML(body, "https://www.example.com:443/page", http.Header{})
	if err != nil {
		t.Fatalf("parsing HTML: %v", err)
	}

	item := persistItem{
		fetchSeq: 1,
		parseResult: parseResult{
			fetchResult: fetchResult{
				urlID:    requestedID,
				url:      "https://example.com/page",
				host:     "example.com",
				depth:    0,
				fetchSeq: 1,
				result: &fetcher.FetchResult{
					RequestedURL: "https://example.com/page",
					FinalURL:     "https://www.example.com:443/page",
					StatusCode:   http.StatusOK,
					ContentType:  "text/html; charset=utf-8",
					Body:         body,
					BodySize:     int64(len(body)),
					RedirectHops: []fetcher.RedirectHop{{
						HopIndex:   0,
						StatusCode: http.StatusMovedPermanently,
						FromURL:    "https://example.com/page",
						ToURL:      "https://www.example.com:443/page",
					}},
				},
			},
			page: page,
			edges: []crawl.DiscoveredEdge{{
				SourceURLID:         requestedID,
				DeclaredTargetURL:   "https://www.example.com/next",
				NormalizedTargetURL: "https://www.example.com/next",
				SourceKind:          "html",
				RelationType:        "link",
				DiscoveryMode:       "static",
				IsInternal:          true,
			}},
			images: []discoveredImage{{
				normalizedURL: "https://www.example.com/logo.png",
				host:          "www.example.com",
				isInternal:    true,
				sourceURLID:   requestedID,
			}},
			issues: []issues.DetectedIssue{{
				IssueType:   "missing_canonical",
				Severity:    "warning",
				Scope:       "page_local",
				DetailsJSON: "{\"url\":\"https://www.example.com/page\"}",
			}},
		},
	}
	if err := eng.persistItem(context.Background(), job.ID, item); err != nil {
		t.Fatalf("persistItem: %v", err)
	}

	finalURL, err := db.GetURLByNormalized(job.ID, "https://www.example.com/page")
	if err != nil {
		t.Fatalf("getting final URL: %v", err)
	}
	duplicate := item
	duplicate.fetchSeq = 2
	duplicate.parseResult.fetchResult.urlID = finalURL.ID
	duplicate.parseResult.fetchResult.url = "https://www.example.com/page"
	duplicate.parseResult.fetchResult.host = "www.example.com"
	duplicate.parseResult.fetchResult.fetchSeq = 2
	duplicateResult := *item.parseResult.fetchResult.result
	duplicateResult.RequestedURL = "https://www.example.com/page"
	duplicateResult.FinalURL = "https://www.example.com/page"
	duplicateResult.RedirectHops = nil
	duplicate.parseResult.fetchResult.result = &duplicateResult
	if err := eng.persistItem(context.Background(), job.ID, duplicate); err != nil {
		t.Fatalf("persistItem duplicate final URL: %v", err)
	}

	var pageURL string
	if err := db.QueryRow(
		"SELECT u.normalized_url FROM pages p JOIN urls u ON u.id = p.url_id WHERE p.job_id = ?",
		job.ID,
	).Scan(&pageURL); err != nil {
		t.Fatalf("querying page URL: %v", err)
	}
	if pageURL != "https://www.example.com/page" {
		t.Fatalf("page URL = %q, want normalized final URL", pageURL)
	}

	var pageCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM pages WHERE job_id = ?", job.ID).Scan(&pageCount); err != nil {
		t.Fatalf("counting page rows: %v", err)
	}
	if pageCount != 1 {
		t.Fatalf("page rows = %d, want 1", pageCount)
	}

	var requestedPageCount int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM pages p JOIN urls u ON u.id = p.url_id WHERE p.job_id = ? AND u.normalized_url = ?",
		job.ID, "https://example.com/page",
	).Scan(&requestedPageCount); err != nil {
		t.Fatalf("counting requested page rows: %v", err)
	}
	if requestedPageCount != 0 {
		t.Fatalf("requested URL page rows = %d, want 0", requestedPageCount)
	}

	var edgeSourceURL string
	if err := db.QueryRow(
		"SELECT u.normalized_url FROM edges e JOIN urls u ON u.id = e.source_url_id WHERE e.job_id = ?",
		job.ID,
	).Scan(&edgeSourceURL); err != nil {
		t.Fatalf("querying edge source URL: %v", err)
	}
	if edgeSourceURL != "https://www.example.com/page" {
		t.Fatalf("edge source URL = %q, want normalized final URL", edgeSourceURL)
	}

	var edgeCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM edges WHERE job_id = ?", job.ID).Scan(&edgeCount); err != nil {
		t.Fatalf("counting edges: %v", err)
	}
	if edgeCount != 1 {
		t.Fatalf("edge rows = %d, want 1", edgeCount)
	}

	var assetSourceURL string
	if err := db.QueryRow(
		"SELECT u.normalized_url FROM asset_references ar JOIN urls u ON u.id = ar.source_page_url_id WHERE ar.job_id = ?",
		job.ID,
	).Scan(&assetSourceURL); err != nil {
		t.Fatalf("querying asset source URL: %v", err)
	}
	if assetSourceURL != "https://www.example.com/page" {
		t.Fatalf("asset source URL = %q, want normalized final URL", assetSourceURL)
	}

	var assetRefCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM asset_references WHERE job_id = ?", job.ID).Scan(&assetRefCount); err != nil {
		t.Fatalf("counting asset refs: %v", err)
	}
	if assetRefCount != 1 {
		t.Fatalf("asset reference rows = %d, want 1", assetRefCount)
	}

	var issueCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM issues WHERE job_id = ?", job.ID).Scan(&issueCount); err != nil {
		t.Fatalf("counting issues: %v", err)
	}
	if issueCount != 1 {
		t.Fatalf("issue rows = %d, want 1", issueCount)
	}
}

func TestHasURLFragment(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "absolute fragment", raw: "https://example.com/#products", want: true},
		{name: "relative fragment", raw: "#products", want: true},
		{name: "plain url", raw: "https://example.com/products", want: false},
		{name: "query hash character escaped", raw: "https://example.com/search?q=%23products", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasURLFragment(tc.raw); got != tc.want {
				t.Fatalf("hasURLFragment(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestQueueBrowserDiscoveredLinkURLs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "browser-discovered.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	job, err := db.CreateJob("crawl", `{}`, `["https://example.com/"]`)
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}
	sourceID, err := db.UpsertURL(job.ID, "https://example.com/", "example.com", "fetched", true, "seed")
	if err != nil {
		t.Fatalf("upserting source: %v", err)
	}
	fetchID, err := db.InsertFetch(storage.FetchInput{JobID: job.ID, FetchSeq: 1, RequestedURLID: sourceID, StatusCode: 200, ContentType: "text/html", RenderMode: "static"})
	if err != nil {
		t.Fatalf("inserting fetch: %v", err)
	}
	title := "Home"
	if _, err := db.InsertPage(storage.PageInput{JobID: job.ID, URLID: sourceID, FetchID: fetchID, Depth: 0, Title: &title, IndexabilityState: "indexable"}); err != nil {
		t.Fatalf("inserting source page: %v", err)
	}
	targetID, err := db.UpsertURL(job.ID, "https://example.com/return-policy", "example.com", "discovered", true, "browser")
	if err != nil {
		t.Fatalf("upserting target: %v", err)
	}
	if _, err := db.InsertEdge(storage.EdgeInput{JobID: job.ID, SourceURLID: sourceID, NormalizedTargetURLID: targetID, SourceKind: "rendered_dom", RelationType: "link", DiscoveryMode: "browser", IsInternal: true, DeclaredTargetURL: "https://example.com/return-policy"}); err != nil {
		t.Fatalf("inserting edge: %v", err)
	}

	eng := &Engine{db: db}
	q := eng.queueBrowserDiscoveredLinkURLs(job.ID, 5)
	if q.Len() != 1 {
		t.Fatalf("queued URLs = %d, want 1", q.Len())
	}
	item, ok := q.Pop()
	if !ok {
		t.Fatal("expected queued item")
	}
	if item.URLID != targetID || item.Depth != 1 {
		t.Fatalf("queued item = %+v, want target ID %d at depth 1", item, targetID)
	}
	rec, err := db.GetURL(targetID)
	if err != nil {
		t.Fatalf("getting target URL: %v", err)
	}
	if rec.Status != "queued" {
		t.Fatalf("target status = %q, want queued", rec.Status)
	}
}
