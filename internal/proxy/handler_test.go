package proxy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"log/slog"

	"github.com/tight-line/gatekeeper/internal/config"
	"github.com/tight-line/gatekeeper/internal/ipfilter"
	"github.com/tight-line/gatekeeper/internal/relay"
	"github.com/tight-line/gatekeeper/internal/verifier"
)

func TestHandler_ServeHTTP(t *testing.T) {
	// Create a test backend that echoes the request
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Backend-Received", "true")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer backend.Close()

	// Build test config
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    "test.example.com",
				Path:        "/webhook",
				IPAllowlist: "test-ips",
				Verifier:    "test-slack",
				Destination: backend.URL,
			},
			{
				Hostname:    "noverify.example.com",
				Path:        "/webhook",
				IPAllowlist: "test-ips",
				Destination: backend.URL,
			},
		},
		Verifiers: map[string]config.VerifierConfig{
			"test-slack": {
				Type:          "slack",
				SigningSecret: "test-secret",
			},
		},
	}

	// Build IP filters
	filters := ipfilter.NewFilterSet()
	filter, err := ipfilter.NewFilter("test-ips", []string{"127.0.0.0/8", "192.168.0.0/16"})
	if err != nil {
		t.Fatalf("failed to create filter: %v", err)
	}
	filters.Add("test-ips", filter)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	tests := []struct {
		name           string
		hostname       string
		path           string
		remoteAddr     string
		body           []byte
		setupHeaders   func(r *http.Request, body []byte)
		expectedStatus int
	}{
		{
			name:           "no matching route",
			hostname:       "unknown.example.com",
			path:           "/webhook",
			remoteAddr:     "127.0.0.1:12345",
			body:           []byte(`{"test":"data"}`),
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "ip not allowed",
			hostname:       "test.example.com",
			path:           "/webhook",
			remoteAddr:     "8.8.8.8:12345",
			body:           []byte(`{"test":"data"}`),
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "missing signature",
			hostname:       "test.example.com",
			path:           "/webhook",
			remoteAddr:     "127.0.0.1:12345",
			body:           []byte(`{"test":"data"}`),
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid signature",
			hostname:   "test.example.com",
			path:       "/webhook",
			remoteAddr: "127.0.0.1:12345",
			body:       []byte(`{"test":"data"}`),
			setupHeaders: func(r *http.Request, body []byte) {
				r.Header.Set("X-Slack-Request-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
				r.Header.Set("X-Slack-Signature", "v0=invalid")
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:       "valid slack request",
			hostname:   "test.example.com",
			path:       "/webhook",
			remoteAddr: "127.0.0.1:12345",
			body:       []byte(`{"test":"data"}`),
			setupHeaders: func(r *http.Request, body []byte) {
				timestamp := strconv.FormatInt(time.Now().Unix(), 10)
				sigBase := fmt.Sprintf("v0:%s:%s", timestamp, string(body))
				mac := hmac.New(sha256.New, []byte("test-secret"))
				mac.Write([]byte(sigBase))
				signature := "v0=" + hex.EncodeToString(mac.Sum(nil))
				r.Header.Set("X-Slack-Request-Timestamp", timestamp)
				r.Header.Set("X-Slack-Signature", signature)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "route without verifier",
			hostname:       "noverify.example.com",
			path:           "/webhook",
			remoteAddr:     "127.0.0.1:12345",
			body:           []byte(`{"test":"data"}`),
			expectedStatus: http.StatusOK,
		},
		{
			name:           "prefix path matching",
			hostname:       "noverify.example.com",
			path:           "/webhook/subpath",
			remoteAddr:     "127.0.0.1:12345",
			body:           []byte(`{"test":"data"}`),
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "https://"+tt.hostname+tt.path, bytes.NewReader(tt.body))
			req.Host = tt.hostname
			req.RemoteAddr = tt.remoteAddr

			if tt.setupHeaders != nil {
				tt.setupHeaders(req, tt.body)
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d (body: %s)", tt.expectedStatus, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestHandler_ForwardHeaders(t *testing.T) {
	// Create a test backend that captures headers
	var capturedHeaders http.Header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    "test.example.com",
				Path:        "/webhook",
				Destination: backend.URL,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	body := []byte(`{"test":"data"}`)
	req := httptest.NewRequest(http.MethodPost, "https://test.example.com/webhook", bytes.NewReader(body))
	req.Host = "test.example.com"
	req.RemoteAddr = "192.168.1.100:12345"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Custom-Header", "custom-value")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	// Check X-Forwarded headers were added (ReverseProxy may append its own)
	xff := capturedHeaders.Get("X-Forwarded-For")
	if xff == "" || xff != "192.168.1.100" && !strings.HasPrefix(xff, "192.168.1.100,") {
		t.Errorf("expected X-Forwarded-For to start with 192.168.1.100, got %s", xff)
	}
	if capturedHeaders.Get("X-Forwarded-Host") != "test.example.com" {
		t.Errorf("expected X-Forwarded-Host=test.example.com, got %s", capturedHeaders.Get("X-Forwarded-Host"))
	}

	// Check original headers are preserved
	if capturedHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type=application/json, got %s", capturedHeaders.Get("Content-Type"))
	}
	if capturedHeaders.Get("X-Custom-Header") != "custom-value" {
		t.Errorf("expected X-Custom-Header=custom-value, got %s", capturedHeaders.Get("X-Custom-Header"))
	}
}

func TestHandler_ForwardHeaders_XFFChain(t *testing.T) {
	// Test that X-Forwarded-For appends to existing chain (handled by httputil.ReverseProxy)
	var capturedHeaders http.Header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    "test.example.com",
				Path:        "/webhook",
				Destination: backend.URL,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "http://test.example.com/webhook", bytes.NewReader(body))
	req.Host = "test.example.com"
	req.RemoteAddr = "10.0.0.1:12345"
	// Simulate request already passed through upstream proxy
	req.Header.Set("X-Forwarded-For", "203.0.113.50, 198.51.100.25")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	// httputil.ReverseProxy appends to existing X-Forwarded-For chain
	xff := capturedHeaders.Get("X-Forwarded-For")
	expected := "203.0.113.50, 198.51.100.25, 10.0.0.1"
	if xff != expected {
		t.Errorf("expected X-Forwarded-For=%q, got %q", expected, xff)
	}
}

func TestHandler_ForwardHeaders_ProtoDetection(t *testing.T) {
	tests := []struct {
		name          string
		useTLS        bool
		existingProto string
		expectedProto string
	}{
		{
			name:          "HTTP request defaults to http",
			useTLS:        false,
			existingProto: "",
			expectedProto: "http",
		},
		{
			name:          "HTTP with existing X-Forwarded-Proto preserves it",
			useTLS:        false,
			existingProto: "https",
			expectedProto: "https",
		},
		{
			name:          "TLS request sets https",
			useTLS:        true,
			existingProto: "",
			expectedProto: "https",
		},
		{
			name:          "TLS takes precedence over existing header",
			useTLS:        true,
			existingProto: "http",
			expectedProto: "https",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var capturedHeaders http.Header
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
				w.WriteHeader(http.StatusOK)
			}))
			defer backend.Close()

			cfg := &config.Config{
				Routes: []config.RouteConfig{
					{
						Hostname:    "test.example.com",
						Path:        "/webhook",
						Destination: backend.URL,
					},
				},
			}

			filters := ipfilter.NewFilterSet()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

			body := []byte(`{}`)
			req := httptest.NewRequest(http.MethodPost, "http://test.example.com/webhook", bytes.NewReader(body))
			req.Host = "test.example.com"
			req.RemoteAddr = "127.0.0.1:12345"

			if tc.useTLS {
				req.TLS = &tls.ConnectionState{}
			}
			if tc.existingProto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.existingProto)
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}

			proto := capturedHeaders.Get("X-Forwarded-Proto")
			if proto != tc.expectedProto {
				t.Errorf("expected X-Forwarded-Proto=%q, got %q", tc.expectedProto, proto)
			}
		})
	}
}

