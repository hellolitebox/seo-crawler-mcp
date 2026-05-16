package issues

import (
	"strings"
	"testing"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/parser"
)

// cleanPage returns a well-formed PageContext that should produce no error/warning issues.
func cleanPage() PageContext {
	return PageContext{
		StatusCode:           200,
		RedirectHopCount:     0,
		TTFBMS:               500,
		ContentType:          "text/html",
		Title:                "A Good SEO Title That Is Just Right",
		TitleLength:          36,
		MetaDescription:      "This is a well-crafted meta description that provides enough detail for search engines.",
		DescriptionLength:    88,
		CanonicalType:        "self",
		HasFavicon:           true,
		H1Count:              1,
		OGTitle:              "OG Title",
		OGDescription:        "OG Description",
		OGImage:              "https://example.com/og.png",
		JSONLDBlocks:         1,
		WordCount:            500,
		MainContentWordCount: 400,
		PageURL:              "https://example.com/page",
		HeadTagCount:         1,
		BodyTagCount:         1,
		ResponseHeaders: map[string][]string{
			"Strict-Transport-Security": {"max-age=31536000"},
			"X-Content-Type-Options":    {"nosniff"},
			"X-Frame-Options":           {"DENY"},
			"Content-Security-Policy":   {"default-src 'self'"},
			"Referrer-Policy":           {"strict-origin-when-cross-origin"},
		},
	}
}

func defaultThresholds() Thresholds {
	return DefaultThresholds()
}

func hasIssue(issues []DetectedIssue, issueType string) bool {
	for _, issue := range issues {
		if issue.IssueType == issueType {
			return true
		}
	}
	return false
}

func countIssues(issues []DetectedIssue, issueType string) int {
	count := 0
	for _, issue := range issues {
		if issue.IssueType == issueType {
			count++
		}
	}
	return count
}

func TestCleanPage_NoIssues(t *testing.T) {
	issues := DetectPageLocalIssues(cleanPage(), defaultThresholds(), 1)
	for _, issue := range issues {
		if issue.Severity == "error" || issue.Severity == "warning" {
			t.Errorf("clean page should have no error/warning issues, got %q (%s)", issue.IssueType, issue.Severity)
		}
	}
}

