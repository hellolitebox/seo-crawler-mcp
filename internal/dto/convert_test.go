package dto

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

func testLookup(id int64) string {
	urls := map[int64]string{
		1: "https://example.com/",
		2: "https://example.com/about",
		3: "https://example.com/contact",
	}
	if u, ok := urls[id]; ok {
		return u
	}
	return ""
}

func TestPageFromStorage_ValidFields(t *testing.T) {
	p := storage.Page{
		ID:                1,
		URLID:             1,
		Depth:             2,
		Title:             sql.NullString{String: "Hello", Valid: true},
		TitleLength:       sql.NullInt64{Int64: 5, Valid: true},
		MetaDescription:   sql.NullString{String: "A desc", Valid: true},
		IndexabilityState: "indexable",
		JSSuspect:         true,
		WordCount:         sql.NullInt64{Int64: 100, Valid: true},
		TextPreview:       sql.NullString{String: "Visible content", Valid: true},
	}

	dto := PageFromStorage(p, testLookup)

	if dto.URL != "https://example.com/" {
		t.Errorf("URL = %q, want %q", dto.URL, "https://example.com/")
	}
	if dto.Title == nil || *dto.Title != "Hello" {
		t.Errorf("Title = %v, want Hello", dto.Title)
	}
	if dto.TitleLength == nil || *dto.TitleLength != 5 {
		t.Errorf("TitleLength = %v, want 5", dto.TitleLength)
	}
	if !dto.JSSuspect {
		t.Error("JSSuspect should be true")
	}
	if dto.WordCount == nil || *dto.WordCount != 100 {
		t.Errorf("WordCount = %v, want 100", dto.WordCount)
	}
	if dto.TextPreview == nil || *dto.TextPreview != "Visible content" {
		t.Errorf("TextPreview = %v, want Visible content", dto.TextPreview)
	}
}

func TestPageFromStorage_NilFields(t *testing.T) {
	p := storage.Page{
		ID:                1,
		URLID:             1,
		IndexabilityState: "indexable",
	}

	dto := PageFromStorage(p, testLookup)

	if dto.Title != nil {
		t.Errorf("Title should be nil, got %v", dto.Title)
	}
	if dto.WordCount != nil {
		t.Errorf("WordCount should be nil, got %v", dto.WordCount)
	}
	if dto.CanonicalIsSelf != nil {
		t.Errorf("CanonicalIsSelf should be nil, got %v", dto.CanonicalIsSelf)
	}
}

func TestPageFromStorage_JSONOmitsNil(t *testing.T) {
	p := storage.Page{
		ID:                1,
		URLID:             1,
		IndexabilityState: "indexable",
	}

	dto := PageFromStorage(p, testLookup)
	data, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	// Nil fields should be omitted.
	if _, ok := m["title"]; ok {
		t.Error("title should be omitted from JSON")
	}
	if _, ok := m["wordCount"]; ok {
		t.Error("wordCount should be omitted from JSON")
	}

	// Required fields should be present.
	if _, ok := m["indexabilityState"]; !ok {
		t.Error("indexabilityState should be in JSON")
	}
}

func TestEdgeFromStorage(t *testing.T) {
	e := storage.Edge{
		ID:                    10,
		SourceURLID:           1,
		NormalizedTargetURLID: sql.NullInt64{Int64: 2, Valid: true},
		SourceKind:            "anchor",
		RelationType:          "hyperlink",
		DiscoveryMode:         "html",
		IsInternal:            true,
		DeclaredTargetURL:     "https://example.com/about",
		AnchorText:            sql.NullString{String: "About", Valid: true},
	}

	dto := EdgeFromStorage(e, testLookup)

	if dto.SourceURL != "https://example.com/" {
		t.Errorf("SourceURL = %q", dto.SourceURL)
	}
	if dto.TargetURL == nil || *dto.TargetURL != "https://example.com/about" {
		t.Error("TargetURL mismatch")
	}
	if dto.AnchorText == nil || *dto.AnchorText != "About" {
		t.Error("AnchorText mismatch")
	}
}

func TestIssueFromStorage(t *testing.T) {
	i := storage.Issue{
		ID:        5,
		URLID:     sql.NullInt64{Int64: 2, Valid: true},
		IssueType: "missing_title",
		Severity:  "warning",
		Scope:     "page",
	}

	dto := IssueFromStorage(i, testLookup)

	if dto.URL == nil || *dto.URL != "https://example.com/about" {
		t.Errorf("URL = %v", dto.URL)
	}
}

func TestFetchFromStorage(t *testing.T) {
	f := storage.Fetch{
		ID:             20,
		FetchSeq:       1,
		RequestedURLID: 1,
		StatusCode:     sql.NullInt64{Int64: 200, Valid: true},
		HTTPMethod:     "GET",
		FetchKind:      "page",
		RenderMode:     "static",
		FetchedAt:      "2026-01-01T00:00:00Z",
	}

	dto := FetchFromStorage(f, testLookup)

	if dto.StatusCode == nil || *dto.StatusCode != 200 {
		t.Errorf("StatusCode = %v, want 200", dto.StatusCode)
	}
}

func TestCrawlSummaryFromStorage(t *testing.T) {
	j := storage.CrawlJob{
		ID:             "job-1",
		Status:         "completed",
		PagesCrawled:   50,
		URLsDiscovered: 100,
		IssuesFound:    5,
		StartedAt:      sql.NullString{String: "2026-01-01T00:00:00Z", Valid: true},
		FinishedAt:     sql.NullString{String: "2026-01-01T01:00:00Z", Valid: true},
	}

	dto := CrawlSummaryFromStorage(j)

	if dto.PagesCrawled != 50 {
		t.Errorf("PagesCrawled = %d, want 50", dto.PagesCrawled)
	}
	if dto.StartedAt == nil || *dto.StartedAt != "2026-01-01T00:00:00Z" {
		t.Error("StartedAt mismatch")
	}
}
