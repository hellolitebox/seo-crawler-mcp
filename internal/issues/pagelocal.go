// Package issues provides SEO issue detection for crawled pages.
package issues

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/parser"
)

// DetectedIssue represents a single SEO issue found on a page.
type DetectedIssue struct {
	IssueType   string `json:"issueType"`
	Severity    string `json:"severity"`    // error, warning, info
	Scope       string `json:"scope"`       // always "page_local" for Phase 1
	DetailsJSON string `json:"detailsJson"` // JSON with issue-specific details
}

// PageContext holds all parsed and fetched data needed for issue detection.
type PageContext struct {
	// From fetch
	StatusCode           int
	RedirectHopCount     int
	RedirectLoopDetected bool
	RedirectHopsExceeded bool
	TTFBMS               int64
	ContentType          string

	// From parser
	Title                 string
	TitleLength           int
	MetaDescription       string
	DescriptionLength     int
	MetaRobots            string
	XRobotsTag            string
	CanonicalType         string // self, cross, absent
	HasFavicon            bool
	H1Count               int
	OGTitle               string
	OGDescription         string
	OGImage               string
	OGUrl                 string
	OGType                string
	TwitterCard           string
	TwitterTitle          string
	TwitterDescription    string
	TwitterImage          string
	JSONLDBlocks          int
	MalformedJSONLD       bool
	JSONLDRaw             string
	WordCount             int
	MainContentWordCount  int
	ImagesWithoutAlt      int
	ImagesWithEmptyAlt    int
	MixedContent          bool
	JSSuspect             bool
	ScriptCount           int
	HasSPARoot            bool
	TitleOutsideHead      bool
	MetaRobotsOutsideHead bool

	// Batch A: title/meta, headings, canonicals
	H1s                        []string // all H1 texts
	H2s                        []string // all H2 texts
	TitleCount                 int
	DescriptionCount           int
	MetaDescriptionOutsideHead bool
	FirstHeadingLevel          int      // level of first heading (1-6), 0 if none
	H1AltTextOnly              []string // alt texts from H1s that contain only an <img>
	CanonicalCount             int
	CanonicalRaw               string
	CanonicalOutsideHead       bool

	// Image details (Batch B)
	Images []parser.DiscoveredImage

	// Edge details (Batch B) — populated from edges built during parse
	InternalOutlinkCount         int
	NonDescriptiveAnchorCount    int
	NonDescriptiveAnchorExamples []string
	InternalNofollowCount        int

	// URL of the page being analyzed (Batch B)
	PageURL string

	// Medium-priority detectors
	ResponseHeaders       map[string][]string // HTTP response headers
	Hreflangs             []parser.HreflangEntry
	FormInsecureActions   []string
	ProtocolRelativeCount int
	HreflangOutsideHead   bool
	InvalidHTMLInHead     []string
	HeadTagCount          int
	BodyTagCount          int
	BodySize              int64  // response body size in bytes
	TextContent           string // extracted visible text for content checks

	// Edge data for unsafe cross-origin links
	UnsafeCrossOriginCount    int
	UnsafeCrossOriginExamples []string
}

// Thresholds holds configurable limits for issue detection.
type Thresholds struct {
	TitleMaxLength       int
	TitleMinLength       int
	DescriptionMaxLength int
	DescriptionMinLength int
	ThinContentThreshold int
	DeepPageThreshold    int
}

// DefaultThresholds returns sensible defaults for SEO issue detection.
func DefaultThresholds() Thresholds {
	return Thresholds{
		TitleMaxLength:       60,
		TitleMinLength:       30,
		DescriptionMaxLength: 160,
		DescriptionMinLength: 70,
		ThinContentThreshold: 200,
		DeepPageThreshold:    3,
	}
}

// initialIssueCapacity is a working set hint for the per-page issues slice.
// Empirically, well-formed pages produce ~10–20 issues; pages with many
// errors (broken images, layout problems) can hit ~40. Sizing the
// backing array up-front avoids 4–6 grow-and-copies per page on a
// hot path that runs once per crawled URL.
const initialIssueCapacity = 32

