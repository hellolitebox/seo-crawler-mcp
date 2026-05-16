package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
	gomcp "github.com/mark3labs/mcp-go/mcp"
)

func readResource(t *testing.T, s *Server, uri string) []gomcp.ResourceContents {
	t.Helper()
	req := gomcp.ReadResourceRequest{}
	req.Params.URI = uri
	// Determine which handler to call based on URI pattern
	var contents []gomcp.ResourceContents
	var err error

	switch {
	case uri == "seo-crawler://jobs":
		contents, err = s.handleJobListResource(context.Background(), req)
	case strings.Contains(uri, "/page/"):
		contents, err = s.handlePageDetailResource(context.Background(), req)
	case len(uri) > len("seo-crawler://jobs/") && !containsSuffix(uri):
		contents, err = s.handleJobDetailResource(context.Background(), req)
	case hasSuffix(uri, "/summary"):
		contents, err = s.handleJobSummaryResource(context.Background(), req)
	case hasSuffix(uri, "/events"):
		contents, err = s.handleJobEventsResource(context.Background(), req)
	default:
		t.Fatalf("unknown resource URI: %s", uri)
	}
	if err != nil {
		t.Fatalf("reading resource %q: %v", uri, err)
	}
	return contents
}

func containsSuffix(uri string) bool {
	return hasSuffix(uri, "/summary") || hasSuffix(uri, "/events")
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func textFromContents(t *testing.T, contents []gomcp.ResourceContents) string {
	t.Helper()
	if len(contents) == 0 {
		t.Fatal("expected at least one resource content")
	}
	tc, ok := contents[0].(gomcp.TextResourceContents)
	if !ok {
		t.Fatalf("expected TextResourceContents, got %T", contents[0])
	}
	return tc.Text
}

func TestResourcesReturnErrorWithoutDatabase(t *testing.T) {
	s := NewServer(ServerConfig{})
	checks := []struct {
		name string
		uri  string
		fn   func(context.Context, gomcp.ReadResourceRequest) ([]gomcp.ResourceContents, error)
	}{
		{"jobs", "seo-crawler://jobs", s.handleJobListResource},
		{"detail", "seo-crawler://jobs/job-1", s.handleJobDetailResource},
		{"summary", "seo-crawler://jobs/job-1/summary", s.handleJobSummaryResource},
		{"events", "seo-crawler://jobs/job-1/events", s.handleJobEventsResource},
		{"page", "seo-crawler://jobs/job-1/page/1", s.handlePageDetailResource},
	}
	for _, tt := range checks {
		t.Run(tt.name, func(t *testing.T) {
			req := gomcp.ReadResourceRequest{}
			req.Params.URI = tt.uri
			if _, err := tt.fn(context.Background(), req); err == nil {
				t.Fatal("expected database unavailable error")
			}
		})
	}
}

func TestJobListResource(t *testing.T) {
	db := setupTestDB(t)
	cfg := config.DefaultConfig()
	s := NewServer(ServerConfig{DB: db, Config: &cfg})

	// Create two jobs
	job1, err := db.CreateJob("crawl", `{"maxPages":10}`, `["https://a.com"]`)
	if err != nil {
		t.Fatalf("creating job1: %v", err)
	}
	job2, err := db.CreateJob("crawl", `{"maxPages":20}`, `["https://b.com"]`)
	if err != nil {
		t.Fatalf("creating job2: %v", err)
	}

	contents := readResource(t, s, "seo-crawler://jobs")
	text := textFromContents(t, contents)

	var jobs []storage.CrawlJob
	if err := json.Unmarshal([]byte(text), &jobs); err != nil {
		t.Fatalf("parsing job list JSON: %v", err)
	}

	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}

	ids := map[string]bool{jobs[0].ID: true, jobs[1].ID: true}
	if !ids[job1.ID] || !ids[job2.ID] {
		t.Errorf("expected jobs %q and %q, got %v", job1.ID, job2.ID, ids)
	}
}