func TestDetectPageLocalIssues(t *testing.T) {
	tests := []struct {
		name       string
		ctx        PageContext
		thresholds Thresholds
		depth      int
		wantTypes  []string
		wantAbsent []string
	}{
		{
			name:       "clean page — zero issues",
			ctx:        cleanPage(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{},
		},
		{
			name: "404 page",
			ctx: func() PageContext {
				p := cleanPage()
				p.StatusCode = 404
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"status_4xx"},
		},
		{
			name: "500 page",
			ctx: func() PageContext {
				p := cleanPage()
				p.StatusCode = 503
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"status_5xx"},
		},
		{
			name: "redirect chain (3 hops)",
			ctx: func() PageContext {
				p := cleanPage()
				p.RedirectHopCount = 3
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"redirect_chain"},
		},
		{
			name: "redirect loop",
			ctx: func() PageContext {
				p := cleanPage()
				p.RedirectLoopDetected = true
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"redirect_loop"},
		},
		{
			name: "redirect hops exceeded",
			ctx: func() PageContext {
				p := cleanPage()
				p.RedirectHopsExceeded = true
				p.RedirectHopCount = 10
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"redirect_hops_exceeded"},
		},
		{
			name: "missing title",
			ctx: func() PageContext {
				p := cleanPage()
				p.Title = ""
				p.TitleLength = 0
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"missing_title"},
		},
		{
			name: "title too long (75 chars)",
			ctx: func() PageContext {
				p := cleanPage()
				p.Title = strings.Repeat("A", 75)
				p.TitleLength = 75
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"title_too_long"},
		},
		{
			name: "title too short (10 chars)",
			ctx: func() PageContext {
				p := cleanPage()
				p.Title = "Short"
				p.TitleLength = 10
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"title_too_short"},
		},
		{
			name: "missing description",
			ctx: func() PageContext {
				p := cleanPage()
				p.MetaDescription = ""
				p.DescriptionLength = 0
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"missing_description"},
		},
		{
			name: "description too long",
			ctx: func() PageContext {
				p := cleanPage()
				p.MetaDescription = strings.Repeat("D", 200)
				p.DescriptionLength = 200
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"description_too_long"},
		},
		{
			name: "description too short",
			ctx: func() PageContext {
				p := cleanPage()
				p.MetaDescription = "Short desc"
				p.DescriptionLength = 30
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"description_too_short"},
		},
		{
			name: "missing canonical",
			ctx: func() PageContext {
				p := cleanPage()
				p.CanonicalType = "absent"
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"missing_canonical"},
		},
		{
			name: "missing favicon",
			ctx: func() PageContext {
				p := cleanPage()
				p.HasFavicon = false
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"missing_favicon"},
		},
		{
			name: "no H1",
			ctx: func() PageContext {
				p := cleanPage()
				p.H1Count = 0
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"missing_h1"},
		},
		{
			name: "multiple H1 (3)",
			ctx: func() PageContext {
				p := cleanPage()
				p.H1Count = 3
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"multiple_h1"},
		},
		{
			name: "no OG tags",
			ctx: func() PageContext {
				p := cleanPage()
				p.OGTitle = ""
				p.OGDescription = ""
				p.OGImage = ""
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"missing_og_title", "missing_og_description", "missing_og_image"},
		},
		{
			name: "no structured data",
			ctx: func() PageContext {
				p := cleanPage()
				p.JSONLDBlocks = 0
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"missing_structured_data"},
		},
		{
			name: "malformed JSON-LD",
			ctx: func() PageContext {
				p := cleanPage()
				p.MalformedJSONLD = true
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"malformed_structured_data"},
		},
		{
			name: "thin content (45 words)",
			ctx: func() PageContext {
				p := cleanPage()
				p.WordCount = 45
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"thin_content"},
		},
		{
			name: "missing alt (3 images)",
			ctx: func() PageContext {
				p := cleanPage()
				p.ImagesWithoutAlt = 3
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"missing_alt_attribute"},
		},
		{
			name: "empty alt attribute",
			ctx: func() PageContext {
				p := cleanPage()
				p.ImagesWithEmptyAlt = 2
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"empty_alt_attribute"},
		},
		{
			name: "mixed content",
			ctx: func() PageContext {
				p := cleanPage()
				p.MixedContent = true
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"mixed_content"},
		},
		{
			name: "slow response (4500ms)",
			ctx: func() PageContext {
				p := cleanPage()
				p.TTFBMS = 4500
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"slow_response"},
			wantAbsent: []string{"very_slow_response"},
		},
		{
			name: "very slow response (12000ms) — both slow and very_slow",
			ctx: func() PageContext {
				p := cleanPage()
				p.TTFBMS = 12000
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"very_slow_response", "slow_response"},
		},
		{
			name:       "deep page (depth 5)",
			ctx:        cleanPage(),
			thresholds: defaultThresholds(),
			depth:      5,
			wantTypes:  []string{"deep_page"},
		},
		{
			name: "JS suspect",
			ctx: func() PageContext {
				p := cleanPage()
				p.JSSuspect = true
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"js_suspect_not_rendered"},
		},
		{
			name: "robots meta/header mismatch — noindex vs index",
			ctx: func() PageContext {
				p := cleanPage()
				p.MetaRobots = "noindex, follow"
				p.XRobotsTag = "index, follow"
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"robots_meta_header_mismatch"},
		},
		{
			name: "robots meta/header match — no issue",
			ctx: func() PageContext {
				p := cleanPage()
				p.MetaRobots = "noindex, nofollow"
				p.XRobotsTag = "nofollow, noindex"
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{},
			wantAbsent: []string{"robots_meta_header_mismatch"},
		},
		{
			name: "robots only meta — no mismatch",
			ctx: func() PageContext {
				p := cleanPage()
				p.MetaRobots = "noindex"
				p.XRobotsTag = ""
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantAbsent: []string{"robots_meta_header_mismatch"},
		},
		{
			name: "multiple issues — missing title + thin content + no H1",
			ctx: func() PageContext {
				p := cleanPage()
				p.Title = ""
				p.TitleLength = 0
				p.WordCount = 45
				p.H1Count = 0
				return p
			}(),
			thresholds: defaultThresholds(),
			depth:      1,
			wantTypes:  []string{"missing_title", "thin_content", "missing_h1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectPageLocalIssues(tt.ctx, tt.thresholds, tt.depth)

			for _, wantType := range tt.wantTypes {
				if !hasIssue(got, wantType) {
					t.Errorf("expected issue %q not found in results", wantType)
				}
			}

			for _, absentType := range tt.wantAbsent {
				if hasIssue(got, absentType) {
					t.Errorf("unexpected issue %q found in results", absentType)
				}
			}

			// Verify all issues have scope "page_local"
			for _, issue := range got {
				if issue.Scope != "page_local" {
					t.Errorf("issue %q has scope %q, want %q", issue.IssueType, issue.Scope, "page_local")
				}
				if issue.DetailsJSON == "" {
					t.Errorf("issue %q has empty detailsJson", issue.IssueType)
				}
			}
		})
	}
}

func TestDetectedIssue_DetailsJSON(t *testing.T) {
	ctx := cleanPage()
	ctx.StatusCode = 404
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)

	for _, issue := range issues {
		if issue.IssueType == "status_4xx" {
			if !strings.Contains(issue.DetailsJSON, `"statusCode":404`) {
				t.Errorf("status_4xx detailsJson should contain statusCode, got %s", issue.DetailsJSON)
			}
			return
		}
	}
	t.Error("status_4xx issue not found")
}

func TestSliceNeverNil(t *testing.T) {
	// Even with zero issues matching, the returned slice must not be nil.
	issues := DetectPageLocalIssues(cleanPage(), defaultThresholds(), 1)
	if issues == nil {
		t.Error("returned slice must not be nil")
	}
}

func TestParseRobotsDirectives(t *testing.T) {
	tests := []struct {
		input string
		want  map[string]bool
	}{
		{"noindex, nofollow", map[string]bool{"noindex": true, "nofollow": true}},
		{"NOINDEX", map[string]bool{"noindex": true}},
		{"index, follow", map[string]bool{"index": true, "follow": true}},
		{"", map[string]bool{}},
	}
	for _, tt := range tests {
		got := parseRobotsDirectives(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("parseRobotsDirectives(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for k := range tt.want {
			if !got[k] {
				t.Errorf("parseRobotsDirectives(%q) missing %q", tt.input, k)
			}
		}
	}
}

func TestDirectivesMatch(t *testing.T) {
	a := map[string]bool{"noindex": true, "nofollow": true}
	b := map[string]bool{"nofollow": true, "noindex": true}
	c := map[string]bool{"index": true, "follow": true}

	if !directivesMatch(a, b) {
		t.Error("expected a and b to match")
	}
	if directivesMatch(a, c) {
		t.Error("expected a and c to not match")
	}
}

func TestIssueCounts(t *testing.T) {
	// Each detector should fire exactly once.
	ctx := cleanPage()
	ctx.StatusCode = 404
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if count := countIssues(issues, "status_4xx"); count != 1 {
		t.Errorf("expected exactly 1 status_4xx issue, got %d", count)
	}
}

func TestDetectPageLocalIssues_InvalidStructuredData(t *testing.T) {
	ctx := cleanPage()
	// BlogPosting missing headline — should emit invalid_structured_data
	ctx.JSONLDRaw = `[{"raw":"{\"@type\":\"BlogPosting\",\"author\":\"Jane\",\"datePublished\":\"2024-01-01\"}","type":"BlogPosting"}]`
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)

	if !hasIssue(issues, "invalid_structured_data") {
		t.Error("expected invalid_structured_data issue for BlogPosting missing headline")
	}
}

func TestDetectPageLocalIssues_IncompleteStructuredData(t *testing.T) {
	ctx := cleanPage()
	// Organization with name but missing logo (recommended) — should emit incomplete_structured_data
	ctx.JSONLDRaw = `[{"raw":"{\"@type\":\"Organization\",\"name\":\"My Org\"}","type":"Organization"}]`
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)

	if !hasIssue(issues, "incomplete_structured_data") {
		t.Error("expected incomplete_structured_data issue for Organization missing recommended props")
	}
	// Should NOT have invalid_structured_data (name is present = all required met)
	if hasIssue(issues, "invalid_structured_data") {
		t.Error("unexpected invalid_structured_data — Organization has all required fields")
	}
}

func TestTitleOutsideHeadIssue(t *testing.T) {
	ctx := cleanPage()
	ctx.TitleOutsideHead = true
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 0)
	if !hasIssue(issues, "title_outside_head") {
		t.Error("expected title_outside_head issue")
	}
}

