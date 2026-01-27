package relayclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewForwarder(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := NewForwarder("http://localhost:8080", "test-channel", logger, false)

	if f.destination != "http://localhost:8080" {
		t.Errorf("expected destination 'http://localhost:8080', got %q", f.destination)
	}
	if f.channelName != "test-channel" {
		t.Errorf("expected channel name 'test-channel', got %q", f.channelName)
	}
}

func TestForwarder_Forward_Success(t *testing.T) {
	// Create a local server that responds to webhooks
	var receivedRequest *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedRequest = r
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom-Response", "value")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"received":true}`))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := NewForwarder(server.URL, "test-channel", logger, false)

	webhook := &Webhook{
		ID:      "webhook-123",
		Method:  "POST",
		Path:    "/original/path",
		Headers: map[string][]string{"Content-Type": {"application/json"}, "X-Original-Header": {"original-value"}},
		Body:    base64.StdEncoding.EncodeToString([]byte(`{"test":"data"}`)),
	}

	resp, err := f.Forward(context.Background(), webhook)
	if err != nil {
		t.Fatalf("Forward failed: %v", err)
	}

	// Check request was forwarded correctly
	if receivedRequest.Method != "POST" {
		t.Errorf("expected method POST, got %s", receivedRequest.Method)
	}
	if receivedRequest.Header.Get("X-Relay-Webhook-ID") != "webhook-123" {
		t.Errorf("expected X-Relay-Webhook-ID header")
	}
	if receivedRequest.Header.Get("X-Relay-Original-Path") != "/original/path" {
		t.Errorf("expected X-Relay-Original-Path header")
	}
	if receivedRequest.Header.Get("X-Original-Header") != "original-value" {
		t.Errorf("expected original headers to be preserved")
	}

	// Check response
	if resp.RequestID != "webhook-123" {
		t.Errorf("expected request ID 'webhook-123', got %q", resp.RequestID)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected status 201, got %d", resp.StatusCode)
	}
	if len(resp.Headers["Content-Type"]) == 0 || resp.Headers["Content-Type"][0] != "application/json" {
		t.Errorf("expected Content-Type header in response, got %v", resp.Headers["Content-Type"])
	}
	if len(resp.Headers["X-Custom-Response"]) == 0 || resp.Headers["X-Custom-Response"][0] != "value" {
		t.Errorf("expected X-Custom-Response header in response, got %v", resp.Headers["X-Custom-Response"])
	}

	// Decode response body
	body, err := base64.StdEncoding.DecodeString(resp.Body)
	if err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if string(body) != `{"received":true}` {
		t.Errorf("expected body '{\"received\":true}', got %q", string(body))
	}
}

func TestForwarder_Forward_InvalidBase64Body(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := NewForwarder("http://localhost:8080", "test-channel", logger, false)

	webhook := &Webhook{
		ID:     "webhook-123",
		Method: "POST",
		Path:   "/test",
		Body:   "not-valid-base64!!!",
	}

	_, err := f.Forward(context.Background(), webhook)
	if err == nil {
		t.Error("expected error for invalid base64 body")
	}
}

func TestForwarder_Forward_ServerError(t *testing.T) {
	// Server is not reachable
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := NewForwarder("http://localhost:99999", "test-channel", logger, false)

	webhook := &Webhook{
		ID:     "webhook-123",
		Method: "POST",
		Path:   "/test",
		Body:   base64.StdEncoding.EncodeToString([]byte(`{}`)),
	}

	_, err := f.Forward(context.Background(), webhook)
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

func TestForwarder_Forward_ContextCancelled(t *testing.T) {
	// Create a slow server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This will block
		<-r.Context().Done()
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := NewForwarder(server.URL, "test-channel", logger, false)

	webhook := &Webhook{
		ID:     "webhook-123",
		Method: "POST",
		Path:   "/test",
		Body:   base64.StdEncoding.EncodeToString([]byte(`{}`)),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := f.Forward(ctx, webhook)
	if err == nil {
		t.Error("expected error for canceled context")
	}
}

func TestForwarder_Forward_InvalidMethod(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := NewForwarder("http://localhost:8080", "test-channel", logger, false)

	webhook := &Webhook{
		ID:     "webhook-123",
		Method: "INVALID\x00METHOD", // Control character makes method invalid
		Path:   "/test",
		Body:   base64.StdEncoding.EncodeToString([]byte(`{}`)),
	}

	_, err := f.Forward(context.Background(), webhook)
	if err == nil {
		t.Error("expected error for invalid method")
	}
}

// errorReader is a reader that always returns an error
type errorReader struct{}

func (e *errorReader) Read(_ []byte) (n int, err error) {
	return 0, errors.New("simulated read error")
}

func (e *errorReader) Close() error {
	return nil
}

// errorTransport is an http.RoundTripper that returns a response with a body that errors on read
type errorTransport struct{}

func (t *errorTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       &errorReader{},
		Header:     make(http.Header),
	}, nil
}

func TestForwarder_Forward_BodyReadError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := NewForwarder("http://localhost:8080", "test-channel", logger, false)
	f.client = &http.Client{Transport: &errorTransport{}}

	webhook := &Webhook{
		ID:     "webhook-123",
		Method: "POST",
		Path:   "/test",
		Body:   base64.StdEncoding.EncodeToString([]byte(`{}`)),
	}

	_, err := f.Forward(context.Background(), webhook)
	if err == nil {
		t.Error("expected error for body read failure")
	}
}

