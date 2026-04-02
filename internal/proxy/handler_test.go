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
	"github.com/tight-line/gatekeeper/internal/ratelimit"
	"github.com/tight-line/gatekeeper/internal/relay"
	"github.com/tight-line/gatekeeper/internal/verifier"
)

// Test string constants used throughout handler_test.go.
// Defined once to satisfy SonarCloud S1192 (no duplicate string literals).
const (
	testExampleHost          = "test.example.com"
	testWebhookPath          = "/webhook"
	testIPFilterName         = "test-ips"
	testNoVerifyHost         = "noverify.example.com"
	testSecret               = "test-secret"
	errFmtHandler            = "failed to create handler: %v"
	testLoopbackAddr         = "127.0.0.1:12345"
	testSlackTimestampHeader = "X-Slack-Request-Timestamp"
	testSlackSigHeader       = "X-Slack-Signature"
	testSlackSigFmt          = "v0:%s:%s"
	errFmtStatusBody         = "expected status %d, got %d (body: %s)"
	testExampleWebhookHTTPS  = "https://test.example.com/webhook"
	testPrivateAddr          = "192.168.1.100:12345" // NOSONAR - test fixture: RFC 1918 private IP with port
	testHeaderContentType    = "Content-Type"
	testContentTypeJSON      = "application/json"
	testCustomHeader         = "X-Custom-Header"
	errFmtStatus200          = "expected status 200, got %d"
	testHeaderXFF            = "X-Forwarded-For"
	testExampleWebhookHTTP   = "http://test.example.com/webhook"
	testHooksPath            = "/hooks"
	testEventPush            = "event=push"
	testTokenSecret          = "token=secret"
	testHooksPrefix          = "/hooks/"
	testHooksGithub          = "/hooks/github"
	testBackendURL           = "http://backend"
	testHost                 = "test.com"
	testToken                = "test-token"
	testWebhookURL           = "https://test.com/webhook"
	errFmtStatus500          = "expected status 500, got %d"
	errFmtStatus502          = "expected status 502, got %d"
	testCustomHeaderShort    = "X-Custom"
	errFmtStatusD            = "expected status %d, got %d"
	testTruncated            = "... (truncated)"
	testSlackWebhookPath     = "/slack-webhook"
	testNoVerifierPath       = "/no-verifier"
	testSlackVerifierName    = "my-slack"
	testGithubVerifierName   = "my-github"
	testShopifyVerifierName  = "my-shopify"
	testNoopVerifierName     = "my-noop"
	testGitlabVerifierName   = "my-gitlab"
	testHeaderContentLength  = "Content-Length"
	testGraphWebhookPath     = "/graph-webhook"
	testMSGraphVerifierName  = "ms-graph"
	errFmtRequest200         = "request %d: expected 200, got %d"
	testPerIPMode            = "per-ip"
	testBaseURL              = "https://test.com"
	testPort                 = ":12345"
	testWebhooksHost         = "webhooks.example.com"
	errFmtWrapped            = "wrapped: %w"
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
				Hostname:    testExampleHost,
				Path:        testWebhookPath,
				IPAllowlist: testIPFilterName,
				Verifier:    "test-slack",
				Destination: backend.URL,
			},
			{
				Hostname:    testNoVerifyHost,
				Path:        testWebhookPath,
				IPAllowlist: testIPFilterName,
				Destination: backend.URL,
			},
		},
		Verifiers: map[string]config.VerifierConfig{
			"test-slack": {
				Type:          "slack",
				SigningSecret: testSecret,
			},
		},
	}

	// Build IP filters
	filters := ipfilter.NewFilterSet()
	filter, err := ipfilter.NewFilter(testIPFilterName, []string{testCIDRLoopback, testCIDRPrivate16})
	if err != nil {
		t.Fatalf("failed to create filter: %v", err)
	}
	filters.Add(testIPFilterName, filter)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
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
			path:           testWebhookPath,
			remoteAddr:     testLoopbackAddr,
			body:           []byte(`{"test":"data"}`),
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "ip not allowed",
			hostname:       testExampleHost,
			path:           testWebhookPath,
			remoteAddr:     testPublicIP + testPort,
			body:           []byte(`{"test":"data"}`),
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "missing signature",
			hostname:       testExampleHost,
			path:           testWebhookPath,
			remoteAddr:     testLoopbackAddr,
			body:           []byte(`{"test":"data"}`),
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid signature",
			hostname:   testExampleHost,
			path:       testWebhookPath,
			remoteAddr: testLoopbackAddr,
			body:       []byte(`{"test":"data"}`),
			setupHeaders: func(r *http.Request, body []byte) {
				r.Header.Set(testSlackTimestampHeader, strconv.FormatInt(time.Now().Unix(), 10))
				r.Header.Set(testSlackSigHeader, "v0=invalid")
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:       "valid slack request",
			hostname:   testExampleHost,
			path:       testWebhookPath,
			remoteAddr: testLoopbackAddr,
			body:       []byte(`{"test":"data"}`),
			setupHeaders: func(r *http.Request, body []byte) {
				timestamp := strconv.FormatInt(time.Now().Unix(), 10)
				sigBase := fmt.Sprintf(testSlackSigFmt, timestamp, string(body))
				mac := hmac.New(sha256.New, []byte(testSecret))
				mac.Write([]byte(sigBase))
				signature := "v0=" + hex.EncodeToString(mac.Sum(nil))
				r.Header.Set(testSlackTimestampHeader, timestamp)
				r.Header.Set(testSlackSigHeader, signature)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "route without verifier",
			hostname:       testNoVerifyHost,
			path:           testWebhookPath,
			remoteAddr:     testLoopbackAddr,
			body:           []byte(`{"test":"data"}`),
			expectedStatus: http.StatusOK,
		},
		{
			name:           "prefix path matching",
			hostname:       testNoVerifyHost,
			path:           "/webhook/subpath",
			remoteAddr:     testLoopbackAddr,
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
				t.Errorf(errFmtStatusBody, tt.expectedStatus, rr.Code, rr.Body.String())
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
				Hostname:    testExampleHost,
				Path:        testWebhookPath,
				Destination: backend.URL,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
	}

	body := []byte(`{"test":"data"}`)
	req := httptest.NewRequest(http.MethodPost, testExampleWebhookHTTPS, bytes.NewReader(body))
	req.Host = testExampleHost
	req.RemoteAddr = testPrivateIP + testPort
	req.Header.Set(testHeaderContentType, testContentTypeJSON)
	req.Header.Set(testCustomHeader, "custom-value")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf(errFmtStatus200, rr.Code)
	}

	// Check X-Forwarded headers were added (ReverseProxy may append its own)
	xff := capturedHeaders.Get(testHeaderXFF)
	if xff == "" || xff != testPrivateIP && !strings.HasPrefix(xff, testPrivateIP+",") {
		t.Errorf("expected X-Forwarded-For to start with %s, got %s", testPrivateIP, xff)
	}
	if capturedHeaders.Get("X-Forwarded-Host") != testExampleHost {
		t.Errorf("expected X-Forwarded-Host=test.example.com, got %s", capturedHeaders.Get("X-Forwarded-Host"))
	}

	// Check original headers are preserved
	if capturedHeaders.Get(testHeaderContentType) != testContentTypeJSON {
		t.Errorf("expected Content-Type=application/json, got %s", capturedHeaders.Get(testHeaderContentType))
	}
	if capturedHeaders.Get(testCustomHeader) != "custom-value" {
		t.Errorf("expected X-Custom-Header=custom-value, got %s", capturedHeaders.Get(testCustomHeader))
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
				Hostname:    testExampleHost,
				Path:        testWebhookPath,
				Destination: backend.URL,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, testExampleWebhookHTTP, bytes.NewReader(body))
	req.Host = testExampleHost
	req.RemoteAddr = testPrivate10IP + testPort
	// Simulate request already passed through upstream proxy
	req.Header.Set(testHeaderXFF, testDocIP1+", "+testDocIP2)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf(errFmtStatus200, rr.Code)
	}

	// httputil.ReverseProxy appends to existing X-Forwarded-For chain
	xff := capturedHeaders.Get(testHeaderXFF)
	expected := testDocIP1 + ", " + testDocIP2 + ", " + testPrivate10IP
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
						Hostname:    testExampleHost,
						Path:        testWebhookPath,
						Destination: backend.URL,
					},
				},
			}

			filters := ipfilter.NewFilterSet()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

			body := []byte(`{}`)
			req := httptest.NewRequest(http.MethodPost, testExampleWebhookHTTP, bytes.NewReader(body))
			req.Host = testExampleHost
			req.RemoteAddr = testLoopbackAddr

			if tc.useTLS {
				req.TLS = &tls.ConnectionState{}
			}
			if tc.existingProto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.existingProto)
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf(errFmtStatus200, rr.Code)
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
				Hostname:    testExampleHost,
				Path:        testHooksPath,
				Destination: backend.URL + "/api/webhooks",
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
	}

	// Request to /hooks/github/events?challenge=abc should forward to /api/webhooks/github/events?challenge=abc
	req := httptest.NewRequest(http.MethodPost, "https://test.example.com/hooks/github/events?challenge=abc", nil)
	req.Host = testExampleHost
	req.RemoteAddr = testLoopbackAddr

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf(errFmtStatus200, rr.Code)
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
			requestQuery:  testEventPush,
			expectedQuery: testEventPush,
		},
		{
			name:          "destination has query, request doesn't",
			destQuery:     testTokenSecret,
			requestQuery:  "",
			expectedQuery: testTokenSecret,
		},
		{
			name:          "both have query params - should merge",
			destQuery:     testTokenSecret,
			requestQuery:  testEventPush,
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
						Hostname:    testExampleHost,
						Path:        testWebhookPath,
						Destination: dest,
					},
				},
			}

			filters := ipfilter.NewFilterSet()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

			reqURL := testExampleWebhookHTTPS
			if tc.requestQuery != "" {
				reqURL += "?" + tc.requestQuery
			}

			req := httptest.NewRequest(http.MethodPost, reqURL, bytes.NewReader([]byte("{}")))
			req.Host = testExampleHost
			req.RemoteAddr = testLoopbackAddr

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf(errFmtStatus200, rr.Code)
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
				Hostname:    testExampleHost,
				Path:        testHooksPath,
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
		{"exact match", testHooksPath, http.StatusOK},
		{"with trailing slash", testHooksPrefix, http.StatusOK},
		{"with suffix", testHooksGithub, http.StatusOK},
		{"similar prefix but not segment boundary", "/hookshot", http.StatusNotFound},
		{"similar prefix with more chars", "/hooks123", http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "https://test.example.com"+tc.path, nil)
			req.Host = testExampleHost
			req.RemoteAddr = testLoopbackAddr

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
				Hostname:    testExampleHost,
				Path:        testWebhookPath,
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
			req := httptest.NewRequest(http.MethodPost, testExampleWebhookHTTPS, bytes.NewReader(body))
			req.Host = testExampleHost
			req.RemoteAddr = testLoopbackAddr

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
				Hostname:    testExampleHost,
				Path:        testHooksPrefix,
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
		{"exact match with trailing slash", testHooksPrefix, http.StatusOK},
		{"deeper path", testHooksGithub, http.StatusOK},
		{"even deeper path", "/hooks/github/events", http.StatusOK},
		{"without trailing slash - no match", testHooksPath, http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "https://test.example.com"+tc.path, nil)
			req.Host = testExampleHost
			req.RemoteAddr = testLoopbackAddr

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
			{Hostname: testHost, Path: "/", Destination: testBackendURL},
		},
		Verifiers: map[string]config.VerifierConfig{
			"slack":              {Type: "slack", SigningSecret: "secret"},
			"github":             {Type: "github", Secret: "secret"},
			"shopify":            {Type: "shopify", Secret: "secret"},
			"apikey":             {Type: "api_key", Header: "X-API-Key", Token: "token"},
			"hmac":               {Type: "hmac", Header: "X-Sig", Secret: "secret", Hash: "SHA256", Encoding: "hex"},
			"json_field":         {Type: "json_field", Path: "clientState", Token: "secret"},
			"query_param":        {Type: "query_param", Name: "token", Token: "secret"},
			"header_query_param": {Type: "header_query_param", Header: "X-Goog-Channel-Token", Name: "secret", Token: "mytoken"},
			"noop":               {Type: "noop"},
			"gitlab":             {Type: "gitlab", Token: "secret"},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
	}

	// Verify all verifiers were created
	if len(handler.verifiers) != 10 {
		t.Errorf("expected 10 verifiers, got %d", len(handler.verifiers))
	}
}