func TestMetaRobotsOutsideHeadIssue(t *testing.T) {
	ctx := cleanPage()
	ctx.MetaRobotsOutsideHead = true
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 0)
	if !hasIssue(issues, "meta_robots_outside_head") {
		t.Error("expected meta_robots_outside_head issue")
	}
}

func TestNoOutsideHeadIssuesOnCleanPage(t *testing.T) {
	ctx := cleanPage()
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 0)
	if hasIssue(issues, "title_outside_head") {
		t.Error("unexpected title_outside_head on clean page")
	}
	if hasIssue(issues, "meta_robots_outside_head") {
		t.Error("unexpected meta_robots_outside_head on clean page")
	}
}

// ── Batch B: Image issue tests ──────────────────────────────────────

func TestAltTextTooLong(t *testing.T) {
	ctx := cleanPage()
	ctx.Images = []parser.DiscoveredImage{
		{Src: "a.png", Alt: strings.Repeat("x", 150), HasWidth: true, HasHeight: true},
		{Src: "b.png", Alt: "short", HasWidth: true, HasHeight: true},
	}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 0)
	if !hasIssue(issues, "alt_text_too_long") {
		t.Error("expected alt_text_too_long")
	}
}

func TestAltTextNotTooLong(t *testing.T) {
	ctx := cleanPage()
	ctx.Images = []parser.DiscoveredImage{
		{Src: "a.png", Alt: "Reasonable alt text", HasWidth: true, HasHeight: true},
	}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 0)
	if hasIssue(issues, "alt_text_too_long") {
		t.Error("unexpected alt_text_too_long")
	}
}

