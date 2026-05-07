package storage

import (
	"testing"
	"time"
)

func TestInsertIssuesBatch_InsertsAll(t *testing.T) {
	db := openTempDB(t)
	if _, err := db.Exec(`
		INSERT INTO crawl_jobs (id, type, status, config_json, seed_urls)
		VALUES ('j1', 'spider', 'completed', '{}', '[]')
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const N = 1500
	batch := make([]IssueInput, N)
	for i := 0; i < N; i++ {
		ds := `{"i":` + itoaQuick(i) + `}`
		batch[i] = IssueInput{
			JobID:       "j1",
			IssueType:   "test_issue",
			Severity:    "info",
			Scope:       "page_local",
			DetailsJSON: &ds,
		}
	}

	start := time.Now()
	if err := db.InsertIssuesBatch(batch); err != nil {
		t.Fatalf("InsertIssuesBatch: %v", err)
	}
	elapsed := time.Since(start)
	t.Logf("inserted %d issues in %v", N, elapsed)

	var n int
	db.QueryRow(`SELECT COUNT(*) FROM issues WHERE job_id = 'j1'`).Scan(&n)
	if n != N {
		t.Fatalf("expected %d rows, got %d", N, n)
	}

	// Sanity: the batched insert should be way under what 1500 individual
	// txns would cost. Allow generous slack for slow CI.
	if elapsed > 3*time.Second {
		t.Errorf("batch insert took %v; expected well under 3s for %d rows", elapsed, N)
	}
}

func TestInsertIssuesBatch_EmptyIsNoOp(t *testing.T) {
	db := openTempDB(t)
	if err := db.InsertIssuesBatch(nil); err != nil {
		t.Fatalf("nil batch should be a no-op, got: %v", err)
	}
	if err := db.InsertIssuesBatch([]IssueInput{}); err != nil {
		t.Fatalf("empty batch should be a no-op, got: %v", err)
	}
}

func itoaQuick(n int) string {
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
