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
	body, err := base64.StdEncoding.DecodeString(webhook.Body)
	if err != nil {
		return nil, fmt.Errorf("decoding body: %w", err)
	}

	req, err := f.buildRequest(ctx, webhook, body)
	if err != nil {
		return nil, err
	}

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

	return f.buildResponse(webhook, resp, duration)
}

func (f *Forwarder) buildRequest(ctx context.Context, webhook *Webhook, body []byte) (*http.Request, error) {
	destURL, err := f.buildDestinationURL(webhook.Path)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, webhook.Method, destURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	f.copyHeaders(req, webhook)
	return req, nil
}

func (f *Forwarder) buildDestinationURL(webhookPath string) (string, error) {
	destURL, err := url.Parse(f.destination)
	if err != nil {
		return "", fmt.Errorf("parsing destination: %w", err)
	}

	webhookURL, err := url.Parse(webhookPath)
	if err != nil {
		return "", fmt.Errorf("parsing webhook path: %w", err)
	}

	basePath := strings.TrimSuffix(destURL.Path, "/")
	destURL.Path = basePath + webhookURL.Path

	if destURL.RawQuery != "" && webhookURL.RawQuery != "" {
		destURL.RawQuery = destURL.RawQuery + "&" + webhookURL.RawQuery
	} else if webhookURL.RawQuery != "" {
		destURL.RawQuery = webhookURL.RawQuery
	}

	return destURL.String(), nil
}

func (f *Forwarder) copyHeaders(req *http.Request, webhook *Webhook) {
	for k, values := range webhook.Headers {
		if httputil.ShouldStrip(k) {
			continue
		}
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}

	if req.Header.Get("X-Gatekeeperd-Preserve-Host") == "true" {
		if originalHost := req.Header.Get("X-Gatekeeperd-Original-Host"); originalHost != "" {
			req.Host = originalHost
		}
	}
	req.Header.Del("X-Gatekeeperd-Preserve-Host")
	req.Header.Del("X-Gatekeeperd-Original-Host")

	req.Header.Set("X-Relay-Webhook-ID", webhook.ID)
	req.Header.Set("X-Relay-Original-Path", webhook.Path)
	req.Header.Set("Connection", "close")
}

func (f *Forwarder) buildResponse(webhook *Webhook, resp *http.Response, duration time.Duration) (*Response, error) {
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		f.logger.Error("failed to read response body",
			"channel", f.channelName,
			"webhook_id", webhook.ID,
			"error", err,
		)
		return nil, fmt.Errorf("reading response: %w", err)
	}

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