func TestNewHandler_InvalidVerifierType(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{Hostname: testHost, Path: "/", Destination: testBackendURL},
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
			{Hostname: testHost, Path: "/", Destination: testBackendURL},
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
			{Hostname: testHost, Path: "/", Destination: testBackendURL},
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
				Hostname:   testHost,
				Path:       testWebhookPath,
				RelayToken: testToken,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	rm := relay.NewManager()
	rm.RegisterToken(testToken)
	handler.SetRelayManager(rm)

	// Start a poll to accept the webhook
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()

	webhookReceived := make(chan *relay.Webhook)
	go func() {
		webhook, _ := rm.Poll(pollCtx, testToken)
		webhookReceived <- webhook
	}()

	// Wait for poll to start
	time.Sleep(10 * time.Millisecond)

	// Make request in background (will block waiting for response)
	requestDone := make(chan struct{})
	var responseRecorder *httptest.ResponseRecorder
	go func() {
		body := []byte(`{"test":"data"}`)
		req := httptest.NewRequest(http.MethodPost, testWebhookURL, bytes.NewReader(body))
		req.Host = testHost
		req.RemoteAddr = testLoopbackAddr
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
		Headers:    map[string][]string{testHeaderContentType: {testContentTypeJSON}},
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
	if responseRecorder.Header().Get(testHeaderContentType) != testContentTypeJSON {
		t.Errorf("expected Content-Type header")
	}
}

func TestHandler_RelayNoClient(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:   testHost,
				Path:       testWebhookPath,
				RelayToken: testToken,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	rm := relay.NewManager()
	rm.RegisterToken(testToken)
	handler.SetRelayManager(rm)

	// No poll started - no client connected

	body := []byte(`{"test":"data"}`)
	req := httptest.NewRequest(http.MethodPost, testWebhookURL, bytes.NewReader(body))
	req.Host = testHost
	req.RemoteAddr = testLoopbackAddr
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
				Hostname:   testHost,
				Path:       testWebhookPath,
				RelayToken: testToken,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})
	// Don't set relay manager

	body := []byte(`{"test":"data"}`)
	req := httptest.NewRequest(http.MethodPost, testWebhookURL, bytes.NewReader(body))
	req.Host = testHost
	req.RemoteAddr = testLoopbackAddr
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf(errFmtStatus500, rr.Code)
	}
}

