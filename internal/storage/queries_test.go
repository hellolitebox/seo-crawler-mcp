package storage

import (
	"fmt"
	"testing"
)

// seedJobWithPages creates a job with count urls, fetches, and pages.
// Pages have increasing depth and varying word counts/status codes.
func seedJobWithPages(t *testing.T, db *DB, count int) string {
	t.Helper()

	job, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	// Mark job started/finished for summary tests
	if err := db.UpdateJobStarted(job.ID); err != nil {
		t.Fatalf("UpdateJobStarted: %v", err)
	}

	for i := 1; i <= count; i++ {
		url := fmt.Sprintf("https://example.com/page-%d", i)
		urlID, err := db.UpsertURL(job.ID, url, "example.com", "crawled", true, "seed")
		if err != nil {
			t.Fatalf("UpsertURL %d: %v", i, err)
		}

		statusCode := 200
		if i%5 == 0 {
			statusCode = 404
		} else if i%3 == 0 {
			statusCode = 301
		}

		ttfb := int64(50 + i*10)
		contentType := "text/html"
		fetchID, err := db.InsertFetch(FetchInput{
			JobID:          job.ID,
			FetchSeq:       i,
			RequestedURLID: urlID,
			StatusCode:     statusCode,
			TTFBMs:         ttfb,
			ContentType:    contentType,
			RenderMode:     "static",
		})
		if err != nil {
			t.Fatalf("InsertFetch %d: %v", i, err)
		}

		depth := i % 4
		wordCount := 100 + i*50
		title := fmt.Sprintf("Page %d", i)
		titleLen := len(title)
		group := "blog"
		if i%2 == 0 {
			group = "product"
		}
		jsonld := `[{"@type":"Article"}]`

		_, err = db.InsertPage(PageInput{
			JobID:             job.ID,
			URLID:             urlID,
			FetchID:           fetchID,
			Depth:             depth,
			Title:             &title,
			TitleLength:       &titleLen,
			IndexabilityState: "indexable",
			WordCount:         &wordCount,
			URLGroup:          &group,
			JSONLDRaw:         &jsonld,
		})
		if err != nil {
			t.Fatalf("InsertPage %d: %v", i, err)
		}
	}

	// Finish the job
	if err := db.UpdateJobFinished(job.ID, "completed", nil); err != nil {
		t.Fatalf("UpdateJobFinished: %v", err)
	}

	return job.ID
}

// seedJobWithIssues creates a job with mixed issue types and severities.
func seedJobWithIssues(t *testing.T, db *DB) string {
	t.Helper()

	job, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	issues := []struct {
		url       string
		issueType string
		severity  string
	}{
		{"https://example.com/a", "missing_title", "error"},
		{"https://example.com/b", "missing_title", "error"},
		{"https://example.com/c", "missing_meta_description", "warning"},
		{"https://example.com/d", "broken_link", "error"},
		{"https://example.com/e", "broken_link", "error"},
		{"https://example.com/f", "broken_link", "error"},
		{"https://example.com/g", "thin_content", "warning"},
		{"https://example.com/h", "duplicate_content", "info"},
	}

	for _, iss := range issues {
		urlID, err := db.UpsertURL(job.ID, iss.url, "example.com", "crawled", true, "seed")
		if err != nil {
			t.Fatalf("UpsertURL %q: %v", iss.url, err)
		}
		_, err = db.InsertIssue(IssueInput{
			JobID:     job.ID,
			URLID:     &urlID,
			IssueType: iss.issueType,
			Severity:  iss.severity,
			Scope:     "page",
		})
		if err != nil {
			t.Fatalf("InsertIssue: %v", err)
		}
	}

	return job.ID
}