func TestMissingImageSizeAttributes(t *testing.T) {
	ctx := cleanPage()
	ctx.Images = []parser.DiscoveredImage{
		{Src: "a.png", Alt: "A", HasWidth: false, HasHeight: false},
		{Src: "b.png", Alt: "B", HasWidth: true, HasHeight: true},
		{Src: "c.png", Alt: "C", HasWidth: true, HasHeight: false}, // has one, should NOT trigger
	}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 0)
	if !hasIssue(issues, "missing_image_size_attributes") {
		t.Error("expected missing_image_size_attributes")
	}
}

func TestImageWithOneDimensionDoesNotTrigger(t *testing.T) {
	ctx := cleanPage()
	ctx.Images = []parser.DiscoveredImage{
		{Src: "a.png", Alt: "A", HasWidth: true, HasHeight: false},
	}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 0)
	if hasIssue(issues, "missing_image_size_attributes") {
		t.Error("unexpected missing_image_size_attributes when one dimension is present")
	}
}

// ── Batch B: Link issue tests ───────────────────────────────────────

func TestNonDescriptiveAnchorText(t *testing.T) {
	ctx := cleanPage()
	ctx.NonDescriptiveAnchorCount = 3
	ctx.NonDescriptiveAnchorExamples = []string{"click here", "read more", "here"}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 0)
	if !hasIssue(issues, "non_descriptive_anchor_text") {
		t.Error("expected non_descriptive_anchor_text")
	}
}

func TestInternalNofollowOutlink(t *testing.T) {
	ctx := cleanPage()
	ctx.InternalNofollowCount = 2
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 0)
	if !hasIssue(issues, "internal_nofollow_outlink") {
		t.Error("expected internal_nofollow_outlink")
	}
}

// ── Batch B: URL issue tests ────────────────────────────────────────