func TestHandler_RelayDeliveryContextCancelled(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:   testHost,
				Path:       testWebhookPath,
				RelayToken: testToken,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	rm := relay.NewManager()
	rm.RegisterToken(testToken)
	handler.SetRelayManager(rm)

	// Start a poll but don't send response (will cause context timeout)
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()

	go func() {
		_, _ = rm.Poll(pollCtx, testToken)
	}()

	time.Sleep(10 * time.Millisecond)

	// Make request with canceled context
	body := []byte(`{"test":"data"}`)
	req := httptest.NewRequest(http.MethodPost, testWebhookURL, bytes.NewReader(body))
	req.Host = testHost
	req.RemoteAddr = testLoopbackAddr

	// Create a context that times out quickly
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Should get 502 Bad Gateway on delivery error (context canceled)
	if rr.Code != http.StatusBadGateway {
		t.Errorf(errFmtStatus502, rr.Code)
	}
}

func TestHandler_RelayDeliveryExplicitCancel(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:   testHost,
				Path:       testWebhookPath,
				RelayToken: testToken,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	rm := relay.NewManager()
	rm.RegisterToken(testToken)
	handler.SetRelayManager(rm)

	// Start a poll that will receive the webhook but cancel before responding
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()

	webhookReceived := make(chan struct{})
	go func() {
		webhook, _ := rm.Poll(pollCtx, testToken)
		if webhook != nil {
			close(webhookReceived)
			// Don't send response - let the request context be canceled
		}
	}()

	time.Sleep(10 * time.Millisecond)

	// Make request with a context that we'll cancel explicitly
	body := []byte(`{"test":"data"}`)
	req := httptest.NewRequest(http.MethodPost, testWebhookURL, bytes.NewReader(body))
	req.Host = testHost
	req.RemoteAddr = testLoopbackAddr

	// Create a context that we cancel explicitly (not timeout)
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	// Cancel the context after a short delay
	go func() {
		<-webhookReceived
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Should get 502 Bad Gateway on delivery error (context.Canceled)
	if rr.Code != http.StatusBadGateway {
		t.Errorf(errFmtStatus502, rr.Code)
	}
}

func TestHandler_VerifierNotFound(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    testHost,
				Path:        testWebhookPath,
				Verifier:    "nonexistent",
				Destination: testBackendURL,
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
	req := httptest.NewRequest(http.MethodPost, testWebhookURL, bytes.NewReader(body))
	req.Host = testHost
	req.RemoteAddr = testLoopbackAddr
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf(errFmtStatus500, rr.Code)
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
				Hostname:    testHost,
				Path:        testWebhookPath,
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
	req.RemoteAddr = testLoopbackAddr
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf(errFmtStatus200, rr.Code)
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
				Path:        testWebhookPath,
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
	req.RemoteAddr = testLoopbackAddr
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
				Hostname:    testHost,
				Path:        testWebhookPath,
				Destination: backend.URL,
				// No IPAllowlist - any IP should be allowed
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, testWebhookURL, bytes.NewReader(body))
	req.Host = testHost
	req.RemoteAddr = testPublicIP + testPort // Would be blocked if there was an allowlist
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf(errFmtStatus200, rr.Code)
	}
}

func TestHandler_WriteRelayResponse_EmptyBody(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{Hostname: testHost, Path: "/", RelayToken: "token"},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	rr := httptest.NewRecorder()
	handler.writeRelayResponse(rr, &relay.Response{
		StatusCode: 204,
		Headers:    map[string][]string{testCustomHeaderShort: {"value"}},
		Body:       "", // Empty body
	})

	if rr.Code != 204 {
		t.Errorf("expected status 204, got %d", rr.Code)
	}
	if rr.Header().Get(testCustomHeaderShort) != "value" {
		t.Errorf("expected X-Custom header")
	}
}

func TestHandler_WriteRelayResponse_InvalidBase64(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{Hostname: testHost, Path: "/", RelayToken: "token"},
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
		t.Errorf(errFmtStatus200, rr.Code)
	}
}

func TestHandler_InvalidDestinationURL(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    testHost,
				Path:        testWebhookPath,
				Destination: "://invalid-url", // Invalid URL
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, testWebhookURL, bytes.NewReader(body))
	req.Host = testHost
	req.RemoteAddr = testLoopbackAddr
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Errorf(errFmtStatus502, rr.Code)
	}
}

// errorReader returns an error when Read is called
type errorReader struct {
	err error
}

func (e *errorReader) Read(_ []byte) (n int, err error) {
	return 0, e.err
}

