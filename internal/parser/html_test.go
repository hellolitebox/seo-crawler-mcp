package parser

import (
	"net/http"
	"os"
	"testing"
)

func loadTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatalf("loading testdata %q: %v", name, err)
	}
	return data
}

func TestParseBasicPage(t *testing.T) {
	body := loadTestdata(t, "basic-page.html")
	headers := http.Header{}

	r, err := ParseHTML(body, "https://example.com/page", headers)
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}

	// Title
	if r.Title != "Test Page Title" {
		t.Errorf("Title = %q, want %q", r.Title, "Test Page Title")
	}
	if r.TitleLength != 15 {
		t.Errorf("TitleLength = %d, want 15", r.TitleLength)
	}

	// Meta description
	if r.MetaDescription != "A test page description for SEO testing" {
		t.Errorf("MetaDescription = %q", r.MetaDescription)
	}

	// Meta robots
	if r.MetaRobots != "index, follow" {
		t.Errorf("MetaRobots = %q", r.MetaRobots)
	}

	// Indexability
	if r.IndexabilityState != "indexable" {
		t.Errorf("IndexabilityState = %q, want indexable", r.IndexabilityState)
	}

	// Canonical
	if r.CanonicalResolved != "https://example.com/page" {
		t.Errorf("CanonicalResolved = %q", r.CanonicalResolved)
	}
	if r.CanonicalType != "self" {
		t.Errorf("CanonicalType = %q, want self", r.CanonicalType)
	}

	// Rel next/prev
	if r.RelNext == nil || r.RelNext.Resolved != "https://example.com/page2" {
		t.Errorf("RelNext = %v", r.RelNext)
	}
	if r.RelPrev == nil || r.RelPrev.Resolved != "https://example.com/page0" {
		t.Errorf("RelPrev = %v", r.RelPrev)
	}

	// Hreflang
	if len(r.Hreflangs) != 2 {
		t.Errorf("Hreflangs count = %d, want 2", len(r.Hreflangs))
	}

	// Headings
	if len(r.Headings.H1) != 1 || r.Headings.H1[0] != "Main Heading" {
		t.Errorf("H1 = %v", r.Headings.H1)
	}
	if len(r.Headings.H2) != 1 || r.Headings.H2[0] != "Sub Heading" {
		t.Errorf("H2 = %v", r.Headings.H2)
	}

	// OG tags
	if r.OpenGraph.Title != "OG Test Title" {
		t.Errorf("OG Title = %q", r.OpenGraph.Title)
	}

	// Twitter
	if r.TwitterCard.Card != "summary_large_image" {
		t.Errorf("Twitter Card = %q", r.TwitterCard.Card)
	}
	if r.TwitterCard.Title != "Twitter Title" {
		t.Errorf("Twitter Title = %q", r.TwitterCard.Title)
	}

	// JSON-LD
	if len(r.JSONLDBlocks) != 1 {
		t.Errorf("JSONLDBlocks count = %d, want 1", len(r.JSONLDBlocks))
	}
	if len(r.JSONLDTypes) != 1 || r.JSONLDTypes[0] != "WebPage" {
		t.Errorf("JSONLDTypes = %v", r.JSONLDTypes)
	}

	// Links (should have nav1, about, external, footer-link = 4)
	if len(r.Links) < 3 {
		t.Errorf("Links count = %d, want >= 3", len(r.Links))
	}

	// Images
	if len(r.Images) != 3 {
		t.Errorf("Images count = %d, want 3", len(r.Images))
	}

	// Word count should be > 0
	if r.ExtractedWordCount < 10 {
		t.Errorf("ExtractedWordCount = %d, too low", r.ExtractedWordCount)
	}

	// Content hash should be set
	if r.ContentHash == "" {
		t.Error("ContentHash is empty")
	}

	// Not JS suspect (has content)
	if r.JSSuspect {
		t.Error("JSSuspect should be false for basic page")
	}
}

func TestParseMissingMeta(t *testing.T) {
	body := loadTestdata(t, "missing-meta.html")
	headers := http.Header{}

	r, err := ParseHTML(body, "https://example.com/minimal", headers)
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}

	if r.Title != "" {
		t.Errorf("Title = %q, want empty", r.Title)
	}
	if r.MetaDescription != "" {
		t.Errorf("MetaDescription = %q, want empty", r.MetaDescription)
	}
	if r.CanonicalType != "absent" {
		t.Errorf("CanonicalType = %q, want absent", r.CanonicalType)
	}
	if r.IndexabilityState != "indexable" {
		t.Errorf("IndexabilityState = %q, want indexable", r.IndexabilityState)
	}
}

