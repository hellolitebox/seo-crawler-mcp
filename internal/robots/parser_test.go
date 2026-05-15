package robots_test

import (
	"testing"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/robots"
)

const testRobotsTxt = `# robots.txt for test
User-agent: *
Disallow: /private/
Allow: /private/public
Crawl-delay: 2

User-agent: Googlebot
Disallow: /nogoogle/
Allow: /nogoogle/exception

Sitemap: https://example.com/sitemap1.xml
Sitemap: https://example.com/sitemap2.xml
`

func TestParse(t *testing.T) {
	rf, err := robots.Parse(testRobotsTxt)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if len(rf.Sitemaps) != 2 {
		t.Fatalf("expected 2 sitemaps, got %d", len(rf.Sitemaps))
	}
	if rf.Sitemaps[0] != "https://example.com/sitemap1.xml" {
		t.Errorf("sitemap[0] = %q, want %q", rf.Sitemaps[0], "https://example.com/sitemap1.xml")
	}
	if rf.Sitemaps[1] != "https://example.com/sitemap2.xml" {
		t.Errorf("sitemap[1] = %q, want %q", rf.Sitemaps[1], "https://example.com/sitemap2.xml")
	}

	if len(rf.Rules) == 0 {
		t.Fatal("expected rules, got none")
	}
}

func TestCrawlDelay(t *testing.T) {
	rf, err := robots.Parse(testRobotsTxt)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	// Googlebot has its own group with no crawl-delay → 0
	if d := rf.CrawlDelay("Googlebot"); d != 0 {
		t.Errorf("CrawlDelay(Googlebot) = %d, want 0", d)
	}

	// Random bot falls back to * group → 2
	if d := rf.CrawlDelay("RandomBot"); d != 2 {
		t.Errorf("CrawlDelay(RandomBot) = %d, want 2", d)
	}
}

func TestIsAllowed(t *testing.T) {
	rf, err := robots.Parse(testRobotsTxt)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	tests := []struct {
		ua   string
		path string
		want bool
	}{
		{"RandomBot", "/page", true},
		{"RandomBot", "/private/secret", false},
		{"RandomBot", "/private/public", true},
		{"RandomBot", "/", true},
		{"Googlebot", "/nogoogle/test", false},
		{"Googlebot", "/nogoogle/exception", true},
		{"Googlebot", "/page", true},
	}

	for _, tt := range tests {
		got := rf.IsAllowed(tt.ua, tt.path)
		if got != tt.want {
			t.Errorf("IsAllowed(%q, %q) = %v, want %v", tt.ua, tt.path, got, tt.want)
		}
	}
}

func TestEmptyRobotsTxt(t *testing.T) {
	rf, err := robots.Parse("")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if !rf.IsAllowed("AnyBot", "/anything") {
		t.Error("empty robots.txt should allow everything")
	}

	if len(rf.Sitemaps) != 0 {
		t.Errorf("expected 0 sitemaps, got %d", len(rf.Sitemaps))
	}
}

func TestEmptyDisallow(t *testing.T) {
	content := "User-agent: *\nDisallow:\n"
	rf, err := robots.Parse(content)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if !rf.IsAllowed("AnyBot", "/anything") {
		t.Error("empty Disallow should allow everything")
	}
}

func TestWildcardPattern(t *testing.T) {
	content := "User-agent: *\nDisallow: /foo*bar\n"
	rf, err := robots.Parse(content)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if rf.IsAllowed("Bot", "/foo123bar") {
		t.Error("/foo123bar should be disallowed by /foo*bar")
	}

	if !rf.IsAllowed("Bot", "/foo123baz") {
		t.Error("/foo123baz should be allowed (doesn't match /foo*bar)")
	}
}

func TestAnchorPattern(t *testing.T) {
	content := "User-agent: *\nDisallow: /exact$\n"
	rf, err := robots.Parse(content)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if rf.IsAllowed("Bot", "/exact") {
		t.Error("/exact should be disallowed by /exact$")
	}

	if !rf.IsAllowed("Bot", "/exact/more") {
		t.Error("/exact/more should be allowed (anchor prevents match)")
	}
}

func TestCommentOnlyLines(t *testing.T) {
	content := "# just a comment\n# another comment\n"
	rf, err := robots.Parse(content)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if !rf.IsAllowed("Bot", "/anything") {
		t.Error("comment-only robots.txt should allow everything")
	}
}

func TestCaseInsensitiveUserAgent(t *testing.T) {
	content := "User-agent: GoogleBot\nDisallow: /secret/\n"
	rf, err := robots.Parse(content)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if rf.IsAllowed("googlebot", "/secret/page") {
		t.Error("case-insensitive UA matching should work")
	}
}

func TestMultipleUserAgentsShareGroup(t *testing.T) {
	content := "User-agent: BotA\nUser-agent: BotB\nDisallow: /shared/\n"
	rf, err := robots.Parse(content)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if rf.IsAllowed("BotA", "/shared/page") {
		t.Error("BotA should be disallowed from /shared/")
	}
	if rf.IsAllowed("BotB", "/shared/page") {
		t.Error("BotB should be disallowed from /shared/")
	}
}

func TestNoMatchingGroup(t *testing.T) {
	content := "User-agent: SpecificBot\nDisallow: /nope/\n"
	rf, err := robots.Parse(content)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	// No * group and no matching group → allow all
	if !rf.IsAllowed("OtherBot", "/nope/page") {
		t.Error("no matching group should allow everything")
	}
}

func TestLongerPatternWins(t *testing.T) {
	content := `User-agent: *
Disallow: /a/
Allow: /a/b/
Disallow: /a/b/c/
`
	rf, err := robots.Parse(content)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if rf.IsAllowed("Bot", "/a/x") {
		t.Error("/a/x should be disallowed")
	}
	if !rf.IsAllowed("Bot", "/a/b/x") {
		t.Error("/a/b/x should be allowed (longer Allow wins)")
	}
	if rf.IsAllowed("Bot", "/a/b/c/x") {
		t.Error("/a/b/c/x should be disallowed (even longer Disallow)")
	}
}

func TestEqualLengthAllowWins(t *testing.T) {
	content := `User-agent: *
Disallow: /path
Allow: /path
`
	rf, err := robots.Parse(content)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if !rf.IsAllowed("Bot", "/path") {
		t.Error("equal length: Allow should win over Disallow")
	}
}