func TestHandler_BodyReadError(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    testHost,
				Path:        testWebhookPath,
				Destination: testBackendURL,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	req := httptest.NewRequest(http.MethodPost, testWebhookURL, &errorReader{err: fmt.Errorf("read error")})
	req.Host = testHost
	req.RemoteAddr = testLoopbackAddr
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
						Hostname:    testHost,
						Path:        testWebhookPath,
						Destination: backend.URL,
					},
				},
			}

			filters := ipfilter.NewFilterSet()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))

			handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

			body := []byte(`{}`)
			req := httptest.NewRequest(http.MethodPost, testWebhookURL, bytes.NewReader(body))
			req.Host = testHost
			req.RemoteAddr = testLoopbackAddr
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			// The recorded response should match the upstream status
			if rr.Code != tc.upstreamStatus {
				t.Errorf(errFmtStatusD, tc.upstreamStatus, rr.Code)
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
			remoteAddr: testPrivateIP + testPort,
			expectedIP: testPrivateIP,
		},
		{
			name:          "single IP in X-Forwarded-For",
			remoteAddr:    testPrivate10IP + testPort,
			xForwardedFor: testDocIP1,
			expectedIP:    testDocIP1,
		},
		{
			name:          "multiple IPs in X-Forwarded-For uses leftmost",
			remoteAddr:    testPrivate10IP + testPort,
			xForwardedFor: testDocIP1 + ", " + testPrivate10IP2 + ", " + testPrivate10IP,
			expectedIP:    testDocIP1,
		},
		{
			name:          "X-Forwarded-For with spaces",
			remoteAddr:    testPrivate10IP + testPort,
			xForwardedFor: "  " + testDocIP1 + "  ,  " + testPrivate10IP2 + "  ",
			expectedIP:    testDocIP1,
		},
		{
			name:          "IPv6 in X-Forwarded-For",
			remoteAddr:    testPrivate10IP + testPort,
			xForwardedFor: testIPv6Public,
			expectedIP:    testIPv6Public,
		},
		{
			name:          "private IP first, public IP second - returns public",
			remoteAddr:    testPrivate10IP + testPort,
			xForwardedFor: testPrivate10IP3 + ", " + testPublicIP2,
			expectedIP:    testPublicIP2,
		},
		{
			name:          "multiple private IPs then public - returns public",
			remoteAddr:    testPrivate10IP + testPort,
			xForwardedFor: testPrivate10IP3 + ", " + testPrivateIP2 + ", " + testPublicIP2 + ", " + testPrivate172IP,
			expectedIP:    testPublicIP2,
		},
		{
			name:          "loopback IP skipped",
			remoteAddr:    testPrivate10IP + testPort,
			xForwardedFor: "127.0.0.1, " + testDocIP1,
			expectedIP:    testDocIP1,
		},
		{
			name:          "all private IPs - returns leftmost as fallback",
			remoteAddr:    testPrivate10IP + testPort,
			xForwardedFor: testPrivate10IP3 + ", " + testPrivateIP2 + ", " + testPrivate172IP,
			expectedIP:    testPrivate10IP3,
		},
		{
			name:          "link-local IP skipped",
			remoteAddr:    testPrivate10IP + testPort,
			xForwardedFor: testLinkLocalIP3 + ", " + testDocIP1,
			expectedIP:    testDocIP1,
		},
		{
			name:          "single private IP - returns it as fallback",
			remoteAddr:    testPrivate10IP + testPort,
			xForwardedFor: testPrivateIP,
			expectedIP:    testPrivateIP,
		},
		{
			name:          "empty entries in X-Forwarded-For skipped",
			remoteAddr:    testPrivate10IP + testPort,
			xForwardedFor: testPrivate10IP2 + ", , , " + testDocIP1,
			expectedIP:    testDocIP1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xForwardedFor != "" {
				req.Header.Set(testHeaderXFF, tc.xForwardedFor)
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
			remoteAddr: testPrivateIP + testPort,
			expectedIP: testPrivateIP,
		},
		{
			name:          "ignores X-Forwarded-For when trust disabled",
			remoteAddr:    testPrivate10IP + testPort,
			xForwardedFor: testDocIP1,
			expectedIP:    testPrivate10IP,
		},
		{
			name:          "ignores X-Forwarded-For chain when trust disabled",
			remoteAddr:    testPrivate10IP + testPort,
			xForwardedFor: testDocIP1 + ", " + testPrivate10IP2 + ", " + testPrivate10IP,
			expectedIP:    testPrivate10IP,
		},
		{
			name:       "IPv6 RemoteAddr",
			remoteAddr: "[2001:db8::1]:12345",
			expectedIP: testIPv6Public,
		},
		{
			name:       "RemoteAddr without port",
			remoteAddr: testPrivateIP,
			expectedIP: testPrivateIP,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xForwardedFor != "" {
				req.Header.Set(testHeaderXFF, tc.xForwardedFor)
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
				Hostname:    testHost,
				Path:        testWebhookPath,
				IPAllowlist: "allowed",
				Destination: backend.URL,
			},
		},
	}

	// Create filter that only allows 203.0.113.0/24
	filters := ipfilter.NewFilterSet()
	filter, _ := ipfilter.NewFilter("allowed", []string{testCIDRDocNet})
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
			remoteAddr:   testDocIP1 + testPort,
			expectedCode: http.StatusOK,
		},
		{
			name:         "denied by RemoteAddr",
			remoteAddr:   testPrivateIP2 + testPort,
			expectedCode: http.StatusForbidden,
		},
		{
			name:          "allowed by X-Forwarded-For",
			remoteAddr:    testPrivate10IP + testPort,
			xForwardedFor: testDocIP1,
			expectedCode:  http.StatusOK,
		},
		{
			name:          "denied by X-Forwarded-For",
			remoteAddr:    testPrivate10IP + testPort,
			xForwardedFor: testPrivateIP2,
			expectedCode:  http.StatusForbidden,
		},
		{
			name:          "X-Forwarded-For takes precedence over allowed RemoteAddr",
			remoteAddr:    testDocIP1 + testPort,
			xForwardedFor: testPrivateIP2,
			expectedCode:  http.StatusForbidden,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, testWebhookURL, bytes.NewReader([]byte("{}")))
			req.Host = testHost
			req.RemoteAddr = tc.remoteAddr
			if tc.xForwardedFor != "" {
				req.Header.Set(testHeaderXFF, tc.xForwardedFor)
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tc.expectedCode {
				t.Errorf(errFmtStatusD, tc.expectedCode, rr.Code)
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
				Hostname:    testHost,
				Path:        testWebhookPath,
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
			remoteAddr:   testPrivateIP2 + testPort,
			expectedCode: http.StatusForbidden,
		},
		{
			name:          "spoofed X-Forwarded-For is ignored - uses RemoteAddr (denied)",
			remoteAddr:    testPrivateIP2 + testPort,
			xForwardedFor: testDocIP1, // Attacker tries to spoof allowed IP
			expectedCode:  http.StatusForbidden,
		},
		{
			name:          "spoofed X-Forwarded-For is ignored - uses RemoteAddr (allowed)",
			remoteAddr:    testDocIP1 + testPort,
			xForwardedFor: testPrivateIP2, // Would be denied if XFF was trusted
			expectedCode:  http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, testWebhookURL, bytes.NewReader([]byte("{}")))
			req.Host = testHost
			req.RemoteAddr = tc.remoteAddr
			if tc.xForwardedFor != "" {
				req.Header.Set(testHeaderXFF, tc.xForwardedFor)
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tc.expectedCode {
				t.Errorf(errFmtStatusD, tc.expectedCode, rr.Code)
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
			routePath:    testHooksPath,
			requestPath:  testHooksGithub,
			destPath:     "/api",
			expectedPath: "/api/github",
		},
		{
			name:         "exact match route",
			routePath:    testWebhookPath,
			requestPath:  testWebhookPath,
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
						Hostname:    testHost,
						Path:        tc.routePath,
						Destination: backend.URL + tc.destPath,
					},
				},
			}

			filters := ipfilter.NewFilterSet()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

			req := httptest.NewRequest(http.MethodPost, testBaseURL+tc.requestPath, bytes.NewReader([]byte("{}")))
			req.Host = testHost

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf(errFmtStatus200, rr.Code)
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
			err:      fmt.Errorf(errFmtWrapped, verifier.ErrSignatureEmpty),
			expected: "signature_empty",
		},
		{
			name:     "signature mismatch",
			err:      fmt.Errorf(errFmtWrapped, verifier.ErrSignatureMismatch),
			expected: "signature_mismatch",
		},
		{
			name:     "timestamp invalid",
			err:      fmt.Errorf(errFmtWrapped, verifier.ErrTimestampInvalid),
			expected: "timestamp_invalid",
		},
		{
			name:     "timestamp expired",
			err:      fmt.Errorf(errFmtWrapped, verifier.ErrTimestampExpired),
			expected: "timestamp_expired",
		},
		{
			name:     "token mismatch",
			err:      fmt.Errorf(errFmtWrapped, verifier.ErrTokenMismatch),
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

// assertReceivedHost checks the received Host header against expectations.
func assertReceivedHost(t *testing.T, expectedHost, incomingHost, receivedHost string) {
	t.Helper()
	if expectedHost != "" {
		if receivedHost != expectedHost {
			t.Errorf("expected Host header %q, got %q", expectedHost, receivedHost)
		}
		return
	}
	// When not preserving, host should be the backend host (from URL)
	if receivedHost == incomingHost {
		t.Errorf("expected Host header to be destination host, but got original host %q", receivedHost)
	}
}

// assertBackendCalled checks whether the backend was called as expected.
func assertBackendCalled(t *testing.T, backendCalled, backendShouldRun bool) {
	t.Helper()
	if backendCalled == backendShouldRun {
		return
	}
	if backendShouldRun {
		t.Error("expected backend to be called, but it wasn't")
	} else {
		t.Error("expected backend NOT to be called, but it was")
	}
}

// assertStatusAndBody checks the response status code and optional body.
func assertStatusAndBody(t *testing.T, rr *httptest.ResponseRecorder, expectedStatus int, expectedBody string) {
	t.Helper()
	if rr.Code != expectedStatus {
		t.Errorf(errFmtStatusBody, expectedStatus, rr.Code, rr.Body.String())
	}
	if expectedBody != "" && rr.Body.String() != expectedBody {
		t.Errorf("expected body %q, got %q", expectedBody, rr.Body.String())
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
			incomingHost: testWebhooksHost,
			expectedHost: testWebhooksHost,
		},
		{
			name:         "preserve_host false - uses destination host",
			preserveHost: false,
			incomingHost: testWebhooksHost,
			expectedHost: "", // Will be backend host
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			receivedHost = ""

			cfg := &config.Config{
				Routes: []config.RouteConfig{
					{
						Hostname:     testWebhooksHost,
						Path:         testWebhookPath,
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
			req.RemoteAddr = testLoopbackAddr

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf(errFmtStatus200, rr.Code)
			}

			assertReceivedHost(t, tc.expectedHost, tc.incomingHost, receivedHost)
		})
	}
}

func TestNewHandler_BuildValidators(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{Hostname: testHost, Path: "/", Destination: testBackendURL},
		},
		Validators: map[string]config.ValidatorConfig{
			"json": {
				Type:   "json_schema",
				Schema: `{"type": "object", "required": ["id"]}`,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
	}

	// Verify validator was created
	if len(handler.validators) != 1 {
		t.Errorf("expected 1 validator, got %d", len(handler.validators))
	}
}

func TestNewHandler_InvalidValidatorType(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{Hostname: testHost, Path: "/", Destination: testBackendURL},
		},
		Validators: map[string]config.ValidatorConfig{
			"invalid": {Type: "unknown_type"},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	_, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err == nil {
		t.Error("expected error for invalid validator type")
	}
}

func TestHandler_ValidatorNotFound(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    testHost,
				Path:        testWebhookPath,
				Validator:   "nonexistent",
				Destination: testBackendURL,
			},
		},
		Validators: map[string]config.ValidatorConfig{}, // Empty
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})
	// Manually add a route referencing a validator that doesn't exist
	handler.routes[0].Validator = "nonexistent"

	body := []byte(`{"test":"data"}`)
	req := httptest.NewRequest(http.MethodPost, testWebhookURL, bytes.NewReader(body))
	req.Host = testHost
	req.RemoteAddr = testLoopbackAddr
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf(errFmtStatus500, rr.Code)
	}
}

