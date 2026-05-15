package parser

import (
	"testing"
)

func TestValidateJSONLD_Article_Complete(t *testing.T) {
	raw := `{"@type": "Article", "headline": "Test", "author": "John", "datePublished": "2024-01-01", "image": "img.jpg", "dateModified": "2024-01-02", "publisher": "Pub", "mainEntityOfPage": "https://example.com"}`
	results := ValidateJSONLD(raw)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if !r.Valid {
		t.Error("expected Valid=true")
	}
	if len(r.MissingRequired) != 0 {
		t.Errorf("expected no missing required, got %v", r.MissingRequired)
	}
	if len(r.MissingRecommended) != 0 {
		t.Errorf("expected no missing recommended, got %v", r.MissingRecommended)
	}
}

func TestValidateJSONLD_Article_MissingRequired(t *testing.T) {
	raw := `{"@type": "Article", "author": "John", "datePublished": "2024-01-01"}`
	results := ValidateJSONLD(raw)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Valid {
		t.Error("expected Valid=false")
	}
	// Now missing both headline and image
	if len(r.MissingRequired) != 2 {
		t.Errorf("expected 2 missingRequired, got %v", r.MissingRequired)
	}
}

func TestValidateJSONLD_Product_MissingOffers(t *testing.T) {
	raw := `{"@type": "Product", "name": "Widget"}`
	results := ValidateJSONLD(raw)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if !r.Valid {
		t.Error("expected Valid=true (offers is recommended, not required)")
	}
	if len(r.MissingRequired) != 0 {
		t.Errorf("expected no missing required, got %v", r.MissingRequired)
	}
	// offers should be in recommended
	found := false
	for _, p := range r.MissingRecommended {
		if p == "offers" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'offers' in missingRecommended, got %v", r.MissingRecommended)
	}
}

func TestValidateJSONLD_UnknownType(t *testing.T) {
	raw := `{"@type": "UnknownThing", "name": "Test"}`
	results := ValidateJSONLD(raw)

	if len(results) != 0 {
		t.Errorf("expected 0 results for unknown type, got %d", len(results))
	}
}

func TestValidateJSONLD_NestedObjects(t *testing.T) {
	raw := `{
		"@type": "BlogPosting",
		"headline": "Test Post",
		"author": "Jane",
		"datePublished": "2024-01-01",
		"image": "img.jpg",
		"publisher": {
			"@type": "Organization",
			"name": "My Org"
		}
	}`
	results := ValidateJSONLD(raw)

	// Should validate both BlogPosting and nested Organization.
	typeMap := map[string]bool{}
	for _, r := range results {
		typeMap[r.Type] = true
	}
	if !typeMap["BlogPosting"] {
		t.Error("expected BlogPosting result")
	}
	if !typeMap["Organization"] {
		t.Error("expected Organization result from nested publisher")
	}
}

func TestValidateJSONLD_ArrayOfObjects(t *testing.T) {
	raw := `[
		{"@type": "Article", "headline": "A1", "author": "X", "datePublished": "2024-01-01", "image": "img.jpg"},
		{"@type": "Product", "name": "P1"},
		{"@type": "Person", "name": "John"}
	]`
	results := ValidateJSONLD(raw)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	typeMap := map[string]bool{}
	for _, r := range results {
		typeMap[r.Type] = true
	}
	for _, expected := range []string{"Article", "Product", "Person"} {
		if !typeMap[expected] {
			t.Errorf("expected %s in results", expected)
		}
	}
}

func TestValidateJSONLD_TypeArray(t *testing.T) {
	raw := `{"@type": ["Article", "BlogPosting"], "headline": "Test", "author": "X", "datePublished": "2024-01-01", "image": "img.jpg"}`
	results := ValidateJSONLD(raw)

	if len(results) < 2 {
		t.Fatalf("expected at least 2 results for dual @type, got %d", len(results))
	}

	typeMap := map[string]bool{}
	for _, r := range results {
		typeMap[r.Type] = true
	}
	if !typeMap["Article"] {
		t.Error("expected Article result")
	}
	if !typeMap["BlogPosting"] {
		t.Error("expected BlogPosting result")
	}
}

func TestValidateJSONLD_EmptyInput(t *testing.T) {
	results := ValidateJSONLD("")
	if results != nil {
		t.Errorf("expected nil for empty input, got %v", results)
	}
}

func TestValidateJSONLD_MalformedJSON(t *testing.T) {
	results := ValidateJSONLD("{bad json!!!")
	if results != nil {
		t.Errorf("expected nil for malformed JSON, got %v", results)
	}
}

func TestValidateJSONLD_JSONLDBlockWrapper(t *testing.T) {
	// Simulates the format stored by the engine: []JSONLDBlock
	raw := `[{"raw":"{\"@type\":\"Article\",\"author\":\"X\",\"datePublished\":\"2024\"}","type":"Article","malformed":false}]`
	results := ValidateJSONLD(raw)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Type != "Article" {
		t.Errorf("expected type Article, got %s", r.Type)
	}
	// headline and image are missing
	if r.Valid {
		t.Error("expected Valid=false (missing headline and image)")
	}
	foundHeadline := false
	foundImage := false
	for _, p := range r.MissingRequired {
		if p == "headline" {
			foundHeadline = true
		}
		if p == "image" {
			foundImage = true
		}
	}
	if !foundHeadline {
		t.Errorf("expected headline in missingRequired, got %v", r.MissingRequired)
	}
	if !foundImage {
		t.Errorf("expected image in missingRequired, got %v", r.MissingRequired)
	}
}