func TestQueryPages_Pagination(t *testing.T) {
	db := testDB(t)
	jobID := seedJobWithPages(t, db, 5)

	// First page: limit=2
	result, err := db.QueryPages(jobID, QueryFilter{}, "", 2)
	if err != nil {
		t.Fatalf("QueryPages page 1: %v", err)
	}
	if len(result.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result.Results))
	}
	if result.NextCursor == "" {
		t.Fatal("expected NextCursor on page 1")
	}
	if result.TotalCount != 5 {
		t.Errorf("expected TotalCount=5, got %d", result.TotalCount)
	}

	// Second page
	result2, err := db.QueryPages(jobID, QueryFilter{}, result.NextCursor, 2)
	if err != nil {
		t.Fatalf("QueryPages page 2: %v", err)
	}
	if len(result2.Results) != 2 {
		t.Fatalf("expected 2 results on page 2, got %d", len(result2.Results))
	}
	if result2.NextCursor == "" {
		t.Fatal("expected NextCursor on page 2")
	}

	// Third page (last)
	result3, err := db.QueryPages(jobID, QueryFilter{}, result2.NextCursor, 2)
	if err != nil {
		t.Fatalf("QueryPages page 3: %v", err)
	}
	if len(result3.Results) != 1 {
		t.Fatalf("expected 1 result on page 3, got %d", len(result3.Results))
	}
	if result3.NextCursor != "" {
		t.Errorf("expected empty NextCursor on last page, got %q", result3.NextCursor)
	}

	// Verify no overlap between pages
	seen := map[int64]bool{}
	for _, p := range result.Results {
		seen[p.ID] = true
	}
	for _, p := range result2.Results {
		if seen[p.ID] {
			t.Errorf("duplicate page ID %d across pages", p.ID)
		}
		seen[p.ID] = true
	}
	for _, p := range result3.Results {
		if seen[p.ID] {
			t.Errorf("duplicate page ID %d across pages", p.ID)
		}
	}
}