func TestHandler_ValidationFailure(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    testHost,
				Path:        testWebhookPath,
				Validator:   "strict-schema",
				Destination: backend.URL,
			},
		},
		Validators: map[string]config.ValidatorConfig{
			"strict-schema": {
				Type:   "json_schema",
				Schema: `{"type": "object", "required": ["id"], "properties": {"id": {"type": "integer"}}}`,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
	}

	// Invalid payload - missing required "id" field
	body := []byte(`{"name":"test"}`)
	req := httptest.NewRequest(http.MethodPost, testWebhookURL, bytes.NewReader(body))
	req.Host = testHost
	req.RemoteAddr = testLoopbackAddr
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for validation failure, got %d", rr.Code)
	}
}

func TestHandler_ValidationSuccess(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    testHost,
				Path:        testWebhookPath,
				Validator:   "schema",
				Destination: backend.URL,
			},
		},
		Validators: map[string]config.ValidatorConfig{
			"schema": {
				Type:   "json_schema",
				Schema: `{"type": "object", "required": ["id"], "properties": {"id": {"type": "integer"}}}`,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
	}

	// Valid payload
	body := []byte(`{"id": 123}`)
	req := httptest.NewRequest(http.MethodPost, testWebhookURL, bytes.NewReader(body))
	req.Host = testHost
	req.RemoteAddr = testLoopbackAddr
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200 for valid payload, got %d", rr.Code)
	}
}

func TestHandler_ValidationWithVerification(t *testing.T) {
	// Test that validation happens after verification passes
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    testHost,
				Path:        testWebhookPath,
				Verifier:    "slack",
				Validator:   "schema",
				Destination: backend.URL,
			},
		},
		Verifiers: map[string]config.VerifierConfig{
			"slack": {
				Type:          "slack",
				SigningSecret: testSecret,
			},
		},
		Validators: map[string]config.ValidatorConfig{
			"schema": {
				Type:   "json_schema",
				Schema: `{"type": "object", "required": ["id"]}`,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
	}

	// Valid signature but invalid payload
	body := []byte(`{"name":"test"}`)
	req := httptest.NewRequest(http.MethodPost, testWebhookURL, bytes.NewReader(body))
	req.Host = testHost
	req.RemoteAddr = testLoopbackAddr

	// Sign the request
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	sigBase := fmt.Sprintf(testSlackSigFmt, timestamp, string(body))
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(sigBase))
	signature := "v0=" + hex.EncodeToString(mac.Sum(nil))
	req.Header.Set(testSlackTimestampHeader, timestamp)
	req.Header.Set(testSlackSigHeader, signature)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Should get 400 (validation failure) not 401 (verification failure)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for validation failure after successful verification, got %d", rr.Code)
	}
}

func TestHandler_SlackURLVerification(t *testing.T) {
	// Backend should NOT be called for URL verification challenges
	backendCalled := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    testHost,
				Path:        testSlackWebhookPath,
				Verifier:    "slack",
				Destination: backend.URL,
			},
			{
				Hostname:    testHost,
				Path:        "/noop-webhook",
				Verifier:    "noop",
				Destination: backend.URL,
			},
			{
				Hostname:    testHost,
				Path:        testNoVerifierPath,
				Destination: backend.URL,
			},
		},
		Verifiers: map[string]config.VerifierConfig{
			"slack": {
				Type:          "slack",
				SigningSecret: testSecret,
			},
			"noop": {
				Type: "noop",
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
	}

	signRequest := func(body []byte) func(r *http.Request) {
		return func(r *http.Request) {
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			sigBase := fmt.Sprintf(testSlackSigFmt, timestamp, string(body))
			mac := hmac.New(sha256.New, []byte(testSecret))
			mac.Write([]byte(sigBase))
			signature := "v0=" + hex.EncodeToString(mac.Sum(nil))
			r.Header.Set(testSlackTimestampHeader, timestamp)
			r.Header.Set(testSlackSigHeader, signature)
		}
	}

	tests := []struct {
		name             string
		path             string
		body             string
		setupHeaders     func(r *http.Request)
		expectedStatus   int
		expectedBody     string
		backendShouldRun bool
	}{
		{
			name:             "URL verification challenge is handled directly",
			path:             testSlackWebhookPath,
			body:             `{"type":"url_verification","challenge":"test-challenge-123"}`,
			setupHeaders:     signRequest([]byte(`{"type":"url_verification","challenge":"test-challenge-123"}`)),
			expectedStatus:   http.StatusOK,
			expectedBody:     "test-challenge-123",
			backendShouldRun: false,
		},
		{
			name:             "regular Slack event is forwarded",
			path:             testSlackWebhookPath,
			body:             `{"type":"event_callback","event":{"type":"message"}}`,
			setupHeaders:     signRequest([]byte(`{"type":"event_callback","event":{"type":"message"}}`)),
			expectedStatus:   http.StatusOK,
			backendShouldRun: true,
		},
		{
			name:             "URL verification on non-Slack route is forwarded",
			path:             "/noop-webhook",
			body:             `{"type":"url_verification","challenge":"test-challenge"}`,
			expectedStatus:   http.StatusOK,
			backendShouldRun: true,
		},
		{
			name:             "URL verification on route without verifier is forwarded",
			path:             testNoVerifierPath,
			body:             `{"type":"url_verification","challenge":"test-challenge"}`,
			expectedStatus:   http.StatusOK,
			backendShouldRun: true,
		},
		{
			name:             "invalid JSON is forwarded (not treated as URL verification)",
			path:             testSlackWebhookPath,
			body:             `not json`,
			setupHeaders:     signRequest([]byte(`not json`)),
			expectedStatus:   http.StatusOK,
			backendShouldRun: true,
		},
		{
			name:             "missing challenge field is forwarded",
			path:             testSlackWebhookPath,
			body:             `{"type":"url_verification"}`,
			setupHeaders:     signRequest([]byte(`{"type":"url_verification"}`)),
			expectedStatus:   http.StatusOK,
			backendShouldRun: true,
		},
		{
			name:             "empty challenge is forwarded",
			path:             testSlackWebhookPath,
			body:             `{"type":"url_verification","challenge":""}`,
			setupHeaders:     signRequest([]byte(`{"type":"url_verification","challenge":""}`)),
			expectedStatus:   http.StatusOK,
			backendShouldRun: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backendCalled = false

			req := httptest.NewRequest(http.MethodPost, testBaseURL+tc.path, bytes.NewReader([]byte(tc.body)))
			req.Host = testHost
			req.RemoteAddr = testLoopbackAddr

			if tc.setupHeaders != nil {
				tc.setupHeaders(req)
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assertStatusAndBody(t, rr, tc.expectedStatus, tc.expectedBody)
			assertBackendCalled(t, backendCalled, tc.backendShouldRun)
		})
	}
}