func TestHandler_PrefixRoutePreservesPathSuffix(t *testing.T) {
	// Create a test backend that captures the request URL
	var capturedPath string
	var capturedQuery string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    "test.example.com",
				Path:        "/hooks",
				Destination: backend.URL + "/api/webhooks",
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// Request to /hooks/github/events?challenge=abc should forward to /api/webhooks/github/events?challenge=abc
	req := httptest.NewRequest(http.MethodPost, "https://test.example.com/hooks/github/events?challenge=abc", nil)
	req.Host = "test.example.com"
	req.RemoteAddr = "127.0.0.1:12345"

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	// Check that path suffix was preserved
	if capturedPath != "/api/webhooks/github/events" {
		t.Errorf("expected path '/api/webhooks/github/events', got %q", capturedPath)
	}

	// Check that query string was preserved
	if capturedQuery != "challenge=abc" {
		t.Errorf("expected query 'challenge=abc', got %q", capturedQuery)
	}
}

func TestHandler_QueryStringMerging(t *testing.T) {
	var capturedQuery string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	tests := []struct {
		name          string
		destQuery     string // query params in destination URL
		requestQuery  string // query params in incoming request
		expectedQuery string
	}{
		{
			name:          "request has query, destination doesn't",
			destQuery:     "",
			requestQuery:  "event=push",
			expectedQuery: "event=push",
		},
		{
			name:          "destination has query, request doesn't",
			destQuery:     "token=secret",
			requestQuery:  "",
			expectedQuery: "token=secret",
		},
		{
			name:          "both have query params - should merge",
			destQuery:     "token=secret",
			requestQuery:  "event=push",
			expectedQuery: "token=secret&event=push",
		},
		{
			name:          "neither has query params",
			destQuery:     "",
			requestQuery:  "",
			expectedQuery: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			capturedQuery = ""

			dest := backend.URL + "/api"
			if tc.destQuery != "" {
				dest += "?" + tc.destQuery
			}

			cfg := &config.Config{
				Routes: []config.RouteConfig{
					{
						Hostname:    "test.example.com",
						Path:        "/webhook",
						Destination: dest,
					},
				},
			}

			filters := ipfilter.NewFilterSet()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

			reqURL := "https://test.example.com/webhook"
			if tc.requestQuery != "" {
				reqURL += "?" + tc.requestQuery
			}

			req := httptest.NewRequest(http.MethodPost, reqURL, bytes.NewReader([]byte("{}")))
			req.Host = "test.example.com"
			req.RemoteAddr = "127.0.0.1:12345"

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}

			if capturedQuery != tc.expectedQuery {
				t.Errorf("expected query %q, got %q", tc.expectedQuery, capturedQuery)
			}
		})
	}
}

