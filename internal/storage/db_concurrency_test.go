package storage

import (
	"testing"
	"time"
)

func TestOpenAllowsReadsWhileWriteTransactionIsOpen(t *testing.T) {
	db := openTempDB(t)

	if _, err := db.Exec(`
		INSERT INTO crawl_jobs (id, type, status, config_json, seed_urls)
		VALUES ('concurrency-job', 'crawl', 'completed', '{}', '[]')
	`); err != nil {
		t.Fatalf("insert job: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin write tx: %v", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE crawl_jobs SET issues_found = issues_found + 1 WHERE id = 'concurrency-job'`); err != nil {
		t.Fatalf("write in tx: %v", err)
	}

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := db.CountJobs()
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CountJobs during write tx: %v", err)
		}
		if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
			t.Fatalf("read blocked behind write tx for %v", elapsed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("read blocked behind open write transaction; SQLite connection pool is too serialized")
	}
}
