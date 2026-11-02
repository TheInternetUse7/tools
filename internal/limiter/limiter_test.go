package limiter_test

import (
	"testing"
	"time"

	"github.com/TheInternetUse7/tools/internal/limiter"
)

func TestConcurrencyLimiter(t *testing.T) {
	c := limiter.NewConcurrencyLimiter(2)

	if !c.TryAcquire() {
		t.Fatal("expected slot 1 to be acquired")
	}
	if !c.TryAcquire() {
		t.Fatal("expected slot 2 to be acquired")
	}
	if c.TryAcquire() {
		t.Fatal("expected slot 3 to be rejected because capacity is 2")
	}

	if c.Active() != 2 {
		t.Fatalf("expected 2 active slots, got %d", c.Active())
	}

	c.Release()
	if c.Active() != 1 {
		t.Fatalf("expected 1 active slot after release, got %d", c.Active())
	}

	if !c.TryAcquire() {
		t.Fatal("expected to re-acquire released slot")
	}

	c.Release()
	c.Release()
	if c.Active() != 0 {
		t.Fatalf("expected 0 active slots, got %d", c.Active())
	}
}

func TestIPRateLimiter(t *testing.T) {
	window := 200 * time.Millisecond
	lim := limiter.NewIPRateLimiter(3, window)
	defer lim.Stop()

	ip := "192.168.1.100"

	// Request 1, 2, 3 should succeed
	for i := 1; i <= 3; i++ {
		allowed, rem, _ := lim.Allow(ip)
		if !allowed {
			t.Fatalf("request %d should be allowed", i)
		}
		if rem != 3-i {
			t.Fatalf("expected remaining %d, got %d", 3-i, rem)
		}
	}

	// Request 4 should be blocked
	allowed, rem, resetIn := lim.Allow(ip)
	if allowed {
		t.Fatal("request 4 should be rejected by rate limiter")
	}
	if rem != 0 {
		t.Fatalf("expected remaining 0, got %d", rem)
	}
	if resetIn <= 0 {
		t.Fatal("expected positive reset duration")
	}

	// Another IP should still be allowed
	otherIP := "10.0.0.1"
	allowed, _, _ = lim.Allow(otherIP)
	if !allowed {
		t.Fatal("other IP should not be blocked")
	}

	// Wait for window to expire
	time.Sleep(window + 20*time.Millisecond)

	// Now original IP should be allowed again
	allowed, _, _ = lim.Allow(ip)
	if !allowed {
		t.Fatal("expected IP to be allowed after window expires")
	}
}
