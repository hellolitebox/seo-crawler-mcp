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

func TestRunCrawlDiscoversSitemapsForHostsFoundInSitemaps(t *testing.T) {
	var docsURL string
	docs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprintf(w, "User-agent: *\nAllow: /\n")
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\">\n  <url><loc>%s/docs-only</loc></url>\n</urlset>", docsURL)
		case "/landing", "/docs-only":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, "<!doctype html><html><head><title>%s</title><meta name=\"description\" content=\"Docs page\"></head><body><h1>%s</h1><p>Enough content for this docs page to be crawled during testing.</p></body></html>", r.URL.Path, r.URL.Path)
		default:
			w.WriteHeader(404)
		}
	}))
	defer docs.Close()
	docsURL = docs.URL

	var rootURL string
	root := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprintf(w, "User-agent: *\nAllow: /\nSitemap: %s/sitemap.xml\n", rootURL)
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\">\n  <url><loc>%s/</loc></url>\n  <url><loc>%s/landing</loc></url>\n</urlset>", rootURL, docsURL)
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, "<!doctype html><html><head><title>Home</title><meta name=\"description\" content=\"Home\"></head><body><h1>Home</h1><p>Enough content for the root page.</p></body></html>")
		default:
			w.WriteHeader(404)
		}
	}))
	defer root.Close()
	rootURL = root.URL

	db, err := storage.Open(filepath.Join(t.TempDir(), "subdomain-sitemaps.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	cfg := config.DefaultConfig()
	cfg.GlobalConcurrency = 2
	cfg.PerHostConcurrency = 2
	cfg.MaxPages = 20
	cfg.MaxDepth = 10
	cfg.RequestTimeout = 5 * time.Second
	cfg.RenderMode = config.RenderModeStatic
	cfg.AllowPrivateNetworks = true
	cfg.SSRFProtection = false
	cfg.ThinContentThreshold = 5
	cfg.MaxQueryVariantsPerPath = 50

	f := fetcher.New(fetcher.Options{
		UserAgent:           "test-crawler/1.0",
		Timeout:             5 * time.Second,
		MaxResponseBody:     5 * 1024 * 1024,
		MaxDecompressedBody: 20 * 1024 * 1024,
		MaxRedirectHops:     10,
		SSRFGuard:           nil,
	})
	rl := fetcher.NewRateLimiter(cfg.PerHostConcurrency)

	rootParsed, err := url.Parse(root.URL)
	if err != nil {
		t.Fatalf("parsing root URL: %v", err)
	}
	sc, err := urlutil.NewScopeChecker("exact_host", rootParsed.Hostname(), nil)
	if err != nil {
		t.Fatalf("creating scope checker: %v", err)
	}

	seedURLs, _ := json.Marshal([]string{root.URL + "/"})
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

	docsOnly, err := urlutil.Normalize(docs.URL + "/docs-only")
	if err != nil {
		t.Fatalf("normalizing docs-only URL: %v", err)
	}
	var docsOnlyStatus string
	if err := db.QueryRow("SELECT status FROM urls WHERE job_id = ? AND normalized_url = ?", job.ID, docsOnly).Scan(&docsOnlyStatus); err != nil {
		t.Fatalf("querying docs-only URL: %v", err)
	}
	if docsOnlyStatus != "fetched" {
		t.Fatalf("docs-only status = %q, want fetched", docsOnlyStatus)
	}

	var docsSitemapEntries int
	if err := db.QueryRow("SELECT COUNT(*) FROM sitemap_entries WHERE job_id = ? AND source_sitemap_url = ?", job.ID, docs.URL+"/sitemap.xml").Scan(&docsSitemapEntries); err != nil {
		t.Fatalf("querying docs sitemap entries: %v", err)
	}
	if docsSitemapEntries != 1 {
		t.Fatalf("docs sitemap entries = %d, want 1", docsSitemapEntries)
	}
}