// DetectPageLocalIssues runs all Phase 1 detectors and returns found issues.
func DetectPageLocalIssues(ctx PageContext, thresholds Thresholds, depth int) []DetectedIssue {
	issues := make([]DetectedIssue, 0, initialIssueCapacity)

	// HTTP Status
	if ctx.StatusCode >= 400 && ctx.StatusCode <= 499 {
		issues = append(issues, newIssue("status_4xx", "error", map[string]any{
			"statusCode": ctx.StatusCode,
		}))
	}
	if ctx.StatusCode >= 500 && ctx.StatusCode <= 599 {
		issues = append(issues, newIssue("status_5xx", "error", map[string]any{
			"statusCode": ctx.StatusCode,
		}))
	}
	if ctx.RedirectHopCount > 1 {
		issues = append(issues, newIssue("redirect_chain", "warning", map[string]any{
			"hopCount": ctx.RedirectHopCount,
		}))
	}

	// Redirect
	if ctx.RedirectLoopDetected {
		issues = append(issues, newIssue("redirect_loop", "error", map[string]any{
			"detected": true,
		}))
	}
	if ctx.RedirectHopsExceeded {
		issues = append(issues, newIssue("redirect_hops_exceeded", "error", map[string]any{
			"hopCount": ctx.RedirectHopCount,
		}))
	}

	// Title
	if ctx.Title == "" {
		issues = append(issues, newIssue("missing_title", "error", map[string]any{}))
	}
	if ctx.TitleLength > thresholds.TitleMaxLength {
		issues = append(issues, newIssue("title_too_long", "warning", map[string]any{
			"length":    ctx.TitleLength,
			"maxLength": thresholds.TitleMaxLength,
			"title":     ctx.Title,
		}))
	}
	if ctx.TitleLength > 0 && ctx.TitleLength < thresholds.TitleMinLength {
		issues = append(issues, newIssue("title_too_short", "warning", map[string]any{
			"length":    ctx.TitleLength,
			"minLength": thresholds.TitleMinLength,
			"title":     ctx.Title,
		}))
	}

	// Description
	if ctx.MetaDescription == "" {
		issues = append(issues, newIssue("missing_description", "error", map[string]any{}))
	}
	if ctx.DescriptionLength > thresholds.DescriptionMaxLength {
		issues = append(issues, newIssue("description_too_long", "warning", map[string]any{
			"length":    ctx.DescriptionLength,
			"maxLength": thresholds.DescriptionMaxLength,
		}))
	}
	if ctx.DescriptionLength > 0 && ctx.DescriptionLength < thresholds.DescriptionMinLength {
		issues = append(issues, newIssue("description_too_short", "warning", map[string]any{
			"length":    ctx.DescriptionLength,
			"minLength": thresholds.DescriptionMinLength,
		}))
	}

	// Canonical
	if ctx.CanonicalType == "absent" {
		issues = append(issues, newIssue("missing_canonical", "warning", map[string]any{}))
	}

	// Site metadata
	if !ctx.HasFavicon {
		issues = append(issues, newIssue("missing_favicon", "info", map[string]any{}))
	}

	// Headings
	if ctx.H1Count == 0 {
		issues = append(issues, newIssue("missing_h1", "warning", map[string]any{}))
	}
	if ctx.H1Count > 1 {
		issues = append(issues, newIssue("multiple_h1", "warning", map[string]any{
			"count": ctx.H1Count,
		}))
	}

	// Open Graph
	if ctx.OGTitle == "" {
		issues = append(issues, newIssue("missing_og_title", "info", map[string]any{}))
	} else if len(ctx.OGTitle) > 90 {
		issues = append(issues, newIssue("og_title_too_long", "info", map[string]any{
			"length": len(ctx.OGTitle),
			"limit":  90,
		}))
	}
	if ctx.OGDescription == "" {
		issues = append(issues, newIssue("missing_og_description", "info", map[string]any{}))
	} else if len(ctx.OGDescription) > 200 {
		issues = append(issues, newIssue("og_description_too_long", "info", map[string]any{
			"length": len(ctx.OGDescription),
			"limit":  200,
		}))
	}
	if ctx.OGImage == "" {
		issues = append(issues, newIssue("missing_og_image", "info", map[string]any{}))
	}
	if ctx.OGUrl == "" {
		issues = append(issues, newIssue("missing_og_url", "info", map[string]any{}))
	} else if ctx.CanonicalRaw != "" && ctx.OGUrl != ctx.CanonicalRaw {
		issues = append(issues, newIssue("og_url_canonical_mismatch", "warning", map[string]any{
			"ogUrl":     ctx.OGUrl,
			"canonical": ctx.CanonicalRaw,
		}))
	}
	if ctx.OGType == "" {
		issues = append(issues, newIssue("missing_og_type", "info", map[string]any{}))
	}

	// Twitter Card
	if ctx.TwitterCard == "" {
		issues = append(issues, newIssue("missing_twitter_card", "info", map[string]any{}))
	} else {
		validCards := map[string]bool{"summary": true, "summary_large_image": true, "app": true, "player": true}
		if !validCards[ctx.TwitterCard] {
			issues = append(issues, newIssue("invalid_twitter_card_type", "warning", map[string]any{
				"value": ctx.TwitterCard,
				"valid": "summary, summary_large_image, app, player",
			}))
		}
	}
	if ctx.TwitterTitle == "" && ctx.OGTitle == "" {
		issues = append(issues, newIssue("missing_twitter_title", "info", map[string]any{}))
	}
	if ctx.TwitterImage == "" && ctx.OGImage == "" {
		issues = append(issues, newIssue("missing_twitter_image", "info", map[string]any{}))
	}

	// Structured Data
	if ctx.JSONLDBlocks == 0 {
		issues = append(issues, newIssue("missing_structured_data", "info", map[string]any{}))
	}
	if ctx.MalformedJSONLD {
		issues = append(issues, newIssue("malformed_structured_data", "warning", map[string]any{}))
	}

	// Validate structured data semantics
	if ctx.JSONLDRaw != "" && ctx.JSONLDRaw != "[]" {
		validationResults := parser.ValidateJSONLD(ctx.JSONLDRaw)
		for _, r := range validationResults {
			if len(r.MissingRequired) > 0 {
				issues = append(issues, newIssue("invalid_structured_data", "warning", map[string]any{
					"type":            r.Type,
					"missingRequired": r.MissingRequired,
					"scope":           "required",
					"source":          r.Source,
					"googleDocUrl":    r.GoogleDocURL,
				}))
			}
			if len(r.MissingRecommended) > 0 && !r.Nested {
				issues = append(issues, newIssue("incomplete_structured_data", "info", map[string]any{
					"type":               r.Type,
					"missingRecommended": r.MissingRecommended,
					"scope":              "recommended",
				}))
			}
		}
	}

	// Content
	if ctx.WordCount < thresholds.ThinContentThreshold {
		issues = append(issues, newIssue("thin_content", "warning", map[string]any{
			"wordCount": ctx.WordCount,
			"threshold": thresholds.ThinContentThreshold,
		}))
	}

	// Images
	if ctx.ImagesWithoutAlt > 0 {
		issues = append(issues, newIssue("missing_alt_attribute", "warning", map[string]any{
			"count": ctx.ImagesWithoutAlt,
		}))
	}
	if ctx.ImagesWithEmptyAlt > 0 {
		issues = append(issues, newIssue("empty_alt_attribute", "info", map[string]any{
			"count": ctx.ImagesWithEmptyAlt,
		}))
	}

	// Security
	if ctx.MixedContent {
		issues = append(issues, newIssue("mixed_content", "warning", map[string]any{}))
	}

	// Performance
	if ctx.TTFBMS > 10000 {
		issues = append(issues, newIssue("very_slow_response", "warning", map[string]any{
			"ttfbMs": ctx.TTFBMS,
		}))
	}
	if ctx.TTFBMS > 3000 {
		issues = append(issues, newIssue("slow_response", "info", map[string]any{
			"ttfbMs": ctx.TTFBMS,
		}))
	}

	// Depth
	if depth > thresholds.DeepPageThreshold {
		issues = append(issues, newIssue("deep_page", "info", map[string]any{
			"depth":     depth,
			"threshold": thresholds.DeepPageThreshold,
		}))
	}

	// Robots meta vs header mismatch
	if ctx.MetaRobots != "" && ctx.XRobotsTag != "" {
		metaDirectives := parseRobotsDirectives(ctx.MetaRobots)
		headerDirectives := parseRobotsDirectives(ctx.XRobotsTag)
		if !directivesMatch(metaDirectives, headerDirectives) {
			issues = append(issues, newIssue("robots_meta_header_mismatch", "warning", map[string]any{
				"metaRobots": ctx.MetaRobots,
				"xRobotsTag": ctx.XRobotsTag,
			}))
		}
	}

	// JS Rendering
	if ctx.JSSuspect {
		issues = append(issues, newIssue("js_suspect_not_rendered", "info", map[string]any{}))
	}

	// Tags outside <head>
	if ctx.TitleOutsideHead {
		issues = append(issues, newIssue("title_outside_head", "warning", map[string]any{}))
	}
	if ctx.MetaRobotsOutsideHead {
		issues = append(issues, newIssue("meta_robots_outside_head", "warning", map[string]any{}))
	}

	// ── Batch A: Title/Meta + Headings + Canonicals ────────────────────

	// title_same_as_h1
	if ctx.Title != "" && len(ctx.H1s) > 0 {
		if strings.EqualFold(strings.TrimSpace(ctx.Title), strings.TrimSpace(ctx.H1s[0])) {
			issues = append(issues, newIssue("title_same_as_h1", "warning", map[string]any{
				"title": ctx.Title,
				"h1":    ctx.H1s[0],
			}))
		}
	}

	// multiple_title_tags
	if ctx.TitleCount > 1 {
		issues = append(issues, newIssue("multiple_title_tags", "warning", map[string]any{
			"count": ctx.TitleCount,
		}))
	}

	// multiple_meta_descriptions
	if ctx.DescriptionCount > 1 {
		issues = append(issues, newIssue("multiple_meta_descriptions", "warning", map[string]any{
			"count": ctx.DescriptionCount,
		}))
	}

	// meta_description_outside_head
	if ctx.MetaDescriptionOutsideHead {
		issues = append(issues, newIssue("meta_description_outside_head", "warning", map[string]any{}))
	}

	// h1_too_long
	for _, h1 := range ctx.H1s {
		if len([]rune(h1)) > 70 {
			issues = append(issues, newIssue("h1_too_long", "info", map[string]any{
				"length": len([]rune(h1)),
				"h1":     h1,
			}))
		}
	}

	// h1_non_sequential
	if ctx.FirstHeadingLevel > 1 {
		issues = append(issues, newIssue("h1_non_sequential", "warning", map[string]any{
			"firstHeadingLevel": ctx.FirstHeadingLevel,
		}))
	}

	// h1_alt_text_only
	for _, alt := range ctx.H1AltTextOnly {
		issues = append(issues, newIssue("h1_alt_text_only", "warning", map[string]any{
			"alt": alt,
		}))
	}

	// missing_h2
	if len(ctx.H2s) == 0 {
		issues = append(issues, newIssue("missing_h2", "info", map[string]any{}))
	}

	// h2_non_sequential: H2 without a preceding H1
	if len(ctx.H2s) > 0 && ctx.H1Count == 0 {
		issues = append(issues, newIssue("h2_non_sequential", "warning", map[string]any{}))
	} else if len(ctx.H2s) > 0 && ctx.FirstHeadingLevel > 1 {
		// First heading is H2+ meaning H2 appears before H1
		issues = append(issues, newIssue("h2_non_sequential", "warning", map[string]any{}))
	}

	// h2_too_long
	for _, h2 := range ctx.H2s {
		if len([]rune(h2)) > 70 {
			issues = append(issues, newIssue("h2_too_long", "info", map[string]any{
				"length": len([]rune(h2)),
				"h2":     h2,
			}))
		}
	}

	// multiple_canonicals
	if ctx.CanonicalCount > 1 {
		issues = append(issues, newIssue("multiple_canonicals", "warning", map[string]any{
			"count": ctx.CanonicalCount,
		}))
	}

	// canonical_is_relative
	if ctx.CanonicalRaw != "" && !strings.HasPrefix(strings.ToLower(ctx.CanonicalRaw), "http") {
		issues = append(issues, newIssue("canonical_is_relative", "warning", map[string]any{
			"canonical": ctx.CanonicalRaw,
		}))
	}

	// canonical_outside_head
	if ctx.CanonicalOutsideHead {
		issues = append(issues, newIssue("canonical_outside_head", "warning", map[string]any{}))
	}

	// ── Batch B: Image issues ──────────────────────────────────────────

	// alt_text_too_long
	if len(ctx.Images) > 0 {
		var longAltCount int
		var maxLen int
		for _, img := range ctx.Images {
			l := len(img.Alt)
			if l > 100 {
				longAltCount++
				if l > maxLen {
					maxLen = l
				}
			}
		}
		if longAltCount > 0 {
			issues = append(issues, newIssue("alt_text_too_long", "warning", map[string]any{
				"count":     longAltCount,
				"maxLength": maxLen,
			}))
		}
	}

	// missing_image_size_attributes
	if len(ctx.Images) > 0 {
		var missingSizeCount int
		for _, img := range ctx.Images {
			if !img.HasWidth && !img.HasHeight {
				missingSizeCount++
			}
		}
		if missingSizeCount > 0 {
			issues = append(issues, newIssue("missing_image_size_attributes", "info", map[string]any{
				"count": missingSizeCount,
			}))
		}
	}

	// ── Batch B: Link issues ───────────────────────────────────────────

	// non_descriptive_anchor_text
	if ctx.NonDescriptiveAnchorCount > 0 {
		issues = append(issues, newIssue("non_descriptive_anchor_text", "warning", map[string]any{
			"count":    ctx.NonDescriptiveAnchorCount,
			"examples": ctx.NonDescriptiveAnchorExamples,
		}))
	}

	// internal_nofollow_outlink
	if ctx.InternalNofollowCount > 0 {
		issues = append(issues, newIssue("internal_nofollow_outlink", "warning", map[string]any{
			"count": ctx.InternalNofollowCount,
		}))
	}

	// ── Batch B: URL issues ────────────────────────────────────────────
	issues = append(issues, DetectURLIssues(ctx.PageURL)...)

	// ── Medium: Security Headers ───────────────────────────────────────

	if ctx.ResponseHeaders != nil {
		pageURLParsed, _ := url.Parse(ctx.PageURL)
		isHTTPS := pageURLParsed != nil && pageURLParsed.Scheme == "https"

		if isHTTPS && getHeader(ctx.ResponseHeaders, "Strict-Transport-Security") == "" {
			issues = append(issues, newIssue("missing_hsts_header", "warning", map[string]any{}))
		}
		if getHeader(ctx.ResponseHeaders, "X-Content-Type-Options") == "" {
			issues = append(issues, newIssue("missing_x_content_type_options", "info", map[string]any{}))
		}
		if getHeader(ctx.ResponseHeaders, "X-Frame-Options") == "" {
			issues = append(issues, newIssue("missing_x_frame_options", "info", map[string]any{}))
		}
		if getHeader(ctx.ResponseHeaders, "Content-Security-Policy") == "" {
			issues = append(issues, newIssue("missing_content_security_policy", "info", map[string]any{}))
		}
		rp := getHeader(ctx.ResponseHeaders, "Referrer-Policy")
		if rp == "" || !isSecureReferrerPolicy(rp) {
			issues = append(issues, newIssue("missing_referrer_policy", "info", map[string]any{}))
		}
	}

	// unsafe_cross_origin_links
	if ctx.UnsafeCrossOriginCount > 0 {
		issues = append(issues, newIssue("unsafe_cross_origin_links", "warning", map[string]any{
			"count":    ctx.UnsafeCrossOriginCount,
			"examples": ctx.UnsafeCrossOriginExamples,
		}))
	}

	// form_on_http
	for _, formAction := range ctx.FormInsecureActions {
		issues = append(issues, newIssue("form_on_http", "warning", map[string]any{
			"formAction": formAction,
		}))
	}

	// protocol_relative_urls
	if ctx.ProtocolRelativeCount > 0 {
		issues = append(issues, newIssue("protocol_relative_urls", "info", map[string]any{
			"count": ctx.ProtocolRelativeCount,
		}))
	}

	// ── Medium: Hreflang ───────────────────────────────────────────────

	if len(ctx.Hreflangs) > 0 {
		// hreflang_missing_self
		hasSelf := false
		for _, entry := range ctx.Hreflangs {
			if normalizeForComparison(entry.URL) == normalizeForComparison(ctx.PageURL) {
				hasSelf = true
				break
			}
		}
		if !hasSelf {
			issues = append(issues, newIssue("hreflang_missing_self", "warning", map[string]any{
				"pageUrl": ctx.PageURL,
			}))
		}

		// hreflang_missing_x_default
		hasXDefault := false
		for _, entry := range ctx.Hreflangs {
			if strings.ToLower(entry.Lang) == "x-default" {
				hasXDefault = true
				break
			}
		}
		if !hasXDefault {
			issues = append(issues, newIssue("hreflang_missing_x_default", "warning", map[string]any{}))
		}

		// hreflang_invalid_language_code
		for _, entry := range ctx.Hreflangs {
			lang := strings.ToLower(entry.Lang)
			if lang == "x-default" {
				continue
			}
			if !isValidHreflangCode(lang) {
				issues = append(issues, newIssue("hreflang_invalid_language_code", "warning", map[string]any{
					"invalidCode": entry.Lang,
					"url":         entry.URL,
				}))
			}
		}
	}

	// hreflang_outside_head
	if ctx.HreflangOutsideHead {
		issues = append(issues, newIssue("hreflang_outside_head", "warning", map[string]any{}))
	}

	// ── Medium: HTML Validation ────────────────────────────────────────

	if len(ctx.InvalidHTMLInHead) > 0 {
		issues = append(issues, newIssue("invalid_html_in_head", "warning", map[string]any{
			"elements": ctx.InvalidHTMLInHead,
		}))
	}

	if ctx.HeadTagCount > 1 {
		issues = append(issues, newIssue("multiple_head_tags", "warning", map[string]any{
			"count": ctx.HeadTagCount,
		}))
	}

	if ctx.BodyTagCount > 1 {
		issues = append(issues, newIssue("multiple_body_tags", "warning", map[string]any{
			"count": ctx.BodyTagCount,
		}))
	}

	if ctx.BodySize > 15*1024*1024 {
		issues = append(issues, newIssue("html_too_large", "warning", map[string]any{
			"sizeKB": ctx.BodySize / 1024,
		}))
	}

	// ── Medium: Content ────────────────────────────────────────────────

	if ctx.TextContent != "" && strings.Contains(strings.ToLower(ctx.TextContent), "lorem ipsum") {
		issues = append(issues, newIssue("lorem_ipsum_detected", "warning", map[string]any{}))
	}

	// soft_404: page returns 200 but looks like an error page
	if ctx.StatusCode == 200 && ctx.WordCount < 100 {
		titleLower := strings.ToLower(ctx.Title)
		textLower := strings.ToLower(ctx.TextContent)
		soft404Patterns := []string{"page not found", "404", "not found", "doesn't exist", "does not exist", "no longer available"}
		for _, pattern := range soft404Patterns {
			if strings.Contains(titleLower, pattern) {
				issues = append(issues, newIssue("soft_404", "info", map[string]any{
					"signal": "title contains '" + pattern + "'",
				}))
				break
			}
			if strings.Contains(textLower, pattern) {
				issues = append(issues, newIssue("soft_404", "info", map[string]any{
					"signal": "body contains '" + pattern + "'",
				}))
				break
			}
		}
	}

	return issues
}

