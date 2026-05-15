package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
	gomcp "github.com/mark3labs/mcp-go/mcp"
)

// seedTestData creates a job with URLs, fetches, pages, issues, and edges for testing.
func seedTestData(t *testing.T, db *storage.DB) string {
	t.Helper()

	job, err := db.CreateJob("crawl", `{}`, `["https://example.com"]`)
	if err != nil {
		t.Fatalf("creating job: %v", err)
	}
	if err := db.UpdateJobStarted(job.ID); err != nil {
		t.Fatalf("starting job: %v", err)
	}

	// Create URLs.
	url1, err := db.UpsertURL(job.ID, "https://example.com/", "example.com", "crawled", true, "seed")
	if err != nil {
		t.Fatalf("upserting URL 1: %v", err)
	}
	url2, err := db.UpsertURL(job.ID, "https://example.com/about", "example.com", "crawled", true, "link")
	if err != nil {
		t.Fatalf("upserting URL 2: %v", err)
	}
	url3, err := db.UpsertURL(job.ID, "https://external.com/page", "external.com", "discovered", false, "link")
	if err != nil {
		t.Fatalf("upserting URL 3: %v", err)
	}

	// Create fetches.
	fetch1, err := db.InsertFetch(storage.FetchInput{
		JobID:          job.ID,
		FetchSeq:       1,
		RequestedURLID: url1,
		StatusCode:     200,
		TTFBMs:         100,
		ContentType:    "text/html",
		HTTPMethod:     "GET",
		FetchKind:      "full",
		RenderMode:     "static",
	})
	if err != nil {
		t.Fatalf("inserting fetch 1: %v", err)
	}
	fetch2, err := db.InsertFetch(storage.FetchInput{
		JobID:          job.ID,
		FetchSeq:       2,
		RequestedURLID: url2,
		StatusCode:     200,
		TTFBMs:         150,
		ContentType:    "text/html",
		HTTPMethod:     "GET",
		FetchKind:      "full",
		RenderMode:     "static",
	})
	if err != nil {
		t.Fatalf("inserting fetch 2: %v", err)
	}

	// Create pages.
	title1 := "Home Page"
	wc1 := 500
	_, err = db.InsertPage(storage.PageInput{
		JobID:             job.ID,
		URLID:             url1,
		FetchID:           fetch1,
		Depth:             0,
		Title:             &title1,
		IndexabilityState: "indexable",
		WordCount:         &wc1,
	})
	if err != nil {
		t.Fatalf("inserting page 1: %v", err)
	}

	title2 := "About Page"
	wc2 := 300
	_, err = db.InsertPage(storage.PageInput{
		JobID:             job.ID,
		URLID:             url2,
		FetchID:           fetch2,
		Depth:             1,
		Title:             &title2,
		IndexabilityState: "indexable",
		WordCount:         &wc2,
	})
	if err != nil {
		t.Fatalf("inserting page 2: %v", err)
	}

	// Create issues.
	details := `{"actual":65}`
	_, err = db.InsertIssue(storage.IssueInput{
		JobID:       job.ID,
		URLID:       &url1,
		IssueType:   "title_too_long",
		Severity:    "warning",
		Scope:       "page_local",
		DetailsJSON: &details,
	})
	if err != nil {
		t.Fatalf("inserting issue 1: %v", err)
	}

	details2 := `{}`
	_, err = db.InsertIssue(storage.IssueInput{
		JobID:       job.ID,
		URLID:       &url2,
		IssueType:   "missing_meta_description",
		Severity:    "warning",
		Scope:       "page_local",
		DetailsJSON: &details2,
	})
	if err != nil {
		t.Fatalf("inserting issue 2: %v", err)
	}

	// Create edges.
	_, err = db.InsertEdge(storage.EdgeInput{
		JobID:                 job.ID,
		SourceURLID:           url1,
		NormalizedTargetURLID: url2,
		SourceKind:            "html",
		RelationType:          "hyperlink",
		DiscoveryMode:         "link",
		IsInternal:            true,
		DeclaredTargetURL:     "/about",
	})
	if err != nil {
		t.Fatalf("inserting edge 1: %v", err)
	}

	_, err = db.InsertEdge(storage.EdgeInput{
		JobID:                 job.ID,
		SourceURLID:           url1,
		NormalizedTargetURLID: url3,
		SourceKind:            "html",
		RelationType:          "hyperlink",
		DiscoveryMode:         "link",
		IsInternal:            false,
		DeclaredTargetURL:     "https://external.com/page",
	})
	if err != nil {
		t.Fatalf("inserting edge 2: %v", err)
	}

	// Update counters and finish.
	if err := db.UpdateJobCounters(job.ID, 2, 3, 2); err != nil {
		t.Fatalf("updating counters: %v", err)
	}
	if err := db.UpdateJobFinished(job.ID, "completed", nil); err != nil {
		t.Fatalf("finishing job: %v", err)
	}

	return job.ID
}

