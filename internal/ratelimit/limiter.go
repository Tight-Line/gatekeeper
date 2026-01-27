package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// DefaultCleanupInterval is the default interval for cleaning up stale per-IP entries
const DefaultCleanupInterval = 5 * time.Minute

// DefaultIdleTimeout is the default time after which idle per-IP entries are removed
const DefaultIdleTimeout = 10 * time.Minute

// Config holds rate limiter configuration
type Config struct {
	TotalRPS        float64       // Total requests per second across all IPs
	PerIPRPS        float64       // Requests per second per client IP (0 = disabled)
	Burst           int           // Burst allowance for spike handling
	CleanupInterval time.Duration // How often to scan for stale per-IP entries
	IdleTimeout     time.Duration // Remove per-IP limiter after idle time
}

// ipEntry holds a per-IP rate limiter and its last access time
type ipEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// Limiter implements rate limiting with both total and per-IP limits
type Limiter struct {
	name   string
	config Config

	total *rate.Limiter

	mu      sync.Mutex
	perIP   map[string]*ipEntry
	stopCh  chan struct{}
	stopped bool
}

// New creates a new rate limiter with the given configuration
func New(name string, cfg Config) *Limiter {
	// Apply defaults
	if cfg.CleanupInterval <= 0 {
		cfg.CleanupInterval = DefaultCleanupInterval
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = DefaultIdleTimeout
	}

	l := &Limiter{
		name:   name,
		config: cfg,
		total:  rate.NewLimiter(rate.Limit(cfg.TotalRPS), cfg.Burst),
		perIP:  make(map[string]*ipEntry),
		stopCh: make(chan struct{}),
	}

	// Start cleanup goroutine if per-IP limiting is enabled
	if cfg.PerIPRPS > 0 {
		go l.cleanupLoop()
	}

	return l
}

// Allow checks if a request from the given client IP should be allowed.
// Returns the reason for denial ("total" or "per_ip") if denied, or "" if allowed.
func (l *Limiter) Allow(clientIP string) (allowed bool, reason string) {
	// Check total rate limit first
	if !l.total.Allow() {
		return false, "total"
	}

	// Check per-IP rate limit if enabled
	if l.config.PerIPRPS > 0 {
		limiter := l.getOrCreateIPLimiter(clientIP)
		if !limiter.Allow() {
			return false, "per_ip"
		}
	}

	return true, ""
}

// getOrCreateIPLimiter returns the rate limiter for the given IP, creating one if needed
func (l *Limiter) getOrCreateIPLimiter(clientIP string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.perIP[clientIP]
	if ok {
		entry.lastSeen = time.Now()
		return entry.limiter
	}

	limiter := rate.NewLimiter(rate.Limit(l.config.PerIPRPS), l.config.Burst)
	l.perIP[clientIP] = &ipEntry{
		limiter:  limiter,
		lastSeen: time.Now(),
	}
	return limiter
}

// cleanupLoop periodically removes stale per-IP entries
func (l *Limiter) cleanupLoop() {
	ticker := time.NewTicker(l.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			l.cleanup()
		case <-l.stopCh:
			return
		}
	}
}

// cleanup removes per-IP entries that have been idle for too long
func (l *Limiter) cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := time.Now().Add(-l.config.IdleTimeout)
	for ip, entry := range l.perIP {
		if entry.lastSeen.Before(cutoff) {
			delete(l.perIP, ip)
		}
	}
}

// Stop stops the cleanup goroutine
func (l *Limiter) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.stopped {
		l.stopped = true
		close(l.stopCh)
	}
}

// Name returns the limiter's name
func (l *Limiter) Name() string {
	return l.name
}

// PerIPCount returns the current number of tracked per-IP entries (for testing/metrics)
func (l *Limiter) PerIPCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.perIP)
}