// nonDescriptiveAnchors is the set of generic anchor texts considered non-descriptive.
var nonDescriptiveAnchors = map[string]bool{
	"click here": true,
	"read more":  true,
	"learn more": true,
	"here":       true,
	"this":       true,
	"more":       true,
	"link":       true,
	"go":         true,
	"visit":      true,
}

// IsNonDescriptiveAnchor checks if the given anchor text is generic/non-descriptive.
func IsNonDescriptiveAnchor(anchor string) bool {
	return nonDescriptiveAnchors[strings.ToLower(strings.TrimSpace(anchor))]
}

// DetectURLIssues checks a URL string for common SEO URL issues.
func DetectURLIssues(rawURL string) []DetectedIssue {
	if rawURL == "" {
		return nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}

	var issues []DetectedIssue

	// url_uppercase — path contains uppercase letters
	if hasUppercase(parsed.Path) {
		issues = append(issues, newIssue("url_uppercase", "info", map[string]any{
			"url": rawURL,
		}))
	}

	// url_underscores — path contains underscores
	if strings.Contains(parsed.Path, "_") {
		issues = append(issues, newIssue("url_underscores", "info", map[string]any{
			"url": rawURL,
		}))
	}

	// url_contains_space — encoded spaces (%20 or +)
	if strings.Contains(rawURL, "%20") || strings.Contains(parsed.RawQuery, "+") || strings.Contains(parsed.Path, "+") {
		issues = append(issues, newIssue("url_contains_space", "warning", map[string]any{
			"url": rawURL,
		}))
	}

	// url_has_parameters — has query string
	if parsed.RawQuery != "" {
		params := []string{}
		for key := range parsed.Query() {
			params = append(params, key)
		}
		issues = append(issues, newIssue("url_has_parameters", "info", map[string]any{
			"url":    rawURL,
			"params": params,
		}))
	}

	// url_too_long — over 115 characters
	if len(rawURL) > 115 {
		issues = append(issues, newIssue("url_too_long", "info", map[string]any{
			"url":    rawURL,
			"length": len(rawURL),
		}))
	}

	// url_multiple_slashes — consecutive slashes in path
	if strings.Contains(parsed.Path, "//") {
		issues = append(issues, newIssue("url_multiple_slashes", "warning", map[string]any{
			"url": rawURL,
		}))
	}

	// url_repetitive_path — repeating path segments
	if seg := findRepetitiveSegment(parsed.Path); seg != "" {
		issues = append(issues, newIssue("url_repetitive_path", "warning", map[string]any{
			"url":             rawURL,
			"repeatedSegment": seg,
		}))
	}

	return issues
}

