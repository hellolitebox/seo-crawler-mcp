package storage

import "testing"

func TestInsertAndGetIssues(t *testing.T) {
	db := testDB(t)

	job, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	urlID, err := db.UpsertURL(job.ID, "https://example.com/", "example.com", "pending", true, "seed")
	if err != nil {
		t.Fatalf("UpsertURL: %v", err)
	}

	details := `{"expected":"title"}`
	id, err := db.InsertIssue(IssueInput{
		JobID:       job.ID,
		URLID:       &urlID,
		IssueType:   "missing_title",
		Severity:    "warning",
		Scope:       "page",
		DetailsJSON: &details,
	})
	if err != nil {
		t.Fatalf("InsertIssue: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero issue ID")
	}

	// Insert a site-level issue (no URL).
	_, err = db.InsertIssue(IssueInput{
		JobID:     job.ID,
		IssueType: "no_sitemap",
		Severity:  "error",
		Scope:     "site",
	})
	if err != nil {
		t.Fatalf("InsertIssue site-level: %v", err)
	}

	issues, err := db.GetIssuesByJob(job.ID, 10, "")
	if err != nil {
		t.Fatalf("GetIssuesByJob: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(issues))
	}
}

func TestCountIssuesByTypeAndSeverity(t *testing.T) {
	db := testDB(t)

	job, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	_, err = db.InsertIssue(IssueInput{
		JobID: job.ID, IssueType: "missing_title", Severity: "warning", Scope: "page",
	})
	if err != nil {
		t.Fatalf("InsertIssue 1: %v", err)
	}
	_, err = db.InsertIssue(IssueInput{
		JobID: job.ID, IssueType: "missing_title", Severity: "warning", Scope: "page",
	})
	if err != nil {
		t.Fatalf("InsertIssue 2: %v", err)
	}
	_, err = db.InsertIssue(IssueInput{
		JobID: job.ID, IssueType: "broken_link", Severity: "error", Scope: "page",
	})
	if err != nil {
		t.Fatalf("InsertIssue 3: %v", err)
	}

	byType, err := db.CountIssuesByType(job.ID)
	if err != nil {
		t.Fatalf("CountIssuesByType: %v", err)
	}
	if byType["missing_title"] != 2 {
		t.Errorf("expected 2 missing_title, got %d", byType["missing_title"])
	}
	if byType["broken_link"] != 1 {
		t.Errorf("expected 1 broken_link, got %d", byType["broken_link"])
	}

	bySev, err := db.CountIssuesBySeverity(job.ID)
	if err != nil {
		t.Fatalf("CountIssuesBySeverity: %v", err)
	}
	if bySev["warning"] != 2 {
		t.Errorf("expected 2 warnings, got %d", bySev["warning"])
	}
	if bySev["error"] != 1 {
		t.Errorf("expected 1 error, got %d", bySev["error"])
	}
}

func TestCountIssuesByJobsScopesToRequestedJobs(t *testing.T) {
	db := testDB(t)
	if _, err := db.Exec(`INSERT INTO crawl_jobs (id, type, status, config_json, seed_urls) VALUES (?, 'crawl', 'completed', '{}', ?)`, "visible-a", `["https://visible.example"]`); err != nil {
		t.Fatalf("create visible job: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO crawl_jobs (id, type, status, config_json, seed_urls) VALUES (?, 'crawl', 'completed', '{}', ?)`, "hidden-b", `["https://hidden.example"]`); err != nil {
		t.Fatalf("create hidden job: %v", err)
	}
	if _, err := db.InsertIssue(IssueInput{JobID: "visible-a", IssueType: "missing_title", Severity: "warning", Scope: "page"}); err != nil {
		t.Fatalf("insert visible-a issue: %v", err)
	}
	if _, err := db.InsertIssue(IssueInput{JobID: "visible-a", IssueType: "missing_h1", Severity: "warning", Scope: "page"}); err != nil {
		t.Fatalf("insert visible-a issue 2: %v", err)
	}
	if _, err := db.InsertIssue(IssueInput{JobID: "hidden-b", IssueType: "missing_title", Severity: "warning", Scope: "page"}); err != nil {
		t.Fatalf("insert hidden-b issue: %v", err)
	}

	counts, err := db.CountIssuesByJobs([]string{"visible-a"})
	if err != nil {
		t.Fatalf("CountIssuesByJobs: %v", err)
	}
	if counts["visible-a"] != 2 {
		t.Fatalf("visible-a count = %d, want 2", counts["visible-a"])
	}
	if _, ok := counts["hidden-b"]; ok {
		t.Fatalf("hidden-b should not be counted when it was not requested")
	}
}