func TestGetCrawlSummary(t *testing.T) {
	db := setupTestDB(t)
	jobID := seedTestData(t, db)
	cfg := config.DefaultConfig()

	s := NewServer(ServerConfig{DB: db, Config: &cfg})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"jobId": jobID,
	}

	result, err := s.handleGetCrawlSummary(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}

	// Parse and verify summary fields.
	var summary storage.CrawlSummary
	for _, content := range result.Content {
		if tc, ok := content.(gomcp.TextContent); ok {
			if err := json.Unmarshal([]byte(tc.Text), &summary); err != nil {
				t.Fatalf("parsing summary: %v", err)
			}
		}
	}

	if summary.TotalPages != 2 {
		t.Errorf("expected 2 total pages, got %d", summary.TotalPages)
	}
	if summary.TotalURLs != 3 {
		t.Errorf("expected 3 total URLs, got %d", summary.TotalURLs)
	}
	if summary.TotalIssues != 2 {
		t.Errorf("expected 2 total issues, got %d", summary.TotalIssues)
	}
	if summary.IssuesByType["title_too_long"] != 1 {
		t.Errorf("expected 1 title_too_long issue, got %d", summary.IssuesByType["title_too_long"])
	}
	if summary.StatusCodeDistribution[200] != 2 {
		t.Errorf("expected 2 200-status fetches, got %d", summary.StatusCodeDistribution[200])
	}
}

func TestGetCrawlSummary_NilDB(t *testing.T) {
	s := NewServer(ServerConfig{})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"jobId": "some-id"}

	result, err := s.handleGetCrawlSummary(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when DB is nil")
	}
}

func TestGetCrawlResults_Pages(t *testing.T) {
	db := setupTestDB(t)
	jobID := seedTestData(t, db)
	cfg := config.DefaultConfig()

	s := NewServer(ServerConfig{DB: db, Config: &cfg})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"jobId": jobID,
		"view":  "pages",
		"limit": float64(1),
	}

	result, err := s.handleGetCrawlResults(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}

	var resp struct {
		Results    []json.RawMessage `json:"results"`
		NextCursor string            `json:"nextCursor"`
		TotalCount int               `json:"totalCount"`
	}
	for _, content := range result.Content {
		if tc, ok := content.(gomcp.TextContent); ok {
			if err := json.Unmarshal([]byte(tc.Text), &resp); err != nil {
				t.Fatalf("parsing response: %v", err)
			}
		}
	}

	if resp.TotalCount != 2 {
		t.Errorf("expected totalCount 2, got %d", resp.TotalCount)
	}
	if len(resp.Results) != 1 {
		t.Errorf("expected 1 result (limit=1), got %d", len(resp.Results))
	}
	if resp.NextCursor == "" {
		t.Error("expected nextCursor for pagination")
	}

	// Fetch second page.
	req.Params.Arguments.(map[string]any)["cursor"] = resp.NextCursor
	result2, err := s.handleGetCrawlResults(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error on page 2: %v", err)
	}
	if result2.IsError {
		t.Fatalf("page 2 error: %v", result2.Content)
	}

	var resp2 struct {
		Results    []json.RawMessage `json:"results"`
		NextCursor string            `json:"nextCursor"`
	}
	for _, content := range result2.Content {
		if tc, ok := content.(gomcp.TextContent); ok {
			if err := json.Unmarshal([]byte(tc.Text), &resp2); err != nil {
				t.Fatalf("parsing page 2: %v", err)
			}
		}
	}

	if len(resp2.Results) != 1 {
		t.Errorf("expected 1 result on page 2, got %d", len(resp2.Results))
	}
}

func TestGetCrawlResults_Issues(t *testing.T) {
	db := setupTestDB(t)
	jobID := seedTestData(t, db)
	cfg := config.DefaultConfig()

	s := NewServer(ServerConfig{DB: db, Config: &cfg})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"jobId":     jobID,
		"view":      "issues",
		"issueType": "title_too_long",
	}

	result, err := s.handleGetCrawlResults(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}

	var resp struct {
		Results    []json.RawMessage `json:"results"`
		TotalCount int               `json:"totalCount"`
	}
	for _, content := range result.Content {
		if tc, ok := content.(gomcp.TextContent); ok {
			if err := json.Unmarshal([]byte(tc.Text), &resp); err != nil {
				t.Fatalf("parsing response: %v", err)
			}
		}
	}

	if resp.TotalCount != 1 {
		t.Errorf("expected 1 filtered issue, got %d", resp.TotalCount)
	}
}

func TestGetCrawlResults_InvalidView(t *testing.T) {
	db := setupTestDB(t)
	jobID := seedTestData(t, db)

	s := NewServer(ServerConfig{DB: db})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"jobId": jobID,
		"view":  "nonexistent",
	}

	result, err := s.handleGetCrawlResults(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for invalid view")
	}
}

