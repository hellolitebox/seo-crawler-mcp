package fetcher

import (
	"context"
	"sync"
	"time"
)

const ttfbRingSize = 10

// hostState holds the semaphore and crawl-delay state for a single host.
type hostState struct {
	sem             chan struct{}
	mu              sync.Mutex
	baseDelay       time.Duration
	throttleDelay   time.Duration
	throttleUntil   time.Time
	throttleVersion uint64
	lastFetch       time.Time

	// TTFB ring buffer for slow-host detection.
	ttfbRing  [ttfbRingSize]int64
	ttfbCount int
	ttfbIdx   int
}

// RateLimiter enforces per-host concurrency limits and crawl delays.
type RateLimiter struct {
	perHost int
	hosts   sync.Map // map[string]*hostState
}

// NewRateLimiter creates a rate limiter with the given per-host concurrency.
func NewRateLimiter(perHostConcurrency int) *RateLimiter {
	if perHostConcurrency < 1 {
		perHostConcurrency = 1
	}
	return &RateLimiter{
		perHost: perHostConcurrency,
	}
}

// Reset clears all per-host state (semaphores, crawl delays, TTFB
// samples). Call between crawls when the same RateLimiter instance is
// shared across jobs to avoid unbounded growth and to prevent stale
// crawl-delay or slow-host data from leaking across jobs.
//
// Not safe to call while a crawl is in progress: existing AcquireContext
// callers hold references to hostState values that would be orphaned.
func (rl *RateLimiter) Reset() {
	rl.hosts.Range(func(k, _ any) bool {
		rl.hosts.Delete(k)
		return true
	})
}

func (rl *RateLimiter) getState(host string) *hostState {
	val, ok := rl.hosts.Load(host)
	if ok {
		return val.(*hostState)
	}

	sem := make(chan struct{}, rl.perHost)
	for range rl.perHost {
		sem <- struct{}{}
	}

	state := &hostState{sem: sem}
	actual, loaded := rl.hosts.LoadOrStore(host, state)
	if loaded {
		return actual.(*hostState)
	}
	return state
}

// Acquire blocks until a slot is available for the host, respecting crawl delay.
func (rl *RateLimiter) Acquire(host string) {
	_ = rl.AcquireContext(context.Background(), host)
}

// AcquireContext blocks until a slot is available for the host, respecting crawl delay.
// If the context is cancelled while waiting, no slot is consumed.
func (rl *RateLimiter) AcquireContext(ctx context.Context, host string) error {
	state := rl.getState(host)

	// Wait for semaphore slot.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-state.sem:
	}

	// Enforce crawl delay.
	state.mu.Lock()
	defer state.mu.Unlock()
	delay := state.effectiveDelayLocked(time.Now())
	if delay > 0 && !state.lastFetch.IsZero() {
		elapsed := time.Since(state.lastFetch)
		if elapsed < delay {
			wait := delay - elapsed
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				state.sem <- struct{}{}
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	state.lastFetch = time.Now()

	return nil
}

// Release returns a slot for the host.
func (rl *RateLimiter) Release(host string) {
	state := rl.getState(host)
	state.sem <- struct{}{}
}

// SetCrawlDelay sets the minimum delay between requests to a host (from robots.txt).
func (rl *RateLimiter) SetCrawlDelay(host string, delay time.Duration) {
	state := rl.getState(host)
	state.mu.Lock()
	state.baseDelay = delay
	state.mu.Unlock()
}

// ThrottleHost temporarily extends the host's crawl delay to dur (if
// it's greater than the current delay) and schedules its restoration
// after that period. Uses time.AfterFunc so the timer lives in the
// runtime timer wheel — no blocked goroutine per throttled host, and
// the timer is cheap to leave running if the crawl ends mid-throttle.
func (rl *RateLimiter) ThrottleHost(host string, dur time.Duration) {
	if dur <= 0 {
		return
	}
	state := rl.getState(host)
	now := time.Now()
	expires := now.Add(dur)
	state.mu.Lock()
	if dur <= state.throttleDelay && !expires.After(state.throttleUntil) {
		state.mu.Unlock()
		return
	}
	if dur > state.throttleDelay {
		state.throttleDelay = dur
	}
	if expires.After(state.throttleUntil) {
		state.throttleUntil = expires
	}
	state.throttleVersion++
	version := state.throttleVersion
	wait := time.Until(state.throttleUntil)
	state.mu.Unlock()

	time.AfterFunc(wait, func() {
		state.mu.Lock()
		if state.throttleVersion == version && !time.Now().Before(state.throttleUntil) {
			state.throttleDelay = 0
			state.throttleUntil = time.Time{}
		}
		state.mu.Unlock()
	})
}

func (state *hostState) effectiveDelayLocked(now time.Time) time.Duration {
	if !state.throttleUntil.IsZero() && !now.Before(state.throttleUntil) {
		state.throttleDelay = 0
		state.throttleUntil = time.Time{}
	}
	if state.throttleDelay > state.baseDelay {
		return state.throttleDelay
	}
	return state.baseDelay
}

// RecordTTFB records a TTFB sample for a host and returns the average TTFB in
// milliseconds once the ring buffer is full (ttfbRingSize samples). Returns
// (avg, true) when full, (0, false) otherwise.
func (rl *RateLimiter) RecordTTFB(host string, ttfbMS int64) (avgMS int64, full bool) {
	state := rl.getState(host)
	state.mu.Lock()
	defer state.mu.Unlock()

	state.ttfbRing[state.ttfbIdx] = ttfbMS
	state.ttfbIdx = (state.ttfbIdx + 1) % ttfbRingSize
	if state.ttfbCount < ttfbRingSize {
		state.ttfbCount++
	}

	if state.ttfbCount < ttfbRingSize {
		return 0, false
	}

	var sum int64
	for _, v := range state.ttfbRing {
		sum += v
	}
	return sum / ttfbRingSize, true
}
