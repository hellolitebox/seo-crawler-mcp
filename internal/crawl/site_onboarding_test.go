package crawl

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/fetcher"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

const testUserAgent = "test-bot"

const testRobotsTxt = `User-agent: *
Disallow: /private/
Allow: /public/
Crawl-delay: 5

User-agent: test-bot
Disallow: /admin/
Crawl-delay: 3

Sitemap: SITEMAPURL
`

const testSitemapXML = `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>BASEURL/page1</loc><lastmod>2026-01-01</lastmod></url>
  <url><loc>BASEURL/page2</loc><changefreq>weekly</changefreq><priority>0.8</priority></url>
</urlset>`

const testLlmsTxt = `# About
This is a test site.

# API
See https://example.com/api for docs.
`

func setupTestServer(serveRobots, serveSitemap, serveLlms bool, robotsStatus, sitemapStatus, llmsStatus int) *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		if !serveRobots {
			w.WriteHeader(robotsStatus)
			return
		}
		w.WriteHeader(robotsStatus)
		if robotsStatus == 200 {
			// Replace SITEMAPURL placeholder after we know the server URL.
			// We'll handle this in the handler via a closure.
			fmt.Fprint(w, r.Header.Get("X-Robots-Content"))
		}
	})

	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		if !serveSitemap {
			w.WriteHeader(sitemapStatus)
			return
		}
		w.WriteHeader(sitemapStatus)
		if sitemapStatus == 200 {
			fmt.Fprint(w, r.Header.Get("X-Sitemap-Content"))
		}
	})

	mux.HandleFunc("/sitemap_index.xml", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})

	mux.HandleFunc("/llms.txt", func(w http.ResponseWriter, _ *http.Request) {
		if !serveLlms {
			w.WriteHeader(llmsStatus)
			return
		}
		w.WriteHeader(llmsStatus)
		if llmsStatus == 200 {
			fmt.Fprint(w, testLlmsTxt)
		}
	})

	return httptest.NewServer(mux)
}

// setupFullServer creates a server that serves all resources with content baked in.
func setupFullServer() *httptest.Server {
	var serverURL string
	mux := http.NewServeMux()

	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		content := strings.ReplaceAll(testRobotsTxt, "SITEMAPURL", serverURL+"/sitemap.xml")
		fmt.Fprint(w, content)
	})

	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		content := strings.ReplaceAll(testSitemapXML, "BASEURL", serverURL)
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, content)
	})

	mux.HandleFunc("/sitemap_index.xml", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})

	mux.HandleFunc("/llms.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, testLlmsTxt)
	})

	ts := httptest.NewServer(mux)
	serverURL = ts.URL
	return ts
}

func setupDB(t *testing.T, jobID string) *storage.DB {
	t.Helper()
	tmpFile := fmt.Sprintf("%s/test-onboard-%d.db", os.TempDir(), time.Now().UnixNano())
	db, err := storage.Open(tmpFile)
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(tmpFile)
	})

	// Insert the crawl job so FK constraints are satisfied.
	_, err = db.Exec(
		`INSERT INTO crawl_jobs (id, type, status, config_json, seed_urls) VALUES (?, 'full', 'running', '{}', '[]')`,
		jobID,
	)
	if err != nil {
		t.Fatalf("inserting test job: %v", err)
	}

	return db
}

func setupFetcher() *fetcher.Fetcher {
	return fetcher.New(fetcher.Options{
		UserAgent:       testUserAgent,
		Timeout:         10 * time.Second,
		MaxResponseBody: 10 * 1024 * 1024,
	})
}

func TestOnboardHost_Full(t *testing.T) {
	ts := setupFullServer()
	defer ts.Close()

	db := setupDB(t, "job-1")
	f := setupFetcher()
	onboarder := NewHostOnboarder(f, db, 1000, testUserAgent)

	host := strings.TrimPrefix(ts.URL, "http://")
	info, err := onboarder.OnboardHost(context.Background(), "job-1", host, "http")
	if err != nil {
		t.Fatalf("OnboardHost failed: %v", err)
	}

	// Verify robots.txt was parsed.
	if info.RobotsFile == nil {
		t.Fatal("expected RobotsFile to be non-nil")
	}
	if info.RobotsRaw == "" {
		t.Fatal("expected RobotsRaw to be non-empty")
	}
	if !info.RobotsFile.IsAllowed(testUserAgent, "/public/page") {
		t.Error("expected /public/page to be allowed")
	}
	if info.RobotsFile.IsAllowed(testUserAgent, "/admin/secret") {
		t.Error("expected /admin/secret to be disallowed")
	}

	// Verify sitemaps were discovered.
	if len(info.SitemapURLs) == 0 {
		t.Fatal("expected sitemap URLs to be discovered")
	}
	if len(info.SitemapEntries) != 2 {
		t.Fatalf("expected 2 sitemap entries, got %d", len(info.SitemapEntries))
	}

	// Verify llms.txt was found.
	if !info.LlmsTxtFound {
		t.Fatal("expected llms.txt to be found")
	}
	if info.LlmsTxt == nil {
		t.Fatal("expected LlmsTxt to be non-nil")
	}
	if len(info.LlmsTxt.Sections) == 0 {
		t.Error("expected sections in llms.txt")
	}

	// Verify crawl-delay.
	if info.CrawlDelay != 3*time.Second {
		t.Errorf("expected crawl-delay 3s, got %v", info.CrawlDelay)
	}

	// Verify DB storage — robots directives.
	directives, err := db.GetRobotsDirectivesByHost("job-1", host, 100)
	if err != nil {
		t.Fatalf("querying robots directives: %v", err)
	}
	if len(directives) == 0 {
		t.Error("expected robots directives in DB")
	}
}