func TestHandler_PrefixRouteSegmentBoundary(t *testing.T) {
	// Test that prefix matching is segment-aware: /hooks should NOT match /hookshot
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    "test.example.com",
				Path:        "/hooks",
				Destination: backend.URL,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	tests := []struct {
		name           string
		path           string
		expectedStatus int
	}{
		{"exact match", "/hooks", http.StatusOK},
		{"with trailing slash", "/hooks/", http.StatusOK},
		{"with suffix", "/hooks/github", http.StatusOK},
		{"similar prefix but not segment boundary", "/hookshot", http.StatusNotFound},
		{"similar prefix with more chars", "/hooks123", http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "https://test.example.com"+tc.path, nil)
			req.Host = "test.example.com"
			req.RemoteAddr = "127.0.0.1:12345"

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tc.expectedStatus {
				t.Errorf("path %q: expected status %d, got %d", tc.path, tc.expectedStatus, rr.Code)
			}
		})
	}
}

func TestHandler_BodySizeLimit(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    "test.example.com",
				Path:        "/webhook",
				Destination: backend.URL,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Create handler with small body size limit for testing
	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{
		MaxBodySize: 100, // 100 bytes
	})

	tests := []struct {
		name           string
		bodySize       int
		expectedStatus int
	}{
		{"body within limit", 50, http.StatusOK},
		{"body at limit", 100, http.StatusOK},
		{"body exceeds limit", 101, http.StatusRequestEntityTooLarge},
		{"body well over limit", 1000, http.StatusRequestEntityTooLarge},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := bytes.Repeat([]byte("x"), tc.bodySize)
			req := httptest.NewRequest(http.MethodPost, "https://test.example.com/webhook", bytes.NewReader(body))
			req.Host = "test.example.com"
			req.RemoteAddr = "127.0.0.1:12345"

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tc.expectedStatus {
				t.Errorf("body size %d: expected status %d, got %d", tc.bodySize, tc.expectedStatus, rr.Code)
			}
		})
	}
}

func TestHandler_PrefixRouteWithTrailingSlash(t *testing.T) {
	// Test that routes ending with "/" match deeper paths
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    "test.example.com",
				Path:        "/hooks/",
				Destination: backend.URL,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	tests := []struct {
		name           string
		path           string
		expectedStatus int
	}{
		{"exact match with trailing slash", "/hooks/", http.StatusOK},
		{"deeper path", "/hooks/github", http.StatusOK},
		{"even deeper path", "/hooks/github/events", http.StatusOK},
		{"without trailing slash - no match", "/hooks", http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "https://test.example.com"+tc.path, nil)
			req.Host = "test.example.com"
			req.RemoteAddr = "127.0.0.1:12345"

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tc.expectedStatus {
				t.Errorf("path %q: expected status %d, got %d", tc.path, tc.expectedStatus, rr.Code)
			}
		})
	}
}

