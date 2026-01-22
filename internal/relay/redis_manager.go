package relay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tight-line/gatekeeper/internal/httputil"
)

const (
	// Redis key prefixes
	keyPrefixWebhooks = "relay:%s:webhooks" // Stream for webhook queue
	keyPrefixResponse = "relay:response:%s" // Pub/sub channel for responses
	keyPrefixTokens   = "relay:tokens"      // Set of registered tokens

	// Consumer group name for relay clients
	consumerGroupName = "relay-clients"

	// Stream max length (approximate) to prevent unbounded growth
	streamMaxLen = 10000

	// Default timeout for blocking operations
	defaultBlockTimeout = 30 * time.Second

	// Recovery settings
	recoveryInterval    = 30 * time.Second // How often to check for stuck messages
	pendingIdleTimeout  = 60 * time.Second // How long a message can be pending before reclaim
	maxDeliveryAttempts = 3                // Max retries before dead letter
)

// RedisManager is a Redis-backed implementation of Manager.
// It uses Redis streams for webhook queues and pub/sub for response routing,
// enabling multi-replica deployments and concurrent webhook processing.
type RedisManager struct {
	client     redis.UniversalClient
	consumerID string // Unique ID for this instance: {hostname}-{uuid}
	logger     *slog.Logger

	mu     sync.RWMutex
	tokens map[string]bool // Local cache of registered tokens

	// Recovery goroutine management
	recoveryCancel context.CancelFunc
	recoveryDone   chan struct{}
}

// getHostname is a variable to allow testing
var getHostname = os.Hostname

// NewRedisManager creates a new Redis-backed relay manager.
// The URI supports redis://, rediss://, valkey://, and valkeys:// schemes.
func NewRedisManager(uri string, logger *slog.Logger) (*RedisManager, error) {
	opts, err := parseRedisURI(uri)
	if err != nil {
		return nil, fmt.Errorf("parsing redis URI: %w", err)
	}

	client := redis.NewClient(opts)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connecting to redis: %w", err)
	}

	// Generate unique consumer ID
	hostname, _ := getHostname()
	if hostname == "" {
		hostname = "unknown"
	}
	consumerID := fmt.Sprintf("%s-%s", hostname, uuid.New().String()[:8])

	if logger == nil {
		logger = slog.Default()
	}

	return &RedisManager{
		client:     client,
		consumerID: consumerID,
		logger:     logger,
		tokens:     make(map[string]bool),
	}, nil
}

// parseRedisURI parses a Redis URI into client options.
// Supports redis://, rediss://, valkey://, valkeys:// schemes.
func parseRedisURI(uri string) (*redis.Options, error) {
	// Normalize valkey:// to redis://
	normalizedURI := uri
	if strings.HasPrefix(uri, "valkey://") {
		normalizedURI = "redis://" + uri[9:]
	} else if strings.HasPrefix(uri, "valkeys://") {
		normalizedURI = "rediss://" + uri[10:]
	}

	opts, err := redis.ParseURL(normalizedURI)
	if err != nil {
		return nil, err
	}

	return opts, nil
}

// streamKey returns the Redis stream key for a token's webhook queue
func streamKey(token string) string {
	return fmt.Sprintf(keyPrefixWebhooks, token)
}

// responseChannel returns the pub/sub channel for a webhook response
func responseChannel(webhookID string) string {
	return fmt.Sprintf(keyPrefixResponse, webhookID)
}

// RegisterToken registers a relay token as valid.
// Creates the consumer group for the token's stream if it doesn't exist.
func (m *RedisManager) RegisterToken(token string) {
	m.mu.Lock()
	m.tokens[token] = true
	m.mu.Unlock()

	// Add to Redis set for cross-replica visibility
	ctx := context.Background()
	m.client.SAdd(ctx, keyPrefixTokens, token)

	// Create consumer group (ignore error if already exists)
	key := streamKey(token)
	err := m.client.XGroupCreateMkStream(ctx, key, consumerGroupName, "0").Err()
	// Ignore BUSYGROUP error (group already exists) - this is expected in multi-replica setup
	_ = err
}

