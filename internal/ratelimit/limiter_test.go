package ratelimit

import (
	"testing"
	"time"
)

func TestNew_DefaultValues(t *testing.T) {
	l := New("test", Config{
		TotalRPS: 100,
		PerIPRPS: 10,
		Burst:    20,
	})
	defer l.Stop()

	if l.config.CleanupInterval != DefaultCleanupInterval {
		t.Errorf("expected default cleanup interval %v, got %v", DefaultCleanupInterval, l.config.CleanupInterval)
	}
	if l.config.IdleTimeout != DefaultIdleTimeout {
		t.Errorf("expected default idle timeout %v, got %v", DefaultIdleTimeout, l.config.IdleTimeout)
	}
}

func TestNew_CustomValues(t *testing.T) {
	l := New("test", Config{
		TotalRPS:        100,
		PerIPRPS:        10,
		Burst:           20,
		CleanupInterval: 1 * time.Minute,
		IdleTimeout:     2 * time.Minute,
	})
	defer l.Stop()

	if l.config.CleanupInterval != 1*time.Minute {
		t.Errorf("expected cleanup interval 1m, got %v", l.config.CleanupInterval)
	}
	if l.config.IdleTimeout != 2*time.Minute {
		t.Errorf("expected idle timeout 2m, got %v", l.config.IdleTimeout)
	}
}

func TestLimiter_Name(t *testing.T) {
	l := New("my-limiter", Config{
		TotalRPS: 100,
		Burst:    10,
	})
	defer l.Stop()

	if l.Name() != "my-limiter" {
		t.Errorf("expected name 'my-limiter', got %q", l.Name())
	}
}

func TestLimiter_Allow_TotalLimit(t *testing.T) {
	l := New("test", Config{
		TotalRPS: 1, // 1 request per second
		Burst:    1, // Only 1 burst
		PerIPRPS: 0, // No per-IP limiting
	})
	defer l.Stop()

	// First request should be allowed
	allowed, reason := l.Allow("192.168.1.1")
	if !allowed {
		t.Errorf("expected first request to be allowed, got denied with reason: %s", reason)
	}

	// Second request should be denied (exceeded burst)
	allowed, reason = l.Allow("192.168.1.1")
	if allowed {
		t.Error("expected second request to be denied due to total limit")
	}
	if reason != "total" {
		t.Errorf("expected reason 'total', got %q", reason)
	}
}

func TestLimiter_Allow_PerIPLimit(t *testing.T) {
	l := New("test", Config{
		TotalRPS: 10000, // Very high total limit (refills quickly)
		PerIPRPS: 1,     // 1 request per second per IP
		Burst:    1,     // Only 1 burst
	})
	defer l.Stop()

	// First request from IP1 should be allowed
	allowed, reason := l.Allow("192.168.1.1")
	if !allowed {
		t.Errorf("expected first request from IP1 to be allowed, got denied with reason: %s", reason)
	}

	// Brief pause to let total limiter refill (10000 RPS = 10 tokens/ms)
	time.Sleep(1 * time.Millisecond)

	// Second request from IP1 should be denied by per-IP limit
	// (total has refilled, but per-IP with 1 RPS hasn't)
	allowed, reason = l.Allow("192.168.1.1")
	if allowed {
		t.Error("expected second request from IP1 to be denied")
	}
	if reason != "per_ip" {
		t.Errorf("expected reason 'per_ip', got %q", reason)
	}

	// Brief pause to let total limiter refill again
	time.Sleep(1 * time.Millisecond)

	// First request from IP2 should be allowed
	// (total has refilled, and IP2 has its own fresh per-IP limiter)
	allowed, reason = l.Allow("192.168.1.2")
	if !allowed {
		t.Errorf("expected first request from IP2 to be allowed, got denied with reason: %s", reason)
	}
}

func TestLimiter_Allow_NoPerIPLimit(t *testing.T) {
	l := New("test", Config{
		TotalRPS: 100,
		PerIPRPS: 0, // Per-IP limiting disabled
		Burst:    100,
	})
	defer l.Stop()

	// Multiple requests from same IP should all be allowed (up to total limit)
	for i := 0; i < 50; i++ {
		allowed, reason := l.Allow("192.168.1.1")
		if !allowed {
			t.Errorf("request %d: expected to be allowed, got denied with reason: %s", i, reason)
		}
	}
}