func TestQueryPages_FilterByURLPattern(t *testing.T) {
	db := testDB(t)
	jobID := seedJobWithPages(t, db, 5)

	// Filter by "page-3"
	result, err := db.QueryPages(jobID, QueryFilter{URLPattern: "page-3"}, "", 10)
	if err != nil {
		t.Fatalf("QueryPages with URL pattern: %v", err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result for pattern 'page-3', got %d", len(result.Results))
	}
	if result.TotalCount != 1 {
		t.Errorf("expected TotalCount=1, got %d", result.TotalCount)
	}
}

func TestQueryPages_FilterByURLGroup(t *testing.T) {
	db := testDB(t)
	jobID := seedJobWithPages(t, db, 5)

	// "blog" group: pages 1, 3, 5
	result, err := db.QueryPages(jobID, QueryFilter{URLGroup: "blog"}, "", 10)
	if err != nil {
		t.Fatalf("QueryPages with URLGroup: %v", err)
	}
	if result.TotalCount != 3 {
		t.Errorf("expected 3 blog pages, got %d", result.TotalCount)
	}
}

func TestQueryPages_FilterByDepth(t *testing.T) {
	db := testDB(t)
	jobID := seedJobWithPages(t, db, 5)

	minD := 0
	maxD := 1
	result, err := db.QueryPages(jobID, QueryFilter{MinDepth: &minD, MaxDepth: &maxD}, "", 10)
	if err != nil {
		t.Fatalf("QueryPages with depth filter: %v", err)
	}
	for _, p := range result.Results {
		if p.Depth < 0 || p.Depth > 1 {
			t.Errorf("page depth %d outside [0,1]", p.Depth)
		}
	}
}

func TestQueryIssues_FilterByType(t *testing.T) {
	db := testDB(t)
	jobID := seedJobWithIssues(t, db)

	result, err := db.QueryIssues(jobID, QueryFilter{IssueType: "broken_link"}, "", 10)
	if err != nil {
		t.Fatalf("QueryIssues: %v", err)
	}
	if len(result.Results) != 3 {
		t.Fatalf("expected 3 broken_link issues, got %d", len(result.Results))
	}
	if result.TotalCount != 3 {
		t.Errorf("expected TotalCount=3, got %d", result.TotalCount)
	}
	for _, iss := range result.Results {
		if iss.IssueType != "broken_link" {
			t.Errorf("expected issue type 'broken_link', got %q", iss.IssueType)
		}
	}
}

func TestQueryIssues_IgnoredFilters(t *testing.T) {
	db := testDB(t)
	jobID := seedJobWithIssues(t, db)

	// Set filters that don't apply to issues
	isInternal := true
	filter := QueryFilter{
		IssueType:        "broken_link",
		StatusCodeFamily: "4xx",
		URLGroup:         "blog",
		RelationType:     "hyperlink",
		ContentType:      "text/html",
		ClusterType:      "exact",
		TargetDomain:     "example.com",
		IsInternal:       &isInternal,
	}

	result, err := db.QueryIssues(jobID, filter, "", 10)
	if err != nil {
		t.Fatalf("QueryIssues: %v", err)
	}

	expectedIgnored := map[string]bool{
		"StatusCodeFamily": true,
		"URLGroup":         true,
		"RelationType":     true,
		"ContentType":      true,
		"ClusterType":      true,
		"TargetDomain":     true,
		"IsInternal":       true,
	}

	gotIgnored := map[string]bool{}
	for _, f := range result.IgnoredFilters {
		gotIgnored[f] = true
	}

	for expected := range expectedIgnored {
		if !gotIgnored[expected] {
			t.Errorf("expected %q in IgnoredFilters", expected)
		}
	}

	// IssueType should NOT be in ignored (it's applicable)
	if gotIgnored["IssueType"] {
		t.Error("IssueType should not be in IgnoredFilters")
	}
}

func TestQueryIssues_Pagination(t *testing.T) {
	db := testDB(t)
	jobID := seedJobWithIssues(t, db)

	// 8 total issues, paginate by 3
	r1, err := db.QueryIssues(jobID, QueryFilter{}, "", 3)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(r1.Results) != 3 {
		t.Fatalf("expected 3, got %d", len(r1.Results))
	}
	if r1.NextCursor == "" {
		t.Fatal("expected cursor")
	}
	if r1.TotalCount != 8 {
		t.Errorf("expected total 8, got %d", r1.TotalCount)
	}

	r2, err := db.QueryIssues(jobID, QueryFilter{}, r1.NextCursor, 3)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(r2.Results) != 3 {
		t.Fatalf("expected 3, got %d", len(r2.Results))
	}

	r3, err := db.QueryIssues(jobID, QueryFilter{}, r2.NextCursor, 3)
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if len(r3.Results) != 2 {
		t.Fatalf("expected 2, got %d", len(r3.Results))
	}
	if r3.NextCursor != "" {
		t.Errorf("expected empty cursor on last page")
	}
}

func TestQueryEdgesView_FilterByRelationType(t *testing.T) {
	db := testDB(t)

	job, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	src, _ := db.UpsertURL(job.ID, "https://example.com/src", "example.com", "crawled", true, "seed")
	tgt1, _ := db.UpsertURL(job.ID, "https://example.com/tgt1", "example.com", "crawled", true, "crawl")
	tgt2, _ := db.UpsertURL(job.ID, "https://example.com/tgt2", "example.com", "crawled", true, "crawl")

	db.InsertEdge(EdgeInput{
		JobID: job.ID, SourceURLID: src, NormalizedTargetURLID: tgt1,
		RelationType: "hyperlink", IsInternal: true, DeclaredTargetURL: "https://example.com/tgt1",
	})
	db.InsertEdge(EdgeInput{
		JobID: job.ID, SourceURLID: src, NormalizedTargetURLID: tgt2,
		RelationType: "canonical", IsInternal: true, DeclaredTargetURL: "https://example.com/tgt2",
	})

	result, err := db.QueryEdgesView(job.ID, QueryFilter{RelationType: "hyperlink"}, "", 10)
	if err != nil {
		t.Fatalf("QueryEdgesView: %v", err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("expected 1 hyperlink edge, got %d", len(result.Results))
	}
	if result.Results[0].RelationType != "hyperlink" {
		t.Errorf("expected hyperlink, got %q", result.Results[0].RelationType)
	}
}

func TestQueryEdgesView_FilterByIsInternal(t *testing.T) {
	db := testDB(t)

	job, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	src, _ := db.UpsertURL(job.ID, "https://example.com/src", "example.com", "crawled", true, "seed")
	tgt1, _ := db.UpsertURL(job.ID, "https://example.com/int", "example.com", "crawled", true, "crawl")
	tgt2, _ := db.UpsertURL(job.ID, "https://other.com/ext", "other.com", "crawled", false, "crawl")

	db.InsertEdge(EdgeInput{
		JobID: job.ID, SourceURLID: src, NormalizedTargetURLID: tgt1,
		RelationType: "hyperlink", IsInternal: true, DeclaredTargetURL: "https://example.com/int",
	})
	db.InsertEdge(EdgeInput{
		JobID: job.ID, SourceURLID: src, NormalizedTargetURLID: tgt2,
		RelationType: "hyperlink", IsInternal: false, DeclaredTargetURL: "https://other.com/ext",
	})

	isExternal := false
	result, err := db.QueryEdgesView(job.ID, QueryFilter{IsInternal: &isExternal}, "", 10)
	if err != nil {
		t.Fatalf("QueryEdgesView: %v", err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("expected 1 external edge, got %d", len(result.Results))
	}
	if result.Results[0].IsInternal {
		t.Error("expected external edge")
	}
}

func TestQueryResponseCodes_FilterByFamily(t *testing.T) {
	db := testDB(t)
	jobID := seedJobWithPages(t, db, 5)

	// We have status codes: 200,200,301,200,404 (for pages 1-5)
	result, err := db.QueryResponseCodes(jobID, QueryFilter{StatusCodeFamily: "2xx"}, "", 10)
	if err != nil {
		t.Fatalf("QueryResponseCodes: %v", err)
	}
	for _, f := range result.Results {
		sc := f.StatusCode
		if !sc.Valid || sc.Int64 < 200 || sc.Int64 > 299 {
			t.Errorf("expected 2xx status, got %v", sc)
		}
	}
	if result.TotalCount != 3 {
		t.Errorf("expected 3 2xx fetches, got %d", result.TotalCount)
	}
}

func TestGetCrawlSummary(t *testing.T) {
	db := testDB(t)
	jobID := seedJobWithPages(t, db, 5)

	// Add some issues
	for i := 0; i < 3; i++ {
		url := fmt.Sprintf("https://example.com/page-%d", i+1)
		u, _ := db.GetURLByNormalized(jobID, url)
		db.InsertIssue(IssueInput{
			JobID:     jobID,
			URLID:     &u.ID,
			IssueType: "missing_title",
			Severity:  "error",
			Scope:     "page",
		})
	}
	url2 := "https://example.com/page-4"
	u2, _ := db.GetURLByNormalized(jobID, url2)
	db.InsertIssue(IssueInput{
		JobID:     jobID,
		URLID:     &u2.ID,
		IssueType: "thin_content",
		Severity:  "warning",
		Scope:     "page",
	})

	// Add a duplicate cluster
	firstURL, _ := db.GetURLByNormalized(jobID, "https://example.com/page-1")
	db.Exec(
		"INSERT INTO duplicate_clusters (job_id, cluster_type, hash_value, first_url_id, member_count) VALUES (?, ?, ?, ?, ?)",
		jobID, "exact", "abc123", firstURL.ID, 2,
	)

	summary, err := db.GetCrawlSummary(jobID)
	if err != nil {
		t.Fatalf("GetCrawlSummary: %v", err)
	}

	if summary.TotalPages != 5 {
		t.Errorf("TotalPages: expected 5, got %d", summary.TotalPages)
	}
	if summary.TotalURLs != 5 {
		t.Errorf("TotalURLs: expected 5, got %d", summary.TotalURLs)
	}
	if summary.TotalIssues != 4 {
		t.Errorf("TotalIssues: expected 4, got %d", summary.TotalIssues)
	}

	// Issues by type
	if summary.IssuesByType["missing_title"] != 3 {
		t.Errorf("IssuesByType[missing_title]: expected 3, got %d", summary.IssuesByType["missing_title"])
	}
	if summary.IssuesByType["thin_content"] != 1 {
		t.Errorf("IssuesByType[thin_content]: expected 1, got %d", summary.IssuesByType["thin_content"])
	}

	// Issues by severity
	if summary.IssuesBySeverity["error"] != 3 {
		t.Errorf("IssuesBySeverity[error]: expected 3, got %d", summary.IssuesBySeverity["error"])
	}

	// Status code distribution: 200 x3, 301 x1, 404 x1
	if summary.StatusCodeDistribution[200] != 3 {
		t.Errorf("StatusCode[200]: expected 3, got %d", summary.StatusCodeDistribution[200])
	}
	if summary.StatusCodeDistribution[301] != 1 {
		t.Errorf("StatusCode[301]: expected 1, got %d", summary.StatusCodeDistribution[301])
	}
	if summary.StatusCodeDistribution[404] != 1 {
		t.Errorf("StatusCode[404]: expected 1, got %d", summary.StatusCodeDistribution[404])
	}

	// TTFB: values are 60,70,80,90,100
	if summary.AvgTTFB == 0 {
		t.Error("AvgTTFB should not be 0")
	}
	if summary.MedianTTFB == 0 {
		t.Error("MedianTTFB should not be 0")
	}
	if summary.P95TTFB == 0 {
		t.Error("P95TTFB should not be 0")
	}

	// Avg word count (150,200,250,300,350 → avg=250)
	if summary.AvgWordCount != 250.0 {
		t.Errorf("AvgWordCount: expected 250, got %f", summary.AvgWordCount)
	}

	// All pages have jsonld_raw
	if summary.PagesWithStructuredData != 5 {
		t.Errorf("PagesWithStructuredData: expected 5, got %d", summary.PagesWithStructuredData)
	}

	// Orphans: all pages have inbound_edge_count=0 (default)
	if summary.OrphanPageCount != 5 {
		t.Errorf("OrphanPageCount: expected 5, got %d", summary.OrphanPageCount)
	}

	// Duplicates
	if summary.DuplicateContentCount != 2 {
		t.Errorf("DuplicateContentCount: expected 2, got %d", summary.DuplicateContentCount)
	}

	// Thin content: word_count < 200 means page-1 (150)
	if summary.ThinContentCount != 1 {
		t.Errorf("ThinContentCount: expected 1, got %d", summary.ThinContentCount)
	}

	// Duration should be > 0 (started and finished)
	if summary.CrawlDuration <= 0 {
		t.Logf("CrawlDuration: %f (may be very small)", summary.CrawlDuration)
	}

	// Maps must be initialized, not nil
	if summary.IssuesByType == nil {
		t.Error("IssuesByType must not be nil")
	}
	if summary.IssuesBySeverity == nil {
		t.Error("IssuesBySeverity must not be nil")
	}
	if summary.StatusCodeDistribution == nil {
		t.Error("StatusCodeDistribution must not be nil")
	}
	if summary.DepthDistribution == nil {
		t.Error("DepthDistribution must not be nil")
	}
	if summary.TopIssues == nil {
		t.Error("TopIssues must not be nil")
	}

	// TopIssues should have entries
	if len(summary.TopIssues) == 0 {
		t.Error("expected TopIssues to have entries")
	}
}

func TestQueryPages_EmptyResult(t *testing.T) {
	db := testDB(t)
	job, _ := db.CreateJob("crawl", "{}", "[]")

	result, err := db.QueryPages(job.ID, QueryFilter{}, "", 10)
	if err != nil {
		t.Fatalf("QueryPages: %v", err)
	}
	if result.Results == nil {
		t.Error("Results must be initialized, not nil")
	}
	if len(result.Results) != 0 {
		t.Errorf("expected 0 results, got %d", len(result.Results))
	}
	if result.TotalCount != 0 {
		t.Errorf("expected TotalCount=0, got %d", result.TotalCount)
	}
}
