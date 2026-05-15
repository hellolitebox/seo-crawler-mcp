package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
	gomcp "github.com/mark3labs/mcp-go/mcp"
)

func TestAnalyzeSEOPrompt(t *testing.T) {
	db := setupTestDB(t)
	cfg := config.DefaultConfig()
	s := NewServer(ServerConfig{DB: db, Config: &cfg})

	job, err := db.CreateJob("crawl", `{}`, `["https://example.com"]`)
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	// Insert some data so summary has content
	urlID, err := db.UpsertURL(job.ID, "https://example.com", "example.com", "pending", true, "seed")
	if err != nil {
		t.Fatalf("inserting URL: %v", err)
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

	req := gomcp.GetPromptRequest{}
	req.Params.Name = "analyze_technical_seo"
	req.Params.Arguments = map[string]string{"jobId": job.ID}

	result, err := s.handleAnalyzeSEOPrompt(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}

	msg := result.Messages[0]
	if msg.Role != gomcp.RoleUser {
		t.Errorf("expected role %q, got %q", gomcp.RoleUser, msg.Role)
	}

	tc, ok := msg.Content.(gomcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", msg.Content)
	}

	if !strings.Contains(tc.Text, "Analyze the technical SEO") {
		t.Error("prompt text missing expected preamble")
	}
	if !strings.Contains(tc.Text, "totalIssues") {
		t.Error("prompt text missing summary data")
	}
}

func TestAnalyzeSEOPrompt_MissingJobID(t *testing.T) {
	db := setupTestDB(t)
	cfg := config.DefaultConfig()
	s := NewServer(ServerConfig{DB: db, Config: &cfg})

	req := gomcp.GetPromptRequest{}
	req.Params.Name = "analyze_technical_seo"
	req.Params.Arguments = map[string]string{}

	_, err := s.handleAnalyzeSEOPrompt(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for missing jobId")
	}
}

func TestInvestigateURLPrompt(t *testing.T) {
	db := setupTestDB(t)
	cfg := config.DefaultConfig()
	s := NewServer(ServerConfig{DB: db, Config: &cfg})

	job, err := db.CreateJob("crawl", `{}`, `["https://example.com"]`)
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}

	urlID, err := db.UpsertURL(job.ID, "https://example.com/page", "example.com", "pending", true, "link")
	if err != nil {
		t.Fatalf("inserting URL: %v", err)
	}

	// Insert a fetch so we can create a page
	fetchID, err := db.InsertFetch(storage.FetchInput{
		JobID:          job.ID,
		FetchSeq:       1,
		RequestedURLID: urlID,
		StatusCode:     200,
		TTFBMs:         50,
		ContentType:    "text/html",
		RenderMode:     "static",
	})
	if err != nil {
		t.Fatalf("inserting fetch: %v", err)
	}

	title := "Test Page"
	titleLen := 9
	_, err = db.InsertPage(storage.PageInput{
		JobID:             job.ID,
		URLID:             urlID,
		FetchID:           fetchID,
		Depth:             1,
		Title:             &title,
		TitleLength:       &titleLen,
		IndexabilityState: "indexable",
	})
	if err != nil {
		t.Fatalf("inserting page: %v", err)
	}

	_, err = db.InsertIssue(storage.IssueInput{
		JobID:     job.ID,
		URLID:     &urlID,
		IssueType: "short_title",
		Severity:  "info",
		Scope:     "page",
	})
	if err != nil {
		t.Fatalf("inserting issue: %v", err)
	}

	req := gomcp.GetPromptRequest{}
	req.Params.Name = "investigate_url"
	req.Params.Arguments = map[string]string{
		"jobId": job.ID,
		"url":   "https://example.com/page",
	}

	result, err := s.handleInvestigateURLPrompt(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}

	tc, ok := result.Messages[0].Content.(gomcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Messages[0].Content)
	}

	if !strings.Contains(tc.Text, "https://example.com/page") {
		t.Error("prompt text missing target URL")
	}
	if !strings.Contains(tc.Text, "Investigate this URL") {
		t.Error("prompt text missing expected preamble")
	}

	// Verify the embedded data contains page info
	var data investigateURLData
	// Extract JSON between first { and matching }
	jsonStart := strings.Index(tc.Text, "{")
	jsonEnd := strings.LastIndex(tc.Text, "}")
	if jsonStart < 0 || jsonEnd < 0 {
		t.Fatal("no JSON found in prompt text")
	}
	embedded := tc.Text[jsonStart : jsonEnd+1]
	if err := json.Unmarshal([]byte(embedded), &data); err != nil {
		t.Fatalf("parsing embedded JSON: %v", err)
	}

	if data.Page == nil {
		t.Error("expected page data to be present")
	}
	if len(data.Issues) != 1 {
		t.Errorf("expected 1 issue, got %d", len(data.Issues))
	}
}

func TestInvestigateURLPrompt_MissingArgs(t *testing.T) {
	db := setupTestDB(t)
	cfg := config.DefaultConfig()
	s := NewServer(ServerConfig{DB: db, Config: &cfg})

	// Missing both args
	req := gomcp.GetPromptRequest{}
	req.Params.Arguments = map[string]string{}
	_, err := s.handleInvestigateURLPrompt(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for missing args")
	}

	// Missing url
	req.Params.Arguments = map[string]string{"jobId": "abc"}
	_, err = s.handleInvestigateURLPrompt(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for missing url")
	}
}
