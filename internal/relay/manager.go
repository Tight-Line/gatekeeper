package relay

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"sync"

	"github.com/google/uuid"

	"github.com/tight-line/gatekeeper/internal/httputil"
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

// pendingRequest tracks a request waiting for a response
type pendingRequest struct {
	responseCh chan *Response
}

// waiter represents an active poll request
type waiter struct {
	id     string
	cancel context.CancelFunc
}

// Manager handles relay token registration and webhook delivery
type Manager struct {
	mu       sync.RWMutex
	tokens   map[string]bool            // valid tokens
	channels map[string]chan *Webhook   // token -> webhook channel
	waiters  map[string]*waiter         // token -> current waiter
	pending  map[string]*pendingRequest // request ID -> pending request
}

// NewManager creates a new relay manager
func NewManager() *Manager {
	return &Manager{
		tokens:   make(map[string]bool),
		channels: make(map[string]chan *Webhook),
		waiters:  make(map[string]*waiter),
		pending:  make(map[string]*pendingRequest),
	}
}

// RegisterToken registers a relay token as valid
func (m *Manager) RegisterToken(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[token] = true
	// Create a buffered channel of size 1 - we only hold one webhook at a time
	m.channels[token] = make(chan *Webhook, 1)
}

// IsValidToken checks if a token is registered
func (m *Manager) IsValidToken(token string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tokens[token]
}

// IsConnected checks if a relay client is currently polling for the token
func (m *Manager) IsConnected(token string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, connected := m.waiters[token]
	return connected
}

// Shutdown cancels all waiting pollers
func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, w := range m.waiters {
		w.cancel()
	}
}

// Deliver sends a webhook to a waiting relay client and waits for the response.
// Returns ErrInvalidToken if token is not registered.
// Returns ErrNoClient if no relay client is connected.
// Blocks until the relay client sends a response or context is canceled.
func (m *Manager) Deliver(ctx context.Context, token string, webhook *Webhook) (*Response, error) {
	m.mu.RLock()
	if !m.tokens[token] {
		m.mu.RUnlock()
		return nil, ErrInvalidToken
	}
	ch := m.channels[token]
	_, connected := m.waiters[token]
	m.mu.RUnlock()

	if !connected {
		return nil, ErrNoClient
	}

	// Generate ID if not set
	if webhook.ID == "" {
		webhook.ID = uuid.New().String()
	}

	// Create pending request to receive response
	responseCh := make(chan *Response, 1)
	m.mu.Lock()
	m.pending[webhook.ID] = &pendingRequest{responseCh: responseCh}
	m.mu.Unlock()

	// Clean up pending request when done
	defer func() {
		m.mu.Lock()
		delete(m.pending, webhook.ID)
		m.mu.Unlock()
	}()

	// Send the webhook with context cancellation support
	// Use select to avoid blocking indefinitely if waiter disconnects
	select {
	case ch <- webhook:
		// Webhook sent successfully
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Wait for response from relay client
	select {
	case resp := <-responseCh:
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// DeliverHTTPRequest creates a Webhook from an HTTP request and delivers it.
// If preserveHost is true, special headers are added to instruct the relay client
// to use the original Host header when forwarding to its destination.
func (m *Manager) DeliverHTTPRequest(ctx context.Context, token string, r *http.Request, body []byte, preserveHost bool) (*Response, error) {
	// Strip hop-by-hop headers and Content-Length (will be recalculated at each hop)
	// These headers are connection-specific and must not be forwarded
	headers := httputil.StripHopByHopHeaders(r.Header)

	// Add special headers for relay client to handle Host preservation
	if preserveHost {
		headers["X-Gatekeeperd-Preserve-Host"] = []string{"true"}
		headers["X-Gatekeeperd-Original-Host"] = []string{r.Host}
	}

	webhook := &Webhook{
		ID:      uuid.New().String(),
		Method:  r.Method,
		Path:    r.URL.RequestURI(), // Includes path and query string
		Headers: headers,
		Body:    base64.StdEncoding.EncodeToString(body),
	}

	return m.Deliver(ctx, token, webhook)
}

// SendResponse delivers a response from the relay client back to the waiting caller
func (m *Manager) SendResponse(resp *Response) error {
	m.mu.RLock()
	pending, ok := m.pending[resp.RequestID]
	m.mu.RUnlock()

	if !ok {
		return ErrRequestNotFound
	}

	// Non-blocking send to handle duplicate/late responses gracefully
	// Channel has buffer of 1; if already full (duplicate), we drop silently
	select {
	case pending.responseCh <- resp:
		return nil
	default:
		// Channel full - duplicate response, ignore it
		return nil
	}
}

// Poll waits for a webhook to be available for the given token
// Returns the webhook when available, or nil if context is canceled
// If another poller connects for the same token, the previous one is canceled
func (m *Manager) Poll(ctx context.Context, token string) (*Webhook, error) {
	m.mu.Lock()
	if !m.tokens[token] {
		m.mu.Unlock()
		return nil, ErrInvalidToken
	}

	// Cancel any existing waiter for this token
	if existing := m.waiters[token]; existing != nil {
		existing.cancel()
	}

	// Create a new cancellable context for this waiter
	pollCtx, cancel := context.WithCancel(ctx)
	waiterID := uuid.New().String()
	m.waiters[token] = &waiter{id: waiterID, cancel: cancel}
	ch := m.channels[token]
	m.mu.Unlock()

	// Clean up when done
	defer func() {
		m.mu.Lock()
		// Only remove if we're still the current waiter
		if w := m.waiters[token]; w != nil && w.id == waiterID {
			delete(m.waiters, token)
		}
		m.mu.Unlock()
	}()

	select {
	case webhook := <-ch:
		return webhook, nil
	case <-pollCtx.Done():
		return nil, pollCtx.Err()
	}
}

// TokenCount returns the number of registered tokens
func (m *Manager) TokenCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.tokens)
}

// ConnectedCount returns the number of tokens with connected clients
func (m *Manager) ConnectedCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.waiters)
}
