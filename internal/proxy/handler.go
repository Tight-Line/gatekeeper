package proxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/tight-line/gatekeeper/internal/config"
	gkhttputil "github.com/tight-line/gatekeeper/internal/httputil"
	"github.com/tight-line/gatekeeper/internal/ipfilter"
	"github.com/tight-line/gatekeeper/internal/metrics"
	"github.com/tight-line/gatekeeper/internal/relay"
	"github.com/tight-line/gatekeeper/internal/validator"
	"github.com/tight-line/gatekeeper/internal/verifier"
)

const errInternalServerError = "Internal Server Error"

// HandlerOptions configures the proxy handler
type HandlerOptions struct {
	// TrustXForwardedFor controls whether the handler trusts the X-Forwarded-For
	// header for determining client IP addresses. Set this to true when running
	// behind a reverse proxy (ingress controller, load balancer) that sets this
	// header. When false, the handler uses the TCP connection's remote address.
	TrustXForwardedFor bool

	// MaxBodySize is the maximum allowed request body size in bytes.
	// If zero, defaults to config.DefaultMaxBodySize (10MB).
	MaxBodySize int64

	// DebugPayloads enables logging of request/response payloads for debugging.
	// When enabled, request bodies and response bodies are logged to stdout.
	DebugPayloads bool
}

// Handler handles incoming webhook requests
type Handler struct {
	routes             []config.RouteConfig
	verifiers          map[string]verifier.Verifier
	verifierTypes      map[string]string // verifier name -> type (e.g., "slack", "github")
	validators         map[string]validator.Validator
	filters            *ipfilter.FilterSet
	relay              relay.Manager
	logger             *slog.Logger
	trustXForwardedFor bool
	maxBodySize        int64
	debugPayloads      bool

	// Route lookup maps
	routesByHostPath map[string]*config.RouteConfig // "host:path" -> route
}

// NewHandler creates a new proxy handler
func NewHandler(cfg *config.Config, filters *ipfilter.FilterSet, logger *slog.Logger, opts HandlerOptions) (*Handler, error) {
	maxBodySize := opts.MaxBodySize
	if maxBodySize <= 0 {
		maxBodySize = config.DefaultMaxBodySize
	}

	h := &Handler{
		routes:             cfg.Routes,
		verifiers:          make(map[string]verifier.Verifier),
		verifierTypes:      make(map[string]string),
		validators:         make(map[string]validator.Validator),
		filters:            filters,
		logger:             logger,
		trustXForwardedFor: opts.TrustXForwardedFor,
		maxBodySize:        maxBodySize,
		debugPayloads:      opts.DebugPayloads,
		routesByHostPath:   make(map[string]*config.RouteConfig),
	}

	// Build verifiers from config
	for name, vc := range cfg.Verifiers {
		v, err := buildVerifier(vc)
		if err != nil {
			return nil, err
		}
		h.verifiers[name] = v
		h.verifierTypes[name] = vc.Type
	}

	// Build validators from config
	for name, valCfg := range cfg.Validators {
		v, err := buildValidator(valCfg)
		if err != nil {
			return nil, fmt.Errorf("building validator %q: %w", name, err)
		}
		h.validators[name] = v
	}

	// Build route lookup map (hostnames are lowercased for case-insensitive matching per RFC 7230)
	for i := range cfg.Routes {
		route := &cfg.Routes[i]
		key := strings.ToLower(route.Hostname) + ":" + route.Path
		h.routesByHostPath[key] = route
	}

	return h, nil
}

// SetRelayManager sets the relay manager for handling relay delivery
func (h *Handler) SetRelayManager(rm relay.Manager) {
	h.relay = rm
}

