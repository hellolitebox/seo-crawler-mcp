package httpserver

import (
	"testing"
	"time"
)

func TestRateLimiter_AllowsUpToLimit(t *testing.T) {
	rl := newRateLimiter(3, time.Hour)

	for i := 0; i < 3; i++ {
		if !rl.allow("alice") {
			t.Fatalf("expected allow on attempt %d", i+1)
		}
	}
	if rl.allow("alice") {
		t.Fatalf("expected deny after limit reached")
	}
}

func TestRateLimiter_PerKey(t *testing.T) {
	rl := newRateLimiter(1, time.Hour)

	if !rl.allow("alice") {
		t.Fatalf("alice first request should pass")
	}
	if rl.allow("alice") {
		t.Fatalf("alice second should be denied")
	}
	if !rl.allow("bob") {
		t.Fatalf("bob should not be affected by alice's window")
	}
}

func TestRateLimiter_WindowReset(t *testing.T) {
	rl := newRateLimiter(2, 10*time.Millisecond)

	if !rl.allow("alice") || !rl.allow("alice") {
		t.Fatalf("alice should consume both slots")
	}
	if rl.allow("alice") {
		t.Fatalf("alice over limit before window expires")
	}

	time.Sleep(15 * time.Millisecond)

	if !rl.allow("alice") {
		t.Fatalf("window should have reset by now")
	}
}