// IsValidToken checks if a token is registered
func (m *RedisManager) IsValidToken(token string) bool {
	// Check local cache first
	m.mu.RLock()
	if m.tokens[token] {
		m.mu.RUnlock()
		return true
	}
	m.mu.RUnlock()

	// Check Redis
	ctx := context.Background()
	exists, err := m.client.SIsMember(ctx, keyPrefixTokens, token).Result()
	if err != nil {
		return false
	}

	if exists {
		// Update local cache
		m.mu.Lock()
		m.tokens[token] = true
		m.mu.Unlock()
	}

	return exists
}

// IsConnected checks if any relay client is currently polling for the token.
// In Redis mode, this checks if there are any consumers in the consumer group.
func (m *RedisManager) IsConnected(token string) bool {
	ctx := context.Background()
	key := streamKey(token)

	info, err := m.client.XInfoGroups(ctx, key).Result()
	if err != nil {
		return false
	}

	for _, group := range info {
		if group.Name == consumerGroupName && group.Consumers > 0 {
			return true
		}
	}

	return false
}

// Deliver sends a webhook to a waiting relay client and waits for the response.
// Returns ErrInvalidToken if token is not registered.
// Blocks until the relay client sends a response or context is canceled.
// Note: In Redis mode, ErrNoClient is not returned. If no consumer is available,
// the call will block until context timeout (unlike MemoryManager which fails fast).
func (m *RedisManager) Deliver(ctx context.Context, token string, webhook *Webhook) (*Response, error) {
	if !m.IsValidToken(token) {
		return nil, ErrInvalidToken
	}

	// Generate ID if not set
	if webhook.ID == "" {
		webhook.ID = uuid.New().String()
	}

	// Set expiry time if not already set
	if webhook.ExpiresAt == 0 {
		webhook.ExpiresAt = time.Now().Add(DefaultWebhookExpiry).Unix()
	}

	// Subscribe to response channel BEFORE adding to stream
	pubsub := m.client.Subscribe(ctx, responseChannel(webhook.ID))
	defer pubsub.Close()

	// Wait for subscription to be ready
	_, err := pubsub.Receive(ctx)
	if err != nil {
		return nil, fmt.Errorf("subscribing to response channel: %w", err)
	}

	// Serialize webhook for stream
	webhookJSON, err := json.Marshal(webhook)
	if err != nil {
		return nil, fmt.Errorf("marshaling webhook: %w", err)
	}

	// Add webhook to stream
	key := streamKey(token)
	_, err = m.client.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		MaxLen: streamMaxLen,
		Approx: true,
		Values: map[string]any{
			"webhook": string(webhookJSON),
		},
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("adding webhook to stream: %w", err)
	}

	// Wait for response via pub/sub
	ch := pubsub.Channel()
	select {
	case msg := <-ch:
		var resp Response
		if err := json.Unmarshal([]byte(msg.Payload), &resp); err != nil {
			return nil, fmt.Errorf("unmarshaling response: %w", err)
		}
		return &resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// DeliverHTTPRequest creates a Webhook from an HTTP request and delivers it.
func (m *RedisManager) DeliverHTTPRequest(ctx context.Context, token string, r *http.Request, body []byte, preserveHost bool) (*Response, error) {
	headers := httputil.StripHopByHopHeaders(r.Header)

	if preserveHost {
		headers["X-Gatekeeperd-Preserve-Host"] = []string{"true"}
		headers["X-Gatekeeperd-Original-Host"] = []string{r.Host}
	}

	webhook := &Webhook{
		ID:      uuid.New().String(),
		Method:  r.Method,
		Path:    r.URL.RequestURI(),
		Headers: headers,
		Body:    base64.StdEncoding.EncodeToString(body),
	}

	return m.Deliver(ctx, token, webhook)
}

// Poll waits for a webhook to be available for the given token.
// Uses Redis consumer groups to ensure each webhook is processed by only one consumer.
// Expired webhooks are automatically acknowledged and skipped.
func (m *RedisManager) Poll(ctx context.Context, token string) (*Webhook, error) {
	if !m.IsValidToken(token) {
		return nil, ErrInvalidToken
	}

	key := streamKey(token)

	for {
		// Check context before each iteration
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// First, check for any pending messages that were claimed but not ACKed
		// (e.g., from a previous crash)
		pending, err := m.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    consumerGroupName,
			Consumer: m.consumerID,
			Streams:  []string{key, "0"}, // "0" means read pending messages
			Count:    1,
		}).Result()

		if err != nil && err != redis.Nil {
			return nil, fmt.Errorf("reading pending messages: %w", err)
		}

		// If we have a pending message, process it
		if len(pending) > 0 && len(pending[0].Messages) > 0 {
			msg := pending[0].Messages[0]
			webhook, err := m.parseStreamMessage(msg)
			if err != nil {
				// ACK invalid message to prevent infinite loop
				_ = m.client.XAck(ctx, key, consumerGroupName, msg.ID).Err()
				continue
			}

			// Check if webhook is expired
			if webhook.IsExpired() {
				// ACK expired webhook and continue to next message
				_ = m.client.XAck(ctx, key, consumerGroupName, msg.ID).Err()
				continue
			}

			return webhook, nil
		}

		// Calculate block timeout from context deadline
		blockTimeout := defaultBlockTimeout
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining < blockTimeout {
				blockTimeout = remaining
			}
			if blockTimeout <= 0 {
				return nil, ctx.Err()
			}
		}

		// No pending messages, wait for new ones
		streams, err := m.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    consumerGroupName,
			Consumer: m.consumerID,
			Streams:  []string{key, ">"}, // ">" means read new messages only
			Count:    1,
			Block:    blockTimeout,
		}).Result()

		if err == redis.Nil {
			// Timeout - no messages available
			return nil, ctx.Err()
		}
		if err != nil {
			// Check if context was canceled
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("reading from stream: %w", err)
		}

		if len(streams) == 0 || len(streams[0].Messages) == 0 {
			return nil, ctx.Err()
		}

		msg := streams[0].Messages[0]
		webhook, err := m.parseStreamMessage(msg)
		if err != nil {
			// ACK invalid message to prevent infinite loop
			_ = m.client.XAck(ctx, key, consumerGroupName, msg.ID).Err()
			continue
		}

		// Check if webhook is expired
		if webhook.IsExpired() {
			// ACK expired webhook and continue to next message
			_ = m.client.XAck(ctx, key, consumerGroupName, msg.ID).Err()
			continue
		}

		return webhook, nil
	}
}