// buildVerifier creates a verifier from config
func buildVerifier(vc config.VerifierConfig) (verifier.Verifier, error) {
	switch vc.Type {
	case "slack":
		return verifier.NewSlackVerifier(vc.SigningSecret, vc.MaxTimestampAge), nil
	case "github":
		return verifier.NewGitHubVerifier(vc.Secret), nil
	case "shopify":
		return verifier.NewShopifyVerifier(vc.Secret), nil
	case "api_key":
		return verifier.NewAPIKeyVerifier(vc.Header, vc.Token), nil
	case "hmac":
		return verifier.NewHMACVerifier(vc.Header, vc.Secret, vc.Hash, vc.Encoding)
	case "json_field":
		return verifier.NewJSONFieldVerifier(vc.Path, vc.Token), nil
	case "query_param":
		return verifier.NewQueryParamVerifier(vc.Name, vc.Token), nil
	case "header_query_param":
		return verifier.NewHeaderQueryParamVerifier(vc.Header, vc.Name, vc.Token), nil
	case "noop":
		return verifier.NewNoopVerifier(), nil
	default:
		return nil, fmt.Errorf("unknown verifier type: %s", vc.Type)
	}
}

// buildValidator creates a validator from config
func buildValidator(vc config.ValidatorConfig) (validator.Validator, error) {
	switch vc.Type {
	case "json_schema":
		return validator.NewJSONSchemaValidator(validator.JSONSchemaConfig{
			SchemaFile:    vc.SchemaFile,
			SchemaContent: vc.Schema,
			Name:          vc.SchemaFile, // Use file path as name, or empty for inline
		})
	default:
		return nil, fmt.Errorf("unknown validator type: %s", vc.Type)
	}
}

// requestContext holds the state for processing a single request
type requestContext struct {
	hostname string
	route    *config.RouteConfig
	body     []byte
	start    time.Time
}

// ServeHTTP handles incoming requests
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := &requestContext{start: time.Now()}

	ctx.hostname = strings.ToLower(r.Host)
	if host, _, err := net.SplitHostPort(ctx.hostname); err == nil {
		ctx.hostname = host
	}

	ctx.route = h.findRoute(ctx.hostname, r.URL.Path)
	if ctx.route == nil {
		h.handleNotFound(w, r, ctx)
		return
	}

	if !h.checkIPAllowlist(w, r, ctx) {
		return
	}

	var err error
	ctx.body, err = h.readBody(w, r, ctx)
	if err != nil {
		return
	}

	// Log incoming request payload if debug is enabled
	if h.debugPayloads {
		h.logRequestPayload(r, ctx)
	}

	if !h.verifyRequest(w, r, ctx) {
		return
	}

	// Handle Slack URL verification challenge directly (no forwarding needed)
	// This allows setup of Slack webhooks even in relay mode where latency might cause timeout
	if h.handleSlackURLVerification(w, r, ctx) {
		return
	}

	if !h.validatePayload(w, r, ctx) {
		return
	}

	if ctx.route.RelayToken != "" {
		h.handleRelay(w, r, ctx)
	} else {
		h.handleForward(w, r, ctx)
	}
}

func (h *Handler) handleNotFound(w http.ResponseWriter, r *http.Request, ctx *requestContext) {
	h.logger.Warn("no matching route",
		"hostname", ctx.hostname,
		"path", r.URL.Path,
		"remote_addr", r.RemoteAddr,
	)
	metrics.RecordRequest("unknown", "unknown", "404", time.Since(ctx.start).Seconds())
	http.Error(w, "Not Found", http.StatusNotFound)
}

func (h *Handler) checkIPAllowlist(w http.ResponseWriter, r *http.Request, ctx *requestContext) bool {
	if ctx.route.IPAllowlist == "" {
		return true
	}

	clientIP := h.getClientIP(r)
	if h.filters.Allow(ctx.route.IPAllowlist, clientIP) {
		return true
	}

	h.logger.Warn("ip not allowed",
		"hostname", ctx.hostname,
		"path", r.URL.Path,
		"client_ip", clientIP,
		"remote_addr", r.RemoteAddr,
		"allowlist", ctx.route.IPAllowlist,
	)
	metrics.RecordIPDenied(ctx.hostname, ctx.route.IPAllowlist)
	metrics.RecordRequest(ctx.hostname, ctx.route.Path, "403", time.Since(ctx.start).Seconds())
	http.Error(w, "Forbidden", http.StatusForbidden)
	return false
}