func TestHandler_SlackURLVerification_Relay(t *testing.T) {
	// Test that URL verification is handled directly even in relay mode
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:   testHost,
				Path:       testWebhookPath,
				Verifier:   "slack",
				RelayToken: testToken,
			},
		},
		Verifiers: map[string]config.VerifierConfig{
			"slack": {
				Type:          "slack",
				SigningSecret: testSecret,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	// Setup relay manager but DON'T start polling
	// This simulates relay mode where there might be latency or no client connected
	rm := relay.NewManager()
	rm.RegisterToken(testToken)
	handler.SetRelayManager(rm)

	// Send URL verification request
	body := []byte(`{"type":"url_verification","challenge":"relay-test-challenge"}`)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	sigBase := fmt.Sprintf(testSlackSigFmt, timestamp, string(body))
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(sigBase))
	signature := "v0=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, testWebhookURL, bytes.NewReader(body))
	req.Host = testHost
	req.RemoteAddr = testLoopbackAddr
	req.Header.Set(testSlackTimestampHeader, timestamp)
	req.Header.Set(testSlackSigHeader, signature)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Should respond immediately with challenge, NOT 503 (no relay client)
	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "relay-test-challenge" {
		t.Errorf("expected body 'relay-test-challenge', got %q", rr.Body.String())
	}
}

func TestHandler_VerifierTypesMap(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{Hostname: testHost, Path: "/", Destination: testBackendURL},
		},
		Verifiers: map[string]config.VerifierConfig{
			testSlackVerifierName:   {Type: "slack", SigningSecret: "secret"},
			testGithubVerifierName:  {Type: "github", Secret: "secret"},
			testShopifyVerifierName: {Type: "shopify", Secret: "secret"},
			testNoopVerifierName:    {Type: "noop"},
			testGitlabVerifierName:  {Type: "gitlab", Token: "secret"},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
	}

	// Verify verifier types are tracked
	if handler.verifierTypes[testSlackVerifierName] != "slack" {
		t.Errorf("expected verifierTypes['my-slack']='slack', got %q", handler.verifierTypes[testSlackVerifierName])
	}
	if handler.verifierTypes[testGithubVerifierName] != "github" {
		t.Errorf("expected verifierTypes['my-github']='github', got %q", handler.verifierTypes[testGithubVerifierName])
	}
	if handler.verifierTypes[testShopifyVerifierName] != "shopify" {
		t.Errorf("expected verifierTypes['my-shopify']='shopify', got %q", handler.verifierTypes[testShopifyVerifierName])
	}
	if handler.verifierTypes[testNoopVerifierName] != "noop" {
		t.Errorf("expected verifierTypes['my-noop']='noop', got %q", handler.verifierTypes[testNoopVerifierName])
	}
	if handler.verifierTypes[testGitlabVerifierName] != "gitlab" {
		t.Errorf("expected verifierTypes['my-gitlab']='gitlab', got %q", handler.verifierTypes[testGitlabVerifierName])
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip       string
		expected bool
	}{
		// Private IPv4 (RFC 1918)
		{testPrivate10IP, true},
		{testPrivate10IP4, true},
		{testPrivate172IP, true},
		{testPrivate172IP2, true},
		{testPrivateIP3, true},
		{testPrivateIP4, true},

		// Loopback
		{testLoopbackIP, true},
		{testLoopbackIP2, true},

		// Link-local
		{testLinkLocalIP, true},
		{testLinkLocalIP2, true},

		// Public IPv4
		{testPublicIP, false},
		{testDocIP1, false},
		{testPublicIP2, false},
		{testPublicIP3, false},

		// IPv6 loopback
		{testIPv6Loopback, true},

		// IPv6 link-local
		{testIPv6LinkLocal, true},

		// IPv6 private (ULA)
		{testIPv6ULA, true},

		// IPv6 public
		{testIPv6Public, false},

		// Invalid
		{"not-an-ip", false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.ip, func(t *testing.T) {
			result := isPrivateIP(tc.ip)
			if result != tc.expected {
				t.Errorf("isPrivateIP(%q) = %v, want %v", tc.ip, result, tc.expected)
			}
		})
	}
}

func TestHandler_WriteRelayResponse_StripsHopByHopHeaders(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:   testExampleHost,
				Path:       testWebhookPath,
				RelayToken: testToken,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	// Setup relay manager
	relayManager := relay.NewManager()
	relayManager.RegisterToken(testToken)
	handler.SetRelayManager(relayManager)

	// Start a poll in background to make the relay client "connected"
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()

	go func() {
		webhook, _ := relayManager.Poll(pollCtx, testToken)
		if webhook != nil {
			// Send response with hop-by-hop headers
			_ = relayManager.SendResponse(&relay.Response{
				RequestID:  webhook.ID,
				StatusCode: 200,
				Headers: map[string][]string{
					testHeaderContentType:   {testContentTypeJSON},
					testCustomHeaderShort:   {"preserved"},
					"Connection":            {"keep-alive"},
					"Keep-Alive":            {"timeout=5"},
					"Transfer-Encoding":     {"chunked"},
					testHeaderContentLength: {"9999"}, // Wrong length
				},
				Body: base64.StdEncoding.EncodeToString([]byte(`{"ok":true}`)),
			})
		}
	}()

	// Wait for poll to start
	time.Sleep(10 * time.Millisecond)

	// Send request to trigger relay delivery
	req := httptest.NewRequest(http.MethodPost, testExampleWebhookHTTPS, bytes.NewReader([]byte("{}")))
	req.Host = testExampleHost
	req.RemoteAddr = testLoopbackAddr

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf(errFmtStatus200, rr.Code)
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
	if rr.Header().Get(testHeaderContentLength) != fmt.Sprintf("%d", expectedLen) {
		t.Errorf("Content-Length should be %d, got %q", expectedLen, rr.Header().Get(testHeaderContentLength))
	}

	// Custom header should be preserved
	if rr.Header().Get(testCustomHeaderShort) != "preserved" {
		t.Error("X-Custom header should be preserved")
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
			expected: string(bytes.Repeat([]byte("a"), 8192)) + testTruncated,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := truncateForLog(tc.input)
			if result != tc.expected {
				if len(tc.expected) > 100 {
					t.Errorf("expected length %d (truncated=%v), got length %d (truncated=%v)",
						len(tc.expected), strings.HasSuffix(tc.expected, testTruncated),
						len(result), strings.HasSuffix(result, testTruncated))
				} else {
					t.Errorf("expected %q, got %q", tc.expected, result)
				}
			}
		})
	}
}

