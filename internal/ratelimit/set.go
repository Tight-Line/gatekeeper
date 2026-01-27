package ratelimit

import (
	"sync"
)

// Set manages a collection of named rate limiters
type Set struct {
	mu       sync.RWMutex
	limiters map[string]*Limiter
}

// NewSet creates a new limiter set
func NewSet() *Set {
	return &Set{
		limiters: make(map[string]*Limiter),
	}
}

// Add adds a limiter to the set
func (s *Set) Add(name string, limiter *Limiter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limiters[name] = limiter
}

// Get returns the limiter with the given name, or nil if not found
func (s *Set) Get(name string) *Limiter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.limiters[name]
}

// Allow checks if a request is allowed by the named limiter.
// Returns (true, "") if allowed, (false, reason) if denied.
// If the limiter is not found, returns (true, "") (fail open).
func (s *Set) Allow(name, clientIP string) (allowed bool, reason string) {
	limiter := s.Get(name)
	if limiter == nil {
		return true, ""
	}
	return limiter.Allow(clientIP)
}

// Stop stops all limiters in the set
func (s *Set) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, limiter := range s.limiters {
		limiter.Stop()
	}
}

// Names returns the names of all limiters in the set
func (s *Set) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0, len(s.limiters))
	for name := range s.limiters {
		names = append(names, name)
	}
	return names
}