func TestDetectURLIssues(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		wantTypes  []string
		wantAbsent []string
	}{
		{
			name:       "clean URL",
			url:        "https://example.com/blog/my-post",
			wantTypes:  []string{},
			wantAbsent: []string{"url_uppercase", "url_underscores", "url_contains_space", "url_has_parameters", "url_too_long", "url_multiple_slashes", "url_repetitive_path"},
		},
		{
			name:      "uppercase path",
			url:       "https://example.com/Blog/My-Post",
			wantTypes: []string{"url_uppercase"},
		},
		{
			name:      "underscores",
			url:       "https://example.com/blog/my_post",
			wantTypes: []string{"url_underscores"},
		},
		{
			name:      "space %20",
			url:       "https://example.com/blog/my%20post",
			wantTypes: []string{"url_contains_space"},
		},
		{
			name:      "query parameters",
			url:       "https://example.com/search?q=test&page=1",
			wantTypes: []string{"url_has_parameters"},
		},
		{
			name:      "long URL",
			url:       "https://example.com/" + strings.Repeat("a", 100),
			wantTypes: []string{"url_too_long"},
		},
		{
			name:      "multiple slashes",
			url:       "https://example.com/blog//post",
			wantTypes: []string{"url_multiple_slashes"},
		},
		{
			name:      "repetitive path",
			url:       "https://example.com/blog/blog/post",
			wantTypes: []string{"url_repetitive_path"},
		},
		{
			name:      "empty URL returns no issues",
			url:       "",
			wantTypes: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectURLIssues(tt.url)

			for _, wantType := range tt.wantTypes {
				if !hasIssue(got, wantType) {
					t.Errorf("expected issue %q not found", wantType)
				}
			}
			for _, absentType := range tt.wantAbsent {
				if hasIssue(got, absentType) {
					t.Errorf("unexpected issue %q found", absentType)
				}
			}
		})
	}
}

func TestIsNonDescriptiveAnchor(t *testing.T) {
	tests := []struct {
		anchor string
		want   bool
	}{
		{"click here", true},
		{"Click Here", true},
		{"  read more  ", true},
		{"here", true},
		{"Our Services", false},
		{"Learn about SEO", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsNonDescriptiveAnchor(tt.anchor); got != tt.want {
			t.Errorf("IsNonDescriptiveAnchor(%q) = %v, want %v", tt.anchor, got, tt.want)
		}
	}
}

func TestURLIssuesIntegratedInPageLocal(t *testing.T) {
	ctx := cleanPage()
	ctx.PageURL = "https://example.com/Blog/my_post"
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 0)
	if !hasIssue(issues, "url_uppercase") {
		t.Error("expected url_uppercase from PageURL")
	}
	if !hasIssue(issues, "url_underscores") {
		t.Error("expected url_underscores from PageURL")
	}
}

// ── Batch A tests ──────────────────────────────────────────────────────

func TestBatchA_TitleSameAsH1(t *testing.T) {
	ctx := cleanPage()
	ctx.Title = "My Page Title"
	ctx.H1s = []string{"My Page Title"}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "title_same_as_h1") {
		t.Error("expected title_same_as_h1 issue")
	}
}

func TestBatchA_TitleSameAsH1_CaseInsensitive(t *testing.T) {
	ctx := cleanPage()
	ctx.Title = "my page title"
	ctx.H1s = []string{"My Page Title"}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "title_same_as_h1") {
		t.Error("expected title_same_as_h1 issue (case-insensitive)")
	}
}

func TestBatchA_MultipleTitleTags(t *testing.T) {
	ctx := cleanPage()
	ctx.TitleCount = 3
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "multiple_title_tags") {
		t.Error("expected multiple_title_tags issue")
	}
}

func TestBatchA_MultipleMetaDescriptions(t *testing.T) {
	ctx := cleanPage()
	ctx.DescriptionCount = 2
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "multiple_meta_descriptions") {
		t.Error("expected multiple_meta_descriptions issue")
	}
}

func TestBatchA_MetaDescriptionOutsideHead(t *testing.T) {
	ctx := cleanPage()
	ctx.MetaDescriptionOutsideHead = true
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "meta_description_outside_head") {
		t.Error("expected meta_description_outside_head issue")
	}
}

