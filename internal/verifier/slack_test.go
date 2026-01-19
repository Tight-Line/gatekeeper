package verifier

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSlackVerifier_Verify(t *testing.T) {
	secret := "test-signing-secret"
	verifier := NewSlackVerifier(secret, 5*time.Minute)

	tests := []verifierTestCase{
		{
			name: "valid signature",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"type":"event_callback","event":{"type":"message"}}`)
				timestamp := strconv.FormatInt(time.Now().Unix(), 10)
				signature := computeSlackSignature(secret, timestamp, body)

				req := httptest.NewRequest(http.MethodPost, "/slack/events", strings.NewReader(string(body)))
				req.Header.Set("X-Slack-Request-Timestamp", timestamp)
				req.Header.Set("X-Slack-Signature", signature)
				return req, body
			},
			wantErr: false,
		},
		{
			name: "missing timestamp header",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"type":"event_callback"}`)
				req := httptest.NewRequest(http.MethodPost, "/slack/events", strings.NewReader(string(body)))
				req.Header.Set("X-Slack-Signature", "v0=abc123")
				return req, body
			},
			wantErr:   true,
			errString: "timestamp is invalid",
		},
		{
			name: "invalid timestamp format",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"type":"event_callback"}`)
				req := httptest.NewRequest(http.MethodPost, "/slack/events", strings.NewReader(string(body)))
				req.Header.Set("X-Slack-Request-Timestamp", "not-a-number")
				req.Header.Set("X-Slack-Signature", "v0=abc123")
				return req, body
			},
			wantErr:   true,
			errString: "cannot parse timestamp",
		},
		{
			name: "future timestamp (clock skew)",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"type":"event_callback"}`)
				// 10 minutes in the future
				timestamp := strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10)
				signature := computeSlackSignature(secret, timestamp, body)

				req := httptest.NewRequest(http.MethodPost, "/slack/events", strings.NewReader(string(body)))
				req.Header.Set("X-Slack-Request-Timestamp", timestamp)
				req.Header.Set("X-Slack-Signature", signature)
				return req, body
			},
			wantErr:   true,
			errString: "timestamp is",
		},
		{
			name: "missing signature header",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"type":"event_callback"}`)
				timestamp := strconv.FormatInt(time.Now().Unix(), 10)
				req := httptest.NewRequest(http.MethodPost, "/slack/events", strings.NewReader(string(body)))
				req.Header.Set("X-Slack-Request-Timestamp", timestamp)
				return req, body
			},
			wantErr:   true,
			errString: "signature header is empty",
		},
		{
			name: "expired timestamp",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"type":"event_callback"}`)
				// 10 minutes ago
				timestamp := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
				signature := computeSlackSignature(secret, timestamp, body)

				req := httptest.NewRequest(http.MethodPost, "/slack/events", strings.NewReader(string(body)))
				req.Header.Set("X-Slack-Request-Timestamp", timestamp)
				req.Header.Set("X-Slack-Signature", signature)
				return req, body
			},
			wantErr:   true,
			errString: "timestamp is too old",
		},
		{
			name: "invalid signature",
			setup: func() (*http.Request, []byte) {
				body := []byte(`{"type":"event_callback"}`)
				timestamp := strconv.FormatInt(time.Now().Unix(), 10)

				req := httptest.NewRequest(http.MethodPost, "/slack/events", strings.NewReader(string(body)))
				req.Header.Set("X-Slack-Request-Timestamp", timestamp)
				req.Header.Set("X-Slack-Signature", "v0=invalid")
				return req, body
			},
			wantErr:   true,
			errString: "signature does not match",
		},
		{
			name: "tampered body",
			setup: func() (*http.Request, []byte) {
				originalBody := []byte(`{"type":"event_callback"}`)
				tamperedBody := []byte(`{"type":"malicious"}`)
				timestamp := strconv.FormatInt(time.Now().Unix(), 10)
				// Sign with original body
				signature := computeSlackSignature(secret, timestamp, originalBody)

				req := httptest.NewRequest(http.MethodPost, "/slack/events", strings.NewReader(string(tamperedBody)))
				req.Header.Set("X-Slack-Request-Timestamp", timestamp)
				req.Header.Set("X-Slack-Signature", signature)
				return req, tamperedBody
			},
			wantErr:   true,
			errString: "signature does not match",
		},
	}

	runVerifierTests(t, verifier, tests)
}

func TestSlackVerifier_Type(t *testing.T) {
	v := NewSlackVerifier("secret", 5*time.Minute)
	assertVerifierType(t, v, "slack")
}

func TestSlackVerifier_DefaultMaxTimestampAge(t *testing.T) {
	// When maxTimestampAge is 0, it should default to 5 minutes
	v := NewSlackVerifier("secret", 0)
	if v.maxTimestampAge != 5*time.Minute {
		t.Errorf("expected default maxTimestampAge of 5m, got %v", v.maxTimestampAge)
	}
}

// computeSlackSignature computes a valid Slack signature for testing
func computeSlackSignature(secret, timestamp string, body []byte) string {
	baseString := fmt.Sprintf("v0:%s:%s", timestamp, string(body))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(baseString))
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}
