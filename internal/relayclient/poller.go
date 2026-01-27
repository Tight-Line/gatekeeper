package relayclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

var (
	// ErrMaxConsecutiveFailures is returned when the poller exceeds the max consecutive failures
	ErrMaxConsecutiveFailures = errors.New("max consecutive failures reached")
)

const (
	// TokenHeader is the header used to pass the relay token
	TokenHeader = "X-Relay-Token"
)

// Webhook represents a webhook received from the relay server
type Webhook struct {
	ID      string              `json:"id"`
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers"` // Multi-value headers (like http.Header)
	Body    string              `json:"body"`    // base64 encoded
}

// Poller handles long-polling for a single relay channel
type Poller struct {
	serverURL   string
	token       string
	channelName string
	forwarder   *Forwarder
	logger      *slog.Logger
	client      *http.Client

	// Backoff settings
	minBackoff             time.Duration
	maxBackoff             time.Duration
	maxConsecutiveFailures int

	// Worker pool settings
	workers   int
	webhookCh chan *Webhook

	// Debug settings
	debugPayloads bool
}

// NewPoller creates a new poller for a channel
func NewPoller(serverURL, token, channelName string, forwarder *Forwarder, logger *slog.Logger, maxConsecutiveFailures, workers int, debugPayloads bool) *Poller {
	if workers <= 0 {
		workers = 1
	}
	return &Poller{
		serverURL:              serverURL,
		token:                  token,
		channelName:            channelName,
		forwarder:              forwarder,
		logger:                 logger,
		maxConsecutiveFailures: maxConsecutiveFailures,
		workers:                workers,
		debugPayloads:          debugPayloads,
		client: &http.Client{
			Timeout: 60 * time.Second, // Longer than server poll timeout
		},
		minBackoff: 100 * time.Millisecond,
		maxBackoff: 30 * time.Second,
	}
}

// Run starts the polling loop, blocking until context is canceled or max consecutive failures is reached.
// Returns nil on graceful shutdown, ErrMaxConsecutiveFailures if failure limit exceeded.
func (p *Poller) Run(ctx context.Context) error {
	// Create webhook channel for worker pool
	p.webhookCh = make(chan *Webhook, p.workers)

	// Start worker pool
	var wg sync.WaitGroup
	for i := 0; i < p.workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			p.worker(ctx, workerID)
		}(i)
	}

	p.logger.Info("started worker pool",
		"channel", p.channelName,
		"workers", p.workers,
	)

	// Run the poll loop
	err := p.pollLoop(ctx)

	// Close channel and wait for workers to finish
	close(p.webhookCh)
	wg.Wait()

	p.logger.Info("poller stopped",
		"channel", p.channelName,
	)

	return err
}

// pollLoop handles the main polling loop
func (p *Poller) pollLoop(ctx context.Context) error {
	backoff := p.minBackoff
	consecutiveFailures := 0
	connected := false

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		webhook, err := p.poll(ctx)
		if err != nil {
			if done, retErr := p.handlePollError(ctx, err, &backoff, &consecutiveFailures); done {
				return retErr
			}
			continue
		}

		// Log connection status
		if !connected {
			p.logger.Info("connected to server",
				"channel", p.channelName,
			)
			connected = true
		} else if consecutiveFailures > 0 {
			p.logger.Info("connection recovered",
				"channel", p.channelName,
				"previous_failures", consecutiveFailures,
			)
		}

		// Reset backoff and failure count on successful connection
		backoff = p.minBackoff
		consecutiveFailures = 0

		if webhook != nil {
			// Dispatch to worker pool
			select {
			case p.webhookCh <- webhook:
				// Webhook dispatched to worker
			case <-ctx.Done():
				return nil
			}
		}
	}
}

// worker processes webhooks from the channel
func (p *Poller) worker(ctx context.Context, workerID int) {
	for webhook := range p.webhookCh {
		p.handleWebhook(ctx, webhook, workerID)
	}
}

