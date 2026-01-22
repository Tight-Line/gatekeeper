package relay

import (
	"context"
	"errors"
	"net/http"
)

var (
	// ErrInvalidToken is returned when a relay token is not registered
	ErrInvalidToken = errors.New("invalid relay token")
	// ErrNoClient is returned when no relay client is connected for the token
	ErrNoClient = errors.New("no relay client connected")
	// ErrRequestNotFound is returned when a response is received for an unknown request
	ErrRequestNotFound = errors.New("request not found")
)

// Webhook represents a webhook request to be delivered via relay
type Webhook struct {
	ID      string              `json:"id"`
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers"` // Multi-value headers (like http.Header)
	Body    string              `json:"body"`    // base64 encoded
}

// Response represents a response from the relay client's destination
type Response struct {
	RequestID  string              `json:"request_id"`
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers"` // Multi-value headers (supports Set-Cookie, etc.)
	Body       string              `json:"body"`    // base64 encoded
}

// Manager defines the interface for relay webhook delivery.
// Implementations handle queueing webhooks, routing responses back to callers,
// and managing relay client connections.
type Manager interface {
	// RegisterToken registers a relay token as valid
	RegisterToken(token string)

	// IsValidToken checks if a token is registered
	IsValidToken(token string) bool

	// IsConnected checks if a relay client is currently polling for the token
	IsConnected(token string) bool

	// Deliver sends a webhook to a waiting relay client and waits for the response.
	// Returns ErrInvalidToken if token is not registered.
	// Returns ErrNoClient if no relay client is connected.
	// Blocks until the relay client sends a response or context is canceled.
	Deliver(ctx context.Context, token string, webhook *Webhook) (*Response, error)

	// DeliverHTTPRequest creates a Webhook from an HTTP request and delivers it.
	// If preserveHost is true, special headers are added to instruct the relay client
	// to use the original Host header when forwarding to its destination.
	DeliverHTTPRequest(ctx context.Context, token string, r *http.Request, body []byte, preserveHost bool) (*Response, error)

	// Poll waits for a webhook to be available for the given token.
	// Returns the webhook when available, or nil if context is canceled.
	// If another poller connects for the same token, the previous one is canceled.
	Poll(ctx context.Context, token string) (*Webhook, error)

	// SendResponse delivers a response from the relay client back to the waiting caller.
	// Returns ErrRequestNotFound if the request ID is not found.
	SendResponse(resp *Response) error

	// Shutdown cancels all waiting pollers and cleans up resources
	Shutdown()

	// TokenCount returns the number of registered tokens
	TokenCount() int

	// ConnectedCount returns the number of tokens with connected clients
	ConnectedCount() int
}
