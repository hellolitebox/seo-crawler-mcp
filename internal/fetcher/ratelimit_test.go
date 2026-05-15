package fetcher

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRateLimiter_Concurrency(t *testing.T) {
	rl := NewRateLimiter(2)
	host := "example.com"

	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	var wg sync.WaitGroup

	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rl.Acquire(host)
			defer rl.Release(host)

			cur := concurrent.Add(1)
			for {
				old := maxConcurrent.Load()
				if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			concurrent.Add(-1)
		}()
	}

	wg.Wait()

	got := maxConcurrent.Load()
	if got > 2 {
		t.Errorf("max concurrent = %d, want <= 2", got)
	}
	if got < 1 {
		t.Errorf("max concurrent = %d, want >= 1", got)
	}
}

func TestRateLimiter_RecordTTFB(t *testing.T) {
	rl := NewRateLimiter(1)
	host := "ttfb.example.com"

	// First 9 samples: not full yet
	for i := range 9 {
		avg, full := rl.RecordTTFB(host, int64(6000+i))
		if full {
			t.Fatalf("expected not full at sample %d", i+1)
		}
		if avg != 0 {
			t.Fatalf("expected avg=0 when not full, got %d", avg)
		}
	}

	// 10th sample: full, avg should be computed
	avg, full := rl.RecordTTFB(host, 6000)
	if !full {
		t.Fatal("expected full after 10 samples")
	}
	if avg < 6000 {
		t.Errorf("expected avg >= 6000, got %d", avg)
	}

	// Another host: not affected
	_, full2 := rl.RecordTTFB("other.example.com", 100)
	if full2 {
		t.Error("other host should not be full after 1 sample")
	}
}

func TestRateLimiter_RecordTTFB_LowValues(t *testing.T) {
	rl := NewRateLimiter(1)
	host := "fast.example.com"

	for range 10 {
		rl.RecordTTFB(host, 100)
	}
	avg, full := rl.RecordTTFB(host, 100)
	if !full {
		t.Fatal("expected full")
	}
	if avg > 5000 {
		t.Errorf("expected avg <= 5000 for fast host, got %d", avg)
	}
}

func TestRateLimiter_CrawlDelay(t *testing.T) {
	rl := NewRateLimiter(1)
	host := "slow.example.com"
	rl.SetCrawlDelay(host, 100*time.Millisecond)

	rl.Acquire(host)
	rl.Release(host)

	start := time.Now()
	rl.Acquire(host)
	rl.Release(host)
	elapsed := time.Since(start)

	if elapsed < 80*time.Millisecond {
		t.Errorf("elapsed = %v, want >= ~100ms (crawl delay)", elapsed)
	}
}

func TestRateLimiter_ConcurrentCrawlDelayWaitersAreSpaced(t *testing.T) {
	rl := NewRateLimiter(3)
	host := "concurrent-delay.example.com"
	rl.SetCrawlDelay(host, 60*time.Millisecond)

	rl.Acquire(host)
	rl.Release(host)

	start := time.Now()
	times := make(chan time.Duration, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := rl.AcquireContext(context.Background(), host); err != nil {
				t.Errorf("AcquireContext: %v", err)
				return
			}
			times <- time.Since(start)
			rl.Release(host)
		}()
	}
	wg.Wait()
	close(times)

	got := make([]time.Duration, 0, 2)
	for tm := range times {
		got = append(got, tm)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 acquire times, got %d", len(got))
	}
	if got[0] > got[1] {
		got[0], got[1] = got[1], got[0]
	}
	if got[1]-got[0] < 45*time.Millisecond {
		t.Fatalf("concurrent waiters were not spaced by crawl delay: %v", got)
	}
}

func TestRateLimiter_ThrottleDoesNotEraseLongerCrawlDelay(t *testing.T) {
	rl := NewRateLimiter(1)
	host := "throttle-baseline.example.com"
	rl.SetCrawlDelay(host, 100*time.Millisecond)
	rl.ThrottleHost(host, 20*time.Millisecond)
	time.Sleep(40 * time.Millisecond)

	state := rl.getState(host)
	state.mu.Lock()
	delay := state.effectiveDelay(time.Now())
	state.mu.Unlock()
	if delay != 100*time.Millisecond {
		t.Fatalf("effective delay = %v, want crawl delay preserved", delay)
	}
}

func TestRateLimiter_OverlappingThrottleKeepsLongerTimer(t *testing.T) {
	rl := NewRateLimiter(1)
	host := "throttle-overlap.example.com"
	rl.ThrottleHost(host, 100*time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	rl.ThrottleHost(host, 200*time.Millisecond)
	time.Sleep(120 * time.Millisecond)

	state := rl.getState(host)
	state.mu.Lock()
	delay := state.effectiveDelay(time.Now())
	state.mu.Unlock()
	if delay != 200*time.Millisecond {
		t.Fatalf("effective delay = %v, want longer throttle still active", delay)
	}
}
