package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/crawl"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/fetcher"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/urlutil"
)

// TestCrossScopeRedirect_OutOfScopeStatus verifies that when a fetch result
// has a FinalURL on a different domain than the requested URL, the final URL
// is recorded with "out_of_scope" status.
func TestCrossScopeRedirect_OutOfScopeStatus(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-cross-scope.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	cfg := config.DefaultConfig()
	cfg.GlobalConcurrency = 1
	cfg.MaxPages = 100
	cfg.ThinContentThreshold = 10

	f := fetcher.New(fetcher.Options{
		UserAgent:       "test-crawler/1.0",
		Timeout:         5 * time.Second,
		MaxResponseBody: 5 * 1024 * 1024,
	})

	// Scope checker: only example.com is in scope
	sc, _ := urlutil.NewScopeChecker("exact_host", "example.com", nil)

	seedURLs, _ := json.Marshal([]string{"https://example.com/"})
	job, err := db.CreateJob("crawl", "{}", string(seedURLs))
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	eng := New(EngineConfig{
		DB:           db,
		Fetcher:      f,
		ScopeChecker: sc,
		Config:       &cfg,
	})

	// Manually call persistItem with a fetch result simulating a cross-scope redirect
	urlID, err := db.UpsertURL(job.ID, "https://example.com/old-page", "example.com", "fetched", true, "link")
	if err != nil {
		t.Fatalf("upserting URL: %v", err)
	}

	item := persistItem{
		parseResult: parseResult{
			fetchResult: fetchResult{
				urlID: urlID,
				url:   "https://example.com/old-page",
				host:  "example.com",
				depth: 0,
				result: &fetcher.FetchResult{
					RequestedURL: "https://example.com/old-page",
					FinalURL:     "https://other-domain.com/landing", // out of scope
					StatusCode:   301,
					ContentType:  "text/html",
					Body:         []byte("<html><head><title>Redirected</title></head><body>redirected</body></html>"),
					BodySize:     72,
					RedirectHops: []fetcher.RedirectHop{
						{HopIndex: 0, StatusCode: 301, FromURL: "https://example.com/old-page", ToURL: "https://other-domain.com/landing"},
					},
				},
			},
		},
		fetchSeq: 1,
	}

	// Update job to running status
	db.Exec("UPDATE crawl_jobs SET status = 'running' WHERE id = ?", job.ID)

	ctx := context.Background()
	if err := eng.persistItem(ctx, job.ID, item); err != nil {
		t.Fatalf("persistItem: %v", err)
	}

	// Verify the out-of-scope final URL got status "out_of_scope"
	extURL, err := db.GetURLByNormalized(job.ID, "https://other-domain.com/landing")
	if err != nil {
		t.Fatalf("external URL not found in DB: %v", err)
	}
	if extURL.Status != "out_of_scope" {
		t.Errorf("external URL status = %q, want out_of_scope", extURL.Status)
	}
	if extURL.IsInternal {
		t.Error("external URL should not be marked as internal")
	}
}

func TestCrossScopeRedirectDoesNotCreateExternalPage(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-cross-scope-page.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	cfg := config.DefaultConfig()
	cfg.ThinContentThreshold = 10
	sc, err := urlutil.NewScopeChecker("exact_host", "example.com", nil)
	if err != nil {
		t.Fatalf("creating scope checker: %v", err)
	}
	job, err := db.CreateJob("crawl", "{}", "[\"https://example.com/\"]")
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}
	eng := New(EngineConfig{
		DB:           db,
		Fetcher:      fetcher.New(fetcher.Options{Timeout: time.Second}),
		ScopeChecker: sc,
		Config:       &cfg,
	})
	requestedID, err := db.UpsertURL(job.ID, "https://example.com/go", "example.com", "fetched", true, "seed")
	if err != nil {
		t.Fatalf("upserting requested URL: %v", err)
	}

	fr := fetchResult{
		urlID: requestedID,
		url:   "https://example.com/go",
		host:  "example.com",
		depth: 1,
		result: &fetcher.FetchResult{
			RequestedURL: "https://example.com/go",
			FinalURL:     "https://github.com/example/project",
			StatusCode:   http.StatusOK,
			ContentType:  "text/html; charset=utf-8",
			Body:         []byte("<!doctype html><html><head><title>External</title></head><body><h1>External</h1></body></html>"),
			BodySize:     94,
		},
	}
	pr := eng.processParseResult(context.Background(), job.ID, fr, nil, &atomic.Int64{}, &atomic.Int64{}, newQueryVariantsTracker(), 100, 10, 1000)
	if pr.page != nil {
		t.Fatal("out-of-scope final URL should not be parsed as a page")
	}
	if err := eng.persistItem(context.Background(), job.ID, persistItem{parseResult: pr, fetchSeq: 1}); err != nil {
		t.Fatalf("persistItem: %v", err)
	}

	var pageCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM pages WHERE job_id = ?", job.ID).Scan(&pageCount); err != nil {
		t.Fatalf("counting pages: %v", err)
	}
	if pageCount != 0 {
		t.Fatalf("page count = %d, want 0", pageCount)
	}
	extURL, err := db.GetURLByNormalized(job.ID, "https://github.com/example/project")
	if err != nil {
		t.Fatalf("external final URL not recorded: %v", err)
	}
	if extURL.IsInternal || extURL.Status != "out_of_scope" {
		t.Fatalf("external final URL internal=%v status=%q, want false/out_of_scope", extURL.IsInternal, extURL.Status)
	}
}

