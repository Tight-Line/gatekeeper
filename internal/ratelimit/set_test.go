package ratelimit

import (
	"sort"
	"testing"
)

func TestNewSet(t *testing.T) {
	s := NewSet()
	if s == nil {
		t.Fatal("expected non-nil Set")
	}
	if len(s.Names()) != 0 {
		t.Errorf("expected empty set, got %d limiters", len(s.Names()))
	}
}

func TestSet_AddAndGet(t *testing.T) {
	s := NewSet()
	defer s.Stop()

	limiter := New("test", Config{
		TotalRPS: 100,
		PerIPRPS: 10,
		Burst:    20,
	})

	s.Add("test", limiter)

	got := s.Get("test")
	if got == nil {
		t.Fatal("expected to get limiter, got nil")
	}
	if got.Name() != "test" {
		t.Errorf("expected limiter name 'test', got %q", got.Name())
	}
}

func TestSet_Get_NotFound(t *testing.T) {
	s := NewSet()

	got := s.Get("nonexistent")
	if got != nil {
		t.Errorf("expected nil for nonexistent limiter, got %v", got)
	}
}

func TestSet_Allow_LimiterExists(t *testing.T) {
	s := NewSet()
	defer s.Stop()

	limiter := New("test", Config{
		TotalRPS: 1,
		PerIPRPS: 1,
		Burst:    1,
	})
	s.Add("test", limiter)

	// First request should be allowed
	allowed, reason := s.Allow("test", "192.168.1.1")
	if !allowed {
		t.Errorf("expected first request to be allowed, got denied with reason: %s", reason)
	}

	// Second request should be denied
	allowed, _ = s.Allow("test", "192.168.1.1")
	if allowed {
		t.Error("expected second request to be denied")
	}
}

func TestSet_Allow_LimiterNotFound(t *testing.T) {
	s := NewSet()

	// Should fail open when limiter not found
	allowed, reason := s.Allow("nonexistent", "192.168.1.1")
	if !allowed {
		t.Errorf("expected request to be allowed (fail open), got denied with reason: %s", reason)
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %q", reason)
	}
}

func TestSet_Names(t *testing.T) {
	s := NewSet()
	defer s.Stop()

	s.Add("limiter1", New("limiter1", Config{TotalRPS: 100, Burst: 10}))
	s.Add("limiter2", New("limiter2", Config{TotalRPS: 100, Burst: 10}))
	s.Add("limiter3", New("limiter3", Config{TotalRPS: 100, Burst: 10}))

	names := s.Names()
	if len(names) != 3 {
		t.Errorf("expected 3 names, got %d", len(names))
	}

	sort.Strings(names)
	expected := []string{"limiter1", "limiter2", "limiter3"}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("expected name %q at index %d, got %q", expected[i], i, name)
		}
	}
}

func TestSet_Stop(t *testing.T) {
	s := NewSet()

	s.Add("limiter1", New("limiter1", Config{TotalRPS: 100, PerIPRPS: 10, Burst: 10}))
	s.Add("limiter2", New("limiter2", Config{TotalRPS: 100, PerIPRPS: 10, Burst: 10}))

	// Stop should not panic and should stop all limiters
	s.Stop()
}

func TestSet_Concurrent(t *testing.T) {
	s := NewSet()
	defer s.Stop()

	s.Add("test", New("test", Config{
		TotalRPS: 1000,
		PerIPRPS: 100,
		Burst:    100,
	}))

	// Run concurrent Allow calls
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				s.Allow("test", "192.168.1.1")
			}
			done <- struct{}{}
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}