func (h *Handler) readBody(w http.ResponseWriter, r *http.Request, ctx *requestContext) ([]byte, error) {
	limitedReader := io.LimitReader(r.Body, h.maxBodySize+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		h.logger.Error("failed to read body",
			"hostname", ctx.hostname,
			"path", r.URL.Path,
			"error", err,
		)
		metrics.RecordRequest(ctx.hostname, ctx.route.Path, "400", time.Since(ctx.start).Seconds())
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return nil, err
	}

	if int64(len(body)) > h.maxBodySize {
		h.logger.Warn("request body too large",
			"hostname", ctx.hostname,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"max_size", h.maxBodySize,
		)
		metrics.RecordRequest(ctx.hostname, ctx.route.Path, "413", time.Since(ctx.start).Seconds())
		http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
		return nil, errors.New("body too large")
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func (h *Handler) verifyRequest(w http.ResponseWriter, r *http.Request, ctx *requestContext) bool {
	if ctx.route.Verifier == "" {
		return true
	}

	v, ok := h.verifiers[ctx.route.Verifier]
	if !ok {
		h.logger.Error("verifier not found",
			"hostname", ctx.hostname,
			"path", r.URL.Path,
			"verifier", ctx.route.Verifier,
		)
		metrics.RecordRequest(ctx.hostname, ctx.route.Path, "500", time.Since(ctx.start).Seconds())
		http.Error(w, errInternalServerError, http.StatusInternalServerError)
		return false
	}

	if err := v.Verify(r, ctx.body); err != nil {
		h.logger.Warn("verification failed",
			"hostname", ctx.hostname,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"verifier", ctx.route.Verifier,
			"error", err.Error(),
		)
		metrics.RecordVerificationFailure(ctx.hostname, ctx.route.Verifier, categorizeVerificationError(err))
		metrics.RecordRequest(ctx.hostname, ctx.route.Path, "401", time.Since(ctx.start).Seconds())
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}

	return true
}

// slackURLVerification represents Slack's URL verification challenge request
type slackURLVerification struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
}

// handleSlackURLVerification checks if this is a Slack URL verification challenge
// and responds directly without forwarding. This is necessary because:
// 1. Slack requires a response within 3 seconds
// 2. Relay mode may have latency that exceeds this timeout
// 3. The backend doesn't need to handle this - it's just a setup handshake
//
// Returns true if the request was handled (caller should return), false otherwise.
func (h *Handler) handleSlackURLVerification(w http.ResponseWriter, r *http.Request, ctx *requestContext) bool {
	// Only handle for Slack verifier routes
	if ctx.route.Verifier == "" || h.verifierTypes[ctx.route.Verifier] != "slack" {
		return false
	}

	// Try to parse as URL verification request
	var req slackURLVerification
	if err := json.Unmarshal(ctx.body, &req); err != nil {
		return false
	}

	// Check if this is a URL verification challenge
	if req.Type != "url_verification" || req.Challenge == "" {
		return false
	}

	// Respond with the challenge directly
	h.logger.Info("handling Slack URL verification challenge",
		"hostname", ctx.hostname,
		"path", r.URL.Path,
	)

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(req.Challenge))

	metrics.RecordRequest(ctx.hostname, ctx.route.Path, "200", time.Since(ctx.start).Seconds())
	return true
}

func (h *Handler) validatePayload(w http.ResponseWriter, r *http.Request, ctx *requestContext) bool {
	if ctx.route.Validator == "" {
		return true
	}

	val, ok := h.validators[ctx.route.Validator]
	if !ok {
		h.logger.Error("validator not found",
			"hostname", ctx.hostname,
			"path", r.URL.Path,
			"validator", ctx.route.Validator,
		)
		metrics.RecordRequest(ctx.hostname, ctx.route.Path, "500", time.Since(ctx.start).Seconds())
		http.Error(w, errInternalServerError, http.StatusInternalServerError)
		return false
	}

	if err := val.Validate(ctx.body); err != nil {
		h.logger.Warn("validation failed",
			"hostname", ctx.hostname,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"validator", ctx.route.Validator,
			"error", err.Error(),
		)
		metrics.RecordValidationFailure(ctx.hostname, ctx.route.Validator)
		metrics.RecordRequest(ctx.hostname, ctx.route.Path, "400", time.Since(ctx.start).Seconds())
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return false
	}

	return true
}