// parseStreamMessage extracts a Webhook from a Redis stream message
func (m *RedisManager) parseStreamMessage(msg redis.XMessage) (*Webhook, error) {
	webhookJSON, ok := msg.Values["webhook"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid webhook data in stream message")
	}

	var webhook Webhook
	if err := json.Unmarshal([]byte(webhookJSON), &webhook); err != nil {
		return nil, fmt.Errorf("unmarshaling webhook: %w", err)
	}

	// Store the stream message ID in the webhook for ACK later
	// We'll use a special header for this
	if webhook.Headers == nil {
		webhook.Headers = make(map[string][]string)
	}
	webhook.Headers["X-Relay-Stream-ID"] = []string{msg.ID}

	return &webhook, nil
}

// SendResponse delivers a response from the relay client back to the waiting caller.
// Also ACKs the original message in the stream.
func (m *RedisManager) SendResponse(resp *Response) error {
	ctx := context.Background()

	// Publish response to the waiting Deliver call
	respJSON, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshaling response: %w", err)
	}

	channel := responseChannel(resp.RequestID)
	if err := m.client.Publish(ctx, channel, string(respJSON)).Err(); err != nil {
		return fmt.Errorf("publishing response: %w", err)
	}

	return nil
}

// AckWebhook acknowledges a webhook has been processed.
// This should be called after SendResponse to remove the message from pending.
func (m *RedisManager) AckWebhook(token, streamID string) error {
	ctx := context.Background()
	key := streamKey(token)
	return m.client.XAck(ctx, key, consumerGroupName, streamID).Err()
}

// Shutdown cleans up resources
func (m *RedisManager) Shutdown() {
	// Stop recovery goroutine if running
	if m.recoveryCancel != nil {
		m.recoveryCancel()
		<-m.recoveryDone
	}

	if m.client != nil {
		m.client.Close()
	}
}

