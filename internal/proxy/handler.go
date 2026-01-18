package proxy

import (
	"bytes"
	"encoding/base64"
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
	"github.com/tight-line/gatekeeper/internal/verifier"
)

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
}

// Handler handles incoming webhook requests
type Handler struct {
	routes             []config.RouteConfig
	verifiers          map[string]verifier.Verifier
	filters            *ipfilter.FilterSet
	relay              *relay.Manager
	logger             *slog.Logger
	trustXForwardedFor bool
	maxBodySize        int64

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
		filters:            filters,
		logger:             logger,
		trustXForwardedFor: opts.TrustXForwardedFor,
		maxBodySize:        maxBodySize,
		routesByHostPath:   make(map[string]*config.RouteConfig),
	}

	// Build verifiers from config
	for name, vc := range cfg.Verifiers {
		v, err := buildVerifier(vc)
		if err != nil {
			return nil, err
		}
		h.verifiers[name] = v
	}

	// Build route lookup map
	for i := range cfg.Routes {
		route := &cfg.Routes[i]
		key := route.Hostname + ":" + route.Path
		h.routesByHostPath[key] = route
	}

	return h, nil
}

// SetRelayManager sets the relay manager for handling relay delivery
func (h *Handler) SetRelayManager(rm *relay.Manager) {
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
	case "noop":
		return verifier.NewNoopVerifier(), nil
	default:
		return nil, fmt.Errorf("unknown verifier type: %s", vc.Type)
	}
}

