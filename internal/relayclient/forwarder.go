package relayclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tight-line/gatekeeper/internal/httputil"
)

// Response represents a response to send back to the relay server
type Response struct {
	RequestID  string              `json:"request_id"`
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers"` // Multi-value headers (supports Set-Cookie, etc.)
	Body       string              `json:"body"`    // base64 encoded
}

// Forwarder delivers webhooks to a local destination and returns responses
type Forwarder struct {
	destination string
	channelName string
	logger      *slog.Logger
	client      *http.Client
}

// NewForwarder creates a new forwarder for a channel
func NewForwarder(destination, channelName string, logger *slog.Logger) *Forwarder {
	return &Forwarder{
		destination: destination,
		channelName: channelName,
		logger:      logger,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Forward delivers a webhook to the local destination and returns the response
func (f *Forwarder) Forward(ctx context.Context, webhook *Webhook) (*Response, error) {
	// Decode body from base64
	body, err := base64.StdEncoding.DecodeString(webhook.Body)
	if err != nil {
		return nil, fmt.Errorf("decoding body: %w", err)
	}

	// Build the full destination URL
	// The destination is a base URL (e.g., http://localhost:8080 or http://localhost:8080/api)
	// The webhook.Path contains the original request URI including query string
	destURL, err := url.Parse(f.destination)
	if err != nil {
		return nil, fmt.Errorf("parsing destination: %w", err)
	}

	// Parse webhook path to extract path and query separately
	webhookURL, err := url.Parse(webhook.Path)
	if err != nil {
		return nil, fmt.Errorf("parsing webhook path: %w", err)
	}

	// Combine: destination base path + webhook path
	// e.g., destination=/api?token=x, webhook=/hooks/github?y=1 -> /api/hooks/github?token=x&y=1
	basePath := strings.TrimSuffix(destURL.Path, "/")
	destURL.Path = basePath + webhookURL.Path

	// Merge query params: preserve destination params and add webhook params
	if destURL.RawQuery != "" && webhookURL.RawQuery != "" {
		destURL.RawQuery = destURL.RawQuery + "&" + webhookURL.RawQuery
	} else if webhookURL.RawQuery != "" {
		destURL.RawQuery = webhookURL.RawQuery
	}
	// If only destination has query params, they're already in destURL.RawQuery

	// Create request to the combined URL
	req, err := http.NewRequestWithContext(ctx, webhook.Method, destURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Copy original headers, skipping hop-by-hop headers that are connection-specific
	// Note: Content-Length is also skipped - it will be set correctly by the HTTP client
	// based on the actual body length
	for k, values := range webhook.Headers {
		if httputil.ShouldStrip(k) {
			continue
		}
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}

	// Handle Host header preservation from gatekeeperd
	// If X-Gatekeeperd-Preserve-Host is "true", use the original host from X-Gatekeeperd-Original-Host
	if req.Header.Get("X-Gatekeeperd-Preserve-Host") == "true" {
		if originalHost := req.Header.Get("X-Gatekeeperd-Original-Host"); originalHost != "" {
			req.Host = originalHost
		}
	}
	// Remove internal gatekeeperd headers - they shouldn't be forwarded to destination
	req.Header.Del("X-Gatekeeperd-Preserve-Host")
	req.Header.Del("X-Gatekeeperd-Original-Host")

	// Add relay metadata headers
	req.Header.Set("X-Relay-Webhook-ID", webhook.ID)
	req.Header.Set("X-Relay-Original-Path", webhook.Path)

	// Ensure connection is not kept alive - each request is independent
	req.Header.Set("Connection", "close")

	start := time.Now()
	resp, err := f.client.Do(req)
	duration := time.Since(start)

	if err != nil {
		f.logger.Error("forward failed",
			"channel", f.channelName,
			"webhook_id", webhook.ID,
			"destination", f.destination,
			"error", err,
			"duration_ms", duration.Milliseconds(),
		)
		return nil, fmt.Errorf("forward request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		f.logger.Error("failed to read response body",
			"channel", f.channelName,
			"webhook_id", webhook.ID,
			"error", err,
		)
		return nil, fmt.Errorf("reading response: %w", err)
	}

	// Capture response headers, stripping hop-by-hop headers
	// These are connection-specific and must not be forwarded back through the relay
	respHeaders := httputil.StripHopByHopHeaders(resp.Header)

	f.logger.Info("webhook forwarded",
		"channel", f.channelName,
		"webhook_id", webhook.ID,
		"destination", f.destination,
		"status", resp.StatusCode,
		"duration_ms", duration.Milliseconds(),
	)

	return &Response{
		RequestID:  webhook.ID,
		StatusCode: resp.StatusCode,
		Headers:    respHeaders,
		Body:       base64.StdEncoding.EncodeToString(respBody),
	}, nil
}