func TestBatchA_H1TooLong(t *testing.T) {
	ctx := cleanPage()
	ctx.H1s = []string{strings.Repeat("A", 80)}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "h1_too_long") {
		t.Error("expected h1_too_long issue")
	}
}

func TestBatchA_H1NonSequential(t *testing.T) {
	ctx := cleanPage()
	ctx.FirstHeadingLevel = 2
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "h1_non_sequential") {
		t.Error("expected h1_non_sequential issue")
	}
}

func TestBatchA_H1AltTextOnly(t *testing.T) {
	ctx := cleanPage()
	ctx.H1AltTextOnly = []string{"Company Logo"}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "h1_alt_text_only") {
		t.Error("expected h1_alt_text_only issue")
	}
}

func TestBatchA_MissingH2(t *testing.T) {
	ctx := cleanPage()
	ctx.H2s = []string{}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "missing_h2") {
		t.Error("expected missing_h2 issue")
	}
}

func TestBatchA_H2NonSequential_NoH1(t *testing.T) {
	ctx := cleanPage()
	ctx.H1Count = 0
	ctx.H2s = []string{"Some H2"}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "h2_non_sequential") {
		t.Error("expected h2_non_sequential issue when H2 exists but no H1")
	}
}

func TestBatchA_H2TooLong(t *testing.T) {
	ctx := cleanPage()
	ctx.H2s = []string{strings.Repeat("B", 80)}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "h2_too_long") {
		t.Error("expected h2_too_long issue")
	}
}

func TestBatchA_MultipleCanonicals(t *testing.T) {
	ctx := cleanPage()
	ctx.CanonicalCount = 2
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "multiple_canonicals") {
		t.Error("expected multiple_canonicals issue")
	}
}

func TestBatchA_CanonicalIsRelative(t *testing.T) {
	ctx := cleanPage()
	ctx.CanonicalRaw = "/page"
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "canonical_is_relative") {
		t.Error("expected canonical_is_relative issue")
	}
}

func TestBatchA_CanonicalOutsideHead(t *testing.T) {
	ctx := cleanPage()
	ctx.CanonicalOutsideHead = true
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "canonical_outside_head") {
		t.Error("expected canonical_outside_head issue")
	}
}

// ── Medium: Security Headers ───────────────────────────────────────

func TestMedium_MissingHSTSHeader(t *testing.T) {
	ctx := cleanPage()
	ctx.PageURL = "https://example.com/page"
	ctx.ResponseHeaders = map[string][]string{
		"X-Content-Type-Options":  {"nosniff"},
		"X-Frame-Options":         {"DENY"},
		"Content-Security-Policy": {"default-src 'self'"},
		"Referrer-Policy":         {"strict-origin-when-cross-origin"},
	}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "missing_hsts_header") {
		t.Error("expected missing_hsts_header issue")
	}
}

func TestMedium_HSTSNotRequiredOnHTTP(t *testing.T) {
	ctx := cleanPage()
	ctx.PageURL = "http://example.com/page"
	ctx.ResponseHeaders = map[string][]string{}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if hasIssue(issues, "missing_hsts_header") {
		t.Error("missing_hsts_header should not fire on HTTP pages")
	}
}

func TestMedium_MissingXContentTypeOptions(t *testing.T) {
	ctx := cleanPage()
	delete(ctx.ResponseHeaders, "X-Content-Type-Options")
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "missing_x_content_type_options") {
		t.Error("expected missing_x_content_type_options issue")
	}
}

func TestMedium_MissingXFrameOptions(t *testing.T) {
	ctx := cleanPage()
	delete(ctx.ResponseHeaders, "X-Frame-Options")
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "missing_x_frame_options") {
		t.Error("expected missing_x_frame_options issue")
	}
}

func TestMedium_MissingCSP(t *testing.T) {
	ctx := cleanPage()
	delete(ctx.ResponseHeaders, "Content-Security-Policy")
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "missing_content_security_policy") {
		t.Error("expected missing_content_security_policy issue")
	}
}

func TestMedium_MissingReferrerPolicy(t *testing.T) {
	ctx := cleanPage()
	delete(ctx.ResponseHeaders, "Referrer-Policy")
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "missing_referrer_policy") {
		t.Error("expected missing_referrer_policy issue")
	}
}