func TestNewHandler_BuildVerifiers(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{Hostname: "test.com", Path: "/", Destination: "http://backend"},
		},
		Verifiers: map[string]config.VerifierConfig{
			"slack":   {Type: "slack", SigningSecret: "secret"},
			"github":  {Type: "github", Secret: "secret"},
			"shopify": {Type: "shopify", Secret: "secret"},
			"apikey":  {Type: "api_key", Header: "X-API-Key", Token: "token"},
			"hmac":    {Type: "hmac", Header: "X-Sig", Secret: "secret", Hash: "SHA256", Encoding: "hex"},
			"noop":    {Type: "noop"},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// Verify all verifiers were created
	if len(handler.verifiers) != 6 {
		t.Errorf("expected 6 verifiers, got %d", len(handler.verifiers))
	}
}

func TestNewHandler_InvalidVerifierType(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{Hostname: "test.com", Path: "/", Destination: "http://backend"},
		},
		Verifiers: map[string]config.VerifierConfig{
			"invalid": {Type: "unknown_type"},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	_, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err == nil {
		t.Error("expected error for invalid verifier type")
	}
}

func TestNewHandler_HMACVerifierError(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{Hostname: "test.com", Path: "/", Destination: "http://backend"},
		},
		Verifiers: map[string]config.VerifierConfig{
			"hmac": {Type: "hmac", Header: "X-Sig", Secret: "secret", Hash: "INVALID", Encoding: "hex"},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	_, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err == nil {
		t.Error("expected error for invalid HMAC hash algorithm")
	}
}

func TestHandler_SetRelayManager(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{Hostname: "test.com", Path: "/", Destination: "http://backend"},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	rm := relay.NewManager()
	handler.SetRelayManager(rm)

	if handler.relay != rm {
		t.Error("relay manager not set correctly")
	}
}

func TestHandler_RelayDelivery(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:   "test.com",
				Path:       "/webhook",
				RelayToken: "test-token",
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	rm := relay.NewManager()
	rm.RegisterToken("test-token")
	handler.SetRelayManager(rm)

	// Start a poll to accept the webhook
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()

	webhookReceived := make(chan *relay.Webhook)
	go func() {
		webhook, _ := rm.Poll(pollCtx, "test-token")
		webhookReceived <- webhook
	}()

	// Wait for poll to start
	time.Sleep(10 * time.Millisecond)

	// Make request in background (will block waiting for response)
	requestDone := make(chan struct{})
	var responseRecorder *httptest.ResponseRecorder
	go func() {
		body := []byte(`{"test":"data"}`)
		req := httptest.NewRequest(http.MethodPost, "https://test.com/webhook", bytes.NewReader(body))
		req.Host = "test.com"
		req.RemoteAddr = "127.0.0.1:12345"
		responseRecorder = httptest.NewRecorder()
		handler.ServeHTTP(responseRecorder, req)
		close(requestDone)
	}()

	// Receive the webhook
	var receivedWebhook *relay.Webhook
	select {
	case receivedWebhook = <-webhookReceived:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for webhook")
	}

	// Send response back
	err := rm.SendResponse(&relay.Response{
		RequestID:  receivedWebhook.ID,
		StatusCode: 201,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       base64.StdEncoding.EncodeToString([]byte(`{"ok":true}`)),
	})
	if err != nil {
		t.Fatalf("SendResponse failed: %v", err)
	}

	// Wait for request to complete
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for request")
	}

	if responseRecorder.Code != 201 {
		t.Errorf("expected status 201, got %d", responseRecorder.Code)
	}
	if responseRecorder.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type header")
	}
}

func TestHandler_RelayNoClient(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:   "test.com",
				Path:       "/webhook",
				RelayToken: "test-token",
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	rm := relay.NewManager()
	rm.RegisterToken("test-token")
	handler.SetRelayManager(rm)

	// No poll started - no client connected

	body := []byte(`{"test":"data"}`)
	req := httptest.NewRequest(http.MethodPost, "https://test.com/webhook", bytes.NewReader(body))
	req.Host = "test.com"
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rr.Code)
	}
}

func TestHandler_RelayManagerNotConfigured(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:   "test.com",
				Path:       "/webhook",
				RelayToken: "test-token",
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})
	// Don't set relay manager

	body := []byte(`{"test":"data"}`)
	req := httptest.NewRequest(http.MethodPost, "https://test.com/webhook", bytes.NewReader(body))
	req.Host = "test.com"
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rr.Code)
	}
}

func TestHandler_RelayDeliveryContextCancelled(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:   "test.com",
				Path:       "/webhook",
				RelayToken: "test-token",
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	rm := relay.NewManager()
	rm.RegisterToken("test-token")
	handler.SetRelayManager(rm)

	// Start a poll but don't send response (will cause context timeout)
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()

	go func() {
		_, _ = rm.Poll(pollCtx, "test-token")
	}()

	time.Sleep(10 * time.Millisecond)

	// Make request with canceled context
	body := []byte(`{"test":"data"}`)
	req := httptest.NewRequest(http.MethodPost, "https://test.com/webhook", bytes.NewReader(body))
	req.Host = "test.com"
	req.RemoteAddr = "127.0.0.1:12345"

	// Create a context that times out quickly
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Should get 502 Bad Gateway on delivery error (context canceled)
	if rr.Code != http.StatusBadGateway {
		t.Errorf("expected status 502, got %d", rr.Code)
	}
}

