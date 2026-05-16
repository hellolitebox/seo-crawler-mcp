package sitemap_test

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/sitemap"
)

func TestParseXML_TwoEntries(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url>
    <loc>https://example.com/page1</loc>
    <lastmod>2026-01-01</lastmod>
    <changefreq>daily</changefreq>
    <priority>0.8</priority>
  </url>
  <url>
    <loc>https://example.com/page2</loc>
    <lastmod>2026-02-01</lastmod>
    <changefreq>weekly</changefreq>
    <priority>0.5</priority>
  </url>
</urlset>`)

	entries, err := sitemap.ParseXML(data)
	if err != nil {
		t.Fatalf("ParseXML() error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].Loc != "https://example.com/page1" {
		t.Errorf("entry[0].Loc = %q, want %q", entries[0].Loc, "https://example.com/page1")
	}
	if entries[0].Lastmod != "2026-01-01" {
		t.Errorf("entry[0].Lastmod = %q, want %q", entries[0].Lastmod, "2026-01-01")
	}
	if entries[0].Changefreq != "daily" {
		t.Errorf("entry[0].Changefreq = %q, want %q", entries[0].Changefreq, "daily")
	}
	if entries[0].Priority != 0.8 {
		t.Errorf("entry[0].Priority = %f, want 0.8", entries[0].Priority)
	}

	if entries[1].Loc != "https://example.com/page2" {
		t.Errorf("entry[1].Loc = %q, want %q", entries[1].Loc, "https://example.com/page2")
	}
	if entries[1].Priority != 0.5 {
		t.Errorf("entry[1].Priority = %f, want 0.5", entries[1].Priority)
	}
}

func TestParseXML_Empty(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
</urlset>`)

	entries, err := sitemap.ParseXML(data)
	if err != nil {
		t.Fatalf("ParseXML() error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestParseIndex_TwoSitemaps(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap>
    <loc>https://example.com/sitemap1.xml</loc>
  </sitemap>
  <sitemap>
    <loc>https://example.com/sitemap2.xml</loc>
  </sitemap>
</sitemapindex>`)

	urls, err := sitemap.ParseIndex(data)
	if err != nil {
		t.Fatalf("ParseIndex() error: %v", err)
	}
	if len(urls) != 2 {
		t.Fatalf("expected 2 URLs, got %d", len(urls))
	}
	if urls[0] != "https://example.com/sitemap1.xml" {
		t.Errorf("urls[0] = %q, want %q", urls[0], "https://example.com/sitemap1.xml")
	}
	if urls[1] != "https://example.com/sitemap2.xml" {
		t.Errorf("urls[1] = %q, want %q", urls[1], "https://example.com/sitemap2.xml")
	}
}

func TestFetchAndParse_IndexWithChild(t *testing.T) {
	childXML := `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/a</loc></url>
  <url><loc>https://example.com/b</loc></url>
</urlset>`

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	indexXML := `<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap><loc>` + srv.URL + `/child.xml</loc></sitemap>
</sitemapindex>`

	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(indexXML))
	})
	mux.HandleFunc("/child.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(childXML))
	})

	entries, count, err := sitemap.FetchAndParse(srv.URL+"/sitemap.xml", 1000, srv.Client())
	if err != nil {
		t.Fatalf("FetchAndParse() error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if count != 2 {
		t.Errorf("expected sitemapCount=2, got %d", count)
	}
	if entries[0].SourceURL != srv.URL+"/child.xml" {
		t.Errorf("entry[0].SourceURL = %q, want %q", entries[0].SourceURL, srv.URL+"/child.xml")
	}
}

func TestFetchAndParse_StopsUnboundedIndexTraversal(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for i := 0; i < 1001; i++ {
		b.WriteString(`<sitemap><loc>`)
		b.WriteString(srv.URL)
		b.WriteString(`/child-`)
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(`.xml</loc></sitemap>`)
	}
	b.WriteString(`</sitemapindex>`)

	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(b.String()))
	})

	_, _, err := sitemap.FetchAndParse(srv.URL+"/sitemap.xml", 1, srv.Client())
	if err == nil {
		t.Fatalf("FetchAndParse() error = nil, want traversal cap error")
	}
	if !strings.Contains(err.Error(), "sitemap traversal exceeded") {
		t.Fatalf("FetchAndParse() error = %v", err)
	}
}

func TestFetchAndParse_DirectUrlset(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/page</loc></url>
</urlset>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(xml))
	}))
	defer srv.Close()

	entries, count, err := sitemap.FetchAndParse(srv.URL+"/sitemap.xml", 1000, srv.Client())
	if err != nil {
		t.Fatalf("FetchAndParse() error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if count != 1 {
		t.Errorf("expected sitemapCount=1, got %d", count)
	}
	if entries[0].SourceURL != srv.URL+"/sitemap.xml" {
		t.Errorf("entry[0].SourceURL = %q, want %q", entries[0].SourceURL, srv.URL+"/sitemap.xml")
	}
}

func TestFetchAndParse_MaxEntriesCap(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/1</loc></url>
  <url><loc>https://example.com/2</loc></url>
  <url><loc>https://example.com/3</loc></url>
  <url><loc>https://example.com/4</loc></url>
  <url><loc>https://example.com/5</loc></url>
</urlset>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(xml))
	}))
	defer srv.Close()

	entries, _, err := sitemap.FetchAndParse(srv.URL+"/sitemap.xml", 3, srv.Client())
	if err != nil {
		t.Fatalf("FetchAndParse() error: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries (capped), got %d", len(entries))
	}
}

func TestFetchAndParse_GzipSitemap(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/gz-page</loc></url>
</urlset>`

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Write([]byte(xml))
	gw.Close()
	compressed := buf.Bytes()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Write(compressed)
	}))
	defer srv.Close()

	entries, count, err := sitemap.FetchAndParse(srv.URL+"/sitemap.xml.gz", 1000, srv.Client())
	if err != nil {
		t.Fatalf("FetchAndParse() error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if count != 1 {
		t.Errorf("expected sitemapCount=1, got %d", count)
	}
	if entries[0].Loc != "https://example.com/gz-page" {
		t.Errorf("entry.Loc = %q, want %q", entries[0].Loc, "https://example.com/gz-page")
	}
}