func TestParseSPAStub(t *testing.T) {
	body := loadTestdata(t, "spa-stub.html")
	headers := http.Header{}

	r, err := ParseHTML(body, "https://example.com/app", headers)
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}

	if !r.JSSuspect {
		t.Error("JSSuspect should be true for SPA stub")
	}
	if !r.HasSPARoot {
		t.Error("HasSPARoot should be true")
	}
	if r.ScriptCount != 6 {
		t.Errorf("ScriptCount = %d, want 6", r.ScriptCount)
	}
	if r.ExtractedWordCount >= 50 {
		t.Errorf("ExtractedWordCount = %d, should be < 50", r.ExtractedWordCount)
	}
}

func TestCanonicalType(t *testing.T) {
	tests := []struct {
		name      string
		html      string
		pageURL   string
		wantType  string
	}{
		{
			name:     "self",
			html:     `<html><head><link rel="canonical" href="https://example.com/page"></head><body></body></html>`,
			pageURL:  "https://example.com/page",
			wantType: "self",
		},
		{
			name:     "cross",
			html:     `<html><head><link rel="canonical" href="https://example.com/other"></head><body></body></html>`,
			pageURL:  "https://example.com/page",
			wantType: "cross",
		},
		{
			name:     "absent",
			html:     `<html><head></head><body></body></html>`,
			pageURL:  "https://example.com/page",
			wantType: "absent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := ParseHTML([]byte(tt.html), tt.pageURL, http.Header{})
			if err != nil {
				t.Fatalf("ParseHTML error: %v", err)
			}
			if r.CanonicalType != tt.wantType {
				t.Errorf("CanonicalType = %q, want %q", r.CanonicalType, tt.wantType)
			}
		})
	}
}

func TestJSONLDExtraction(t *testing.T) {
	html := `<html><head>
		<script type="application/ld+json">{"@type": "Organization", "name": "Test"}</script>
		<script type="application/ld+json">{"@type": "WebSite", "url": "https://example.com"}</script>
	</head><body></body></html>`

	r, err := ParseHTML([]byte(html), "https://example.com", http.Header{})
	if err != nil {
		t.Fatal(err)
	}

	if len(r.JSONLDBlocks) != 2 {
		t.Errorf("JSONLDBlocks = %d, want 2", len(r.JSONLDBlocks))
	}
	if len(r.JSONLDTypes) != 2 {
		t.Errorf("JSONLDTypes = %d, want 2", len(r.JSONLDTypes))
	}
	if r.JSONLDTypes[0] != "Organization" {
		t.Errorf("type[0] = %q", r.JSONLDTypes[0])
	}
	if r.JSONLDTypes[1] != "WebSite" {
		t.Errorf("type[1] = %q", r.JSONLDTypes[1])
	}
}

func TestJSONLDMalformed(t *testing.T) {
	html := `<html><head>
		<script type="application/ld+json">{invalid json}</script>
	</head><body></body></html>`

	r, err := ParseHTML([]byte(html), "https://example.com", http.Header{})
	if err != nil {
		t.Fatal(err)
	}

	if len(r.JSONLDBlocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(r.JSONLDBlocks))
	}
	if !r.JSONLDBlocks[0].Malformed {
		t.Error("expected malformed=true")
	}
}

func TestHreflangExtraction(t *testing.T) {
	html := `<html><head>
		<link rel="alternate" hreflang="en" href="https://example.com/en">
		<link rel="alternate" hreflang="es" href="https://example.com/es">
		<link rel="alternate" hreflang="x-default" href="https://example.com/">
	</head><body></body></html>`

	r, err := ParseHTML([]byte(html), "https://example.com/en", http.Header{})
	if err != nil {
		t.Fatal(err)
	}

	if len(r.Hreflangs) != 3 {
		t.Fatalf("Hreflangs = %d, want 3", len(r.Hreflangs))
	}
	if r.Hreflangs[0].Lang != "en" {
		t.Errorf("lang[0] = %q", r.Hreflangs[0].Lang)
	}
	if r.Hreflangs[2].Lang != "x-default" {
		t.Errorf("lang[2] = %q", r.Hreflangs[2].Lang)
	}
}

func TestNoindexMeta(t *testing.T) {
	html := `<html><head><meta name="robots" content="noindex, follow"></head><body></body></html>`
	r, err := ParseHTML([]byte(html), "https://example.com", http.Header{})
	if err != nil {
		t.Fatal(err)
	}
	if r.IndexabilityState != "noindex_meta" {
		t.Errorf("IndexabilityState = %q, want noindex_meta", r.IndexabilityState)
	}
}