func TestHandler_VerifierNotFound(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    "test.com",
				Path:        "/webhook",
				Verifier:    "nonexistent",
				Destination: "http://backend",
			},
		},
		Verifiers: map[string]config.VerifierConfig{}, // Empty
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})
	// Manually add a route referencing a verifier that doesn't exist
	handler.routes[0].Verifier = "nonexistent"

	body := []byte(`{"test":"data"}`)
	req := httptest.NewRequest(http.MethodPost, "https://test.com/webhook", bytes.NewReader(body))
	req.Host = "test.com"
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rr.Code)
	}
}

func TestHandler_HostWithPort(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    "test.com",
				Path:        "/webhook",
				Destination: backend.URL,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	// Host with port should still match
	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "https://test.com:8443/webhook", bytes.NewReader(body))
	req.Host = "test.com:8443"
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestHandler_IPv6Host(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    "::1",
				Path:        "/webhook",
				Destination: backend.URL,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	// IPv6 host with port [::1]:8080 should match route for ::1
	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "http://[::1]:8080/webhook", bytes.NewReader(body))
	req.Host = "[::1]:8080"
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200 for IPv6 host with port, got %d", rr.Code)
	}
}

func TestHandler_RouteWithoutIPAllowlist(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    "test.com",
				Path:        "/webhook",
				Destination: backend.URL,
				// No IPAllowlist - any IP should be allowed
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "https://test.com/webhook", bytes.NewReader(body))
	req.Host = "test.com"
	req.RemoteAddr = "8.8.8.8:12345" // Would be blocked if there was an allowlist
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestHandler_WriteRelayResponse_EmptyBody(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{Hostname: "test.com", Path: "/", RelayToken: "token"},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	rr := httptest.NewRecorder()
	handler.writeRelayResponse(rr, &relay.Response{
		StatusCode: 204,
		Headers:    map[string][]string{"X-Custom": {"value"}},
		Body:       "", // Empty body
	})

	if rr.Code != 204 {
		t.Errorf("expected status 204, got %d", rr.Code)
	}
	if rr.Header().Get("X-Custom") != "value" {
		t.Errorf("expected X-Custom header")
	}
}

func TestHandler_WriteRelayResponse_InvalidBase64(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{Hostname: "test.com", Path: "/", RelayToken: "token"},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	rr := httptest.NewRecorder()
	handler.writeRelayResponse(rr, &relay.Response{
		StatusCode: 200,
		Body:       "not-valid-base64!!!",
	})

	// Should still write status code, just no body
	if rr.Code != 200 {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestHandler_InvalidDestinationURL(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    "test.com",
				Path:        "/webhook",
				Destination: "://invalid-url", // Invalid URL
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "https://test.com/webhook", bytes.NewReader(body))
	req.Host = "test.com"
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Errorf("expected status 502, got %d", rr.Code)
	}
}

// errorReader returns an error when Read is called
type errorReader struct {
	err error
}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, e.err
}

func TestHandler_BodyReadError(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    "test.com",
				Path:        "/webhook",
				Destination: "http://backend",
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	req := httptest.NewRequest(http.MethodPost, "https://test.com/webhook", &errorReader{err: fmt.Errorf("read error")})
	req.Host = "test.com"
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandler_UpstreamErrorStatusRecorded(t *testing.T) {
	// Test that upstream 4xx/5xx status codes are properly captured
	tests := []struct {
		name           string
		upstreamStatus int
	}{
		{"upstream 400", http.StatusBadRequest},
		{"upstream 404", http.StatusNotFound},
		{"upstream 500", http.StatusInternalServerError},
		{"upstream 502", http.StatusBadGateway},
		{"upstream 503", http.StatusServiceUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.upstreamStatus)
				_, _ = w.Write([]byte("upstream error"))
			}))
			defer backend.Close()

			cfg := &config.Config{
				Routes: []config.RouteConfig{
					{
						Hostname:    "test.com",
						Path:        "/webhook",
						Destination: backend.URL,
					},
				},
			}

			filters := ipfilter.NewFilterSet()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))

			handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

			body := []byte(`{}`)
			req := httptest.NewRequest(http.MethodPost, "https://test.com/webhook", bytes.NewReader(body))
			req.Host = "test.com"
			req.RemoteAddr = "127.0.0.1:12345"
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			// The recorded response should match the upstream status
			if rr.Code != tc.upstreamStatus {
				t.Errorf("expected status %d, got %d", tc.upstreamStatus, rr.Code)
			}
		})
	}
}

