package issues

import (
	"encoding/json"
	"testing"
)

// allKnownIssueTypes lists every issue type string emitted by page-local
// detectors, global detectors, and the crawl engine.
// Keep this in sync when adding new issue types.
var allKnownIssueTypes = []string{
	// Page-local (DetectPageLocalIssues)
	"status_4xx",
	"status_5xx",
	"redirect_chain",
	"redirect_loop",
	"redirect_hops_exceeded",
	"missing_title",
	"title_too_long",
	"title_too_short",
	"missing_description",
	"description_too_long",
	"description_too_short",
	"missing_canonical",
	"missing_h1",
	"multiple_h1",
	"missing_og_title",
	"missing_og_description",
	"missing_og_image",
	"missing_structured_data",
	"malformed_structured_data",
	"invalid_structured_data",
	"incomplete_structured_data",
	"thin_content",
	"missing_alt_attribute",
	"empty_alt_attribute",
	"mixed_content",
	"very_slow_response",
	"slow_response",
	"deep_page",
	"robots_meta_header_mismatch",
	"js_suspect_not_rendered",
	"title_outside_head",
	"meta_robots_outside_head",
	"title_same_as_h1",
	"multiple_title_tags",
	"multiple_meta_descriptions",
	"meta_description_outside_head",
	"h1_too_long",
	"h1_non_sequential",
	"h1_alt_text_only",
	"missing_h2",
	"h2_non_sequential",
	"h2_too_long",
	"multiple_canonicals",
	"canonical_is_relative",
	"canonical_outside_head",

	// Batch B: Image issues (page-local + global)
	"image_over_100kb",
	"alt_text_too_long",
	"missing_image_size_attributes",

	// Batch B: Link issues (page-local + global)
	"no_internal_outlinks",
	"non_descriptive_anchor_text",
	"internal_nofollow_outlink",

	// Batch B: URL issues (page-local)
	"url_uppercase",
	"url_underscores",
	"url_contains_space",
	"url_has_parameters",
	"url_too_long",
	"url_multiple_slashes",
	"url_repetitive_path",

	// Global (DetectGlobalIssues)
	"duplicate_title",
	"duplicate_description",
	"duplicate_content",
	"orphan_page",
	"hreflang_not_reciprocal",
	"broken_hreflang_target",
	"canonical_to_non_200",
	"canonical_chain",
	"canonical_to_redirect",
	"broken_pagination_chain",
	"pagination_canonical_mismatch",
	"sitemap_non_200",
	"crawled_not_in_sitemap",
	"in_sitemap_not_crawled",
	"in_sitemap_robots_blocked",
	"http_to_https_missing",
	"js_only_navigation",
	"duplicate_h1",
	"duplicate_h2",
	"non_indexable_canonical",
	"unlinked_canonical",

	// Medium: Security Headers
	"missing_hsts_header",
	"missing_x_content_type_options",
	"missing_x_frame_options",
	"missing_content_security_policy",
	"missing_referrer_policy",
	"unsafe_cross_origin_links",
	"form_on_http",
	"protocol_relative_urls",

	// Medium: Hreflang
	"hreflang_missing_self",
	"hreflang_missing_x_default",
	"hreflang_invalid_language_code",
	"hreflang_outside_head",

	// Medium: Sitemap
	"non_indexable_in_sitemap",
	"url_in_multiple_sitemaps",
	"sitemap_too_large",

	// Medium: HTML Validation
	"invalid_html_in_head",
	"multiple_head_tags",
	"multiple_body_tags",
	"html_too_large",

	// Medium: Content
	"lorem_ipsum_detected",
	"soft_404",

	// Engine-level
	"crawl_trap_suspected",
	"rate_limited",
	"slow_host",
}

func TestAllIssueTypesHaveExplanations(t *testing.T) {
	for _, issueType := range allKnownIssueTypes {
		exp, ok := Explanations[issueType]
		if !ok {
			t.Errorf("issue type %q has no entry in Explanations map", issueType)
			continue
		}
		if exp.Title == "" {
			t.Errorf("Explanations[%q].Title is empty", issueType)
		}
		if exp.Description == "" {
			t.Errorf("Explanations[%q].Description is empty", issueType)
		}
		if exp.Impact == "" {
			t.Errorf("Explanations[%q].Impact is empty", issueType)
		}
		if exp.Fix == "" {
			t.Errorf("Explanations[%q].Fix is empty", issueType)
		}
	}
}

func TestExplanationsMapHasNoExtras(t *testing.T) {
	known := make(map[string]bool, len(allKnownIssueTypes))
	for _, it := range allKnownIssueTypes {
		known[it] = true
	}
	for key := range Explanations {
		if !known[key] {
			t.Errorf("Explanations map contains unknown issue type %q (not in allKnownIssueTypes)", key)
		}
	}
}

func TestExplanationsJSON(t *testing.T) {
	jsonStr := ExplanationsJSON()
	if jsonStr == "" || jsonStr == "{}" {
		t.Fatal("ExplanationsJSON() returned empty or {}")
	}

	var parsed map[string]IssueExplanation
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("ExplanationsJSON() is not valid JSON: %v", err)
	}

	if len(parsed) != len(Explanations) {
		t.Errorf("ExplanationsJSON() has %d entries, Explanations map has %d", len(parsed), len(Explanations))
	}
}