func TestJobDetailResource(t *testing.T) {
	db := setupTestDB(t)
	cfg := config.DefaultConfig()
	s := NewServer(ServerConfig{DB: db, Config: &cfg})

	job, err := db.CreateJob("crawl", `{}`, `["https://example.com"]`)
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	// Insert a URL and an issue
	urlID, err := db.UpsertURL(job.ID, "https://example.com", "example.com", "pending", true, "seed")
	if err != nil {
		t.Fatalf("inserting URL: %v", err)
	}
	if err := db.UpdateURLStatus(urlID, "crawled"); err != nil {
		t.Fatalf("updating URL status: %v", err)
	}
	_, err = db.InsertIssue(storage.IssueInput{
		JobID:     job.ID,
		URLID:     &urlID,
		IssueType: "missing_title",
		Severity:  "warning",
		Scope:     "page",
	})
	if err != nil {
		t.Fatalf("inserting issue: %v", err)
	}

	contents := readResource(t, s, "seo-crawler://jobs/"+job.ID)
	text := textFromContents(t, contents)

	var detail jobDetailPayload
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		t.Fatalf("parsing detail JSON: %v", err)
	}

	if detail.URLsByStatus == nil {
		t.Fatal("expected urlsByStatus to be non-nil")
	}
	if detail.URLsByStatus["crawled"] != 1 {
		t.Errorf("expected 1 crawled URL, got %d", detail.URLsByStatus["crawled"])
	}
	if detail.IssuesByType == nil {
		t.Fatal("expected issuesByType to be non-nil")
	}
	if detail.IssuesByType["missing_title"] != 1 {
		t.Errorf("expected 1 missing_title issue, got %d", detail.IssuesByType["missing_title"])
	}
}

func TestJobSummaryResource(t *testing.T) {
	db := setupTestDB(t)
	cfg := config.DefaultConfig()
	s := NewServer(ServerConfig{DB: db, Config: &cfg})

	job, err := db.CreateJob("crawl", `{}`, `["https://example.com"]`)
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	contents := readResource(t, s, "seo-crawler://jobs/"+job.ID+"/summary")
	text := textFromContents(t, contents)

	var summary map[string]any
	if err := json.Unmarshal([]byte(text), &summary); err != nil {
		t.Fatalf("parsing summary JSON: %v", err)
	}

	// Verify expected fields exist
	for _, key := range []string{"totalPages", "totalUrls", "totalIssues"} {
		if _, ok := summary[key]; !ok {
			t.Errorf("expected field %q in summary", key)
		}
	}
}

