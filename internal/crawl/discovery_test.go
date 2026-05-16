package crawl

import (
	"encoding/json"
	"testing"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/parser"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/urlutil"
)

func newScope(t *testing.T) *urlutil.ScopeChecker {
	t.Helper()
	sc, err := urlutil.NewScopeChecker("exact_host", "example.com", nil)
	if err != nil {
		t.Fatalf("failed to create scope: %v", err)
	}
	return sc
}

func TestBuildEdges_Links(t *testing.T) {
	scope := newScope(t)
	result := &parser.ParseResult{
		Links: []parser.DiscoveredLink{
			{URL: "https://example.com/page1", AnchorText: "Page 1", Rel: ""},
			{URL: "https://other.com/page2", AnchorText: "External", Rel: "nofollow ugc"},
		},
	}

	edges := BuildEdges(1, "https://example.com/", result, scope, "static")
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(edges))
	}

	// First edge: internal link
	if edges[0].RelationType != "link" {
		t.Errorf("expected relation_type link, got %q", edges[0].RelationType)
	}
	if !edges[0].IsInternal {
		t.Error("expected first link to be internal")
	}
	if edges[0].AnchorText != "Page 1" {
		t.Errorf("expected anchor text 'Page 1', got %q", edges[0].AnchorText)
	}
	if edges[0].DiscoveryMode != "static" {
		t.Errorf("expected discovery mode static, got %q", edges[0].DiscoveryMode)
	}

	// Second edge: external with rel flags
	if edges[1].IsInternal {
		t.Error("expected second link to be external")
	}
	var flags []string
	if err := json.Unmarshal([]byte(edges[1].RelFlagsJSON), &flags); err != nil {
		t.Fatalf("failed to parse rel flags: %v", err)
	}
	if len(flags) != 2 {
		t.Fatalf("expected 2 rel flags, got %d: %v", len(flags), flags)
	}
}

func TestBuildEdges_Canonical(t *testing.T) {
	scope := newScope(t)
	result := &parser.ParseResult{
		CanonicalResolved: "https://example.com/canonical",
	}

	edges := BuildEdges(1, "https://example.com/", result, scope, "static")
	found := false
	for _, e := range edges {
		if e.RelationType == "canonical" {
			found = true
			if !e.IsInternal {
				t.Error("expected canonical to be internal")
			}
		}
	}
	if !found {
		t.Error("expected canonical edge")
	}
}

func TestBuildEdges_Hreflang(t *testing.T) {
	scope := newScope(t)
	result := &parser.ParseResult{
		Hreflangs: []parser.HreflangEntry{
			{Lang: "en", URL: "https://example.com/en"},
			{Lang: "es", URL: "https://example.com/es"},
		},
	}

	edges := BuildEdges(1, "https://example.com/", result, scope, "static")
	count := 0
	for _, e := range edges {
		if e.RelationType == "hreflang" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 hreflang edges, got %d", count)
	}
}

func TestBuildEdges_DropsMailtoAndJavascript(t *testing.T) {
	scope := newScope(t)
	result := &parser.ParseResult{
		Links: []parser.DiscoveredLink{
			{URL: "mailto:test@example.com", AnchorText: "Email"},
			{URL: "javascript:void(0)", AnchorText: "JS"},
			{URL: "tel:+1234567890", AnchorText: "Phone"},
			{URL: "https://example.com/real", AnchorText: "Real"},
		},
	}

	edges := BuildEdges(1, "https://example.com/", result, scope, "static")
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge (mailto/js/tel dropped), got %d", len(edges))
	}
	if edges[0].DeclaredTargetURL != "https://example.com/real" {
		t.Errorf("expected real link, got %q", edges[0].DeclaredTargetURL)
	}
}

func TestBuildEdges_DropsSamePageLinks(t *testing.T) {
	scope := newScope(t)
	result := &parser.ParseResult{
		Links: []parser.DiscoveredLink{
			{URL: "https://example.com/#productos", AnchorText: "Productos"},
			{URL: "https://example.com/", AnchorText: "Home"},
			{URL: "https://example.com/about#team", AnchorText: "Team"},
		},
	}

	edges := BuildEdges(1, "https://example.com/", result, scope, "browser")
	if len(edges) != 1 {
		t.Fatalf("expected only the cross-page anchor edge, got %d: %#v", len(edges), edges)
	}
	if edges[0].NormalizedTargetURL != "https://example.com/about" {
		t.Errorf("expected /about edge, got %q", edges[0].NormalizedTargetURL)
	}
}

func TestBuildEdges_Pagination(t *testing.T) {
	scope := newScope(t)
	result := &parser.ParseResult{
		RelNext: &parser.RelLink{Raw: "/page/2", Resolved: "https://example.com/page/2"},
		RelPrev: &parser.RelLink{Raw: "/page/0", Resolved: "https://example.com/page/0"},
	}

	edges := BuildEdges(1, "https://example.com/page/1", result, scope, "static")
	types := map[string]bool{}
	for _, e := range edges {
		types[e.RelationType] = true
	}
	if !types["pagination_next"] {
		t.Error("expected pagination_next edge")
	}
	if !types["pagination_prev"] {
		t.Error("expected pagination_prev edge")
	}
}

func TestBuildEdges_RelFlags(t *testing.T) {
	scope := newScope(t)
	result := &parser.ParseResult{
		Links: []parser.DiscoveredLink{
			{URL: "https://other.com/sponsored", AnchorText: "Ad", Rel: "nofollow sponsored"},
		},
	}

	edges := BuildEdges(1, "https://example.com/", result, scope, "static")
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}

	var flags []string
	if err := json.Unmarshal([]byte(edges[0].RelFlagsJSON), &flags); err != nil {
		t.Fatalf("failed to parse rel flags: %v", err)
	}

	flagSet := map[string]bool{}
	for _, f := range flags {
		flagSet[f] = true
	}
	if !flagSet["nofollow"] {
		t.Error("expected nofollow in rel flags")
	}
	if !flagSet["sponsored"] {
		t.Error("expected sponsored in rel flags")
	}
}

func TestBuildEdges_BrowserMode(t *testing.T) {
	scope := newScope(t)
	result := &parser.ParseResult{
		Links: []parser.DiscoveredLink{
			{URL: "https://example.com/page", AnchorText: "Link"},
		},
	}

	edges := BuildEdges(1, "https://example.com/", result, scope, "browser")
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if edges[0].SourceKind != "rendered_dom" {
		t.Errorf("expected source_kind rendered_dom, got %q", edges[0].SourceKind)
	}
	if edges[0].DiscoveryMode != "browser" {
		t.Errorf("expected discovery_mode browser, got %q", edges[0].DiscoveryMode)
	}
}