func TestGetClientIP_TrustEnabled(t *testing.T) {
	// Create a handler with TrustXForwardedFor enabled
	cfg := &config.Config{}
	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{TrustXForwardedFor: true})

	tests := []struct {
		name          string
		remoteAddr    string
		xForwardedFor string
		expectedIP    string
	}{
		{
			name:       "no X-Forwarded-For uses RemoteAddr (stripped port)",
			remoteAddr: "192.168.1.100:12345",
			expectedIP: "192.168.1.100",
		},
		{
			name:          "single IP in X-Forwarded-For",
			remoteAddr:    "10.0.0.1:12345",
			xForwardedFor: "203.0.113.50",
			expectedIP:    "203.0.113.50",
		},
		{
			name:          "multiple IPs in X-Forwarded-For uses leftmost",
			remoteAddr:    "10.0.0.1:12345",
			xForwardedFor: "203.0.113.50, 10.0.0.5, 10.0.0.1",
			expectedIP:    "203.0.113.50",
		},
		{
			name:          "X-Forwarded-For with spaces",
			remoteAddr:    "10.0.0.1:12345",
			xForwardedFor: "  203.0.113.50  ,  10.0.0.5  ",
			expectedIP:    "203.0.113.50",
		},
		{
			name:          "IPv6 in X-Forwarded-For",
			remoteAddr:    "10.0.0.1:12345",
			xForwardedFor: "2001:db8::1",
			expectedIP:    "2001:db8::1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xForwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tc.xForwardedFor)
			}

			ip := handler.getClientIP(req)
			if ip != tc.expectedIP {
				t.Errorf("expected IP %q, got %q", tc.expectedIP, ip)
			}
		})
	}
}

func TestGetClientIP_TrustDisabled(t *testing.T) {
	// Create a handler with TrustXForwardedFor disabled (default)
	cfg := &config.Config{}
	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{TrustXForwardedFor: false})

	tests := []struct {
		name          string
		remoteAddr    string
		xForwardedFor string
		expectedIP    string
	}{
		{
			name:       "uses RemoteAddr (stripped port)",
			remoteAddr: "192.168.1.100:12345",
			expectedIP: "192.168.1.100",
		},
		{
			name:          "ignores X-Forwarded-For when trust disabled",
			remoteAddr:    "10.0.0.1:12345",
			xForwardedFor: "203.0.113.50",
			expectedIP:    "10.0.0.1",
		},
		{
			name:          "ignores X-Forwarded-For chain when trust disabled",
			remoteAddr:    "10.0.0.1:12345",
			xForwardedFor: "203.0.113.50, 10.0.0.5, 10.0.0.1",
			expectedIP:    "10.0.0.1",
		},
		{
			name:       "IPv6 RemoteAddr",
			remoteAddr: "[2001:db8::1]:12345",
			expectedIP: "2001:db8::1",
		},
		{
			name:       "RemoteAddr without port",
			remoteAddr: "192.168.1.100",
			expectedIP: "192.168.1.100",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xForwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tc.xForwardedFor)
			}

			ip := handler.getClientIP(req)
			if ip != tc.expectedIP {
				t.Errorf("expected IP %q, got %q", tc.expectedIP, ip)
			}
		})
	}
}

func TestHandler_IPAllowlistWithXForwardedFor(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    "test.com",
				Path:        "/webhook",
				IPAllowlist: "allowed",
				Destination: backend.URL,
			},
		},
	}

	// Create filter that only allows 203.0.113.0/24
	filters := ipfilter.NewFilterSet()
	filter, _ := ipfilter.NewFilter("allowed", []string{"203.0.113.0/24"})
	filters.Add("allowed", filter)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Enable TrustXForwardedFor to test X-Forwarded-For behavior
	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{TrustXForwardedFor: true})

	tests := []struct {
		name          string
		remoteAddr    string
		xForwardedFor string
		expectedCode  int
	}{
		{
			name:         "allowed by RemoteAddr",
			remoteAddr:   "203.0.113.50:12345",
			expectedCode: http.StatusOK,
		},
		{
			name:         "denied by RemoteAddr",
			remoteAddr:   "192.168.1.1:12345",
			expectedCode: http.StatusForbidden,
		},
		{
			name:          "allowed by X-Forwarded-For",
			remoteAddr:    "10.0.0.1:12345",
			xForwardedFor: "203.0.113.50",
			expectedCode:  http.StatusOK,
		},
		{
			name:          "denied by X-Forwarded-For",
			remoteAddr:    "10.0.0.1:12345",
			xForwardedFor: "192.168.1.1",
			expectedCode:  http.StatusForbidden,
		},
		{
			name:          "X-Forwarded-For takes precedence over allowed RemoteAddr",
			remoteAddr:    "203.0.113.50:12345",
			xForwardedFor: "192.168.1.1",
			expectedCode:  http.StatusForbidden,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "https://test.com/webhook", bytes.NewReader([]byte("{}")))
			req.Host = "test.com"
			req.RemoteAddr = tc.remoteAddr
			if tc.xForwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tc.xForwardedFor)
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tc.expectedCode {
				t.Errorf("expected status %d, got %d", tc.expectedCode, rr.Code)
			}
		})
	}
}

