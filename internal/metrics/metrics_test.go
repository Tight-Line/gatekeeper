package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler(t *testing.T) {
	// First record some metrics so they appear in output
	RecordRequest("test.com", "/test", "200", 0.1)

	handler := Handler()
	if handler == nil {
		t.Fatal("Handler() returned nil")
	}

	// Make a request to the handler
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Check that response contains some Prometheus metrics
	body := w.Body.String()
	if !strings.Contains(body, "gatekeeper_requests_total") {
		t.Errorf("expected response to contain gatekeeper_requests_total, got:\n%s", body)
	}
}

func TestRecordRequest(t *testing.T) {
	// Just test that it doesn't panic
	RecordRequest("example.com", "/webhook", "200", 0.5)
	RecordRequest("example.com", "/webhook", "500", 1.2)
}

func TestRecordVerificationFailure(t *testing.T) {
	// Just test that it doesn't panic
	RecordVerificationFailure("example.com", "hmac", "invalid_signature")
	RecordVerificationFailure("example.com", "basic", "missing_header")
}

func TestRecordIPDenied(t *testing.T) {
	// Just test that it doesn't panic
	RecordIPDenied("example.com", "github")
	RecordIPDenied("example.com", "slack")
}

func TestRecordIPRangesLoaded(t *testing.T) {
	// Just test that it doesn't panic
	RecordIPRangesLoaded("github", 100)
	RecordIPRangesLoaded("slack", 50)
}

func TestRecordIPRangeFetchError(t *testing.T) {
	// Just test that it doesn't panic
	RecordIPRangeFetchError("github")
	RecordIPRangeFetchError("slack")
}

func TestRecordForwardError(t *testing.T) {
	// Just test that it doesn't panic
	RecordForwardError("example.com", "http://localhost:8080")
	RecordForwardError("example.com", "http://localhost:8081")
}
