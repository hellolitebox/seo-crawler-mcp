package storage

import (
	"os"
	"path/filepath"
	"testing"
)

// expectedTables lists every table the migration must create.
var expectedTables = []string{
	"schema_migrations",
	"crawl_jobs",
	"urls",
	"fetches",
	"pages",
	"edges",
	"redirect_hops",
	"sitemap_entries",
	"robots_directives",
	"llms_findings",
	"assets",
	"asset_references",
	"issues",
	"crawl_events",
	"url_pattern_groups",
	"canonical_clusters",
	"canonical_cluster_members",
	"duplicate_clusters",
	"duplicate_cluster_members",
}

func TestOpenAndMigrate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", dbPath, err)
	}
	defer db.Close()

	// Verify all 19 tables exist.
	for _, table := range expectedTables {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?",
			table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}

	// Verify idempotent: opening again should not error.
	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second Open(%q) failed: %v", dbPath, err)
	}
	db2.Close()

	// Verify migrations were recorded.
	var versions int
	err = db.QueryRow(
		"SELECT COUNT(*) FROM schema_migrations WHERE version IN (1, 2)",
	).Scan(&versions)
	if err != nil {
		t.Fatalf("checking migration versions: %v", err)
	}
	if versions != 2 {
		t.Errorf("expected migrations 1 and 2 recorded, got %d", versions)
	}

	// Verify indexes required by hot live-activity paths exist.
	for _, index := range []string{"idx_fetches_job_id", "idx_crawl_events_job_type_id", "idx_issues_job_id"} {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='index' AND name=?",
			index,
		).Scan(&name)
		if err != nil {
			t.Errorf("index %q not found: %v", index, err)
		}
	}
}

func TestOpen_FailsWithMissingDir(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "nonexistent", "subdir", "test.db")

	_, err := Open(dbPath)
	if err == nil {
		t.Fatal("expected error for missing parent directory, got nil")
	}
}

func TestOpen_WorksWithExistingDir(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", dbPath, err)
	}
	db.Close()

	// Verify the file was actually created.
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("os.Stat(%q) failed: %v", dbPath, err)
	}
	if info.Size() == 0 {
		t.Error("database file is empty")
	}
}

func TestOpen_ForeignKeysEnabled(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", dbPath, err)
	}
	defer db.Close()

	var fkEnabled int
	err = db.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled)
	if err != nil {
		t.Fatalf("checking foreign_keys pragma: %v", err)
	}
	if fkEnabled != 1 {
		t.Errorf("foreign_keys = %d, want 1", fkEnabled)
	}
}

func TestOpen_WALMode(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", dbPath, err)
	}
	defer db.Close()

	var journalMode string
	err = db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("checking journal_mode pragma: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want %q", journalMode, "wal")
	}
}