func TestOnboardHost_NoRobots(t *testing.T) {
	mux := http.NewServeMux()
	var serverURL string

	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		content := strings.ReplaceAll(testSitemapXML, "BASEURL", serverURL)
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, content)
	})
	mux.HandleFunc("/sitemap_index.xml", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})
	mux.HandleFunc("/llms.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})

	ts := httptest.NewServer(mux)
	serverURL = ts.URL
	defer ts.Close()

	db := setupDB(t, "job-2")
	f := setupFetcher()
	onboarder := NewHostOnboarder(f, db, 1000, testUserAgent)

	host := strings.TrimPrefix(ts.URL, "http://")
	info, err := onboarder.OnboardHost(context.Background(), "job-2", host, "http")
	if err != nil {
		t.Fatalf("OnboardHost failed: %v", err)
	}

	// No robots = allow all.
	if info.RobotsFile != nil {
		t.Error("expected RobotsFile to be nil for 404 robots.txt")
	}

	// Should have tried fallback sitemaps.
	hasEvent := false
	for _, e := range info.Events {
		if strings.Contains(e, "fallback") {
			hasEvent = true
			break
		}
	}
	if !hasEvent {
		t.Error("expected fallback sitemap event")
	}

	// Fallback sitemap.xml should have been found.
	if len(info.SitemapEntries) != 2 {
		t.Errorf("expected 2 sitemap entries from fallback, got %d", len(info.SitemapEntries))
	}
}

func TestOnboardHost_RobotsServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	})
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})
	mux.HandleFunc("/sitemap_index.xml", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})
	mux.HandleFunc("/llms.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	f := setupFetcher()
	onboarder := NewHostOnboarder(f, nil, 1000, testUserAgent)

	host := strings.TrimPrefix(ts.URL, "http://")
	info, err := onboarder.OnboardHost(context.Background(), "job-3", host, "http")
	if err != nil {
		t.Fatalf("OnboardHost failed: %v", err)
	}

	if info.RobotsFile != nil {
		t.Error("expected RobotsFile to be nil for 500 robots.txt")
	}

	// Should have a server error event.
	hasWarning := false
	for _, e := range info.Events {
		if strings.Contains(e, "server error") {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Error("expected server error warning event")
	}
}

func TestOnboardHost_NoSitemap(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})
	mux.HandleFunc("/sitemap_index.xml", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})
	mux.HandleFunc("/llms.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	f := setupFetcher()
	onboarder := NewHostOnboarder(f, nil, 1000, testUserAgent)

	host := strings.TrimPrefix(ts.URL, "http://")
	info, err := onboarder.OnboardHost(context.Background(), "job-4", host, "http")
	if err != nil {
		t.Fatalf("OnboardHost failed: %v", err)
	}

	if len(info.SitemapEntries) != 0 {
		t.Errorf("expected 0 sitemap entries, got %d", len(info.SitemapEntries))
	}
}

func TestOnboardHost_NoLlmsTxt(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})
	mux.HandleFunc("/sitemap_index.xml", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})
	mux.HandleFunc("/llms.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	db := setupDB(t, "job-5")
	f := setupFetcher()
	onboarder := NewHostOnboarder(f, db, 1000, testUserAgent)

	host := strings.TrimPrefix(ts.URL, "http://")
	info, err := onboarder.OnboardHost(context.Background(), "job-5", host, "http")
	if err != nil {
		t.Fatalf("OnboardHost failed: %v", err)
	}

	if info.LlmsTxtFound {
		t.Error("expected llms.txt to not be found")
	}
	if info.LlmsTxt != nil {
		t.Error("expected LlmsTxt to be nil")
	}

	// Verify DB has present=false.
	finding, err := db.GetLlmsFindingByHost("job-5", host)
	if err != nil {
		t.Fatalf("querying llms finding: %v", err)
	}
	if finding == nil {
		t.Fatal("expected llms finding in DB")
	}
	if finding.Present {
		t.Error("expected present=false in DB")
	}
}

func TestOnboardHost_CrawlDelay(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "User-agent: test-bot\nCrawl-delay: 10\n\nUser-agent: *\nCrawl-delay: 5\n")
	})
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})
	mux.HandleFunc("/sitemap_index.xml", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})
	mux.HandleFunc("/llms.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	f := setupFetcher()
	onboarder := NewHostOnboarder(f, nil, 1000, testUserAgent)

	host := strings.TrimPrefix(ts.URL, "http://")
	info, err := onboarder.OnboardHost(context.Background(), "job-6", host, "http")
	if err != nil {
		t.Fatalf("OnboardHost failed: %v", err)
	}

	if info.CrawlDelay != 10*time.Second {
		t.Errorf("expected crawl-delay 10s for %q, got %v", testUserAgent, info.CrawlDelay)
	}
}