func TestForwarder_Forward_InvalidDestination(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := NewForwarder("://invalid-url", "test-channel", logger, false)

	webhook := &Webhook{
		ID:     "webhook-123",
		Method: "POST",
		Path:   "/test",
		Body:   base64.StdEncoding.EncodeToString([]byte(`{}`)),
	}

	_, err := f.Forward(context.Background(), webhook)
	if err == nil {
		t.Error("expected error for invalid destination URL")
	}
}

func TestForwarder_Forward_InvalidWebhookPath(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := NewForwarder("http://localhost:8080", "test-channel", logger, false)

	webhook := &Webhook{
		ID:     "webhook-123",
		Method: "POST",
		Path:   "://invalid", // Invalid URL
		Body:   base64.StdEncoding.EncodeToString([]byte(`{}`)),
	}

	_, err := f.Forward(context.Background(), webhook)
	if err == nil {
		t.Error("expected error for invalid webhook path")
	}
}

func TestForwarder_Forward_PreservesQueryString(t *testing.T) {
	var receivedURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.RequestURI()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := NewForwarder(server.URL, "test-channel", logger, false)

	webhook := &Webhook{
		ID:     "webhook-123",
		Method: "POST",
		Path:   "/webhooks/github?challenge=abc123&verify=true",
		Body:   base64.StdEncoding.EncodeToString([]byte(`{}`)),
	}

	_, err := f.Forward(context.Background(), webhook)
	if err != nil {
		t.Fatalf("Forward failed: %v", err)
	}

	if receivedURL != "/webhooks/github?challenge=abc123&verify=true" {
		t.Errorf("expected URL '/webhooks/github?challenge=abc123&verify=true', got %q", receivedURL)
	}
}

func TestForwarder_Forward_CombinesBasePath(t *testing.T) {
	var receivedURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.RequestURI()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Destination has a base path /api
	f := NewForwarder(server.URL+"/api", "test-channel", logger, false)

	webhook := &Webhook{
		ID:     "webhook-123",
		Method: "POST",
		Path:   "/webhooks/github?challenge=abc",
		Body:   base64.StdEncoding.EncodeToString([]byte(`{}`)),
	}

	_, err := f.Forward(context.Background(), webhook)
	if err != nil {
		t.Fatalf("Forward failed: %v", err)
	}

	// Should combine: /api + /webhooks/github?challenge=abc
	if receivedURL != "/api/webhooks/github?challenge=abc" {
		t.Errorf("expected URL '/api/webhooks/github?challenge=abc', got %q", receivedURL)
	}
}