// TestOutOfScopeCanonicalDoesNotHeadDuringPersist verifies that external
// canonical URLs are persisted without doing network I/O inside the SQLite
// transaction. Target status checks belong in a separate post-crawl phase.
func TestOutOfScopeCanonicalDoesNotHeadDuringPersist(t *testing.T) {
	headReceived := false

	// External server that would track HEAD requests if persistItem issued them.
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead && r.URL.Path == "/canonical-target" {
			headReceived = true
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(200)
	}))
	defer external.Close()

	dbPath := filepath.Join(t.TempDir(), "test-head-canonical.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	cfg := config.DefaultConfig()
	cfg.GlobalConcurrency = 1
	cfg.MaxPages = 100
	cfg.ThinContentThreshold = 10

	f := fetcher.New(fetcher.Options{
		UserAgent:       "test-crawler/1.0",
		Timeout:         5 * time.Second,
		MaxResponseBody: 5 * 1024 * 1024,
	})

	// Scope checker: only example.com is in scope; external server is out of scope
	sc, _ := urlutil.NewScopeChecker("exact_host", "example.com", nil)

	seedURLs, _ := json.Marshal([]string{"https://example.com/"})
	job, err := db.CreateJob("crawl", "{}", string(seedURLs))
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	eng := New(EngineConfig{
		DB:           db,
		Fetcher:      f,
		ScopeChecker: sc,
		Config:       &cfg,
	})

	// Manually call persistItem with edges including an out-of-scope canonical
	urlID, err := db.UpsertURL(job.ID, "https://example.com/page", "example.com", "fetched", true, "seed")
	if err != nil {
		t.Fatalf("upserting URL: %v", err)
	}

	canonicalTarget := external.URL + "/canonical-target"

	item := persistItem{
		parseResult: parseResult{
			fetchResult: fetchResult{
				urlID: urlID,
				url:   "https://example.com/page",
				host:  "example.com",
				depth: 0,
				result: &fetcher.FetchResult{
					RequestedURL: "https://example.com/page",
					FinalURL:     "https://example.com/page",
					StatusCode:   200,
					ContentType:  "text/html",
					Body:         []byte("<html><body>test</body></html>"),
					BodySize:     29,
					RedirectHops: []fetcher.RedirectHop{},
				},
			},
			edges: []crawl.DiscoveredEdge{
				{
					SourceURLID:         urlID,
					DeclaredTargetURL:   canonicalTarget,
					NormalizedTargetURL: canonicalTarget,
					SourceKind:          "html",
					RelationType:        "canonical",
					RelFlagsJSON:        "[]",
					DiscoveryMode:       "static",
					IsInternal:          false, // out of scope
				},
			},
		},
		fetchSeq: 1,
	}

	db.Exec("UPDATE crawl_jobs SET status = 'running' WHERE id = ?", job.ID)

	ctx := context.Background()
	if err := eng.persistItem(ctx, job.ID, item); err != nil {
		t.Fatalf("persistItem: %v", err)
	}

	if headReceived {
		t.Error("persistItem should not send HEAD requests while holding the DB transaction")
	}

	// Verify the canonical edge is preserved, but target_status_code is left unset.
	edges, err := db.GetEdgesBySource(job.ID, urlID, 100, "")
	if err != nil {
		t.Fatalf("getting edges: %v", err)
	}

	foundCanonical := false
	for _, edge := range edges {
		if edge.RelationType == "canonical" {
			foundCanonical = true
			if edge.TargetStatusCode.Valid {
				t.Errorf("canonical edge target_status_code = %d, want unset", edge.TargetStatusCode.Int64)
			}
		}
	}
	if !foundCanonical {
		t.Error("expected to find a canonical edge")
	}
}