func TestHandler_DebugPayloads(t *testing.T) {
	// Create a test backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(testHeaderContentType, testContentTypeJSON)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"ok"}`))
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    testExampleHost,
				Path:        testWebhookPath,
				Destination: backend.URL,
			},
		},
	}

	filters := ipfilter.NewFilterSet()

	// Capture log output to verify debug logging
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{
		DebugPayloads: true,
	})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
	}

	req := httptest.NewRequest("POST", testExampleWebhookHTTP, strings.NewReader(`{"test":"data"}`))
	req.Header.Set(testHeaderContentType, testContentTypeJSON)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf(errFmtStatus200, rr.Code)
	}

	logOutput := logBuf.String()

	// Verify debug logging occurred
	if !strings.Contains(logOutput, "debug: incoming request") {
		t.Error("expected 'debug: incoming request' in logs")
	}
	// Body is logged as a JSON string field (quotes escaped), just check key parts exist
	if !strings.Contains(logOutput, "test") {
		t.Error("expected request body content in debug logs")
	}
	if !strings.Contains(logOutput, "debug: outgoing response") {
		t.Error("expected 'debug: outgoing response' in logs")
	}
	if !strings.Contains(logOutput, "result") {
		t.Error("expected response body content in debug logs")
	}
}

func TestHandler_DebugPayloads_Relay(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:   testExampleHost,
				Path:       testWebhookPath,
				RelayToken: testToken,
			},
		},
	}

	filters := ipfilter.NewFilterSet()

	// Capture log output
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{
		DebugPayloads: true,
	})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
	}

	// Set up relay manager
	relayMgr := relay.NewMemoryManager()
	relayMgr.RegisterToken(testToken)
	handler.SetRelayManager(relayMgr)

	// Start relay client goroutine first and give it time to start polling
	pollStarted := make(chan struct{})
	go func() {
		close(pollStarted)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		webhook, err := relayMgr.Poll(ctx, testToken)
		if err != nil || webhook == nil {
			return
		}
		_ = relayMgr.SendResponse(&relay.Response{
			RequestID:  webhook.ID,
			StatusCode: 200,
			Headers:    map[string][]string{testHeaderContentType: {testContentTypeJSON}},
			Body:       base64.StdEncoding.EncodeToString([]byte(`{"relayed":"true"}`)),
		})
	}()

	// Wait for poll to start
	<-pollStarted
	time.Sleep(10 * time.Millisecond)

	req := httptest.NewRequest("POST", testExampleWebhookHTTP, strings.NewReader(`{"relay":"test"}`))
	req.Header.Set(testHeaderContentType, testContentTypeJSON)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf(errFmtStatus200, rr.Code)
	}

	logOutput := logBuf.String()

	// Verify debug logging occurred for relay
	if !strings.Contains(logOutput, "debug: incoming request") {
		t.Error("expected 'debug: incoming request' in logs")
	}
	if !strings.Contains(logOutput, "debug: outgoing response") {
		t.Error("expected 'debug: outgoing response' in logs")
	}
}

func TestHandler_MicrosoftGraphValidation(t *testing.T) {
	// Backend should NOT be called for validation requests
	backendCalled := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    testHost,
				Path:        testGraphWebhookPath,
				Verifier:    testMSGraphVerifierName,
				Destination: backend.URL,
			},
			{
				Hostname:    testHost,
				Path:        testSlackWebhookPath,
				Verifier:    "slack",
				Destination: backend.URL,
			},
			{
				Hostname:    testHost,
				Path:        testNoVerifierPath,
				Destination: backend.URL,
			},
		},
		Verifiers: map[string]config.VerifierConfig{
			testMSGraphVerifierName: {
				Type:  "json_field",
				Path:  "value.0.clientState",
				Token: testToken,
			},
			"slack": {
				Type:          "slack",
				SigningSecret: testSecret,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
	}

	tests := []struct {
		name             string
		path             string
		queryParams      string
		body             string
		expectedStatus   int
		expectedBody     string
		backendShouldRun bool
	}{
		{
			name:             "validation token is echoed back",
			path:             testGraphWebhookPath,
			queryParams:      "validationToken=Validation%3ATestToken123",
			body:             "",
			expectedStatus:   http.StatusOK,
			expectedBody:     "Validation:TestToken123",
			backendShouldRun: false,
		},
		{
			name:             "validation token with special characters",
			path:             testGraphWebhookPath,
			queryParams:      "validationToken=abc%2B%2F%3D123",
			body:             "",
			expectedStatus:   http.StatusOK,
			expectedBody:     "abc+/=123",
			backendShouldRun: false,
		},
		{
			name:             "no validationToken - fails verification (empty body)",
			path:             testGraphWebhookPath,
			queryParams:      "",
			body:             "",
			expectedStatus:   http.StatusUnauthorized,
			backendShouldRun: false,
		},
		{
			name:             "validationToken on non-json_field route is ignored",
			path:             testSlackWebhookPath,
			queryParams:      "validationToken=ShouldBeIgnored",
			body:             "",
			expectedStatus:   http.StatusUnauthorized, // Slack verification fails
			backendShouldRun: false,
		},
		{
			name:             "validationToken on route without verifier is ignored",
			path:             testNoVerifierPath,
			queryParams:      "validationToken=ShouldBeIgnored",
			body:             "",
			expectedStatus:   http.StatusOK, // No verification needed, forwarded to backend
			backendShouldRun: true,
		},
		{
			name:             "regular Graph notification with valid body is forwarded",
			path:             testGraphWebhookPath,
			queryParams:      "",
			body:             `{"value":[{"clientState":"test-token"}]}`,
			expectedStatus:   http.StatusOK,
			backendShouldRun: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backendCalled = false

			url := testBaseURL + tc.path
			if tc.queryParams != "" {
				url += "?" + tc.queryParams
			}

			req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(tc.body)))
			req.Host = testHost
			req.RemoteAddr = testLoopbackAddr
			setGraphContentType(req, tc.body)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assertStatusAndBody(t, rr, tc.expectedStatus, tc.expectedBody)
			assertBackendCalled(t, backendCalled, tc.backendShouldRun)
		})
	}
}

// setGraphContentType sets Content-Type based on whether the body is empty.
func setGraphContentType(req *http.Request, body string) {
	if body != "" {
		req.Header.Set(testHeaderContentType, testContentTypeJSON)
	} else {
		req.Header.Set(testHeaderContentType, "text/plain; charset=utf-8")
	}
}

