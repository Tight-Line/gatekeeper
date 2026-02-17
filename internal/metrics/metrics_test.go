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
	t.Helper()
	RecordRequest("example.com", "/webhook", "200", 0.5)
	RecordRequest("example.com", "/webhook", "500", 1.2)
}

func TestRecordVerificationFailure(t *testing.T) {
	t.Helper()
	RecordVerificationFailure("example.com", "hmac", "invalid_signature")
	RecordVerificationFailure("example.com", "basic", "missing_header")
}

func TestRecordIPDenied(t *testing.T) {
	t.Helper()
	RecordIPDenied("example.com", "github")
	RecordIPDenied("example.com", "slack")
}

func TestRecordIPRangesLoaded(t *testing.T) {
	t.Helper()
	RecordIPRangesLoaded("github", 100)
	RecordIPRangesLoaded("slack", 50)
}

func TestRecordIPRangeFetchError(t *testing.T) {
	t.Helper()
	RecordIPRangeFetchError("github")
	RecordIPRangeFetchError("slack")
}

func TestRecordForwardError(t *testing.T) {
	t.Helper()
	RecordForwardError("example.com", "http://localhost:8080")
	RecordForwardError("example.com", "http://localhost:8081")
}

func TestRecordValidationFailure(t *testing.T) {
	t.Helper()
	RecordValidationFailure("example.com", "json-schema")
	RecordValidationFailure("example.com", "custom-validator")
}

func TestRecordRelayWebhookQueued(t *testing.T) {
	t.Helper()
	RecordRelayWebhookQueued("token1")
	RecordRelayWebhookQueued("token2")
}

func TestRecordRelayWebhookDelivered(t *testing.T) {
	t.Helper()
	RecordRelayWebhookDelivered("token1", 0.5)
	RecordRelayWebhookDelivered("token2", 1.2)
}

func TestRecordRelayDeliveryError(t *testing.T) {
	t.Helper()
	RecordRelayDeliveryError("token1", "timeout")
	RecordRelayDeliveryError("token1", "no_client")
	RecordRelayDeliveryError("token2", "unknown")
}

func TestRecordRelayWebhooksPending(t *testing.T) {
	t.Helper()
	RecordRelayWebhooksPending("token1", 5)
	RecordRelayWebhooksPending("token1", 10)
	RecordRelayWebhooksPending("token2", 0)
}

func TestRecordRelayClientsConnected(t *testing.T) {
	t.Helper()
	RecordRelayClientsConnected("token1", 2)
	RecordRelayClientsConnected("token2", 1)
}

func TestRecordRateLimited(t *testing.T) {
	t.Helper()
	RecordRateLimited("/webhook", "default", "total")
	RecordRateLimited("/webhook", "default", "per_ip")
}