// StartRecovery starts a background goroutine that periodically scans for
// stuck messages (messages claimed but not ACKed) and reclaims them for
// reprocessing. Messages that exceed maxDeliveryAttempts are dead-lettered.
func (m *RedisManager) StartRecovery(ctx context.Context) {
	recoveryCtx, cancel := context.WithCancel(ctx)
	m.recoveryCancel = cancel
	m.recoveryDone = make(chan struct{})

	go func() {
		defer close(m.recoveryDone)

		ticker := time.NewTicker(recoveryInterval)
		defer ticker.Stop()

		for {
			select {
			case <-recoveryCtx.Done():
				return
			case <-ticker.C:
				m.recoverPendingMessages(recoveryCtx)
			}
		}
	}()
}

// recoverPendingMessages scans all token streams for stuck messages and reclaims them.
func (m *RedisManager) recoverPendingMessages(ctx context.Context) {
	// Get all registered tokens
	tokens, err := m.client.SMembers(ctx, keyPrefixTokens).Result()
	if err != nil {
		m.logger.Error("failed to get tokens for recovery", "error", err)
		return
	}

	for _, token := range tokens {
		if ctx.Err() != nil {
			return
		}
		m.recoverTokenPending(ctx, token)
	}
}

// recoverTokenPending recovers stuck messages for a specific token's stream.
func (m *RedisManager) recoverTokenPending(ctx context.Context, token string) {
	key := streamKey(token)

	// Get pending messages across all consumers
	pending, err := m.client.XPending(ctx, key, consumerGroupName).Result()
	if err != nil {
		// Stream may not exist or have no consumer group - this is normal
		return
	}

	if pending.Count == 0 {
		return
	}

	// Get detailed pending info to check idle time and delivery count
	pendingExt, err := m.client.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: key,
		Group:  consumerGroupName,
		Start:  "-",
		End:    "+",
		Count:  100, // Process up to 100 stuck messages per cycle
	}).Result()
	if err != nil {
		m.logger.Error("failed to get pending details", "token", token, "error", err)
		return
	}

	for _, p := range pendingExt {
		if ctx.Err() != nil {
			return
		}

		// Skip messages that haven't been idle long enough
		if p.Idle < pendingIdleTimeout {
			continue
		}

		// Check if message has exceeded max delivery attempts
		if p.RetryCount >= int64(maxDeliveryAttempts) {
			// Dead letter: log and ACK to remove from stream
			m.logger.Warn("dead-lettering webhook after max retries",
				"token", token,
				"message_id", p.ID,
				"consumer", p.Consumer,
				"retry_count", p.RetryCount,
				"idle_time", p.Idle,
			)
			if err := m.client.XAck(ctx, key, consumerGroupName, p.ID).Err(); err != nil {
				m.logger.Error("failed to ACK dead-lettered message",
					"token", token,
					"message_id", p.ID,
					"error", err,
				)
			}
			continue
		}

		// Reclaim the message for this consumer
		claimed, err := m.client.XClaim(ctx, &redis.XClaimArgs{
			Stream:   key,
			Group:    consumerGroupName,
			Consumer: m.consumerID,
			MinIdle:  pendingIdleTimeout,
			Messages: []string{p.ID},
		}).Result()
		if err != nil {
			m.logger.Error("failed to claim stuck message",
				"token", token,
				"message_id", p.ID,
				"error", err,
			)
			continue
		}

		if len(claimed) > 0 {
			m.logger.Info("reclaimed stuck webhook",
				"token", token,
				"message_id", p.ID,
				"previous_consumer", p.Consumer,
				"retry_count", p.RetryCount+1,
			)
		}
	}
}

// TokenCount returns the number of registered tokens
func (m *RedisManager) TokenCount() int {
	ctx := context.Background()
	count, err := m.client.SCard(ctx, keyPrefixTokens).Result()
	if err != nil {
		// Fall back to local cache
		m.mu.RLock()
		defer m.mu.RUnlock()
		return len(m.tokens)
	}
	return int(count)
}

// ConnectedCount returns the number of tokens with connected clients.
// This is expensive in Redis mode as it requires checking each token's consumer group.
func (m *RedisManager) ConnectedCount() int {
	ctx := context.Background()
	tokens, err := m.client.SMembers(ctx, keyPrefixTokens).Result()
	if err != nil {
		return 0
	}

	count := 0
	for _, token := range tokens {
		if m.IsConnected(token) {
			count++
		}
	}
	return count
}

// Ensure RedisManager implements Manager interface
var _ Manager = (*RedisManager)(nil)