func TestLimiter_Allow_BurstHandling(t *testing.T) {
	l := New("test", Config{
		TotalRPS: 10000, // Very high total limit (refills quickly)
		PerIPRPS: 10,
		Burst:    5, // Allow 5 requests burst
	})
	defer l.Stop()

	// Should allow burst of 5 requests
	for i := 0; i < 5; i++ {
		allowed, reason := l.Allow("192.168.1.1")
		if !allowed {
			t.Errorf("burst request %d: expected to be allowed, got denied with reason: %s", i, reason)
		}
	}

	// Brief pause to let total limiter refill
	time.Sleep(1 * time.Millisecond)

	// 6th request should be denied by per-IP limit
	// (total has refilled, but per-IP burst is exhausted)
	allowed, reason := l.Allow("192.168.1.1")
	if allowed {
		t.Error("expected 6th request to be denied (burst exceeded)")
	}
	if reason != "per_ip" {
		t.Errorf("expected reason 'per_ip', got %q", reason)
	}
}

func TestLimiter_PerIPCount(t *testing.T) {
	l := New("test", Config{
		TotalRPS:        100,
		PerIPRPS:        10,
		Burst:           10,
		CleanupInterval: 1 * time.Hour, // Long interval so no cleanup during test
		IdleTimeout:     1 * time.Hour,
	})
	defer l.Stop()

	if l.PerIPCount() != 0 {
		t.Errorf("expected 0 per-IP entries, got %d", l.PerIPCount())
	}

	l.Allow("192.168.1.1")
	if l.PerIPCount() != 1 {
		t.Errorf("expected 1 per-IP entry, got %d", l.PerIPCount())
	}

	l.Allow("192.168.1.2")
	if l.PerIPCount() != 2 {
		t.Errorf("expected 2 per-IP entries, got %d", l.PerIPCount())
	}

	// Same IP should not create new entry
	l.Allow("192.168.1.1")
	if l.PerIPCount() != 2 {
		t.Errorf("expected still 2 per-IP entries, got %d", l.PerIPCount())
	}
}

func TestLimiter_Cleanup(t *testing.T) {
	l := New("test", Config{
		TotalRPS:        100,
		PerIPRPS:        10,
		Burst:           10,
		CleanupInterval: 10 * time.Millisecond,
		IdleTimeout:     20 * time.Millisecond,
	})
	defer l.Stop()

	// Create some per-IP entries
	l.Allow("192.168.1.1")
	l.Allow("192.168.1.2")

	if l.PerIPCount() != 2 {
		t.Errorf("expected 2 per-IP entries, got %d", l.PerIPCount())
	}

	// Wait for idle timeout + cleanup interval
	time.Sleep(50 * time.Millisecond)

	// Entries should be cleaned up
	if l.PerIPCount() != 0 {
		t.Errorf("expected 0 per-IP entries after cleanup, got %d", l.PerIPCount())
	}
}

func TestLimiter_Cleanup_ActiveEntriesNotRemoved(t *testing.T) {
	l := New("test", Config{
		TotalRPS:        100,
		PerIPRPS:        10,
		Burst:           10,
		CleanupInterval: 10 * time.Millisecond,
		IdleTimeout:     50 * time.Millisecond,
	})
	defer l.Stop()

	// Create entries
	l.Allow("192.168.1.1")
	l.Allow("192.168.1.2")

	// Keep one IP active
	for i := 0; i < 5; i++ {
		time.Sleep(15 * time.Millisecond)
		l.Allow("192.168.1.1") // Refresh IP1
	}

	// IP1 should still exist, IP2 should be cleaned up
	count := l.PerIPCount()
	if count != 1 {
		t.Errorf("expected 1 per-IP entry (active one), got %d", count)
	}
}

func TestLimiter_Stop(t *testing.T) {
	l := New("test", Config{
		TotalRPS:        100,
		PerIPRPS:        10,
		Burst:           10,
		CleanupInterval: 10 * time.Millisecond,
		IdleTimeout:     10 * time.Millisecond,
	})

	// Stop should be idempotent
	l.Stop()
	l.Stop() // Should not panic
}

func TestLimiter_NoPerIPCleanupGoroutine(t *testing.T) {
	// When PerIPRPS is 0, no cleanup goroutine should be started
	l := New("test", Config{
		TotalRPS: 100,
		PerIPRPS: 0, // Per-IP limiting disabled
		Burst:    10,
	})
	defer l.Stop()

	// This shouldn't cause any issues - no cleanup goroutine running
	time.Sleep(10 * time.Millisecond)
}

func TestLimiter_TotalLimitBeforePerIP(t *testing.T) {
	// Test that total limit is checked before per-IP limit
	l := New("test", Config{
		TotalRPS: 1, // Very low total limit
		PerIPRPS: 100,
		Burst:    1,
	})
	defer l.Stop()

	// First request allowed
	allowed, _ := l.Allow("192.168.1.1")
	if !allowed {
		t.Error("expected first request to be allowed")
	}

	// Second request should be denied by TOTAL limit (not per-IP)
	allowed, reason := l.Allow("192.168.1.2") // Different IP
	if allowed {
		t.Error("expected second request to be denied")
	}
	if reason != "total" {
		t.Errorf("expected reason 'total' (total should be checked first), got %q", reason)
	}
}