func TestHandler_IPAllowlistIgnoresXForwardedForWhenTrustDisabled(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    "test.com",
				Path:        "/webhook",
				IPAllowlist: "allowed",
				Destination: backend.URL,
			},
		},
	}

	// Create filter that only allows 203.0.113.0/24
	filters := ipfilter.NewFilterSet()
	filter, _ := ipfilter.NewFilter("allowed", []string{"203.0.113.0/24"})
	filters.Add("allowed", filter)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Trust disabled (default) - X-Forwarded-For should be ignored
	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{TrustXForwardedFor: false})

	tests := []struct {
		name          string
		remoteAddr    string
		xForwardedFor string
		expectedCode  int
	}{
		{
			name:         "allowed by RemoteAddr",
			remoteAddr:   "203.0.113.50:12345",
			expectedCode: http.StatusOK,
		},
		{
			name:         "denied by RemoteAddr",
			remoteAddr:   "192.168.1.1:12345",
			expectedCode: http.StatusForbidden,
		},
		{
			name:          "spoofed X-Forwarded-For is ignored - uses RemoteAddr (denied)",
			remoteAddr:    "192.168.1.1:12345",
			xForwardedFor: "203.0.113.50", // Attacker tries to spoof allowed IP
			expectedCode:  http.StatusForbidden,
		},
		{
			name:          "spoofed X-Forwarded-For is ignored - uses RemoteAddr (allowed)",
			remoteAddr:    "203.0.113.50:12345",
			xForwardedFor: "192.168.1.1", // Would be denied if XFF was trusted
			expectedCode:  http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "https://test.com/webhook", bytes.NewReader([]byte("{}")))
			req.Host = "test.com"
			req.RemoteAddr = tc.remoteAddr
			if tc.xForwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tc.xForwardedFor)
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tc.expectedCode {
				t.Errorf("expected status %d, got %d", tc.expectedCode, rr.Code)
			}
		})
	}
}

func TestHandler_RootRoutePrefixForwarding(t *testing.T) {
	var receivedPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	tests := []struct {
		name         string
		routePath    string
		requestPath  string
		destPath     string
		expectedPath string
	}{
		{
			name:         "root route forwards to base path",
			routePath:    "/",
			requestPath:  "/foo",
			destPath:     "/api",
			expectedPath: "/api/foo",
		},
		{
			name:         "root route forwards to root destination",
			routePath:    "/",
			requestPath:  "/bar/baz",
			destPath:     "",
			expectedPath: "/bar/baz",
		},
		{
			name:         "root route with trailing slash destination",
			routePath:    "/",
			requestPath:  "/test",
			destPath:     "/prefix/",
			expectedPath: "/prefix/test",
		},
		{
			name:         "non-root route still works",
			routePath:    "/hooks",
			requestPath:  "/hooks/github",
			destPath:     "/api",
			expectedPath: "/api/github",
		},
		{
			name:         "exact match route",
			routePath:    "/webhook",
			requestPath:  "/webhook",
			destPath:     "/api/receive",
			expectedPath: "/api/receive",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			receivedPath = ""

			cfg := &config.Config{
				Routes: []config.RouteConfig{
					{
						Hostname:    "test.com",
						Path:        tc.routePath,
						Destination: backend.URL + tc.destPath,
					},
				},
			}

			filters := ipfilter.NewFilterSet()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

			req := httptest.NewRequest(http.MethodPost, "https://test.com"+tc.requestPath, bytes.NewReader([]byte("{}")))
			req.Host = "test.com"

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d", rr.Code)
			}
			if receivedPath != tc.expectedPath {
				t.Errorf("expected path %q, got %q", tc.expectedPath, receivedPath)
			}
		})
	}
}