func TestNoindexHeader(t *testing.T) {
	html := `<html><head></head><body></body></html>`
	headers := http.Header{"X-Robots-Tag": []string{"noindex"}}
	r, err := ParseHTML([]byte(html), "https://example.com", headers)
	if err != nil {
		t.Fatal(err)
	}
	if r.IndexabilityState != "noindex_header" {
		t.Errorf("IndexabilityState = %q, want noindex_header", r.IndexabilityState)
	}
}

func TestTitleOutsideHead(t *testing.T) {
	html := []byte(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body>
<title>Wrong Place Title</title>
<p>Hello</p>
</body>
</html>`)
	r, err := ParseHTML(html, "https://example.com/", http.Header{})
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if !r.TitleOutsideHead {
		t.Error("TitleOutsideHead = false, want true")
	}
}

func TestTitleInsideHead(t *testing.T) {
	html := []byte(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>Correct Title</title>
</head>
<body><p>Hello</p></body>
</html>`)
	r, err := ParseHTML(html, "https://example.com/", http.Header{})
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if r.TitleOutsideHead {
		t.Error("TitleOutsideHead = true, want false")
	}
}

func TestAssetExtraction(t *testing.T) {
	html := []byte(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>Asset Test</title>
<script src="/js/app.js"></script>
<script src="/js/vendor.js"></script>
<link rel="stylesheet" href="/css/main.css">
<link rel="stylesheet" href="/css/theme.css">
<link rel="preload" href="/fonts/roboto.woff2" as="font">
<link rel="preload" href="/js/chunk.js" as="script">
<link rel="icon" href="/favicon.ico">
<link rel="preconnect" href="https://fonts.googleapis.com">
</head>
<body>
<video src="/video/intro.mp4"></video>
<audio><source src="/audio/bg.mp3"></audio>
<script src="data:text/javascript,void(0)"></script>
</body>
</html>`)

	r, err := ParseHTML(html, "https://example.com/page", http.Header{})
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}

	// Count by type
	counts := map[string]int{}
	for _, a := range r.Assets {
		counts[a.Type]++
	}

	if counts["script"] != 2 {
		t.Errorf("scripts = %d, want 2 (data: URL should be skipped)", counts["script"])
	}
	if counts["stylesheet"] != 2 {
		t.Errorf("stylesheets = %d, want 2", counts["stylesheet"])
	}
	if counts["font"] != 1 {
		t.Errorf("fonts = %d, want 1", counts["font"])
	}
	if counts["preload"] != 1 {
		t.Errorf("preloads = %d, want 1", counts["preload"])
	}
	if counts["icon"] != 1 {
		t.Errorf("icons = %d, want 1", counts["icon"])
	}
	if counts["video"] != 1 {
		t.Errorf("videos = %d, want 1", counts["video"])
	}
	if counts["audio"] != 1 {
		t.Errorf("audios = %d, want 1", counts["audio"])
	}

	// preconnect should NOT appear
	for _, a := range r.Assets {
		if a.URL == "https://fonts.googleapis.com" {
			t.Error("preconnect URL should have been skipped")
		}
	}

	// Verify URL resolution
	for _, a := range r.Assets {
		if a.Type == "script" && a.URL == "https://example.com/js/app.js" {
			return // found resolved URL
		}
	}
	t.Error("expected script URL to be resolved to absolute")
}

func TestMetaRobotsOutsideHead(t *testing.T) {
	html := []byte(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"><title>Test</title></head>
<body>
<meta name="robots" content="noindex">
<p>Hello</p>
</body>
</html>`)
	r, err := ParseHTML(html, "https://example.com/", http.Header{})
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if !r.MetaRobotsOutsideHead {
		t.Error("MetaRobotsOutsideHead = false, want true")
	}
}

func TestBatchA_MultipleTitles(t *testing.T) {
	html := []byte(`<!DOCTYPE html>
<html>
<head><title>First</title><title>Second</title></head>
<body><p>Hello</p></body>
</html>`)
	r, err := ParseHTML(html, "https://example.com/", http.Header{})
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if r.TitleCount != 2 {
		t.Errorf("TitleCount = %d, want 2", r.TitleCount)
	}
}

func TestBatchA_MultipleMetaDescriptions(t *testing.T) {
	html := []byte(`<!DOCTYPE html>
<html>
<head>
  <title>Test</title>
  <meta name="description" content="First desc">
  <meta name="description" content="Second desc">
</head>
<body><p>Hello</p></body>
</html>`)
	r, err := ParseHTML(html, "https://example.com/", http.Header{})
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if r.DescriptionCount != 2 {
		t.Errorf("DescriptionCount = %d, want 2", r.DescriptionCount)
	}
}