// ServeHTTP handles incoming requests
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Extract hostname (strip port if present)
	// Use net.SplitHostPort to correctly handle IPv6 addresses like [::1]:8080
	hostname := r.Host
	if host, _, err := net.SplitHostPort(hostname); err == nil {
		hostname = host
	}

	// Find matching route
	route := h.findRoute(hostname, r.URL.Path)
	if route == nil {
		h.logger.Warn("no matching route",
			"hostname", hostname,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
		)
		// Use static values to prevent unbounded metric cardinality from arbitrary Host headers and paths
		metrics.RecordRequest("unknown", "unknown", "404", time.Since(start).Seconds())
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	// Check IP allowlist
	// getClientIP respects X-Forwarded-For only if trustXForwardedFor is enabled
	if route.IPAllowlist != "" {
		clientIP := h.getClientIP(r)
		if !h.filters.Allow(route.IPAllowlist, clientIP) {
			h.logger.Warn("ip not allowed",
				"hostname", hostname,
				"path", r.URL.Path,
				"client_ip", clientIP,
				"remote_addr", r.RemoteAddr,
				"allowlist", route.IPAllowlist,
			)
			metrics.RecordIPDenied(hostname, route.IPAllowlist)
			metrics.RecordRequest(hostname, route.Path, "403", time.Since(start).Seconds())
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	// Read body for verification (with size limit to prevent memory exhaustion)
	// Read up to maxBodySize+1 to detect if the body exceeds the limit
	limitedReader := io.LimitReader(r.Body, h.maxBodySize+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		h.logger.Error("failed to read body",
			"hostname", hostname,
			"path", r.URL.Path,
			"error", err,
		)
		metrics.RecordRequest(hostname, route.Path, "400", time.Since(start).Seconds())
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	if int64(len(body)) > h.maxBodySize {
		h.logger.Warn("request body too large",
			"hostname", hostname,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"max_size", h.maxBodySize,
		)
		metrics.RecordRequest(hostname, route.Path, "413", time.Since(start).Seconds())
		http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	// Verify signature
	if route.Verifier != "" {
		v, ok := h.verifiers[route.Verifier]
		if !ok {
			h.logger.Error("verifier not found",
				"hostname", hostname,
				"path", r.URL.Path,
				"verifier", route.Verifier,
			)
			metrics.RecordRequest(hostname, route.Path, "500", time.Since(start).Seconds())
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		if err := v.Verify(r, body); err != nil {
			h.logger.Warn("verification failed",
				"hostname", hostname,
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
				"verifier", route.Verifier,
				"error", err.Error(),
			)
			metrics.RecordVerificationFailure(hostname, route.Verifier, categorizeVerificationError(err))
			metrics.RecordRequest(hostname, route.Path, "401", time.Since(start).Seconds())
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Deliver via relay or forward directly
	if route.RelayToken != "" {
		if h.relay == nil {
			h.logger.Error("relay manager not configured",
				"hostname", hostname,
				"path", r.URL.Path,
			)
			metrics.RecordRequest(hostname, route.Path, "500", time.Since(start).Seconds())
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		// Deliver and wait for response from relay client
		resp, err := h.relay.DeliverHTTPRequest(r.Context(), route.RelayToken, r, body, route.PreserveHost)
		if err != nil {
			if err == relay.ErrNoClient {
				h.logger.Warn("no relay client connected",
					"hostname", hostname,
					"path", r.URL.Path,
					"remote_addr", r.RemoteAddr,
				)
				metrics.RecordRequest(hostname, route.Path, "503", time.Since(start).Seconds())
				http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
				return
			}
			h.logger.Error("relay delivery failed",
				"hostname", hostname,
				"path", r.URL.Path,
				"error", err,
			)
			metrics.RecordRequest(hostname, route.Path, "502", time.Since(start).Seconds())
			http.Error(w, "Bad Gateway", http.StatusBadGateway)
			return
		}

		// Write the relayed response back to the original caller
		h.writeRelayResponse(w, resp)
		statusStr := fmt.Sprintf("%d", resp.StatusCode)
		metrics.RecordRequest(hostname, route.Path, statusStr, time.Since(start).Seconds())
		h.logger.Info("request relayed",
			"hostname", hostname,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"status", resp.StatusCode,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return
	}

	// Forward the request directly
	status, err := h.forward(w, r, route, body)
	if err != nil {
		h.logger.Error("forward failed",
			"hostname", hostname,
			"path", r.URL.Path,
			"destination", route.Destination,
			"error", err,
		)
		metrics.RecordForwardError(hostname, route.Destination)
		metrics.RecordRequest(hostname, route.Path, "502", time.Since(start).Seconds())
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}

	statusStr := fmt.Sprintf("%d", status)
	metrics.RecordRequest(hostname, route.Path, statusStr, time.Since(start).Seconds())
	h.logger.Info("request forwarded",
		"hostname", hostname,
		"path", r.URL.Path,
		"remote_addr", r.RemoteAddr,
		"destination", route.Destination,
		"status", status,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

// findRoute finds a matching route for the hostname and path
func (h *Handler) findRoute(hostname, path string) *config.RouteConfig {
	// Try exact match first
	if route, ok := h.routesByHostPath[hostname+":"+path]; ok {
		return route
	}

	// Try prefix matching (segment-aware: /hooks matches /hooks and /hooks/*, not /hookshot)
	for i := range h.routes {
		route := &h.routes[i]
		if route.Hostname == hostname && strings.HasPrefix(path, route.Path) {
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

// forward proxies the request to the destination transparently and returns the upstream status code
func (h *Handler) forward(w http.ResponseWriter, r *http.Request, route *config.RouteConfig, body []byte) (int, error) {
	destURL, err := url.Parse(route.Destination)
	if err != nil {
		return 0, err
	}

	// For prefix routes, preserve the path suffix beyond the route prefix
	// e.g., route=/hooks, request=/hooks/github -> suffix=/github
	// Special case: for root routes (/), ensure we keep the leading slash
	pathSuffix := strings.TrimPrefix(r.URL.Path, route.Path)
	if pathSuffix != "" && !strings.HasPrefix(pathSuffix, "/") {
		pathSuffix = "/" + pathSuffix
	}

	// Wrap response writer to capture status code
	recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

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
	return recorder.status, nil
}

// statusRecorder wraps http.ResponseWriter to capture the status code
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
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
// If trustXForwardedFor is true, it checks X-Forwarded-For header first
// (trusting the leftmost IP), then falls back to RemoteAddr.
// If trustXForwardedFor is false, it only uses RemoteAddr.
func (h *Handler) getClientIP(r *http.Request) string {
	// Only check X-Forwarded-For if explicitly trusted
	if h.trustXForwardedFor {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// X-Forwarded-For can contain multiple IPs: "client, proxy1, proxy2"
			// The leftmost is the original client
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