func TestHandler_MicrosoftGraphValidation_Relay(t *testing.T) {
	// Test that validation is handled directly even in relay mode
	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:   testHost,
				Path:       testWebhookPath,
				Verifier:   testMSGraphVerifierName,
				RelayToken: testToken,
			},
		},
		Verifiers: map[string]config.VerifierConfig{
			testMSGraphVerifierName: {
				Type:  "json_field",
				Path:  "value.0.clientState",
				Token: testToken,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, _ := NewHandler(cfg, filters, logger, HandlerOptions{})

	// Setup relay manager but DON'T start polling
	// This simulates relay mode where there might be latency or no client connected
	rm := relay.NewManager()
	rm.RegisterToken(testToken)
	handler.SetRelayManager(rm)

	// Send validation request
	req := httptest.NewRequest(http.MethodPost, "https://test.com/webhook?validationToken=relay-test-token", nil)
	req.Host = testHost
	req.RemoteAddr = testLoopbackAddr
	req.Header.Set(testHeaderContentType, "text/plain; charset=utf-8")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Should respond immediately with token, NOT 503 (no relay client)
	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "relay-test-token" {
		t.Errorf("expected body 'relay-test-token', got %q", rr.Body.String())
	}
}

func TestHandler_RateLimiting_NoLimiter(t *testing.T) {
	// Without rate limiters configured, requests should pass through
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    testHost,
				Path:        testWebhookPath,
				Destination: backend.URL,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
	}
	// No SetRateLimiters call - rate limiting not configured

	// Multiple requests should all succeed
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodPost, testWebhookURL, strings.NewReader("test"))
		req.Host = testHost
		req.RemoteAddr = testLoopbackAddr
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf(errFmtRequest200, i, rr.Code)
		}
	}
}

func TestHandler_RateLimiting_TotalLimit(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    testHost,
				Path:        testWebhookPath,
				Destination: backend.URL,
				RateLimiter: "strict",
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
	}

	// Create rate limiter set with very strict limits
	limiters := ratelimit.NewSet()
	defer limiters.Stop()
	limiters.Add("strict", ratelimit.New("strict", ratelimit.Config{
		TotalRPS: 1,
		PerIPRPS: 0, // Only total limiting
		Burst:    1,
	}))
	handler.SetRateLimiters(limiters, "")

	// First request should succeed
	req := httptest.NewRequest(http.MethodPost, testWebhookURL, strings.NewReader("test"))
	req.Host = testHost
	req.RemoteAddr = testLoopbackAddr
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("first request: expected 200, got %d", rr.Code)
	}

	// Second request should be rate limited
	req = httptest.NewRequest(http.MethodPost, testWebhookURL, strings.NewReader("test"))
	req.Host = testHost
	req.RemoteAddr = testPrivateIP2 + testPort // Different IP, but total limit applies
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("second request: expected 429, got %d", rr.Code)
	}
	if rr.Header().Get("Retry-After") != "1" {
		t.Errorf("expected Retry-After: 1, got %q", rr.Header().Get("Retry-After"))
	}
}

func TestHandler_RateLimiting_PerIPLimit(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    testHost,
				Path:        testWebhookPath,
				Destination: backend.URL,
				RateLimiter: testPerIPMode,
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
	}

	// Burst applies to both total and per-IP equally
	// With burst=5 and per_ip_rps=1, each IP gets 5 burst requests before hitting the per-IP limit
	// High total RPS ensures total limit refills fast enough to not be a bottleneck
	limiters := ratelimit.NewSet()
	defer limiters.Stop()
	limiters.Add(testPerIPMode, ratelimit.New(testPerIPMode, ratelimit.Config{
		TotalRPS: 10000, // Very high total limit (refills quickly)
		PerIPRPS: 1,     // Low per-IP limit
		Burst:    5,     // Allow 5 burst requests per IP
	}))
	handler.SetRateLimiters(limiters, "")

	// First 5 requests from IP1 should succeed (using burst)
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, testWebhookURL, strings.NewReader("test"))
		req.Host = testHost
		req.RemoteAddr = testPrivateIP2 + testPort
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("IP1 request %d: expected 200, got %d", i+1, rr.Code)
		}
	}

	// 6th request from IP1 should be rate limited (burst exhausted)
	req := httptest.NewRequest(http.MethodPost, testWebhookURL, strings.NewReader("test"))
	req.Host = testHost
	req.RemoteAddr = testPrivateIP2 + testPort
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("IP1 6th request: expected 429, got %d", rr.Code)
	}

	// First request from IP2 should succeed (different per-IP limiter with its own burst)
	req = httptest.NewRequest(http.MethodPost, testWebhookURL, strings.NewReader("test"))
	req.Host = testHost
	req.RemoteAddr = testPrivateIP5 + testPort
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("IP2 first request: expected 200, got %d", rr.Code)
	}
}

func TestHandler_RateLimiting_GlobalDefault(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    testHost,
				Path:        testWebhookPath,
				Destination: backend.URL,
				// No RateLimiter specified - should use global default
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
	}

	limiters := ratelimit.NewSet()
	defer limiters.Stop()
	limiters.Add("default", ratelimit.New("default", ratelimit.Config{
		TotalRPS: 1,
		Burst:    1,
	}))
	handler.SetRateLimiters(limiters, "default") // Set global default

	// First request should succeed
	req := httptest.NewRequest(http.MethodPost, testWebhookURL, strings.NewReader("test"))
	req.Host = testHost
	req.RemoteAddr = testLoopbackAddr
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("first request: expected 200, got %d", rr.Code)
	}

	// Second request should be rate limited
	req = httptest.NewRequest(http.MethodPost, testWebhookURL, strings.NewReader("test"))
	req.Host = testHost
	req.RemoteAddr = testLoopbackAddr
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("second request: expected 429, got %d", rr.Code)
	}
}

func TestHandler_RateLimiting_RouteOverridesDefault(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    testHost,
				Path:        testWebhookPath,
				Destination: backend.URL,
				RateLimiter: "lenient", // Route-specific limiter
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
	}

	limiters := ratelimit.NewSet()
	defer limiters.Stop()
	// Strict default that would block after 1 request
	limiters.Add("default", ratelimit.New("default", ratelimit.Config{
		TotalRPS: 1,
		Burst:    1,
	}))
	// Lenient route-specific limiter
	limiters.Add("lenient", ratelimit.New("lenient", ratelimit.Config{
		TotalRPS: 100,
		Burst:    10,
	}))
	handler.SetRateLimiters(limiters, "default")

	// Multiple requests should succeed (using lenient limiter, not default)
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, testWebhookURL, strings.NewReader("test"))
		req.Host = testHost
		req.RemoteAddr = testLoopbackAddr
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf(errFmtRequest200, i, rr.Code)
		}
	}
}

func TestHandler_RateLimiting_NoDefaultNoRoute(t *testing.T) {
	// When no default and no route limiter, rate limiting is skipped
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Hostname:    testHost,
				Path:        testWebhookPath,
				Destination: backend.URL,
				// No RateLimiter specified
			},
		},
	}

	filters := ipfilter.NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := NewHandler(cfg, filters, logger, HandlerOptions{})
	if err != nil {
		t.Fatalf(errFmtHandler, err)
	}

	limiters := ratelimit.NewSet()
	defer limiters.Stop()
	limiters.Add("unused", ratelimit.New("unused", ratelimit.Config{
		TotalRPS: 1,
		Burst:    1,
	}))
	handler.SetRateLimiters(limiters, "") // No global default

	// Multiple requests should succeed (no limiter applied)
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodPost, testWebhookURL, strings.NewReader("test"))
		req.Host = testHost
		req.RemoteAddr = testLoopbackAddr
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf(errFmtRequest200, i, rr.Code)
		}
	}
}