func TestForwarder_Forward_MergesQueryParams(t *testing.T) {
	tests := []struct {
		name          string
		destQuery     string
		webhookQuery  string
		expectedQuery string
	}{
		{
			name:          "destination only",
			destQuery:     "token=secret",
			webhookQuery:  "",
			expectedQuery: "token=secret",
		},
		{
			name:          "webhook only",
			destQuery:     "",
			webhookQuery:  "challenge=abc",
			expectedQuery: "challenge=abc",
		},
		{
			name:          "both - merged",
			destQuery:     "token=secret",
			webhookQuery:  "challenge=abc",
			expectedQuery: "token=secret&challenge=abc",
		},
		{
			name:          "neither",
			destQuery:     "",
			webhookQuery:  "",
			expectedQuery: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var receivedQuery string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedQuery = r.URL.RawQuery
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))

			destURL := server.URL + "/api"
			if tc.destQuery != "" {
				destURL += "?" + tc.destQuery
			}
			f := NewForwarder(destURL, "test-channel", logger, false)

			webhookPath := "/webhook"
			if tc.webhookQuery != "" {
				webhookPath += "?" + tc.webhookQuery
			}

			webhook := &Webhook{
				ID:     "webhook-123",
				Method: "POST",
				Path:   webhookPath,
				Body:   base64.StdEncoding.EncodeToString([]byte(`{}`)),
			}

			_, err := f.Forward(context.Background(), webhook)
			if err != nil {
				t.Fatalf("Forward failed: %v", err)
			}

			if receivedQuery != tc.expectedQuery {
				t.Errorf("expected query %q, got %q", tc.expectedQuery, receivedQuery)
			}
		})
	}
}

func TestForwarder_Forward_PreserveHost(t *testing.T) {
	tests := []struct {
		name            string
		preserveHost    string // "true" or "" (not set)
		originalHost    string
		expectedHost    string
		shouldStripHdrs bool
	}{
		{
			name:            "preserve host enabled",
			preserveHost:    "true",
			originalHost:    "webhooks.example.com",
			expectedHost:    "webhooks.example.com",
			shouldStripHdrs: true,
		},
		{
			name:            "preserve host not set",
			preserveHost:    "",
			originalHost:    "",
			expectedHost:    "", // Will be server host
			shouldStripHdrs: true,
		},
		{
			name:            "preserve host false",
			preserveHost:    "false", // Explicitly false (not "true")
			originalHost:    "webhooks.example.com",
			expectedHost:    "", // Will be server host
			shouldStripHdrs: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var receivedHost string
			var receivedPreserveHeader string
			var receivedOriginalHostHeader string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedHost = r.Host
				receivedPreserveHeader = r.Header.Get("X-Gatekeeperd-Preserve-Host")
				receivedOriginalHostHeader = r.Header.Get("X-Gatekeeperd-Original-Host")
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			f := NewForwarder(server.URL, "test-channel", logger, false)

			headers := map[string][]string{
				"Content-Type": {"application/json"},
			}
			if tc.preserveHost != "" {
				headers["X-Gatekeeperd-Preserve-Host"] = []string{tc.preserveHost}
			}
			if tc.originalHost != "" {
				headers["X-Gatekeeperd-Original-Host"] = []string{tc.originalHost}
			}

			webhook := &Webhook{
				ID:      "webhook-123",
				Method:  "POST",
				Path:    "/webhook",
				Headers: headers,
				Body:    base64.StdEncoding.EncodeToString([]byte(`{}`)),
			}

			_, err := f.Forward(context.Background(), webhook)
			if err != nil {
				t.Fatalf("Forward failed: %v", err)
			}

			// Check Host header
			if tc.expectedHost != "" {
				if receivedHost != tc.expectedHost {
					t.Errorf("expected Host %q, got %q", tc.expectedHost, receivedHost)
				}
			}

			// Check that internal headers were stripped
			if tc.shouldStripHdrs {
				if receivedPreserveHeader != "" {
					t.Errorf("expected X-Gatekeeperd-Preserve-Host to be stripped, got %q", receivedPreserveHeader)
				}
				if receivedOriginalHostHeader != "" {
					t.Errorf("expected X-Gatekeeperd-Original-Host to be stripped, got %q", receivedOriginalHostHeader)
				}
			}
		})
	}
}

