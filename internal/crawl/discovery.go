// Package crawl implements crawl orchestration and link discovery.
package crawl

import (
	"encoding/json"
	"strings"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/parser"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/urlutil"
)

// DiscoveredEdge represents a typed edge between two URLs found during crawling.
type DiscoveredEdge struct {
	SourceURLID         int64  `json:"source_url_id"`
	DeclaredTargetURL   string `json:"declared_target_url"`
	NormalizedTargetURL string `json:"normalized_target_url"`
	SourceKind          string `json:"source_kind"`
	RelationType        string `json:"relation_type"`
	RelFlagsJSON        string `json:"rel_flags_json"`
	DiscoveryMode       string `json:"discovery_mode"`
	AnchorText          string `json:"anchor_text"`
	IsInternal          bool   `json:"is_internal"`
	TargetAttr          string `json:"target_attr,omitempty"` // e.g. "_blank"
}

// BuildEdges takes parser output and produces typed edges.
// Uses ScopeChecker to classify internal vs external.
// Uses urlutil.Normalize to normalize target URLs.
// Drops javascript:, mailto:, tel:, data: URLs silently.
func BuildEdges(
	sourceURLID int64,
	pageURL string,
	result *parser.ParseResult,
	scope *urlutil.ScopeChecker,
	discoveryMode string,
) []DiscoveredEdge {
	edges := []DiscoveredEdge{}
	sourceNormalized, _ := urlutil.Normalize(pageURL)

	sourceKind := "html"
	if discoveryMode == "browser" {
		sourceKind = "rendered_dom"
	}

	// 1. Links
	for _, link := range result.Links {
		edge := buildEdge(sourceURLID, link.URL, sourceKind, "link", discoveryMode, scope)
		if edge == nil {
			continue
		}
		if edge.NormalizedTargetURL == sourceNormalized {
			continue
		}
		edge.AnchorText = link.AnchorText
		edge.RelFlagsJSON = parseRelFlags(link.Rel)
		edge.TargetAttr = link.Target
		edges = append(edges, *edge)
	}

	// 2. Canonical
	if result.CanonicalResolved != "" {
		edge := buildEdge(sourceURLID, result.CanonicalResolved, sourceKind, "canonical", discoveryMode, scope)
		if edge != nil {
			edges = append(edges, *edge)
		}
	}

	// 3. Hreflang
	for _, hreflang := range result.Hreflangs {
		edge := buildEdge(sourceURLID, hreflang.URL, sourceKind, "hreflang", discoveryMode, scope)
		if edge != nil {
			edges = append(edges, *edge)
		}
	}

	// 4. Pagination
	if result.RelNext != nil && result.RelNext.Resolved != "" {
		edge := buildEdge(sourceURLID, result.RelNext.Resolved, sourceKind, "pagination_next", discoveryMode, scope)
		if edge != nil {
			edges = append(edges, *edge)
		}
	}
	if result.RelPrev != nil && result.RelPrev.Resolved != "" {
		edge := buildEdge(sourceURLID, result.RelPrev.Resolved, sourceKind, "pagination_prev", discoveryMode, scope)
		if edge != nil {
			edges = append(edges, *edge)
		}
	}

	// 5. Images as asset_ref
	for _, img := range result.Images {
		edge := buildEdge(sourceURLID, img.Src, sourceKind, "asset_ref", discoveryMode, scope)
		if edge != nil {
			edges = append(edges, *edge)
		}
	}

	return edges
}

// buildEdge creates a single edge, returning nil if the URL should be dropped.
func buildEdge(sourceURLID int64, rawURL string, sourceKind string, relationType string, discoveryMode string, scope *urlutil.ScopeChecker) *DiscoveredEdge {
	if rawURL == "" {
		return nil
	}

	if urlutil.IsDroppedScheme(rawURL) {
		return nil
	}

	normalized, err := urlutil.Normalize(rawURL)
	if err != nil {
		return nil
	}

	return &DiscoveredEdge{
		SourceURLID:         sourceURLID,
		DeclaredTargetURL:   rawURL,
		NormalizedTargetURL: normalized,
		SourceKind:          sourceKind,
		RelationType:        relationType,
		RelFlagsJSON:        "[]",
		DiscoveryMode:       discoveryMode,
		IsInternal:          scope.IsInScope(normalized),
	}
}

// parseRelFlags extracts rel attribute values into a JSON array string.
func parseRelFlags(rel string) string {
	if rel == "" {
		return "[]"
	}

	knownFlags := map[string]bool{
		"nofollow":   true,
		"ugc":        true,
		"sponsored":  true,
		"noopener":   true,
		"noreferrer": true,
	}

	parts := strings.Fields(strings.ToLower(rel))
	flags := []string{}
	for _, p := range parts {
		if knownFlags[p] {
			flags = append(flags, p)
		}
	}

	data, _ := json.Marshal(flags)
	return string(data)
}