func TestBatchA_MetaDescriptionOutsideHead(t *testing.T) {
	html := []byte(`<!DOCTYPE html>
<html>
<head><title>Test</title></head>
<body>
<meta name="description" content="Oops in body">
<p>Hello</p>
</body>
</html>`)
	r, err := ParseHTML(html, "https://example.com/", http.Header{})
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if !r.MetaDescriptionOutsideHead {
		t.Error("MetaDescriptionOutsideHead = false, want true")
	}
}

func TestBatchA_FirstHeadingLevel(t *testing.T) {
	html := []byte(`<!DOCTYPE html>
<html>
<head><title>Test</title></head>
<body>
<h2>First heading is H2</h2>
<h1>Then H1</h1>
</body>
</html>`)
	r, err := ParseHTML(html, "https://example.com/", http.Header{})
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if r.FirstHeadingLevel != 2 {
		t.Errorf("FirstHeadingLevel = %d, want 2", r.FirstHeadingLevel)
	}
}

func TestBatchA_FirstHeadingLevel_H1First(t *testing.T) {
	html := []byte(`<!DOCTYPE html>
<html>
<head><title>Test</title></head>
<body>
<h1>Proper H1</h1>
<h2>Sub heading</h2>
</body>
</html>`)
	r, err := ParseHTML(html, "https://example.com/", http.Header{})
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if r.FirstHeadingLevel != 1 {
		t.Errorf("FirstHeadingLevel = %d, want 1", r.FirstHeadingLevel)
	}
}

func TestBatchA_H1AltTextOnly(t *testing.T) {
	html := []byte(`<!DOCTYPE html>
<html>
<head><title>Test</title></head>
<body>
<h1><img src="logo.png" alt="Company Logo"></h1>
<h1>Normal H1 Text</h1>
</body>
</html>`)
	r, err := ParseHTML(html, "https://example.com/", http.Header{})
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if len(r.H1AltTextOnly) != 1 {
		t.Fatalf("H1AltTextOnly length = %d, want 1", len(r.H1AltTextOnly))
	}
	if r.H1AltTextOnly[0] != "Company Logo" {
		t.Errorf("H1AltTextOnly[0] = %q, want %q", r.H1AltTextOnly[0], "Company Logo")
	}
}

func TestBatchA_MultipleCanonicals(t *testing.T) {
	html := []byte(`<!DOCTYPE html>
<html>
<head>
  <title>Test</title>
  <link rel="canonical" href="https://example.com/a">
  <link rel="canonical" href="https://example.com/b">
</head>
<body><p>Hello</p></body>
</html>`)
	r, err := ParseHTML(html, "https://example.com/", http.Header{})
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if r.CanonicalCount != 2 {
		t.Errorf("CanonicalCount = %d, want 2", r.CanonicalCount)
	}
}

func TestBatchA_CanonicalOutsideHead(t *testing.T) {
	html := []byte(`<!DOCTYPE html>
<html>
<head><title>Test</title></head>
<body>
<link rel="canonical" href="https://example.com/page">
<p>Hello</p>
</body>
</html>`)
	r, err := ParseHTML(html, "https://example.com/", http.Header{})
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if !r.CanonicalOutsideHead {
		t.Error("CanonicalOutsideHead = false, want true")
	}
}

func TestParseHTML_ImageSizeAttributes(t *testing.T) {
	html := []byte(`<!DOCTYPE html>
<html><head><title>Test</title></head>
<body>
	<img src="with-both.png" alt="both" width="100" height="200">
	<img src="with-width.png" alt="width only" width="100">
	<img src="with-height.png" alt="height only" height="200">
	<img src="no-size.png" alt="no size">
</body></html>`)
	r, err := ParseHTML(html, "https://example.com/", http.Header{})
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if len(r.Images) != 4 {
		t.Fatalf("Images count = %d, want 4", len(r.Images))
	}

	tests := []struct {
		idx       int
		wantW     bool
		wantH     bool
	}{
		{0, true, true},   // both
		{1, true, false},  // width only
		{2, false, true},  // height only
		{3, false, false}, // neither
	}
	for _, tt := range tests {
		img := r.Images[tt.idx]
		if img.HasWidth != tt.wantW {
			t.Errorf("Image[%d] HasWidth = %v, want %v", tt.idx, img.HasWidth, tt.wantW)
		}
		if img.HasHeight != tt.wantH {
			t.Errorf("Image[%d] HasHeight = %v, want %v", tt.idx, img.HasHeight, tt.wantH)
		}
	}
}
