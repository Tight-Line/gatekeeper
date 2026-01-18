package ipfilter

import (
	"fmt"
	"net"
	"sync"
)

// Filter checks if IP addresses are allowed based on CIDR ranges
type Filter struct {
	mu    sync.RWMutex
	name  string
	nets  []*net.IPNet
	cidrs []string // Original CIDR strings for debugging
}

// NewFilter creates a new IP filter with the given CIDRs
func NewFilter(name string, cidrs []string) (*Filter, error) {
	f := &Filter{
		name:  name,
		cidrs: cidrs,
	}

	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
		}
		f.nets = append(f.nets, ipNet)
	}

	return f, nil
}

// Allow checks if the given IP address is allowed
func (f *Filter) Allow(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	for _, ipNet := range f.nets {
		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}

// AllowFromAddr checks if the given address (ip:port format) is allowed
func (f *Filter) AllowFromAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// Maybe it's just an IP without port
		host = addr
	}
	return f.Allow(host)
}

// Update replaces the current CIDR list with a new one (for dynamic updates)
func (f *Filter) Update(cidrs []string) error {
	var nets []*net.IPNet
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return fmt.Errorf("invalid CIDR %q: %w", cidr, err)
		}
		nets = append(nets, ipNet)
	}

	f.mu.Lock()
	f.nets = nets
	f.cidrs = cidrs
	f.mu.Unlock()

	return nil
}

// Count returns the number of CIDR ranges in the filter
func (f *Filter) Count() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.nets)
}

// Name returns the filter name
func (f *Filter) Name() string {
	return f.name
}

// FilterSet manages multiple named IP filters
type FilterSet struct {
	mu      sync.RWMutex
	filters map[string]*Filter
}

// NewFilterSet creates a new filter set
func NewFilterSet() *FilterSet {
	return &FilterSet{
		filters: make(map[string]*Filter),
	}
}

// Add adds a filter to the set
func (fs *FilterSet) Add(name string, filter *Filter) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.filters[name] = filter
}

// Get retrieves a filter by name
func (fs *FilterSet) Get(name string) (*Filter, bool) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	f, ok := fs.filters[name]
	return f, ok
}

// Allow checks if an IP is allowed by the named filter
// Returns true if the filter doesn't exist (permissive by default when misconfigured)
func (fs *FilterSet) Allow(filterName, ipAddr string) bool {
	f, ok := fs.Get(filterName)
	if !ok {
		// Filter not found - this is a config error, but we fail open
		// Logging should happen at the caller
		return false
	}
	return f.AllowFromAddr(ipAddr)
}