func TestForwarder_Forward_StripsHopByHopHeaders(t *testing.T) {
	var receivedHeaders http.Header
	var receivedConnection string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		receivedConnection = r.Header.Get("Connection")
		// Send back some hop-by-hop headers in response
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Keep-Alive", "timeout=5")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Header().Set("X-Custom-Response", "preserved")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := NewForwarder(server.URL, "test-channel", logger, false)

	webhook := &Webhook{
		ID:     "webhook-123",
		Method: "POST",
		Path:   "/webhook",
		Headers: map[string][]string{
			"Content-Type":      {"application/json"},
			"X-Custom":          {"preserved"},
			"Connection":        {"keep-alive"}, // Should be stripped
			"Keep-Alive":        {"timeout=10"}, // Should be stripped
			"Transfer-Encoding": {"chunked"},    // Should be stripped
			"Content-Length":    {"9999"},       // Should be stripped (wrong value)
		},
		Body: base64.StdEncoding.EncodeToString([]byte(`{"test":true}`)),
	}

	resp, err := f.Forward(context.Background(), webhook)
	if err != nil {
		t.Fatalf("Forward failed: %v", err)
	}

	// Verify hop-by-hop headers were stripped from request
	if receivedHeaders.Get("Keep-Alive") != "" {
		t.Error("Keep-Alive should be stripped from request")
	}
	if receivedHeaders.Get("Transfer-Encoding") != "" {
		t.Error("Transfer-Encoding should be stripped from request")
	}
	// Connection should be "close" (we set it explicitly)
	if receivedConnection != "close" {
		t.Errorf("Connection should be 'close', got %q", receivedConnection)
	}
	// Custom header should be preserved
	if receivedHeaders.Get("X-Custom") != "preserved" {
		t.Error("X-Custom header should be preserved")
	}

	// Verify hop-by-hop headers were stripped from response
	if _, ok := resp.Headers["Connection"]; ok {
		t.Error("Connection should be stripped from response")
	}
	if _, ok := resp.Headers["Keep-Alive"]; ok {
		t.Error("Keep-Alive should be stripped from response")
	}
	if _, ok := resp.Headers["Transfer-Encoding"]; ok {
		t.Error("Transfer-Encoding should be stripped from response")
	}
	// Custom response header should be preserved
	if len(resp.Headers["X-Custom-Response"]) == 0 || resp.Headers["X-Custom-Response"][0] != "preserved" {
		t.Error("X-Custom-Response header should be preserved")
	}
}

func TestForwarder_DebugPayloads(t *testing.T) {
	// Create a local server that responds
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"response":"data"}`))
	}))
	defer server.Close()

	// Capture log output
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	// Enable debug payloads
	f := NewForwarder(server.URL, "test-channel", logger, true)

	webhook := &Webhook{
		ID:      "webhook-debug-test",
		Method:  "POST",
		Path:    "/test",
		Headers: map[string][]string{"Content-Type": {"application/json"}},
		Body:    base64.StdEncoding.EncodeToString([]byte(`{"request":"data"}`)),
	}

	resp, err := f.Forward(context.Background(), webhook)
	if err != nil {
		t.Fatalf("Forward failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	logOutput := logBuf.String()

	// Verify debug logging occurred
	if !strings.Contains(logOutput, "debug: incoming webhook") {
		t.Error("expected 'debug: incoming webhook' in logs")
	}
	// Body is logged as a string field in JSON - check it contains the data (may be escaped)
	if !strings.Contains(logOutput, "request") || !strings.Contains(logOutput, "data") {
		t.Error("expected request body content in debug logs")
	}
	if !strings.Contains(logOutput, "debug: destination response") {
		t.Error("expected 'debug: destination response' in logs")
	}
	if !strings.Contains(logOutput, "response") {
		t.Error("expected response body content in debug logs")
	}
}

func TestTruncateForLog(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected string
	}{
		{
			name:     "short body",
			input:    []byte("hello world"),
			expected: "hello world",
		},
		{
			name:     "empty body",
			input:    []byte{},
			expected: "",
		},
		{
			name:     "exactly 8192 bytes",
			input:    bytes.Repeat([]byte("a"), 8192),
			expected: string(bytes.Repeat([]byte("a"), 8192)),
		},
		{
			name:     "over 8192 bytes gets truncated",
			input:    bytes.Repeat([]byte("a"), 10000),
			expected: string(bytes.Repeat([]byte("a"), 8192)) + "... (truncated)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := truncateForLog(tc.input)
			if result != tc.expected {
				if len(tc.expected) > 100 {
					t.Errorf("expected length %d (truncated=%v), got length %d (truncated=%v)",
						len(tc.expected), strings.HasSuffix(tc.expected, "... (truncated)"),
						len(result), strings.HasSuffix(result, "... (truncated)"))
				} else {
					t.Errorf("expected %q, got %q", tc.expected, result)
				}
			}
		})
	}
}