func TestJobEventsResource(t *testing.T) {
	db := setupTestDB(t)
	cfg := config.DefaultConfig()
	s := NewServer(ServerConfig{DB: db, Config: &cfg})

	job, err := db.CreateJob("crawl", `{}`, `["https://example.com"]`)
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	// Insert events
	_, err = db.InsertEvent(job.ID, "crawl_started", nil, nil)
	if err != nil {
		t.Fatalf("inserting event: %v", err)
	}
	u := "https://example.com"
	_, err = db.InsertEvent(job.ID, "page_fetched", nil, &u)
	if err != nil {
		t.Fatalf("inserting event: %v", err)
	}

	contents := readResource(t, s, "seo-crawler://jobs/"+job.ID+"/events")
	text := textFromContents(t, contents)

	var events []storage.CrawlEvent
	if err := json.Unmarshal([]byte(text), &events); err != nil {
		t.Fatalf("parsing events JSON: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	// Events are returned in DESC order (newest first)
	if events[0].EventType != "page_fetched" {
		t.Errorf("expected first event type %q, got %q", "page_fetched", events[0].EventType)
	}
	if events[1].EventType != "crawl_started" {
		t.Errorf("expected second event type %q, got %q", "crawl_started", events[1].EventType)
	}
}

func TestPageDetailResource(t *testing.T) {
	db := setupTestDB(t)
	cfg := config.DefaultConfig()
	s := NewServer(ServerConfig{DB: db, Config: &cfg})

	job, err := db.CreateJob("crawl", `{}`, `["https://example.com"]`)
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	// Insert a URL
	urlID, err := db.UpsertURL(job.ID, "https://example.com", "example.com", "crawled", true, "seed")
	if err != nil {
		t.Fatalf("inserting URL: %v", err)
	}

	// Insert an issue for this URL
	_, err = db.InsertIssue(storage.IssueInput{
		JobID:     job.ID,
		URLID:     &urlID,
		IssueType: "missing_title",
		Severity:  "warning",
		Scope:     "page",
	})
	if err != nil {
		t.Fatalf("inserting issue: %v", err)
	}

	uri := fmt.Sprintf("seo-crawler://jobs/%s/page/%d", job.ID, urlID)
	contents := readResource(t, s, uri)
	text := textFromContents(t, contents)

	var detail pageDetailPayload
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		t.Fatalf("parsing page detail JSON: %v", err)
	}

	if detail.URL == nil {
		t.Fatal("expected URL to be non-nil")
	}
	if detail.URL.ID != urlID {
		t.Errorf("expected URL ID %d, got %d", urlID, detail.URL.ID)
	}
	if len(detail.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(detail.Issues))
	}
	if detail.Issues[0].IssueType != "missing_title" {
		t.Errorf("expected issue type %q, got %q", "missing_title", detail.Issues[0].IssueType)
	}
	// OutboundEdges and InboundEdges should be empty arrays (not nil)
	if detail.OutboundEdges == nil {
		t.Error("expected outboundEdges to be non-nil empty slice")
	}
	if detail.InboundEdges == nil {
		t.Error("expected inboundEdges to be non-nil empty slice")
	}
	if detail.RedirectHops == nil {
		t.Error("expected redirectHops to be non-nil empty slice")
	}
}

func TestExtractURLID(t *testing.T) {
	tests := []struct {
		uri     string
		wantJob string
		wantURL int64
		wantOK  bool
	}{
		{"seo-crawler://jobs/abc123/page/42", "abc123", 42, true},
		{"seo-crawler://jobs/abc123/page/0", "abc123", 0, true},
		{"seo-crawler://jobs/abc123/page/", "", 0, false},
		{"seo-crawler://jobs//page/42", "", 0, false},
		{"seo-crawler://jobs/abc123/page/notanumber", "", 0, false},
		{"invalid://uri", "", 0, false},
	}

	for _, tt := range tests {
		jobID, urlID, err := extractURLID(tt.uri)
		if tt.wantOK && err != nil {
			t.Errorf("extractURLID(%q): unexpected error: %v", tt.uri, err)
		}
		if !tt.wantOK && err == nil {
			t.Errorf("extractURLID(%q): expected error, got job=%q url=%d", tt.uri, jobID, urlID)
		}
		if jobID != tt.wantJob {
			t.Errorf("extractURLID(%q) jobID = %q, want %q", tt.uri, jobID, tt.wantJob)
		}
		if urlID != tt.wantURL {
			t.Errorf("extractURLID(%q) urlID = %d, want %d", tt.uri, urlID, tt.wantURL)
		}
	}
}

func TestExtractJobID(t *testing.T) {
	tests := []struct {
		uri    string
		wantID string
		wantOK bool
	}{
		{"seo-crawler://jobs/abc123", "abc123", true},
		{"seo-crawler://jobs/abc123/summary", "abc123", true},
		{"seo-crawler://jobs/abc123/events", "abc123", true},
		{"seo-crawler://jobs/", "", false},
		{"invalid://uri", "", false},
	}

	for _, tt := range tests {
		id, err := extractJobID(tt.uri)
		if tt.wantOK && err != nil {
			t.Errorf("extractJobID(%q): unexpected error: %v", tt.uri, err)
		}
		if !tt.wantOK && err == nil {
			t.Errorf("extractJobID(%q): expected error, got %q", tt.uri, id)
		}
		if id != tt.wantID {
			t.Errorf("extractJobID(%q) = %q, want %q", tt.uri, id, tt.wantID)
		}
	}
}
