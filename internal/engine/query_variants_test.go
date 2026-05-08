package engine

import (
	"sync"
	"testing"
)

func TestQueryVariantsTracker_DedupesAndEmitsOnce(t *testing.T) {
	tr := newQueryVariantsTracker()

	const path = "/search"
	const threshold = 3

	// Same query observed many times: should count as 1 unique variant
	// and never emit an issue (well below threshold).
	for i := 0; i < 100; i++ {
		count, shouldEmit := tr.observe(path, "q=hello", threshold)
		if count != 1 {
			t.Fatalf("iter %d: count=%d, want 1 (same query reused)", i, count)
		}
		if shouldEmit {
			t.Fatalf("iter %d: shouldEmit=true with count=1; threshold=%d", i, threshold)
		}
	}

	// Distinct queries up to and including the threshold: no emit yet.
	queries := []string{"q=a", "q=b"} // 2 more uniques → total 3 = threshold
	for _, q := range queries {
		_, shouldEmit := tr.observe(path, q, threshold)
		if shouldEmit {
			t.Fatalf("emitted at or below threshold for %q", q)
		}
	}

	// (threshold + 1)-th unique variant: emit exactly once.
	count, shouldEmit := tr.observe(path, "q=x", threshold)
	if count != 4 {
		t.Fatalf("count=%d, want 4 (one over threshold)", count)
	}
	if !shouldEmit {
		t.Fatalf("expected shouldEmit=true on the boundary crossing")
	}

	// Further uniques over threshold: no more emits.
	for _, q := range []string{"q=y", "q=z", "q=w"} {
		_, shouldEmit := tr.observe(path, q, threshold)
		if shouldEmit {
			t.Fatalf("emitted again for %q after path was already flagged", q)
		}
	}
}

func TestQueryVariantsTracker_PerPathIsolation(t *testing.T) {
	tr := newQueryVariantsTracker()
	const threshold = 1

	// /a crosses threshold; /b should still be allowed up to its own threshold.
	if _, emit := tr.observe("/a", "q=1", threshold); emit {
		t.Fatalf("first observation must not emit (count=1=threshold)")
	}
	if _, emit := tr.observe("/a", "q=2", threshold); !emit {
		t.Fatalf("second unique query on /a should emit (crosses threshold=1)")
	}
	if _, emit := tr.observe("/b", "q=1", threshold); emit {
		t.Fatalf("/b should be tracked independently from /a")
	}
}

func TestQueryVariantsTracker_ConcurrentAccess(t *testing.T) {
	tr := newQueryVariantsTracker()
	const path = "/search"
	const threshold = 100
	const goroutines = 16
	const perGoroutine = 50

	var wg sync.WaitGroup
	emits := make(chan struct{}, goroutines*perGoroutine)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				// Each goroutine produces a disjoint set of queries to
				// guarantee uniqueness.
				q := []byte{byte('a' + g), byte('0' + (i % 10)), byte('0' + (i / 10))}
				_, emit := tr.observe(path, string(q), threshold)
				if emit {
					emits <- struct{}{}
				}
			}
		}(g)
	}
	wg.Wait()
	close(emits)

	count := 0
	for range emits {
		count++
	}
	if count != 1 {
		t.Fatalf("expected exactly one emit across goroutines, got %d", count)
	}
}
