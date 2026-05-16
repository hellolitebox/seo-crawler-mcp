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
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/fetcher"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/urlutil"
)

func TestRunCrawlHonorsMaxDiscoveredURLs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/" {
			fmt.Fprint(w, "<!doctype html><html><head><title>Home</title><meta name=\"description\" content=\"Home\"></head><body><h1>Home</h1>")
			for i := 0; i < 10; i++ {
				fmt.Fprintf(w, "<a href=\"/p%d\">Page %d</a>", i, i)
			}
			fmt.Fprint(w, "</body></html>")
			return
		}
		fmt.Fprintf(w, "<!doctype html><html><head><title>%s</title><meta name=\"description\" content=\"Page\"></head><body><h1>%s</h1><p>Enough content for this page.</p></body></html>", r.URL.Path, r.URL.Path)
	}))
	defer ts.Close()

	db, err := storage.Open(filepath.Join(t.TempDir(), "budget-urls.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	cfg := testCrawlConfig()
	cfg.MaxDiscoveredURLs = 2
	eng := newBudgetTestEngine(t, db, ts.URL, &cfg)

	seedURLs, _ := json.Marshal([]string{ts.URL + "/"})
	job, err := db.CreateJob("crawl", "{\"maxDiscoveredUrls\":2}", string(seedURLs))
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := eng.RunCrawl(ctx, job.ID); err != nil {
		t.Fatalf("RunCrawl: %v", err)
	}

	var urlCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM urls WHERE job_id = ?", job.ID).Scan(&urlCount); err != nil {
		t.Fatalf("counting urls: %v", err)
	}
	if urlCount > 2 {
		t.Fatalf("url rows = %d, want <= maxDiscoveredUrls 2", urlCount)
	}
	assertMetric(t, db, job.ID, "crawl_budget_hit", "max_discovered_urls")
}