func TestValidateJSONLD_GraphObject(t *testing.T) {
	raw := `{
		"@context": "https://schema.org",
		"@graph": [
			{"@type": "WebSite", "name": "Test", "url": "https://example.com"},
			{"@type": "Organization", "name": "Org"}
		]
	}`
	results := ValidateJSONLD(raw)

	typeMap := map[string]bool{}
	for _, r := range results {
		typeMap[r.Type] = true
	}
	if !typeMap["WebSite"] {
		t.Error("expected WebSite result from @graph")
	}
	if !typeMap["Organization"] {
		t.Error("expected Organization result from @graph")
	}
}

func TestValidateJSONLD_MalformedBlockSkipped(t *testing.T) {
	// JSONLDBlock with malformed=true should be skipped.
	raw := `[{"raw":"not json","type":"","malformed":true}]`
	results := ValidateJSONLD(raw)

	if len(results) != 0 {
		t.Errorf("expected 0 results for malformed block, got %d", len(results))
	}
}

func TestValidateJSONLD_EmptyStringCountsAsMissing(t *testing.T) {
	raw := `{"@type": "Article", "headline": "", "author": "X", "datePublished": "2024-01-01"}`
	results := ValidateJSONLD(raw)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Valid {
		t.Error("expected Valid=false for empty headline")
	}
	found := false
	for _, p := range r.MissingRequired {
		if p == "headline" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected headline in missingRequired, got %v", r.MissingRequired)
	}
}

func TestValidateJSONLD_NullValueCountsAsMissing(t *testing.T) {
	raw := `{"@type": "Article", "headline": null, "author": "X", "datePublished": "2024-01-01"}`
	results := ValidateJSONLD(raw)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Valid {
		t.Error("expected Valid=false for null headline")
	}
	found := false
	for _, p := range r.MissingRequired {
		if p == "headline" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected headline in missingRequired, got %v", r.MissingRequired)
	}
}

func TestValidateJSONLD_NestedObjectsMarkedNested(t *testing.T) {
	raw := `{
		"@type": "BlogPosting",
		"headline": "Test",
		"author": "Jane",
		"datePublished": "2024-01-01",
		"image": "img.jpg",
		"publisher": {
			"@type": "Organization",
			"name": "My Org"
		}
	}`
	results := ValidateJSONLD(raw)

	for _, r := range results {
		if r.Type == "BlogPosting" && r.Nested {
			t.Error("expected BlogPosting Nested=false (top-level)")
		}
		if r.Type == "Organization" && !r.Nested {
			t.Error("expected Organization Nested=true")
		}
	}
}

func TestValidateJSONLD_LargeGraphCappedAt50(t *testing.T) {
	// Build a @graph with 100 Organization objects.
	raw := `{"@graph": [`
	for i := 0; i < 100; i++ {
		if i > 0 {
			raw += ","
		}
		raw += `{"@type": "Organization", "name": "Org"}`
	}
	raw += `]}`

	results := ValidateJSONLD(raw)

	if len(results) > 50 {
		t.Errorf("expected at most 50 results, got %d", len(results))
	}
}

func TestValidateJSONLD_EmptyArrayCountsAsMissing(t *testing.T) {
	raw := `{"@type": "Product", "name": []}`
	results := ValidateJSONLD(raw)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Valid {
		t.Error("expected Valid=false for empty array name")
	}
}

func TestValidateJSONLD_EmptyObjectCountsAsMissing(t *testing.T) {
	raw := `{"@type": "Product", "name": {}}`
	results := ValidateJSONLD(raw)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Valid {
		t.Error("expected Valid=false for empty object name")
	}
}

func TestOnlyGoogleRichResultTypesHaveRequired(t *testing.T) {
	googleRichResultTypes := map[string]bool{
		"Article": true, "BlogPosting": true, "NewsArticle": true,
		"Product": true, "LocalBusiness": true, "BreadcrumbList": true,
		"FAQPage": true, "HowTo": true, "Event": true, "Recipe": true,
		"Review": true, "SoftwareApplication": true, "VideoObject": true,
		"Offer": true, "AggregateRating": true, "ItemList": true,
	}

	for typeName, rule := range schemaRules {
		if len(rule.Required) > 0 && !googleRichResultTypes[typeName] {
			t.Errorf("type %q has Required properties but is not a Google Rich Result type — move properties to Recommended", typeName)
		}
		if len(rule.Required) > 0 && rule.Source != "google_rich_results" {
			t.Errorf("type %q has Required properties but Source is %q, expected \"google_rich_results\"", typeName, rule.Source)
		}
	}
}

func TestValidationResultIncludesSource(t *testing.T) {
	// Google Rich Result type should have source populated
	raw := `{"@type": "Article", "headline": "Test", "author": "X", "datePublished": "2024", "image": "img.jpg"}`
	results := ValidateJSONLD(raw)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Source != "google_rich_results" {
		t.Errorf("expected source google_rich_results, got %q", results[0].Source)
	}
	if results[0].GoogleDocURL == "" {
		t.Error("expected GoogleDocURL to be set for Article")
	}

	// Schema.org best practice type
	raw2 := `{"@type": "Organization", "name": "Org"}`
	results2 := ValidateJSONLD(raw2)
	if len(results2) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results2))
	}
	if results2[0].Source != "schema_org_best_practice" {
		t.Errorf("expected source schema_org_best_practice, got %q", results2[0].Source)
	}
	if results2[0].GoogleDocURL != "" {
		t.Errorf("expected empty GoogleDocURL for Organization, got %q", results2[0].GoogleDocURL)
	}
}