func TestForceRenderPatterns_EngineMarksJSSuspect(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!DOCTYPE html><html><head><title>Home</title>
				<meta name="description" content="This home page has enough content to avoid thin content detection during testing."></head>
				<body><h1>Home</h1>
				<p>Enough content to be fine with our low threshold for testing purposes in this integration test.</p>
				<a href="/app/dashboard">Dashboard</a></body></html>`)
		case "/app/dashboard":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			// This page has NO JS frameworks, but forceRenderPatterns should mark it
			fmt.Fprint(w, `<!DOCTYPE html><html><head><title>Dashboard</title>
				<meta name="description" content="This dashboard page should be marked for browser rendering via forceRenderPatterns."></head>
				<body><h1>Dashboard</h1>
				<p>This is a dashboard page. It should be marked as JS suspect due to forceRenderPatterns configuration.</p></body></html>`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()

	dbPath := filepath.Join(t.TempDir(), "test-force-render.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	cfg := config.DefaultConfig()
	cfg.GlobalConcurrency = 2
	cfg.MaxPages = 100
	cfg.MaxDepth = 10
	cfg.RequestTimeout = 5 * time.Second
	cfg.AllowPrivateNetworks = true
	cfg.SSRFProtection = false
	cfg.ThinContentThreshold = 10
	cfg.ForceRenderPatterns = []string{"/app/*"}

	f := fetcher.New(fetcher.Options{
		UserAgent:           "test-crawler/1.0",
		Timeout:             5 * time.Second,
		MaxResponseBody:     5 * 1024 * 1024,
		MaxDecompressedBody: 20 * 1024 * 1024,
		MaxRedirectHops:     10,
	})
	rl := fetcher.NewRateLimiter(cfg.PerHostConcurrency)

	tsURL, _ := url.Parse(ts.URL)
	sc, _ := urlutil.NewScopeChecker("exact_host", tsURL.Hostname(), nil)

	seedURLs, _ := json.Marshal([]string{ts.URL + "/"})
	job, err := db.CreateJob("crawl", "{}", string(seedURLs))
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	eng := New(EngineConfig{
		DB:           db,
		Fetcher:      f,
		RateLimiter:  rl,
		ScopeChecker: sc,
		Config:       &cfg,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := eng.RunCrawl(ctx, job.ID); err != nil {
		t.Fatalf("RunCrawl: %v", err)
	}

	// Verify /app/dashboard page has js_suspect=1
	dashNorm, _ := urlutil.Normalize(ts.URL + "/app/dashboard")
	dashURL, err := db.GetURLByNormalized(job.ID, dashNorm)
	if err != nil {
		t.Fatalf("dashboard URL not found: %v", err)
	}

	dashPage, err := db.GetPageByURL(job.ID, dashURL.ID)
	if err != nil {
		t.Fatalf("dashboard page not found: %v", err)
	}

	if !dashPage.JSSuspect {
		t.Error("dashboard page js_suspect should be true (forced by forceRenderPatterns)")
	}

	// Verify / page does NOT have js_suspect forced
	rootNorm, _ := urlutil.Normalize(ts.URL + "/")
	rootURL, err := db.GetURLByNormalized(job.ID, rootNorm)
	if err != nil {
		t.Fatalf("root URL not found: %v", err)
	}

	rootPage, err := db.GetPageByURL(job.ID, rootURL.ID)
	if err != nil {
		t.Fatalf("root page not found: %v", err)
	}

	if rootPage.JSSuspect {
		t.Error("root page js_suspect should be false (not in forceRenderPatterns)")
	}
}