func TestRunCrawlHonorsMaxOnboardedHosts(t *testing.T) {
	var docsURL string
	docs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nAllow: /\n")
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, "<?xml version=\"1.0\"?><urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\"><url><loc>%s/docs-only</loc></url></urlset>", docsURL)
		case "/landing", "/docs-only":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<!doctype html><html><head><title>Docs</title><meta name=\"description\" content=\"Docs\"></head><body><h1>Docs</h1><p>Enough content.</p></body></html>")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer docs.Close()
	docsURL = docs.URL

	var rootURL string
	root := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nAllow: /\n")
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, "<?xml version=\"1.0\"?><urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\"><url><loc>%s/</loc></url><url><loc>%s/landing</loc></url></urlset>", rootURL, docsURL)
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<!doctype html><html><head><title>Home</title><meta name=\"description\" content=\"Home\"></head><body><h1>Home</h1><p>Enough content.</p></body></html>")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer root.Close()
	rootURL = root.URL

	db, err := storage.Open(filepath.Join(t.TempDir(), "budget-hosts.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	cfg := testCrawlConfig()
	cfg.MaxOnboardedHosts = 1
	eng := newBudgetTestEngine(t, db, root.URL, &cfg)

	seedURLs, _ := json.Marshal([]string{root.URL + "/"})
	job, err := db.CreateJob("crawl", "{\"maxOnboardedHosts\":1}", string(seedURLs))
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := eng.RunCrawl(ctx, job.ID); err != nil {
		t.Fatalf("RunCrawl: %v", err)
	}

	var docsSitemapEntries int
	if err := db.QueryRow("SELECT COUNT(*) FROM sitemap_entries WHERE job_id = ? AND source_sitemap_url = ?", job.ID, docs.URL+"/sitemap.xml").Scan(&docsSitemapEntries); err != nil {
		t.Fatalf("counting docs sitemap entries: %v", err)
	}
	if docsSitemapEntries != 0 {
		t.Fatalf("docs sitemap entries = %d, want host budget to skip docs host", docsSitemapEntries)
	}
	assertMetric(t, db, job.ID, "crawl_budget_hit", "max_onboarded_hosts")
}

func TestCanonicalRegressionCrawlCoversSupplementalSitemapAssetsAnd404s(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nAllow: /\n")
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, "<?xml version=\"1.0\"?><urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\"><url><loc>http://%s/</loc></url></urlset>", r.Host)
		case "/docs/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, "<?xml version=\"1.0\"?><urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\"><url><loc>http://%s/return-policy</loc></url></urlset>", r.Host)
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<!doctype html><html><head><title>Home</title><meta name=\"description\" content=\"Home\"></head><body><h1>Home</h1><img src=\"/_next/image?url=%2Fhero.webp&w=3840&q=75\" width=\"1600\" height=\"900\"><a href=\"/missing\">Missing</a></body></html>")
		case "/return-policy":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<!doctype html><html><head><title>Return Policy</title><meta name=\"description\" content=\"Returns\"></head><body><h1>Return Policy</h1><p>Enough content for return policy.</p></body></html>")
		case "/missing":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, "<!doctype html><html><head><title>Not found</title></head><body><h1>404</h1></body></html>")
		case "/_next/image":
			w.Header().Set("Content-Type", "image/webp")
			fmt.Fprint(w, "RIFFxxxxWEBP")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	db, err := storage.Open(filepath.Join(t.TempDir(), "canonical-regression.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	cfg := testCrawlConfig()
	eng := newBudgetTestEngine(t, db, ts.URL, &cfg)
	seedURLs, _ := json.Marshal([]string{ts.URL + "/"})
	job, err := db.CreateJob("crawl", "{}", string(seedURLs))
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := eng.RunCrawl(ctx, job.ID); err != nil {
		t.Fatalf("RunCrawl: %v", err)
	}

	returnPolicy, _ := urlutil.Normalize(ts.URL + "/return-policy")
	var returnPolicyStatus string
	if err := db.QueryRow("SELECT status FROM urls WHERE job_id = ? AND normalized_url = ?", job.ID, returnPolicy).Scan(&returnPolicyStatus); err != nil {
		t.Fatalf("return-policy missing from crawl: %v", err)
	}
	if returnPolicyStatus != "fetched" {
		t.Fatalf("return-policy status = %q, want fetched", returnPolicyStatus)
	}

	missing, _ := urlutil.Normalize(ts.URL + "/missing")
	var missingPageRows int
	if err := db.QueryRow("SELECT COUNT(*) FROM pages p JOIN urls u ON u.id = p.url_id WHERE p.job_id = ? AND u.normalized_url = ?", job.ID, missing).Scan(&missingPageRows); err != nil {
		t.Fatalf("counting missing page rows: %v", err)
	}
	if missingPageRows != 0 {
		t.Fatalf("404 page rows = %d, want 0", missingPageRows)
	}

	var imageRefs int
	if err := db.QueryRow("SELECT COUNT(*) FROM asset_references WHERE job_id = ? AND reference_type = 'img_src'", job.ID).Scan(&imageRefs); err != nil {
		t.Fatalf("counting image references: %v", err)
	}
	if imageRefs != 1 {
		t.Fatalf("image refs = %d, want 1", imageRefs)
	}
}

func testCrawlConfig() config.Config {
	cfg := config.DefaultConfig()
	cfg.GlobalConcurrency = 2
	cfg.PerHostConcurrency = 2
	cfg.MaxPages = 50
	cfg.MaxDepth = 10
	cfg.MaxDiscoveredURLs = 100
	cfg.MaxOnboardedHosts = 10
	cfg.MaxCrawlDuration = 15 * time.Second
	cfg.RequestTimeout = 2 * time.Second
	cfg.RenderMode = config.RenderModeStatic
	cfg.AllowPrivateNetworks = true
	cfg.SSRFProtection = false
	cfg.ThinContentThreshold = 1
	cfg.MaxQueryVariantsPerPath = 50
	cfg.LanguageToolURL = ""
	cfg.PSIAPIKey = ""
	return cfg
}

func newBudgetTestEngine(t *testing.T, db *storage.DB, seedURL string, cfg *config.Config) *Engine {
	t.Helper()
	parsed, err := url.Parse(seedURL)
	if err != nil {
		t.Fatalf("parsing seed URL: %v", err)
	}
	sc, err := urlutil.NewScopeChecker("exact_host", parsed.Hostname(), nil)
	if err != nil {
		t.Fatalf("creating scope checker: %v", err)
	}
	return New(EngineConfig{
		DB: db,
		Fetcher: fetcher.New(fetcher.Options{
			UserAgent:           "test-crawler/1.0",
			Timeout:             cfg.RequestTimeout,
			MaxResponseBody:     5 * 1024 * 1024,
			MaxDecompressedBody: 20 * 1024 * 1024,
			MaxRedirectHops:     10,
			Retries:             0,
			SSRFGuard:           nil,
		}),
		RateLimiter:  fetcher.NewRateLimiter(cfg.PerHostConcurrency),
		ScopeChecker: sc,
		Config:       cfg,
	})
}

func assertMetric(t *testing.T, db *storage.DB, jobID, metricName, reason string) {
	t.Helper()
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM crawl_events WHERE job_id = ? AND event_type = 'metric' AND json_extract(details_json, '$.name') = ? AND json_extract(details_json, '$.reason') = ?", jobID, metricName, reason).Scan(&count)
	if err != nil {
		t.Fatalf("querying metric %s/%s: %v", metricName, reason, err)
	}
	if count == 0 {
		t.Fatalf("missing metric %s with reason %s", metricName, reason)
	}
}