func TestCategorizeVerificationError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "signature empty",
			err:      fmt.Errorf("wrapped: %w", verifier.ErrSignatureEmpty),
			expected: "signature_empty",
		},
		{
			name:     "signature mismatch",
			err:      fmt.Errorf("wrapped: %w", verifier.ErrSignatureMismatch),
			expected: "signature_mismatch",
		},
		{
			name:     "timestamp invalid",
			err:      fmt.Errorf("wrapped: %w", verifier.ErrTimestampInvalid),
			expected: "timestamp_invalid",
		},
		{
			name:     "timestamp expired",
			err:      fmt.Errorf("wrapped: %w", verifier.ErrTimestampExpired),
			expected: "timestamp_expired",
		},
		{
			name:     "token mismatch",
			err:      fmt.Errorf("wrapped: %w", verifier.ErrTokenMismatch),
			expected: "token_mismatch",
		},
		{
			name:     "unknown error",
			err:      fmt.Errorf("some random error"),
			expected: "unknown",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := categorizeVerificationError(tc.err)
			if result != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestHandler_PreserveHost_Direct(t *testing.T) {
	var receivedHost string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	tests := []struct {
		name         string
		preserveHost bool
		incomingHost string
		expectedHost string
	}{
		{
			name:         "preserve_host true - uses original host",
			preserveHost: true,
			incomingHost: "webhooks.example.com",
			expectedHost: "webhooks.example.com",
		},
		{
			name:         "preserve_host false - uses destination host",
			preserveHost: false,
			incomingHost: "webhooks.example.com",
			expectedHost: "", // Will be backend host
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			receivedHost = ""

			cfg := &config.Config{
				Routes: []config.RouteConfig{
					{
						Hostname:     "webhooks.example.com",
						Path:         "/webhook",
						Destination:  backend.URL,
						PreserveHost: tc.preserveHost,
					},
				},
			}

			filters := ipfilter.NewFilterSet()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

			req := httptest.NewRequest(http.MethodPost, "https://webhooks.example.com/webhook", bytes.NewReader([]byte("{}")))
			req.Host = tc.incomingHost
			req.RemoteAddr = "127.0.0.1:12345"

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}

			if tc.expectedHost != "" {
				if receivedHost != tc.expectedHost {
					t.Errorf("expected Host header %q, got %q", tc.expectedHost, receivedHost)
				}
			} else {
				// When not preserving, host should be the backend host (from URL)
				if receivedHost == tc.incomingHost {
					t.Errorf("expected Host header to be destination host, but got original host %q", receivedHost)
				}
			}
		})
	}
}

func TestHandler_WriteRelayResponse_StripsHopByHopHeaders(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:   "test.example.com",
				Path:       "/webhook",
				RelayToken: "test-token",
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	// Setup relay manager
	relayManager := relay.NewManager()
	relayManager.RegisterToken("test-token")
	handler.SetRelayManager(relayManager)

	// Start a poll in background to make the relay client "connected"
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()

	go func() {
		webhook, _ := relayManager.Poll(pollCtx, "test-token")
		if webhook != nil {
			// Send response with hop-by-hop headers
			_ = relayManager.SendResponse(&relay.Response{
				RequestID:  webhook.ID,
				StatusCode: 200,
				Headers: map[string][]string{
					"Content-Type":      {"application/json"},
					"X-Custom":          {"preserved"},
					"Connection":        {"keep-alive"},
					"Keep-Alive":        {"timeout=5"},
					"Transfer-Encoding": {"chunked"},
					"Content-Length":    {"9999"}, // Wrong length
				},
				Body: base64.StdEncoding.EncodeToString([]byte(`{"ok":true}`)),
			})
		}
	}()

	// Wait for poll to start
	time.Sleep(10 * time.Millisecond)

	// Send request to trigger relay delivery
	req := httptest.NewRequest(http.MethodPost, "https://test.example.com/webhook", bytes.NewReader([]byte("{}")))
	req.Host = "test.example.com"
	req.RemoteAddr = "127.0.0.1:12345"

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	// Verify hop-by-hop headers were stripped
	if rr.Header().Get("Keep-Alive") != "" {
		t.Error("Keep-Alive should be stripped from response")
	}
	if rr.Header().Get("Transfer-Encoding") != "" {
		t.Error("Transfer-Encoding should be stripped from response")
	}

	// Connection should be "close" (we set it explicitly)
	if rr.Header().Get("Connection") != "close" {
		t.Errorf("Connection should be 'close', got %q", rr.Header().Get("Connection"))
	}

	// Content-Length should match actual body length
	expectedLen := len(`{"ok":true}`)
	if rr.Header().Get("Content-Length") != fmt.Sprintf("%d", expectedLen) {
		t.Errorf("Content-Length should be %d, got %q", expectedLen, rr.Header().Get("Content-Length"))
	}

	// Custom header should be preserved
	if rr.Header().Get("X-Custom") != "preserved" {
		t.Error("X-Custom header should be preserved")
	}
}
