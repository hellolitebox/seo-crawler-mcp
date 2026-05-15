package engine

import (
	"testing"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/ssrf"
)

func TestFilterPostCrawlURLsRejectsBlockedTargets(t *testing.T) {
	eng := &Engine{ssrfGuard: ssrf.NewGuard(false)}

	got := eng.filterPostCrawlURLs([]string{
		"https://127.0.0.1/admin",
		"http://169.254.169.254/latest/meta-data",
		"https://93.184.216.34/page",
	}, "test")

	if len(got) != 1 || got[0] != "https://93.184.216.34/page" {
		t.Fatalf("filtered URLs = %v, want only public URL", got)
	}
}

func TestFilterPostCrawlURLsAllowsAllWithoutGuard(t *testing.T) {
	eng := &Engine{}
	urls := []string{"https://127.0.0.1/admin", "https://example.com/"}

	got := eng.filterPostCrawlURLs(urls, "test")

	if len(got) != len(urls) {
		t.Fatalf("filtered URLs = %v, want %v", got, urls)
	}
}

func TestBrowserPostCrawlAuditsDisabledWithSSRFGuard(t *testing.T) {
	if !(&Engine{ssrfGuard: ssrf.NewGuard(false)}).browserPostCrawlAuditsDisabled() {
		t.Fatal("expected browser post-crawl audits disabled with SSRF guard")
	}
	if (&Engine{}).browserPostCrawlAuditsDisabled() {
		t.Fatal("expected browser post-crawl audits allowed without SSRF guard")
	}
}
