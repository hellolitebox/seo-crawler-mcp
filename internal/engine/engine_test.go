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
