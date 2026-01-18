// Package httputil provides HTTP utility functions for header handling.
package httputil

// HopByHopHeaders are headers that must not be forwarded by proxies.
// Per RFC 7230 Section 6.1, these are connection-specific and must be
// consumed by the first proxy that receives them.
var HopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true, // canonicalized
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

// HeadersToStrip are additional headers that should not be forwarded
// because they describe the current hop's message framing, not the payload.
var HeadersToStrip = map[string]bool{
	// Hop-by-hop headers
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,

	// Content-Length must be recalculated for the actual body at each hop
	"Content-Length": true,
}

// IsHopByHop returns true if the header is a hop-by-hop header.
func IsHopByHop(header string) bool {
	return HopByHopHeaders[header]
}

// ShouldStrip returns true if the header should be stripped when proxying.
func ShouldStrip(header string) bool {
	return HeadersToStrip[header]
}

// StripHopByHopHeaders removes hop-by-hop and framing headers from a header map.
// Returns a new map with only the headers that should be forwarded.
func StripHopByHopHeaders(headers map[string][]string) map[string][]string {
	result := make(map[string][]string)
	for k, v := range headers {
		if !ShouldStrip(k) {
			result[k] = append([]string(nil), v...) // Clone the slice
		}
	}
	return result
}