func (h *Handler) handleRelay(w http.ResponseWriter, r *http.Request, ctx *requestContext) {
	if h.relay == nil {
		h.logger.Error("relay manager not configured",
			"hostname", ctx.hostname,
			"path", r.URL.Path,
		)
		metrics.RecordRequest(ctx.hostname, ctx.route.Path, "500", time.Since(ctx.start).Seconds())
		http.Error(w, errInternalServerError, http.StatusInternalServerError)
		return
	}

	// Record that a webhook was queued for relay
	metrics.RecordRelayWebhookQueued(ctx.route.RelayToken)
	deliveryStart := time.Now()

	resp, err := h.relay.DeliverHTTPRequest(r.Context(), ctx.route.RelayToken, r, ctx.body, ctx.route.PreserveHost)
	if err != nil {
		h.handleRelayError(w, r, ctx, err)
		return
	}

	// Record successful delivery
	deliveryDuration := time.Since(deliveryStart).Seconds()
	metrics.RecordRelayWebhookDelivered(ctx.route.RelayToken, deliveryDuration)

	// Log response payload if debug is enabled
	if h.debugPayloads {
		respBody, _ := base64.StdEncoding.DecodeString(resp.Body)
		h.logResponsePayload(ctx, resp.StatusCode, respBody)
	}

	h.writeRelayResponse(w, resp)
	statusStr := fmt.Sprintf("%d", resp.StatusCode)
	metrics.RecordRequest(ctx.hostname, ctx.route.Path, statusStr, time.Since(ctx.start).Seconds())
	h.logger.Info("request relayed",
		"hostname", ctx.hostname,
		"path", r.URL.Path,
		"client_ip", h.getClientIP(r),
		"status", resp.StatusCode,
		"duration_ms", time.Since(ctx.start).Milliseconds(),
	)
}

