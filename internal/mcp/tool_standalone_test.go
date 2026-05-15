package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/fetcher"
	gomcp "github.com/mark3labs/mcp-go/mcp"
)

func newTestFetcher() *fetcher.Fetcher {
	return fetcher.New(fetcher.Options{
		UserAgent:           "test-bot/1.0",
		MaxResponseBody:     10 * 1024 * 1024,
		MaxDecompressedBody: 20 * 1024 * 1024,
		MaxRedirectHops:     10,
	})
}

func TestAnalyzeURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head>
  <title>Test Page</title>
  <meta name="description" content="A test page for SEO analysis.">
  <link rel="canonical" href="`+r.URL.String()+`">
</head>
<body>
  <h1>Welcome</h1>
  <p>This is some content for the page. It should have enough words to pass basic checks.</p>
  <a href="/about">About</a>
  <a href="https://external.com/link">External</a>
  <img src="/img.png" alt="test image">
</body>
</html>`)
	}))
	defer srv.Close()

	f := newTestFetcher()
	s := NewServer(ServerConfig{Fetcher: f})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"url": srv.URL + "/",
	}

	result, err := s.handleAnalyzeURL(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}

	var resp analyzeResult
	for _, content := range result.Content {
		if tc, ok := content.(gomcp.TextContent); ok {
			if err := json.Unmarshal([]byte(tc.Text), &resp); err != nil {
				t.Fatalf("parsing response: %v", err)
			}
		}
	}

	if resp.Page == nil {
		t.Fatal("expected page data")
	}
	if resp.Page.Title != "Test Page" {
		t.Errorf("expected title %q, got %q", "Test Page", resp.Page.Title)
	}
	if resp.Page.H1Count != 1 {
		t.Errorf("expected 1 h1, got %d", resp.Page.H1Count)
	}
	if len(resp.Links) < 2 {
		t.Errorf("expected at least 2 links, got %d", len(resp.Links))
	}
}

func TestAnalyzeURL_MissingURL(t *testing.T) {
	s := NewServer(ServerConfig{})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := s.handleAnalyzeURL(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing URL")
	}
}

func TestCheckRedirects(t *testing.T) {
	finalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>Final</body></html>`)
	}))
	defer finalSrv.Close()

	redirectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, finalSrv.URL+"/final", http.StatusMovedPermanently)
	}))
	defer redirectSrv.Close()

	f := newTestFetcher()
	s := NewServer(ServerConfig{Fetcher: f})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"url": redirectSrv.URL + "/start",
	}

	result, err := s.handleCheckRedirects(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}

	var resp redirectResult
	for _, content := range result.Content {
		if tc, ok := content.(gomcp.TextContent); ok {
			if err := json.Unmarshal([]byte(tc.Text), &resp); err != nil {
				t.Fatalf("parsing response: %v", err)
			}
		}
	}

	if resp.TotalHops != 1 {
		t.Errorf("expected 1 redirect hop, got %d", resp.TotalHops)
	}
	if resp.FinalURL != finalSrv.URL+"/final" {
		t.Errorf("expected final URL %q, got %q", finalSrv.URL+"/final", resp.FinalURL)
	}
	if resp.FinalStatusCode != 200 {
		t.Errorf("expected final status 200, got %d", resp.FinalStatusCode)
	}
}

func TestCheckRedirects_MissingURL(t *testing.T) {
	s := NewServer(ServerConfig{})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := s.handleCheckRedirects(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing URL")
	}
}

func TestCheckRobotsTxt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, `User-agent: *
Disallow: /admin/
Allow: /public/

Sitemap: https://example.com/sitemap.xml
`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	f := newTestFetcher()
	s := NewServer(ServerConfig{Fetcher: f})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"url":       srv.URL,
		"testPaths": []any{"/admin/secret", "/public/page", "/other"},
	}

	result, err := s.handleCheckRobotsTxt(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}

	var resp robotsResult
	for _, content := range result.Content {
		if tc, ok := content.(gomcp.TextContent); ok {
			if err := json.Unmarshal([]byte(tc.Text), &resp); err != nil {
				t.Fatalf("parsing response: %v", err)
			}
		}
	}

	if resp.RuleCount == 0 {
		t.Error("expected some rules")
	}
	if len(resp.TestResults) != 3 {
		t.Fatalf("expected 3 test results, got %d", len(resp.TestResults))
	}

	// /admin/secret should be disallowed
	if resp.TestResults[0].Allowed {
		t.Error("expected /admin/secret to be disallowed")
	}
	// /public/page should be allowed
	if !resp.TestResults[1].Allowed {
		t.Error("expected /public/page to be allowed")
	}
}

func TestCheckRobotsTxt_MissingURL(t *testing.T) {
	s := NewServer(ServerConfig{})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := s.handleCheckRobotsTxt(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing URL")
	}
}

func TestParseSitemap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/</loc></url>
  <url><loc>https://example.com/about</loc></url>
  <url><loc>https://example.com/contact</loc></url>
</urlset>`)
	}))
	defer srv.Close()

	f := newTestFetcher()
	s := NewServer(ServerConfig{Fetcher: f})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"url": srv.URL + "/sitemap.xml",
	}

	result, err := s.handleParseSitemap(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}

	var resp sitemapResult
	for _, content := range result.Content {
		if tc, ok := content.(gomcp.TextContent); ok {
			if err := json.Unmarshal([]byte(tc.Text), &resp); err != nil {
				t.Fatalf("parsing response: %v", err)
			}
		}
	}

	if resp.TotalEntries != 3 {
		t.Errorf("expected 3 entries, got %d", resp.TotalEntries)
	}
	if len(resp.Entries) != 3 {
		t.Errorf("expected 3 entries in array, got %d", len(resp.Entries))
	}
}

func TestParseSitemap_MissingURL(t *testing.T) {
	s := NewServer(ServerConfig{})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := s.handleParseSitemap(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing URL")
	}
}
