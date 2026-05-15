package urlgroup

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/config"
	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

func testDB(t *testing.T) *storage.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", dbPath, err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbPath)
	})
	return db
}

func seedJob(t *testing.T, db *storage.DB, jobID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO crawl_jobs (id, status) VALUES (?, 'completed')`, jobID)
	if err != nil {
		t.Fatalf("seeding job: %v", err)
	}
}

func seedPageWithURL(t *testing.T, db *storage.DB, jobID, normalizedURL string, fetchSeq int) int64 {
	t.Helper()
	result, err := db.Exec(`INSERT INTO urls (job_id, normalized_url, host, status, discovered_via) VALUES (?, ?, 'example.com', 'fetched', 'crawl')`,
		jobID, normalizedURL)
	if err != nil {
		t.Fatalf("seeding URL %q: %v", normalizedURL, err)
	}
	urlID, _ := result.LastInsertId()

	fResult, err := db.Exec(`INSERT INTO fetches (job_id, fetch_seq, requested_url_id, status_code) VALUES (?, ?, ?, 200)`,
		jobID, fetchSeq, urlID)
	if err != nil {
		t.Fatalf("seeding fetch: %v", err)
	}
	fetchID, _ := fResult.LastInsertId()

	_, err = db.Exec(`INSERT INTO pages (job_id, url_id, fetch_id, depth) VALUES (?, ?, ?, 1)`,
		jobID, urlID, fetchID)
	if err != nil {
		t.Fatalf("seeding page: %v", err)
	}
	return urlID
}

func TestAutoDetectBlogGroup(t *testing.T) {
	db := testDB(t)
	jobID := "job-autogroup"
	seedJob(t, db, jobID)

	// Seed 15 blog URLs (should auto-detect)
	for i := 1; i <= 15; i++ {
		seedPageWithURL(t, db, jobID, fmt.Sprintf("https://example.com/blog/post-%d", i), i)
	}
	// Seed 3 about URLs (below threshold, no auto-detect)
	for i := 1; i <= 3; i++ {
		seedPageWithURL(t, db, jobID, fmt.Sprintf("https://example.com/about/page-%d", i), 15+i)
	}

	err := DetectGroups(db, jobID, nil)
	if err != nil {
		t.Fatalf("DetectGroups: %v", err)
	}

	// Check auto-detected group
	var groupCount int
	db.QueryRow(`SELECT COUNT(*) FROM url_pattern_groups WHERE job_id = ? AND source = 'auto'`, jobID).Scan(&groupCount)
	if groupCount != 1 {
		t.Errorf("expected 1 auto-detected group, got %d", groupCount)
	}

	var groupName string
	db.QueryRow(`SELECT name FROM url_pattern_groups WHERE job_id = ? AND source = 'auto'`, jobID).Scan(&groupName)
	if groupName != "blog" {
		t.Errorf("expected group name %q, got %q", "blog", groupName)
	}

	// Check pages were assigned
	var blogPages int
	db.QueryRow(`SELECT COUNT(*) FROM pages WHERE job_id = ? AND url_group = 'blog'`, jobID).Scan(&blogPages)
	if blogPages != 15 {
		t.Errorf("expected 15 blog-grouped pages, got %d", blogPages)
	}

	var aboutPages int
	db.QueryRow(`SELECT COUNT(*) FROM pages WHERE job_id = ? AND url_group IS NOT NULL AND url_group != 'blog'`, jobID).Scan(&aboutPages)
	if aboutPages != 0 {
		t.Errorf("expected 0 about-grouped pages (below threshold), got %d", aboutPages)
	}
}

func TestUserGroupOverride(t *testing.T) {
	db := testDB(t)
	jobID := "job-usergroup"
	seedJob(t, db, jobID)

	// Seed 15 blog URLs
	for i := 1; i <= 15; i++ {
		seedPageWithURL(t, db, jobID, fmt.Sprintf("https://example.com/blog/post-%d", i), i)
	}

	userGroups := []config.URLGroupConfig{
		{Name: "articles", Pattern: "/blog"},
	}

	err := DetectGroups(db, jobID, userGroups)
	if err != nil {
		t.Fatalf("DetectGroups: %v", err)
	}

	// User group should exist
	var userCount int
	db.QueryRow(`SELECT COUNT(*) FROM url_pattern_groups WHERE job_id = ? AND source = 'user'`, jobID).Scan(&userCount)
	if userCount != 1 {
		t.Errorf("expected 1 user group, got %d", userCount)
	}

	// No auto group should exist (overridden)
	var autoCount int
	db.QueryRow(`SELECT COUNT(*) FROM url_pattern_groups WHERE job_id = ? AND source = 'auto'`, jobID).Scan(&autoCount)
	if autoCount != 0 {
		t.Errorf("expected 0 auto groups (user override), got %d", autoCount)
	}

	// Pages should be assigned to user's name
	var articlePages int
	db.QueryRow(`SELECT COUNT(*) FROM pages WHERE job_id = ? AND url_group = 'articles'`, jobID).Scan(&articlePages)
	if articlePages != 15 {
		t.Errorf("expected 15 articles-grouped pages, got %d", articlePages)
	}
}

func TestUserGroupDoesNotOverrideSiblingPrefix(t *testing.T) {
	db := testDB(t)
	jobID := "job-usergroup-sibling"
	seedJob(t, db, jobID)

	for i := 1; i <= 15; i++ {
		seedPageWithURL(t, db, jobID, fmt.Sprintf("https://example.com/blogroll/post-%d", i), i)
	}

	userGroups := []config.URLGroupConfig{
		{Name: "articles", Pattern: "/blog"},
	}

	if err := DetectGroups(db, jobID, userGroups); err != nil {
		t.Fatalf("DetectGroups: %v", err)
	}

	var autoCount int
	db.QueryRow(`SELECT COUNT(*) FROM url_pattern_groups WHERE job_id = ? AND source = 'auto' AND pattern = '/blogroll'`, jobID).Scan(&autoCount)
	if autoCount != 1 {
		t.Errorf("expected /blogroll auto group, got %d", autoCount)
	}

	var grouped int
	db.QueryRow(`SELECT COUNT(*) FROM pages WHERE job_id = ? AND url_group = 'blogroll'`, jobID).Scan(&grouped)
	if grouped != 15 {
		t.Errorf("expected 15 blogroll-grouped pages, got %d", grouped)
	}
}

func TestDetectGroupsRollsBackOnRebuildFailure(t *testing.T) {
	db := testDB(t)
	jobID := "job-group-rollback"
	seedJob(t, db, jobID)

	for i := 1; i <= 15; i++ {
		seedPageWithURL(t, db, jobID, fmt.Sprintf("https://example.com/blog/post-%d", i), i)
	}
	if err := DetectGroups(db, jobID, nil); err != nil {
		t.Fatalf("initial DetectGroups: %v", err)
	}

	_, err := db.Exec(`
		CREATE TRIGGER fail_auto_group
		BEFORE INSERT ON url_pattern_groups
		WHEN NEW.source = 'auto'
		BEGIN
			SELECT RAISE(ABORT, 'forced group rebuild failure');
		END
	`)
	if err != nil {
		t.Fatalf("creating failure trigger: %v", err)
	}

	if err := DetectGroups(db, jobID, nil); err == nil {
		t.Fatal("expected DetectGroups to fail")
	}

	var groupCount int
	db.QueryRow(`SELECT COUNT(*) FROM url_pattern_groups WHERE job_id = ? AND source = 'auto' AND pattern = '/blog'`, jobID).Scan(&groupCount)
	if groupCount != 1 {
		t.Errorf("expected previous group to survive rollback, got %d", groupCount)
	}

	var assigned int
	db.QueryRow(`SELECT COUNT(*) FROM pages WHERE job_id = ? AND url_group = 'blog'`, jobID).Scan(&assigned)
	if assigned != 15 {
		t.Errorf("expected previous page assignments to survive rollback, got %d", assigned)
	}
}

func TestExtractPattern(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://example.com/blog/post-1", "/blog"},
		{"https://example.com/docs/api/v2", "/docs"},
		{"https://example.com/", "/"},
		{"https://example.com/about", "/about"},
	}
	for _, tt := range tests {
		got := extractPattern(tt.url)
		if got != tt.want {
			t.Errorf("extractPattern(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}