func (h *Handler) handleRelayError(w http.ResponseWriter, r *http.Request, ctx *requestContext, err error) {
	if err == relay.ErrNoClient {
		h.logger.Warn("no relay client connected",
			"hostname", ctx.hostname,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
		)
		metrics.RecordRelayDeliveryError(ctx.route.RelayToken, "no_client")
		metrics.RecordRequest(ctx.hostname, ctx.route.Path, "503", time.Since(ctx.start).Seconds())
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	// Determine error reason
	reason := "unknown"
	if errors.Is(err, context.DeadlineExceeded) {
		reason = "timeout"
	} else if errors.Is(err, context.Canceled) {
		reason = "canceled"
	}
	metrics.RecordRelayDeliveryError(ctx.route.RelayToken, reason)

	h.logger.Error("relay delivery failed",
		"hostname", ctx.hostname,
		"path", r.URL.Path,
		"error", err,
	)
	metrics.RecordRequest(ctx.hostname, ctx.route.Path, "502", time.Since(ctx.start).Seconds())
	http.Error(w, "Bad Gateway", http.StatusBadGateway)
}

func (h *Handler) handleForward(w http.ResponseWriter, r *http.Request, ctx *requestContext) {
	status, respBody, err := h.forward(w, r, ctx.route, ctx.body, h.debugPayloads)
	if err != nil {
		h.logger.Error("forward failed",
			"hostname", ctx.hostname,
			"path", r.URL.Path,
			"destination", ctx.route.Destination,
			"error", err,
		)
		metrics.RecordForwardError(ctx.hostname, ctx.route.Destination)
		metrics.RecordRequest(ctx.hostname, ctx.route.Path, "502", time.Since(ctx.start).Seconds())
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}

	// Log response payload if debug is enabled
	if h.debugPayloads && respBody != nil {
		h.logResponsePayload(ctx, status, respBody)
	}

	statusStr := fmt.Sprintf("%d", status)
	metrics.RecordRequest(ctx.hostname, ctx.route.Path, statusStr, time.Since(ctx.start).Seconds())
	h.logger.Info("request forwarded",
		"hostname", ctx.hostname,
		"path", r.URL.Path,
		"client_ip", h.getClientIP(r),
		"destination", ctx.route.Destination,
		"status", status,
		"duration_ms", time.Since(ctx.start).Milliseconds(),
	)
}

// findRoute finds a matching route for the hostname and path.
// Hostname matching is case-insensitive per RFC 7230.
// The hostname parameter should already be lowercased by the caller.
func (h *Handler) findRoute(hostname, path string) *config.RouteConfig {
	// Try exact match first (routesByHostPath keys are already lowercased)
	if route, ok := h.routesByHostPath[hostname+":"+path]; ok {
		return route
	}

	// Try prefix matching (segment-aware: /hooks matches /hooks and /hooks/*, not /hookshot)
	for i := range h.routes {
		route := &h.routes[i]
		if strings.EqualFold(route.Hostname, hostname) && strings.HasPrefix(path, route.Path) {
			// Ensure prefix ends at segment boundary
			// Root route "/" matches all paths (since all paths start with /)
			// Routes ending with "/" already define a segment boundary
			if route.Path == "/" || strings.HasSuffix(route.Path, "/") || len(path) == len(route.Path) || path[len(route.Path)] == '/' {
				return route
			}
		}
	}

	return nil
}

// forward proxies the request to the destination transparently and returns the upstream status code.
// If captureBody is true, it also returns the response body for debugging.
func (h *Handler) forward(w http.ResponseWriter, r *http.Request, route *config.RouteConfig, body []byte, captureBody bool) (status int, respBody []byte, err error) {
	destURL, err := url.Parse(route.Destination)
	if err != nil {
		return 0, nil, err
	}

	// For prefix routes, preserve the path suffix beyond the route prefix
	// e.g., route=/hooks, request=/hooks/github -> suffix=/github
	// Special case: for root routes (/), ensure we keep the leading slash
	pathSuffix := strings.TrimPrefix(r.URL.Path, route.Path)
	if pathSuffix != "" && !strings.HasPrefix(pathSuffix, "/") {
		pathSuffix = "/" + pathSuffix
	}

	// Wrap response writer to capture status code (and optionally body)
	recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK, captureBody: captureBody}

	// Create reverse proxy
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = destURL.Scheme
			req.URL.Host = destURL.Host
			// Combine destination path with any suffix from prefix matching
			req.URL.Path = strings.TrimSuffix(destURL.Path, "/") + pathSuffix
			if req.URL.Path == "" {
				req.URL.Path = "/"
			}
			// Merge query params: preserve destination params and add request params
			switch {
			case destURL.RawQuery != "" && r.URL.RawQuery != "":
				req.URL.RawQuery = destURL.RawQuery + "&" + r.URL.RawQuery
			case r.URL.RawQuery != "":
				req.URL.RawQuery = r.URL.RawQuery
			default:
				req.URL.RawQuery = destURL.RawQuery
			}
			// Set Host header - preserve original or use destination based on config
			if route.PreserveHost {
				req.Host = r.Host
			} else {
				req.Host = destURL.Host
			}

			// Restore body
			req.Body = io.NopCloser(bytes.NewReader(body))
			req.ContentLength = int64(len(body))

			// Add X-Forwarded headers
			// Note: X-Forwarded-For is handled by httputil.ReverseProxy automatically
			// (it appends to existing chain correctly)
			req.Header.Set("X-Forwarded-Host", r.Host)

			// Detect protocol from TLS or preserve existing X-Forwarded-Proto
			proto := "http"
			if r.TLS != nil {
				proto = "https"
			} else if existing := r.Header.Get("X-Forwarded-Proto"); existing != "" {
				proto = existing
			}
			req.Header.Set("X-Forwarded-Proto", proto)
		},
	}

	proxy.ServeHTTP(recorder, r)
	return recorder.status, recorder.body, nil
}

// statusRecorder wraps http.ResponseWriter to capture the status code and optionally the body
type statusRecorder struct {
	http.ResponseWriter
	status      int
	captureBody bool
	body        []byte
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.captureBody {
		r.body = append(r.body, b...)
	}
	return r.ResponseWriter.Write(b)
}