// hasUppercase returns true if the string contains any uppercase letter.
func hasUppercase(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// findRepetitiveSegment returns the first repeated consecutive path segment, or "".
func findRepetitiveSegment(path string) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) < 2 {
		return ""
	}
	for i := 1; i < len(segments); i++ {
		if segments[i] != "" && segments[i] == segments[i-1] {
			return segments[i]
		}
	}
	return ""
}

// parseRobotsDirectives splits a robots directive string into a normalized set.
func parseRobotsDirectives(raw string) map[string]bool {
	directives := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		d := strings.TrimSpace(strings.ToLower(part))
		if d != "" {
			directives[d] = true
		}
	}
	return directives
}

// directivesMatch returns true if two directive sets are identical.
func directivesMatch(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// getHeader retrieves a header value case-insensitively from a map.
func getHeader(headers map[string][]string, key string) string {
	// http.Header is canonicalized, so try canonical first
	if vals, ok := headers[key]; ok && len(vals) > 0 {
		return vals[0]
	}
	// Fallback: case-insensitive search
	keyLower := strings.ToLower(key)
	for k, vals := range headers {
		if strings.ToLower(k) == keyLower && len(vals) > 0 {
			return vals[0]
		}
	}
	return ""
}

// isSecureReferrerPolicy checks if the referrer policy is a secure value.
func isSecureReferrerPolicy(policy string) bool {
	secure := map[string]bool{
		"no-referrer":                     true,
		"no-referrer-when-downgrade":      true,
		"strict-origin":                   true,
		"strict-origin-when-cross-origin": true,
		"same-origin":                     true,
		"origin":                          true,
		"origin-when-cross-origin":        true,
	}
	return secure[strings.TrimSpace(strings.ToLower(policy))]
}

// validISO639_1 contains common ISO 639-1 language codes.
var validISO639_1 = map[string]bool{
	"aa": true, "ab": true, "af": true, "ak": true, "am": true, "an": true, "ar": true, "as": true,
	"av": true, "ay": true, "az": true, "ba": true, "be": true, "bg": true, "bh": true, "bi": true,
	"bm": true, "bn": true, "bo": true, "br": true, "bs": true, "ca": true, "ce": true, "ch": true,
	"co": true, "cr": true, "cs": true, "cu": true, "cv": true, "cy": true, "da": true, "de": true,
	"dv": true, "dz": true, "ee": true, "el": true, "en": true, "eo": true, "es": true, "et": true,
	"eu": true, "fa": true, "ff": true, "fi": true, "fj": true, "fo": true, "fr": true, "fy": true,
	"ga": true, "gd": true, "gl": true, "gn": true, "gu": true, "gv": true, "ha": true, "he": true,
	"hi": true, "ho": true, "hr": true, "ht": true, "hu": true, "hy": true, "hz": true, "ia": true,
	"id": true, "ie": true, "ig": true, "ii": true, "ik": true, "io": true, "is": true, "it": true,
	"iu": true, "ja": true, "jv": true, "ka": true, "kg": true, "ki": true, "kj": true, "kk": true,
	"kl": true, "km": true, "kn": true, "ko": true, "kr": true, "ks": true, "ku": true, "kv": true,
	"kw": true, "ky": true, "la": true, "lb": true, "lg": true, "li": true, "ln": true, "lo": true,
	"lt": true, "lu": true, "lv": true, "mg": true, "mh": true, "mi": true, "mk": true, "ml": true,
	"mn": true, "mr": true, "ms": true, "mt": true, "my": true, "na": true, "nb": true, "nd": true,
	"ne": true, "ng": true, "nl": true, "nn": true, "no": true, "nr": true, "nv": true, "ny": true,
	"oc": true, "oj": true, "om": true, "or": true, "os": true, "pa": true, "pi": true, "pl": true,
	"ps": true, "pt": true, "qu": true, "rm": true, "rn": true, "ro": true, "ru": true, "rw": true,
	"sa": true, "sc": true, "sd": true, "se": true, "sg": true, "si": true, "sk": true, "sl": true,
	"sm": true, "sn": true, "so": true, "sq": true, "sr": true, "ss": true, "st": true, "su": true,
	"sv": true, "sw": true, "ta": true, "te": true, "tg": true, "th": true, "ti": true, "tk": true,
	"tl": true, "tn": true, "to": true, "tr": true, "ts": true, "tt": true, "tw": true, "ty": true,
	"ug": true, "uk": true, "ur": true, "uz": true, "ve": true, "vi": true, "vo": true, "wa": true,
	"wo": true, "xh": true, "yi": true, "yo": true, "za": true, "zh": true, "zu": true,
}

// isValidHreflangCode checks if a hreflang code is valid (ISO 639-1, optionally with region).
func isValidHreflangCode(code string) bool {
	parts := strings.SplitN(code, "-", 2)
	lang := strings.ToLower(parts[0])
	if !validISO639_1[lang] {
		return false
	}
	if len(parts) == 2 {
		region := strings.ToUpper(parts[1])
		// Region should be 2 uppercase letters (ISO 3166-1 alpha-2)
		if len(region) != 2 {
			return false
		}
		for _, ch := range region {
			if ch < 'A' || ch > 'Z' {
				return false
			}
		}
	}
	return true
}

// normalizeForComparison normalizes a URL for comparison (strips trailing slash).
func normalizeForComparison(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String()
}

func newIssue(issueType, severity string, details map[string]any) DetectedIssue {
	detailsBytes, err := json.Marshal(details)
	if err != nil {
		detailsBytes = []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	return DetectedIssue{
		IssueType:   issueType,
		Severity:    severity,
		Scope:       "page_local",
		DetailsJSON: string(detailsBytes),
	}
}
