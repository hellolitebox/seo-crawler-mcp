package fetcher

import (
	"context"
	"sync"
	"time"
)

const ttfbRingSize = 10

// hostState holds the semaphore and crawl-delay state for a single host.
type hostState struct {
	sem       chan struct{}
	mu        sync.Mutex
	delay     time.Duration
	lastFetch time.Time

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
	if state.delay > 0 && !state.lastFetch.IsZero() {
		elapsed := time.Since(state.lastFetch)
		if elapsed < state.delay {
			wait := state.delay - elapsed
			state.mu.Unlock()
			select {
			case <-ctx.Done():
				state.sem <- struct{}{}
				return ctx.Err()
			case <-time.After(wait):
			}
			state.mu.Lock()
		}
	}
	state.lastFetch = time.Now()
	state.mu.Unlock()

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
	state.delay = delay
	state.mu.Unlock()
}

// ThrottleHost temporarily reduces the host to a single concurrent slot and
// enforces the given delay before the next request. The original concurrency
// is restored after the delay expires.
func (rl *RateLimiter) ThrottleHost(host string, dur time.Duration) {
	state := rl.getState(host)
	state.mu.Lock()
	defer state.mu.Unlock()

	// Set crawl delay to the throttle duration.
	if dur > state.delay {
		state.delay = dur
	}

	// Schedule restoration of the original delay after the throttle period.
	go func() {
		time.Sleep(dur)
		state.mu.Lock()
		// Only reset if it hasn't been set to something larger in the meantime.
		state.delay = 0
		state.mu.Unlock()
	}()
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