func TestMedium_InsecureReferrerPolicy(t *testing.T) {
	ctx := cleanPage()
	ctx.ResponseHeaders["Referrer-Policy"] = []string{"unsafe-url"}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "missing_referrer_policy") {
		t.Error("expected missing_referrer_policy for unsafe-url")
	}
}

func TestMedium_UnsafeCrossOriginLinks(t *testing.T) {
	ctx := cleanPage()
	ctx.UnsafeCrossOriginCount = 3
	ctx.UnsafeCrossOriginExamples = []string{"https://evil.com/page"}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "unsafe_cross_origin_links") {
		t.Error("expected unsafe_cross_origin_links issue")
	}
}

func TestMedium_FormOnHTTP(t *testing.T) {
	ctx := cleanPage()
	ctx.FormInsecureActions = []string{"http://example.com/submit"}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "form_on_http") {
		t.Error("expected form_on_http issue")
	}
}

func TestMedium_ProtocolRelativeURLs(t *testing.T) {
	ctx := cleanPage()
	ctx.ProtocolRelativeCount = 5
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "protocol_relative_urls") {
		t.Error("expected protocol_relative_urls issue")
	}
}

// ── Medium: Hreflang ───────────────────────────────────────────────

func TestMedium_HreflangMissingSelf(t *testing.T) {
	ctx := cleanPage()
	ctx.PageURL = "https://example.com/en"
	ctx.Hreflangs = []parser.HreflangEntry{
		{Lang: "es", URL: "https://example.com/es"},
		{Lang: "fr", URL: "https://example.com/fr"},
	}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "hreflang_missing_self") {
		t.Error("expected hreflang_missing_self issue")
	}
}

func TestMedium_HreflangWithSelf(t *testing.T) {
	ctx := cleanPage()
	ctx.PageURL = "https://example.com/en"
	ctx.Hreflangs = []parser.HreflangEntry{
		{Lang: "en", URL: "https://example.com/en"},
		{Lang: "es", URL: "https://example.com/es"},
	}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if hasIssue(issues, "hreflang_missing_self") {
		t.Error("should not flag hreflang_missing_self when self-reference exists")
	}
}

func TestMedium_HreflangMissingXDefault(t *testing.T) {
	ctx := cleanPage()
	ctx.PageURL = "https://example.com/en"
	ctx.Hreflangs = []parser.HreflangEntry{
		{Lang: "en", URL: "https://example.com/en"},
		{Lang: "es", URL: "https://example.com/es"},
	}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "hreflang_missing_x_default") {
		t.Error("expected hreflang_missing_x_default issue")
	}
}

func TestMedium_HreflangWithXDefault(t *testing.T) {
	ctx := cleanPage()
	ctx.PageURL = "https://example.com/en"
	ctx.Hreflangs = []parser.HreflangEntry{
		{Lang: "en", URL: "https://example.com/en"},
		{Lang: "x-default", URL: "https://example.com/"},
	}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if hasIssue(issues, "hreflang_missing_x_default") {
		t.Error("should not flag hreflang_missing_x_default when x-default exists")
	}
}

func TestMedium_HreflangInvalidLanguageCode(t *testing.T) {
	ctx := cleanPage()
	ctx.PageURL = "https://example.com/en"
	ctx.Hreflangs = []parser.HreflangEntry{
		{Lang: "en", URL: "https://example.com/en"},
		{Lang: "xyz", URL: "https://example.com/xyz"},
		{Lang: "x-default", URL: "https://example.com/"},
	}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "hreflang_invalid_language_code") {
		t.Error("expected hreflang_invalid_language_code issue for 'xyz'")
	}
}

func TestMedium_HreflangValidRegionCode(t *testing.T) {
	ctx := cleanPage()
	ctx.PageURL = "https://example.com/en-us"
	ctx.Hreflangs = []parser.HreflangEntry{
		{Lang: "en-US", URL: "https://example.com/en-us"},
		{Lang: "x-default", URL: "https://example.com/"},
	}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if hasIssue(issues, "hreflang_invalid_language_code") {
		t.Error("en-US should be a valid hreflang code")
	}
}

