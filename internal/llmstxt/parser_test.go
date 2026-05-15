package llmstxt

import (
	"testing"
)

func TestParse_TwoSectionsWithURLs(t *testing.T) {
	content := `# About
This is about us. Visit https://example.com for more.

# API
See https://api.example.com/docs for API docs.
Also check https://example.com/blog
`
	result := Parse(content)
	if len(result.Sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(result.Sections))
	}
	if result.Sections[0].Title != "About" {
		t.Errorf("section 0 title: expected About, got %q", result.Sections[0].Title)
	}
	if result.Sections[1].Title != "API" {
		t.Errorf("section 1 title: expected API, got %q", result.Sections[1].Title)
	}
	if len(result.URLs) != 3 {
		t.Fatalf("expected 3 URLs, got %d: %v", len(result.URLs), result.URLs)
	}
}

func TestParse_Empty(t *testing.T) {
	result := Parse("")
	if len(result.Sections) != 0 {
		t.Errorf("expected 0 sections, got %d", len(result.Sections))
	}
	if len(result.URLs) != 0 {
		t.Errorf("expected 0 URLs, got %d", len(result.URLs))
	}
}

func TestParse_NoSectionsJustText(t *testing.T) {
	content := "Just some plain text with https://example.com"
	result := Parse(content)
	if len(result.Sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(result.Sections))
	}
	if result.Sections[0].Title != "" {
		t.Errorf("expected empty title, got %q", result.Sections[0].Title)
	}
	if len(result.URLs) != 1 {
		t.Fatalf("expected 1 URL, got %d", len(result.URLs))
	}
	if result.URLs[0] != "https://example.com" {
		t.Errorf("expected https://example.com, got %q", result.URLs[0])
	}
}

func TestParse_URLsInVariousFormats(t *testing.T) {
	content := `# Links
- [Example](https://example.com/page)
- Raw: http://foo.bar/baz?q=1&r=2
- Markdown: https://test.io/path#fragment
`
	result := Parse(content)
	if len(result.URLs) != 3 {
		t.Fatalf("expected 3 URLs, got %d: %v", len(result.URLs), result.URLs)
	}
}

func TestParse_HeadingLevels(t *testing.T) {
	content := `# H1
Content 1
## H2
Content 2
### H3
Content 3`
	result := Parse(content)
	if len(result.Sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(result.Sections))
	}
	if result.Sections[0].Title != "H1" {
		t.Errorf("expected H1, got %q", result.Sections[0].Title)
	}
	if result.Sections[1].Title != "H2" {
		t.Errorf("expected H2, got %q", result.Sections[1].Title)
	}
	if result.Sections[2].Title != "H3" {
		t.Errorf("expected H3, got %q", result.Sections[2].Title)
	}
}
