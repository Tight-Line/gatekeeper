package httputil

import (
	"testing"
)

func TestIsHopByHop(t *testing.T) {
	tests := []struct {
		header   string
		expected bool
	}{
		{"Connection", true},
		{"Keep-Alive", true},
		{"Proxy-Authenticate", true},
		{"Proxy-Authorization", true},
		{"Te", true},
		{"Trailer", true},
		{"Transfer-Encoding", true},
		{"Upgrade", true},
		{"Content-Type", false},
		{"X-Custom-Header", false},
		{"Authorization", false},
	}

	for _, tc := range tests {
		t.Run(tc.header, func(t *testing.T) {
			result := IsHopByHop(tc.header)
			if result != tc.expected {
				t.Errorf("IsHopByHop(%q) = %v, want %v", tc.header, result, tc.expected)
			}
		})
	}
}

func TestShouldStrip(t *testing.T) {
	tests := []struct {
		header   string
		expected bool
	}{
		// Hop-by-hop headers
		{"Connection", true},
		{"Keep-Alive", true},
		{"Transfer-Encoding", true},
		// Content-Length should be stripped
		{"Content-Length", true},
		// Regular headers should not be stripped
		{"Content-Type", false},
		{"X-Custom-Header", false},
		{"Authorization", false},
	}

	for _, tc := range tests {
		t.Run(tc.header, func(t *testing.T) {
			result := ShouldStrip(tc.header)
			if result != tc.expected {
				t.Errorf("ShouldStrip(%q) = %v, want %v", tc.header, result, tc.expected)
			}
		})
	}
}

func TestStripHopByHopHeaders(t *testing.T) {
	input := map[string][]string{
		"Content-Type":      {"application/json"},
		"X-Custom":          {"value1", "value2"},
		"Connection":        {"keep-alive"},
		"Keep-Alive":        {"timeout=5"},
		"Transfer-Encoding": {"chunked"},
		"Content-Length":    {"1234"},
		"Authorization":     {"Bearer token"},
	}

	result := StripHopByHopHeaders(input)

	// Should be preserved
	if _, ok := result["Content-Type"]; !ok {
		t.Error("Content-Type should be preserved")
	}
	if _, ok := result["X-Custom"]; !ok {
		t.Error("X-Custom should be preserved")
	}
	if _, ok := result["Authorization"]; !ok {
		t.Error("Authorization should be preserved")
	}

	// Should be stripped
	if _, ok := result["Connection"]; ok {
		t.Error("Connection should be stripped")
	}
	if _, ok := result["Keep-Alive"]; ok {
		t.Error("Keep-Alive should be stripped")
	}
	if _, ok := result["Transfer-Encoding"]; ok {
		t.Error("Transfer-Encoding should be stripped")
	}
	if _, ok := result["Content-Length"]; ok {
		t.Error("Content-Length should be stripped")
	}

	// Verify multi-value headers are cloned properly
	if len(result["X-Custom"]) != 2 {
		t.Errorf("X-Custom should have 2 values, got %d", len(result["X-Custom"]))
	}

	// Verify original is not modified
	if _, ok := input["Connection"]; !ok {
		t.Error("Original input should not be modified")
	}
}

func TestStripHopByHopHeaders_EmptyInput(t *testing.T) {
	result := StripHopByHopHeaders(nil)
	if result == nil {
		t.Error("Should return empty map, not nil")
	}
	if len(result) != 0 {
		t.Errorf("Should return empty map, got %d entries", len(result))
	}
}
