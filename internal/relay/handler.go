package relay

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	// DefaultPollTimeout is the default long-poll timeout
	DefaultPollTimeout = 30 * time.Second
	// TokenHeader is the header used to pass the relay token
	TokenHeader = "X-Relay-Token"
)

// Handler handles HTTP requests for the relay protocol
type Handler struct {
	manager     Manager
	logger      *slog.Logger
	pollTimeout time.Duration
}

// NewHandler creates a new relay HTTP handler
func NewHandler(manager Manager, logger *slog.Logger) *Handler {
	return &Handler{
		manager:     manager,
		logger:      logger,
		pollTimeout: DefaultPollTimeout,
	}
}

// SetPollTimeout sets the long-poll timeout duration
func (h *Handler) SetPollTimeout(d time.Duration) {
	h.pollTimeout = d
}

// ServeHTTP handles relay requests
// GET /relay/poll - long-poll for webhooks
// POST /relay/response - send response back
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/relay")

	switch {
	case path == "/poll" && r.Method == http.MethodGet:
		h.handlePoll(w, r)
	case path == "/response" && r.Method == http.MethodPost:
		h.handleResponse(w, r)
	default:
		http.Error(w, "Not Found", http.StatusNotFound)
	}
}

// handlePoll handles GET /relay/poll requests
func (h *Handler) handlePoll(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get(TokenHeader)
	if token == "" {
		http.Error(w, "Missing "+TokenHeader+" header", http.StatusBadRequest)
		return
	}

	if !h.manager.IsValidToken(token) {
		h.logger.Warn("relay poll with invalid token",
			"remote_addr", r.RemoteAddr,
		)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	h.logger.Debug("relay client connected",
		"remote_addr", r.RemoteAddr,
	)

	// Create a context with timeout for long-polling
	ctx, cancel := context.WithTimeout(r.Context(), h.pollTimeout)
	defer cancel()

	webhook, err := h.manager.Poll(ctx, token)
	if err != nil {
		// Normal timeout or canceled by new connection - client should reconnect
		// Note: Poll only returns context errors since we already validated the token
		w.WriteHeader(http.StatusNoContent)
		return
	}

	h.logger.Info("webhook delivered via relay",
		"webhook_id", webhook.ID,
		"method", webhook.Method,
		"path", webhook.Path,
		"remote_addr", r.RemoteAddr,
	)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(webhook); err != nil {
		h.logger.Error("failed to encode webhook response", "error", err)
		return
	}

	// ACK the message after successful delivery to relay client
	// The stream ID is stored in X-Relay-Stream-ID header by Redis manager
	if streamIDs, ok := webhook.Headers["X-Relay-Stream-ID"]; ok && len(streamIDs) > 0 {
		if err := h.manager.AckWebhook(token, streamIDs[0]); err != nil {
			h.logger.Error("failed to ACK webhook",
				"webhook_id", webhook.ID,
				"stream_id", streamIDs[0],
				"error", err,
			)
		}
	}
}

// handleResponse handles POST /relay/response requests
func (h *Handler) handleResponse(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get(TokenHeader)
	if token == "" {
		http.Error(w, "Missing "+TokenHeader+" header", http.StatusBadRequest)
		return
	}

	if !h.manager.IsValidToken(token) {
		h.logger.Warn("relay response with invalid token",
			"remote_addr", r.RemoteAddr,
		)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var resp Response
	if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
		h.logger.Error("failed to decode response",
			"error", err,
			"remote_addr", r.RemoteAddr,
		)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if h.manager.SendResponse(&resp) != nil {
		// SendResponse only returns ErrRequestNotFound
		h.logger.Warn("response for unknown request",
			"request_id", resp.RequestID,
			"remote_addr", r.RemoteAddr,
		)
		http.Error(w, "Request Not Found", http.StatusNotFound)
		return
	}

	h.logger.Info("response received from relay",
		"request_id", resp.RequestID,
		"status_code", resp.StatusCode,
		"remote_addr", r.RemoteAddr,
	)

	w.WriteHeader(http.StatusOK)
}