func TestMedium_HreflangOutsideHead(t *testing.T) {
	ctx := cleanPage()
	ctx.HreflangOutsideHead = true
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "hreflang_outside_head") {
		t.Error("expected hreflang_outside_head issue")
	}
}

// ── Medium: HTML Validation ────────────────────────────────────────

func TestMedium_InvalidHTMLInHead(t *testing.T) {
	ctx := cleanPage()
	ctx.InvalidHTMLInHead = []string{"div", "span"}
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "invalid_html_in_head") {
		t.Error("expected invalid_html_in_head issue")
	}
}

func TestMedium_MultipleHeadTags(t *testing.T) {
	ctx := cleanPage()
	ctx.HeadTagCount = 2
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "multiple_head_tags") {
		t.Error("expected multiple_head_tags issue")
	}
}

func TestMedium_MultipleBodyTags(t *testing.T) {
	ctx := cleanPage()
	ctx.BodyTagCount = 2
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "multiple_body_tags") {
		t.Error("expected multiple_body_tags issue")
	}
}

func TestMedium_HTMLTooLarge(t *testing.T) {
	ctx := cleanPage()
	ctx.BodySize = 16 * 1024 * 1024 // 16MB
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "html_too_large") {
		t.Error("expected html_too_large issue")
	}
}

func TestMedium_HTMLNotTooLarge(t *testing.T) {
	ctx := cleanPage()
	ctx.BodySize = 10 * 1024 * 1024 // 10MB
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if hasIssue(issues, "html_too_large") {
		t.Error("should not flag html_too_large for 10MB")
	}
}

// ── Medium: Content ────────────────────────────────────────────────

func TestMedium_LoremIpsumDetected(t *testing.T) {
	ctx := cleanPage()
	ctx.TextContent = "This is some sample text with Lorem Ipsum dolor sit amet placeholder content."
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "lorem_ipsum_detected") {
		t.Error("expected lorem_ipsum_detected issue")
	}
}

func TestMedium_NoLoremIpsum(t *testing.T) {
	ctx := cleanPage()
	ctx.TextContent = "This is real content about our products and services."
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if hasIssue(issues, "lorem_ipsum_detected") {
		t.Error("should not flag lorem_ipsum_detected for real content")
	}
}

func TestMedium_Soft404_TitleNotFound(t *testing.T) {
	ctx := cleanPage()
	ctx.Title = "Page Not Found"
	ctx.WordCount = 50
	ctx.TextContent = "Sorry, the page you requested could not be located."
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "soft_404") {
		t.Error("expected soft_404 issue for title containing 'not found'")
	}
}

func TestMedium_Soft404_Body404(t *testing.T) {
	ctx := cleanPage()
	ctx.Title = "Error"
	ctx.WordCount = 30
	ctx.TextContent = "Error 404 - the requested resource was not available"
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if !hasIssue(issues, "soft_404") {
		t.Error("expected soft_404 issue for body containing '404'")
	}
}

func TestMedium_Soft404_NotTriggeredForLargePages(t *testing.T) {
	ctx := cleanPage()
	ctx.Title = "Our Blog About 404 Errors"
	ctx.WordCount = 500
	ctx.TextContent = "This article discusses how to handle 404 errors gracefully on your website..."
	issues := DetectPageLocalIssues(ctx, defaultThresholds(), 1)
	if hasIssue(issues, "soft_404") {
		t.Error("should not flag soft_404 for pages with >100 words")
	}
}

// ── Medium: Hreflang code validation helpers ───────────────────────

func TestIsValidHreflangCode(t *testing.T) {
	tests := []struct {
		code  string
		valid bool
	}{
		{"en", true},
		{"en-US", true},
		{"es-MX", true},
		{"zh-CN", true},
		{"xyz", false},
		{"en-USA", false}, // region must be 2 chars
		{"", false},
	}
	for _, tt := range tests {
		got := isValidHreflangCode(tt.code)
		if got != tt.valid {
			t.Errorf("isValidHreflangCode(%q) = %v, want %v", tt.code, got, tt.valid)
		}
	}
}