func (p *Poller) handlePollError(ctx context.Context, err error, backoff *time.Duration, failures *int) (done bool, retErr error) {
	if ctx.Err() != nil {
		return true, nil
	}

	*failures++
	p.logger.Error("poll error",
		"channel", p.channelName,
		"error", err,
		"backoff", *backoff,
		"consecutive_failures", *failures,
		"max_failures", p.maxConsecutiveFailures,
	)

	if *failures >= p.maxConsecutiveFailures {
		p.logger.Error("max consecutive failures reached, exiting",
			"channel", p.channelName,
			"consecutive_failures", *failures,
		)
		return true, ErrMaxConsecutiveFailures
	}

	select {
	case <-time.After(*backoff):
	case <-ctx.Done():
		return true, nil
	}
	*backoff = min(*backoff*2, p.maxBackoff)
	return false, nil
}

func (p *Poller) handleWebhook(ctx context.Context, webhook *Webhook, workerID int) {
	resp, err := p.forwarder.Forward(ctx, webhook)
	if err != nil {
		p.logger.Error("forward error",
			"channel", p.channelName,
			"worker", workerID,
			"webhook_id", webhook.ID,
			"error", err,
		)
		p.sendErrorResponse(ctx, webhook.ID)
		return
	}

	if err := p.sendResponse(ctx, resp); err != nil {
		p.logger.Error("failed to send response",
			"channel", p.channelName,
			"worker", workerID,
			"webhook_id", webhook.ID,
			"error", err,
		)
	}
}

func (p *Poller) sendErrorResponse(ctx context.Context, webhookID string) {
	errorResp := &Response{
		RequestID:  webhookID,
		StatusCode: http.StatusBadGateway,
		Headers:    map[string][]string{"Content-Type": {"text/plain"}},
		Body:       base64.StdEncoding.EncodeToString([]byte("relay: failed to forward to destination")),
	}
	if sendErr := p.sendResponse(ctx, errorResp); sendErr != nil {
		p.logger.Error("failed to send error response",
			"channel", p.channelName,
			"webhook_id", webhookID,
			"error", sendErr,
		)
	}
}

// poll makes a single long-poll request
func (p *Poller) poll(ctx context.Context) (*Webhook, error) {
	pollURL := p.serverURL + "/relay/poll"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set(TokenHeader, p.token)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("poll request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Webhook available
		var webhook Webhook
		if err := json.NewDecoder(resp.Body).Decode(&webhook); err != nil {
			return nil, fmt.Errorf("decoding webhook: %w", err)
		}
		p.logger.Info("webhook received",
			"channel", p.channelName,
			"webhook_id", webhook.ID,
			"method", webhook.Method,
			"path", webhook.Path,
		)
		return &webhook, nil

	case http.StatusNoContent:
		// Poll timeout, reconnect
		p.logger.Debug("poll timeout", "channel", p.channelName)
		return nil, nil

	case http.StatusUnauthorized:
		return nil, fmt.Errorf("unauthorized: invalid relay token")

	case http.StatusServiceUnavailable:
		return nil, fmt.Errorf("server shutting down")

	default:
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}
}

// sendResponse sends the response back to the relay server
func (p *Poller) sendResponse(ctx context.Context, resp *Response) error {
	responseURL := p.serverURL + "/relay/response"

	// Response contains only primitives (string, int, map[string]string) - Marshal cannot fail
	body, _ := json.Marshal(resp)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, responseURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set(TokenHeader, p.token)
	req.Header.Set("Content-Type", "application/json")

	httpResp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending response: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(httpResp.Body)
		return fmt.Errorf("unexpected status %d: %s", httpResp.StatusCode, string(respBody))
	}

	p.logger.Debug("response sent to server",
		"channel", p.channelName,
		"request_id", resp.RequestID,
		"status_code", resp.StatusCode,
	)

	return nil
}
