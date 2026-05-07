package httpserver

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

func newWorkerDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestPurgeWorker_DrainsQueueSequentially(t *testing.T) {
	db := newWorkerDB(t)

	// Seed 5 jobs.
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if _, err := db.Exec(`
			INSERT INTO crawl_jobs (id, type, status, config_json, seed_urls)
			VALUES (?, 'spider', 'completed', '{}', '[]')
		`, id); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	w := newPurgeWorker(db)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		w.enqueue(id)
	}

	// Wait until they're all gone (or timeout).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM crawl_jobs`).Scan(&n)
		if n == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected all 5 jobs to be purged within 3s")
}

func TestPurgeChunked_HandlesLargeJob(t *testing.T) {
	db := newWorkerDB(t)

	if _, err := db.Exec(`
		INSERT INTO crawl_jobs (id, type, status, config_json, seed_urls)
		VALUES ('big', 'spider', 'completed', '{}', '[]')
	`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Insert ~12K urls so chunking kicks in (chunk size = 5000).
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO urls (job_id, normalized_url, host, status, is_internal, discovered_via) VALUES (?, ?, 'example.com', 'fetched', 1, 'seed')`)
	if err != nil {
		tx.Rollback()
		t.Fatalf("prepare: %v", err)
	}
	for i := 0; i < 12000; i++ {
		if _, err := stmt.Exec("big", "https://example.com/path/"+itoa(i)); err != nil {
			tx.Rollback()
			t.Fatalf("insert url: %v", err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	start := time.Now()
	if err := db.PurgeJob("big"); err != nil {
		t.Fatalf("PurgeJob: %v", err)
	}
	t.Logf("purged 12K-url job in %v", time.Since(start))

	var n int
	db.QueryRow(`SELECT COUNT(*) FROM urls WHERE job_id = 'big'`).Scan(&n)
	if n != 0 {
		t.Fatalf("expected all urls purged, got %d remaining", n)
	}
	if _, err := db.GetJob("big"); err == nil {
		t.Fatal("expected job row purged")
	}
}

// tiny itoa to avoid pulling strconv into this test only for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