func TestGetLinkGraph_Outbound(t *testing.T) {
	db := setupTestDB(t)
	jobID := seedTestData(t, db)
	cfg := config.DefaultConfig()

	s := NewServer(ServerConfig{DB: db, Config: &cfg})

	// Find URL ID for example.com/.
	url1, err := db.GetURLByNormalized(jobID, "https://example.com/")
	if err != nil {
		t.Fatalf("getting URL: %v", err)
	}

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"jobId":     jobID,
		"urlId":     float64(url1.ID),
		"direction": "outbound",
	}

	result, err := s.handleGetLinkGraph(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}

	var resp struct {
		Edges []json.RawMessage `json:"edges"`
	}
	for _, content := range result.Content {
		if tc, ok := content.(gomcp.TextContent); ok {
			if err := json.Unmarshal([]byte(tc.Text), &resp); err != nil {
				t.Fatalf("parsing response: %v", err)
			}
		}
	}

	if len(resp.Edges) != 2 {
		t.Errorf("expected 2 outbound edges, got %d", len(resp.Edges))
	}
}

func TestGetLinkGraph_Inbound(t *testing.T) {
	db := setupTestDB(t)
	jobID := seedTestData(t, db)
	cfg := config.DefaultConfig()

	s := NewServer(ServerConfig{DB: db, Config: &cfg})

	url2, err := db.GetURLByNormalized(jobID, "https://example.com/about")
	if err != nil {
		t.Fatalf("getting URL: %v", err)
	}

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"jobId":     jobID,
		"urlId":     float64(url2.ID),
		"direction": "inbound",
	}

	result, err := s.handleGetLinkGraph(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}

	var resp struct {
		Edges []json.RawMessage `json:"edges"`
	}
	for _, content := range result.Content {
		if tc, ok := content.(gomcp.TextContent); ok {
			if err := json.Unmarshal([]byte(tc.Text), &resp); err != nil {
				t.Fatalf("parsing response: %v", err)
			}
		}
	}

	if len(resp.Edges) != 1 {
		t.Errorf("expected 1 inbound edge, got %d", len(resp.Edges))
	}
}

func TestGetCrawlResults_ExternalLinks(t *testing.T) {
	db := setupTestDB(t)
	jobID := seedTestData(t, db)
	cfg := config.DefaultConfig()

	s := NewServer(ServerConfig{DB: db, Config: &cfg})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"jobId": jobID,
		"view":  "external_links",
	}

	result, err := s.handleGetCrawlResults(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}

	var resp struct {
		Results    []json.RawMessage `json:"results"`
		TotalCount int               `json:"totalCount"`
	}
	for _, content := range result.Content {
		if tc, ok := content.(gomcp.TextContent); ok {
			if err := json.Unmarshal([]byte(tc.Text), &resp); err != nil {
				t.Fatalf("parsing response: %v", err)
			}
		}
	}

	if resp.TotalCount != 1 {
		t.Errorf("expected 1 external link, got %d", resp.TotalCount)
	}
}

func TestGetCrawlResults_ResponseCodesFilter(t *testing.T) {
	db := setupTestDB(t)
	jobID := seedTestData(t, db)
	cfg := config.DefaultConfig()

	s := NewServer(ServerConfig{DB: db, Config: &cfg})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"jobId":            jobID,
		"view":             "response_codes",
		"statusCodeFamily": "2xx",
	}

	result, err := s.handleGetCrawlResults(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}

	var resp struct {
		Results        []json.RawMessage `json:"results"`
		TotalCount     int               `json:"totalCount"`
		IgnoredFilters []string          `json:"ignoredFilters"`
	}
	for _, content := range result.Content {
		if tc, ok := content.(gomcp.TextContent); ok {
			if err := json.Unmarshal([]byte(tc.Text), &resp); err != nil {
				t.Fatalf("parsing response: %v", err)
			}
		}
	}

	if resp.TotalCount != 2 {
		t.Errorf("expected 2 2xx fetches, got %d", resp.TotalCount)
	}
}

func TestGetCrawlResults_PagesFilterIgnored(t *testing.T) {
	db := setupTestDB(t)
	jobID := seedTestData(t, db)
	cfg := config.DefaultConfig()

	s := NewServer(ServerConfig{DB: db, Config: &cfg})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"jobId":        jobID,
		"view":         "pages",
		"relationType": "hyperlink", // not applicable to pages view
	}

	result, err := s.handleGetCrawlResults(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}

	var resp struct {
		IgnoredFilters []string `json:"ignoredFilters"`
	}
	for _, content := range result.Content {
		if tc, ok := content.(gomcp.TextContent); ok {
			if err := json.Unmarshal([]byte(tc.Text), &resp); err != nil {
				t.Fatalf("parsing response: %v", err)
			}
		}
	}

	found := false
	for _, f := range resp.IgnoredFilters {
		if f == "RelationType" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'RelationType' in ignoredFilters, got %v", resp.IgnoredFilters)
	}
}

func TestGetLinkGraph_RequiresURLID(t *testing.T) {
	db := setupTestDB(t)
	s := NewServer(ServerConfig{DB: db})

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"jobId": "some-id",
	}

	result, err := s.handleGetLinkGraph(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error when urlId missing")
	}
}
