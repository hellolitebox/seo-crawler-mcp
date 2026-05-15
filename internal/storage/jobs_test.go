package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testDB creates a temporary SQLite database and returns a *DB.
// Cleanup is registered via t.Cleanup.
func testDB(t *testing.T) *DB {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("opening test database: %v", err)
	}

	t.Cleanup(func() {
		db.Close()
		os.RemoveAll(dir)
	})

	return db
}

func TestCreateAndGetJob(t *testing.T) {
	db := testDB(t)

	job, err := db.CreateJob("crawl", `{"maxPages":100}`, `["https://example.com"]`)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if job.ID == "" {
		t.Fatal("expected non-empty job ID")
	}
	if job.Status != "queued" {
		t.Errorf("expected status %q, got %q", "queued", job.Status)
	}
	if job.Type != "crawl" {
		t.Errorf("expected type %q, got %q", "crawl", job.Type)
	}
	if job.PagesCrawled != 0 {
		t.Errorf("expected pagesCrawled 0, got %d", job.PagesCrawled)
	}
	if job.URLsDiscovered != 0 {
		t.Errorf("expected urlsDiscovered 0, got %d", job.URLsDiscovered)
	}
	if job.IssuesFound != 0 {
		t.Errorf("expected issuesFound 0, got %d", job.IssuesFound)
	}

	got, err := db.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.ID != job.ID {
		t.Errorf("expected ID %q, got %q", job.ID, got.ID)
	}
	if got.ConfigJSON != `{"maxPages":100}` {
		t.Errorf("unexpected config: %q", got.ConfigJSON)
	}
}

func TestUpdateJobStatus(t *testing.T) {
	db := testDB(t)

	job, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if err := db.UpdateJobStatus(job.ID, "running"); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}

	got, err := db.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != "running" {
		t.Errorf("expected status %q, got %q", "running", got.Status)
	}
}

func TestUpdateJobStarted(t *testing.T) {
	db := testDB(t)

	job, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if err := db.UpdateJobStarted(job.ID); err != nil {
		t.Fatalf("UpdateJobStarted: %v", err)
	}

	got, err := db.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != "running" {
		t.Errorf("expected status %q, got %q", "running", got.Status)
	}
	if !got.StartedAt.Valid || got.StartedAt.String == "" {
		t.Error("expected started_at to be set")
	}
}

func TestUpdateJobFinished(t *testing.T) {
	db := testDB(t)

	job, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	errMsg := "timeout exceeded"
	if err := db.UpdateJobFinished(job.ID, "failed", &errMsg); err != nil {
		t.Fatalf("UpdateJobFinished: %v", err)
	}

	got, err := db.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != "failed" {
		t.Errorf("expected status %q, got %q", "failed", got.Status)
	}
	if !got.FinishedAt.Valid || got.FinishedAt.String == "" {
		t.Error("expected finished_at to be set")
	}
	if !got.Error.Valid || got.Error.String != "timeout exceeded" {
		t.Errorf("expected error %q, got %v", "timeout exceeded", got.Error)
	}

	// Test with nil error (successful completion).
	job2, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if err := db.UpdateJobFinished(job2.ID, "completed", nil); err != nil {
		t.Fatalf("UpdateJobFinished (nil err): %v", err)
	}

	got2, err := db.GetJob(job2.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got2.Status != "completed" {
		t.Errorf("expected status %q, got %q", "completed", got2.Status)
	}
	if got2.Error.Valid {
		t.Errorf("expected error to be NULL, got %q", got2.Error.String)
	}
}

func TestUpdateJobCounters(t *testing.T) {
	db := testDB(t)

	job, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if err := db.UpdateJobCounters(job.ID, 42, 150, 7); err != nil {
		t.Fatalf("UpdateJobCounters: %v", err)
	}

	got, err := db.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.PagesCrawled != 42 {
		t.Errorf("expected pagesCrawled 42, got %d", got.PagesCrawled)
	}
	if got.URLsDiscovered != 150 {
		t.Errorf("expected urlsDiscovered 150, got %d", got.URLsDiscovered)
	}
	if got.IssuesFound != 7 {
		t.Errorf("expected issuesFound 7, got %d", got.IssuesFound)
	}
}

func TestListJobs(t *testing.T) {
	db := testDB(t)

	_, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob 1: %v", err)
	}
	_, err = db.CreateJob("analyze", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob 2: %v", err)
	}

	jobs, err := db.ListJobs()
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Errorf("expected 2 jobs, got %d", len(jobs))
	}
}

func TestCountActiveJobs(t *testing.T) {
	db := testDB(t)

	_, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob 1: %v", err)
	}
	_, err = db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob 2: %v", err)
	}
	_, err = db.CreateJob("analyze", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob 3: %v", err)
	}

	count, err := db.CountActiveJobs("crawl")
	if err != nil {
		t.Fatalf("CountActiveJobs: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 active crawl jobs, got %d", count)
	}

	count, err = db.CountActiveJobs("analyze")
	if err != nil {
		t.Fatalf("CountActiveJobs: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 active analyze job, got %d", count)
	}
}

func TestCountJobsCreatedSince(t *testing.T) {
	db := testDB(t)

	// Create 3 jobs.
	_, err := db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob 1: %v", err)
	}
	_, err = db.CreateJob("crawl", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob 2: %v", err)
	}
	_, err = db.CreateJob("analyze", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob 3: %v", err)
	}

	// Count since 1 hour ago: should find all 3.
	hourAgo := time.Now().Add(-1 * time.Hour)
	count, err := db.CountJobsCreatedSince(hourAgo)
	if err != nil {
		t.Fatalf("CountJobsCreatedSince: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 jobs since 1h ago, got %d", count)
	}

	// Count since far future: should find 0.
	future := time.Now().Add(24 * time.Hour)
	count, err = db.CountJobsCreatedSince(future)
	if err != nil {
		t.Fatalf("CountJobsCreatedSince (future): %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 jobs since future, got %d", count)
	}
}

func TestCreateJobWithTTL(t *testing.T) {
	db := testDB(t)

	job, err := db.CreateJobWithTTL("analyze", `{"url":"https://example.com"}`, "[]", 24*time.Hour)
	if err != nil {
		t.Fatalf("CreateJobWithTTL: %v", err)
	}

	if job.ID == "" {
		t.Fatal("expected non-empty job ID")
	}
	if !job.TTLExpiresAt.Valid {
		t.Fatal("expected TTLExpiresAt to be set")
	}
	if job.TTLExpiresAt.String == "" {
		t.Fatal("expected non-empty TTLExpiresAt")
	}
}

func TestPurgeExpiredAnalyzeJobs(t *testing.T) {
	db := testDB(t)

	job, err := db.CreateJob("analyze", "{}", "[]")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	_, err = db.Exec(
		`UPDATE crawl_jobs SET ttl_expires_at = '2000-01-01T00:00:00.000Z' WHERE id = ?`,
		job.ID,
	)
	if err != nil {
		t.Fatalf("setting TTL: %v", err)
	}

	purged, err := db.PurgeExpiredAnalyzeJobs()
	if err != nil {
		t.Fatalf("PurgeExpiredAnalyzeJobs: %v", err)
	}
	if purged != 1 {
		t.Errorf("expected 1 purged, got %d", purged)
	}

	_, err = db.GetJob(job.ID)
	if err == nil {
		t.Error("expected error getting purged job, got nil")
	}
}