// categorizeVerificationError returns a fixed category string for a verification error.
// This prevents unbounded cardinality in Prometheus metrics from dynamic error messages.
func categorizeVerificationError(err error) string {
	switch {
	case errors.Is(err, verifier.ErrSignatureEmpty):
		return "signature_empty"
	case errors.Is(err, verifier.ErrSignatureMismatch):
		return "signature_mismatch"
	case errors.Is(err, verifier.ErrTimestampInvalid):
		return "timestamp_invalid"
	case errors.Is(err, verifier.ErrTimestampExpired):
		return "timestamp_expired"
	case errors.Is(err, verifier.ErrTokenMismatch):
		return "token_mismatch"
	default:
		return "unknown"
	}
}

// getClientIP extracts the client IP from a request.
// If trustXForwardedFor is true, it checks X-Forwarded-For header first,
// skipping any private/internal IPs to find the real public client IP.
// If trustXForwardedFor is false, it only uses RemoteAddr.
func (h *Handler) getClientIP(r *http.Request) string {
	// Only check X-Forwarded-For if explicitly trusted
	if h.trustXForwardedFor {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// X-Forwarded-For can contain multiple IPs: "client, proxy1, proxy2"
			// Walk through from left to right, skipping private/internal IPs
			for _, part := range strings.Split(xff, ",") {
				ip := strings.TrimSpace(part)
				if ip == "" {
					continue
				}
				// Skip private/internal IPs - these are intermediate proxies
				if !isPrivateIP(ip) {
					return ip
				}
			}
			// If all IPs are private (e.g., internal network testing), return the leftmost
			if idx := strings.Index(xff, ","); idx != -1 {
				return strings.TrimSpace(xff[:idx])
			}
			return strings.TrimSpace(xff)
		}
	}

	// Use RemoteAddr (strip port if present)
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr might not have a port
		return r.RemoteAddr
	}
	return host
}

// isPrivateIP checks if an IP address is private/internal (RFC 1918, loopback, link-local, etc.)
func isPrivateIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	// Check for loopback
	if ip.IsLoopback() {
		return true
	}

	// Check for link-local
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}

	// Check for private ranges (RFC 1918 for IPv4, RFC 4193 for IPv6)
	if ip.IsPrivate() {
		return true
	}

	return false
}

// writeRelayResponse writes a relay response back to the original caller
func (h *Handler) writeRelayResponse(w http.ResponseWriter, resp *relay.Response) {
	// Decode body first so we can set Content-Length correctly
	var body []byte
	if resp.Body != "" {
		var err error
		body, err = base64.StdEncoding.DecodeString(resp.Body)
		if err != nil {
			h.logger.Error("failed to decode relay response body", "error", err)
			return
		}
	}

	// Copy headers from relay response, skipping hop-by-hop headers
	// These are connection-specific and must not be forwarded
	for k, values := range resp.Headers {
		if gkhttputil.ShouldStrip(k) {
			continue
		}
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}

	// Set correct Content-Length for the actual body we're sending
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))

	// Ensure connection is closed after response
	w.Header().Set("Connection", "close")

	// Write status code
	w.WriteHeader(resp.StatusCode)

	// Write body
	if len(body) > 0 {
		_, _ = w.Write(body)
	}
}

// logRequestPayload logs the incoming request payload for debugging
func (h *Handler) logRequestPayload(r *http.Request, ctx *requestContext) {
	h.logger.Info("debug: incoming request",
		"hostname", ctx.hostname,
		"path", r.URL.Path,
		"method", r.Method,
		"content_type", r.Header.Get("Content-Type"),
		"content_length", len(ctx.body),
		"body", truncateForLog(ctx.body),
	)
}

// logResponsePayload logs the outgoing response payload for debugging
func (h *Handler) logResponsePayload(ctx *requestContext, statusCode int, body []byte) {
	h.logger.Info("debug: outgoing response",
		"hostname", ctx.hostname,
		"path", ctx.route.Path,
		"status", statusCode,
		"content_length", len(body),
		"body", truncateForLog(body),
	)
}

// truncateForLog returns the body as a string, truncated if too long
func truncateForLog(body []byte) string {
	const maxLen = 8192 // 8KB max for logging
	if len(body) <= maxLen {
		return string(body)
	}
	return string(body[:maxLen]) + "... (truncated)"
}
