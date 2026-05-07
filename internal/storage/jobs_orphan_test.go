package storage

import (
	"path/filepath"
	"testing"
)

func openTempDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMarkOrphanedJobsFailed(t *testing.T) {
	db := openTempDB(t)

	insert := func(id, status string) {
		if _, err := db.Exec(`
			INSERT INTO crawl_jobs (id, type, status, config_json, seed_urls)
			VALUES (?, 'spider', ?, '{}', '[]')
		`, id, status); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	insert("running-job", "running")
	insert("queued-job", "queued")
	insert("cancelling-job", "cancelling")
	insert("done-job", "completed")
	insert("dead-job", "failed")

	n, err := db.MarkOrphanedJobsFailed("server restarted")
	if err != nil {
		t.Fatalf("MarkOrphanedJobsFailed: %v", err)
	}
	// Queued jobs survive restart (the queue worker picks them up); only
	// running + cancelling jobs are reaped.
	if n != 2 {
		t.Fatalf("expected 2 jobs reaped, got %d", n)
	}

	// Verify the orphans are now failed with the reason set.
	for _, id := range []string{"running-job", "cancelling-job"} {
		j, err := db.GetJob(id)
		if err != nil {
			t.Fatalf("GetJob(%s): %v", id, err)
		}
		if j.Status != "failed" {
			t.Errorf("%s: expected status=failed, got %s", id, j.Status)
		}
		if !j.Error.Valid || j.Error.String != "server restarted" {
			t.Errorf("%s: expected error='server restarted', got %v", id, j.Error)
		}
		if !j.FinishedAt.Valid {
			t.Errorf("%s: expected finished_at to be set", id)
		}
	}

	// Queued, completed and already-failed jobs should not be touched.
	if j, _ := db.GetJob("queued-job"); j.Status != "queued" {
		t.Errorf("queued-job status changed unexpectedly: %s", j.Status)
	}
	if j, _ := db.GetJob("done-job"); j.Status != "completed" {
		t.Errorf("done-job status changed unexpectedly: %s", j.Status)
	}
	if j, _ := db.GetJob("dead-job"); j.Status != "failed" {
		t.Errorf("dead-job status changed unexpectedly: %s", j.Status)
	}
}

func TestMarkOrphanedJobsFailed_NoOrphans(t *testing.T) {
	db := openTempDB(t)
	n, err := db.MarkOrphanedJobsFailed("nope")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 reaped on empty db, got %d", n)
	}
}
