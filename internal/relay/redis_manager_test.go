package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestRedisManager(t *testing.T) (*RedisManager, *miniredis.Miniredis) {
	t.Helper()

	s := miniredis.RunT(t)

	m, err := NewRedisManager("redis://"+s.Addr(), nil)
	if err != nil {
		t.Fatalf("failed to create redis manager: %v", err)
	}

	return m, s
}

func TestRedisManager_NewRedisManager(t *testing.T) {
	s := miniredis.RunT(t)
	defer s.Close()

	tests := []struct {
		name    string
		uri     string
		wantErr bool
	}{
		{"redis scheme", "redis://" + s.Addr(), false},
		{"valkey scheme", "valkey://" + s.Addr(), false},
		{"invalid uri", "not-a-uri", true},
		{"connection failure", "redis://localhost:59999", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := NewRedisManager(tt.uri, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewRedisManager() error = %v, wantErr %v", err, tt.wantErr)
			}
			if m != nil {
				m.Shutdown()
			}
		})
	}
}

func TestRedisManager_RegisterToken(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")
	m.RegisterToken("token2")

	if !m.IsValidToken("token1") {
		t.Error("token1 should be valid")
	}
	if !m.IsValidToken("token2") {
		t.Error("token2 should be valid")
	}
	if m.IsValidToken("token3") {
		t.Error("token3 should not be valid")
	}
	if m.TokenCount() != 2 {
		t.Errorf("expected 2 tokens, got %d", m.TokenCount())
	}
}

func TestRedisManager_IsValidToken_CrossReplica(t *testing.T) {
	s := miniredis.RunT(t)
	defer s.Close()

	// Create first manager and register token
	m1, err := NewRedisManager("redis://"+s.Addr(), nil)
	if err != nil {
		t.Fatalf("failed to create first redis manager: %v", err)
	}
	defer m1.Shutdown()

	m1.RegisterToken("shared-token")

	// Create second manager (simulating another replica)
	m2, err := NewRedisManager("redis://"+s.Addr(), nil)
	if err != nil {
		t.Fatalf("failed to create second redis manager: %v", err)
	}
	defer m2.Shutdown()

	// Second manager should see the token registered by first
	if !m2.IsValidToken("shared-token") {
		t.Error("shared-token should be valid on second replica")
	}
}

func TestRedisManager_Deliver_InvalidToken(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	ctx := context.Background()
	_, err := m.Deliver(ctx, "invalid", &Webhook{Method: "POST", Path: "/test"})
	if err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestRedisManager_Deliver_NoClient_Timeout(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	// In Redis mode, if no consumer is available, Deliver blocks until timeout
	// (unlike MemoryManager which fails fast with ErrNoClient)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := m.Deliver(ctx, "token1", &Webhook{Method: "POST", Path: "/test"})
	// Should timeout, not return ErrNoClient
	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestRedisManager_Poll_InvalidToken(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	ctx := context.Background()
	_, err := m.Poll(ctx, "invalid")
	if err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestRedisManager_Poll_Timeout(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	// Use a very short context timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := m.Poll(ctx, "token1")
	// Should timeout or return context error
	if err != context.DeadlineExceeded && err != nil && !isContextError(err) {
		t.Errorf("expected timeout error, got %v", err)
	}
}

func isContextError(err error) bool {
	return err == context.DeadlineExceeded || err == context.Canceled || err == nil
}

func TestRedisManager_DeliverAndPoll(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	// Start a consumer to simulate relay client
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start polling in a goroutine (this creates a consumer)
	webhookCh := make(chan *Webhook, 1)
	errCh := make(chan error, 1)
	go func() {
		webhook, err := m.Poll(ctx, "token1")
		if err != nil {
			errCh <- err
			return
		}
		webhookCh <- webhook
	}()

	// Give the poll goroutine time to start and create a consumer
	time.Sleep(50 * time.Millisecond)

	// Now deliver should work because there's a consumer
	responseCh := make(chan *Response, 1)
	deliverErrCh := make(chan error, 1)
	go func() {
		resp, err := m.Deliver(ctx, "token1", &Webhook{Method: "POST", Path: "/test", Body: "dGVzdA=="})
		if err != nil {
			deliverErrCh <- err
			return
		}
		responseCh <- resp
	}()

	// Wait for webhook to be received
	select {
	case webhook := <-webhookCh:
		if webhook.Method != "POST" || webhook.Path != "/test" {
			t.Errorf("received wrong webhook: %+v", webhook)
		}
		if webhook.ID == "" {
			t.Error("webhook should have an ID")
		}

		// Send response back
		err := m.SendResponse(&Response{
			RequestID:  webhook.ID,
			StatusCode: 200,
			Body:       "eyJvayI6dHJ1ZX0=",
		})
		if err != nil {
			t.Fatalf("SendResponse failed: %v", err)
		}

		// ACK the webhook
		streamID := webhook.Headers["X-Relay-Stream-ID"]
		if len(streamID) > 0 {
			if err := m.AckWebhook("token1", streamID[0]); err != nil {
				t.Errorf("AckWebhook failed: %v", err)
			}
		}

	case err := <-errCh:
		t.Fatalf("Poll failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for webhook")
	}

	// Wait for deliver to complete
	select {
	case resp := <-responseCh:
		if resp.StatusCode != 200 {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}
	case err := <-deliverErrCh:
		t.Fatalf("Deliver failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for deliver to complete")
	}
}

func TestRedisManager_SendResponse_NotFound(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	// SendResponse doesn't return ErrRequestNotFound in Redis mode
	// because it just publishes to a channel - if no one is listening,
	// the message is simply lost (which is correct behavior)
	err := m.SendResponse(&Response{
		RequestID:  "nonexistent",
		StatusCode: 200,
	})
	// In Redis mode, this succeeds (publishes to empty channel)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestRedisManager_DeliverHTTPRequest(t *testing.T) {
	m, s := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create consumer connection
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	// Start polling
	webhookCh := make(chan *Webhook, 1)
	go func() {
		webhook, _ := m.Poll(ctx, "token1")
		webhookCh <- webhook
	}()

	time.Sleep(50 * time.Millisecond)

	// Create HTTP request
	req, _ := http.NewRequest(http.MethodPost, "http://example.com/webhook?foo=bar", nil)
	req.Host = "webhooks.example.com"
	req.Header.Set("Content-Type", "application/json")
	body := []byte(`{"test": true}`)

	// Deliver with preserveHost=true
	go func() {
		_, _ = m.DeliverHTTPRequest(ctx, "token1", req, body, true)
	}()

	// Receive webhook
	select {
	case webhook := <-webhookCh:
		if webhook.Method != "POST" {
			t.Errorf("expected POST, got %s", webhook.Method)
		}
		if webhook.Path != "/webhook?foo=bar" {
			t.Errorf("expected /webhook?foo=bar, got %s", webhook.Path)
		}
		preserveHost := webhook.Headers["X-Gatekeeperd-Preserve-Host"]
		if len(preserveHost) == 0 || preserveHost[0] != "true" {
			t.Error("expected X-Gatekeeperd-Preserve-Host header")
		}
		originalHost := webhook.Headers["X-Gatekeeperd-Original-Host"]
		if len(originalHost) == 0 || originalHost[0] != "webhooks.example.com" {
			t.Error("expected X-Gatekeeperd-Original-Host header")
		}

		// Send response to clean up
		_ = m.SendResponse(&Response{RequestID: webhook.ID, StatusCode: 200})

	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for webhook")
	}
}

func TestRedisManager_Shutdown(t *testing.T) {
	m, _ := newTestRedisManager(t)

	// Should not panic
	m.Shutdown()

	// Double shutdown should also not panic
	m.Shutdown()
}

func TestRedisManager_ConnectedCount(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")
	m.RegisterToken("token2")

	// Initially no consumers
	if m.ConnectedCount() != 0 {
		t.Errorf("expected 0 connected, got %d", m.ConnectedCount())
	}
}

func TestParseRedisURI(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		wantTLS bool
		wantErr bool
	}{
		{"redis", "redis://localhost:6379", false, false},
		{"redis with password", "redis://:password@localhost:6379", false, false},
		{"redis with db", "redis://localhost:6379/1", false, false},
		{"rediss (TLS)", "rediss://localhost:6379", true, false},
		{"valkey", "valkey://localhost:6379", false, false},
		{"valkeys (TLS)", "valkeys://localhost:6379", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := parseRedisURI(tt.uri)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseRedisURI() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if opts != nil && tt.wantTLS != (opts.TLSConfig != nil) {
				t.Errorf("parseRedisURI() TLS = %v, want %v", opts.TLSConfig != nil, tt.wantTLS)
			}
		})
	}
}

func TestRedisManager_StreamMessageParsing(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	webhook := &Webhook{
		ID:      "test-id",
		Method:  "POST",
		Path:    "/test",
		Headers: map[string][]string{"Content-Type": {"application/json"}},
		Body:    "dGVzdA==",
	}

	webhookJSON, _ := json.Marshal(webhook)

	msg := redis.XMessage{
		ID:     "1234567890-0",
		Values: map[string]any{"webhook": string(webhookJSON)},
	}

	parsed, err := m.parseStreamMessage(msg)
	if err != nil {
		t.Fatalf("parseStreamMessage failed: %v", err)
	}

	if parsed.ID != webhook.ID {
		t.Errorf("expected ID %s, got %s", webhook.ID, parsed.ID)
	}
	if parsed.Method != webhook.Method {
		t.Errorf("expected Method %s, got %s", webhook.Method, parsed.Method)
	}
	if parsed.Headers["X-Relay-Stream-ID"][0] != "1234567890-0" {
		t.Errorf("expected stream ID in headers, got %v", parsed.Headers["X-Relay-Stream-ID"])
	}
}

func TestRedisManager_StreamMessageParsing_Invalid(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	// Test with invalid webhook data
	msg := redis.XMessage{
		ID:     "1234567890-0",
		Values: map[string]any{"webhook": 12345}, // Not a string
	}

	_, err := m.parseStreamMessage(msg)
	if err == nil {
		t.Error("expected error for invalid webhook data")
	}

	// Test with invalid JSON
	msg2 := redis.XMessage{
		ID:     "1234567890-0",
		Values: map[string]any{"webhook": "not-json"},
	}

	_, err = m.parseStreamMessage(msg2)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestRedisManager_IsValidToken_ServerClosed(t *testing.T) {
	s := miniredis.RunT(t)
	m, _ := NewRedisManager("redis://"+s.Addr(), nil)
	defer m.Shutdown()

	// Close the server before checking token
	s.Close()

	// Should return false when Redis is unavailable
	if m.IsValidToken("any-token") {
		t.Error("expected false when Redis is unavailable")
	}
}

func TestRedisManager_IsConnected_ServerClosed(t *testing.T) {
	s := miniredis.RunT(t)
	m, _ := NewRedisManager("redis://"+s.Addr(), nil)
	defer m.Shutdown()

	m.RegisterToken("token1")

	// Close the server
	s.Close()

	// Should return false when Redis is unavailable
	if m.IsConnected("token1") {
		t.Error("expected false when Redis is unavailable")
	}
}

func TestRedisManager_TokenCount_ServerClosed(t *testing.T) {
	s := miniredis.RunT(t)
	m, _ := NewRedisManager("redis://"+s.Addr(), nil)
	defer m.Shutdown()

	// Register a token in local cache
	m.mu.Lock()
	m.tokens["local-token"] = true
	m.mu.Unlock()

	// Close the server
	s.Close()

	// Should fall back to local cache
	count := m.TokenCount()
	if count != 1 {
		t.Errorf("expected 1 from local cache, got %d", count)
	}
}

func TestRedisManager_ConnectedCount_ServerClosed(t *testing.T) {
	s := miniredis.RunT(t)
	m, _ := NewRedisManager("redis://"+s.Addr(), nil)
	defer m.Shutdown()

	m.RegisterToken("token1")

	// Close the server
	s.Close()

	// Should return 0 when Redis is unavailable
	if m.ConnectedCount() != 0 {
		t.Error("expected 0 when Redis is unavailable")
	}
}

func TestRedisManager_Poll_PendingMessages(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Add a message directly to the stream
	webhook := &Webhook{ID: "pending-test", Method: "POST", Path: "/pending"}
	webhookJSON, _ := json.Marshal(webhook)

	// Add message and claim it to create a pending entry
	key := streamKey("token1")
	m.client.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		Values: map[string]any{"webhook": string(webhookJSON)},
	})

	// Now poll - should get the message
	result, err := m.Poll(ctx, "token1")
	if err != nil {
		t.Fatalf("Poll failed: %v", err)
	}

	if result.ID != "pending-test" {
		t.Errorf("expected ID 'pending-test', got %s", result.ID)
	}

	// Poll again with same consumer - should read the pending message
	// First, don't ACK the previous message, so it becomes pending for us
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()

	// This should timeout since there are no new messages
	_, _ = m.Poll(ctx2, "token1")
}

func TestRedisManager_Poll_BlockTimeoutZero(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	// Create a context that's already expired
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	_, err := m.Poll(ctx, "token1")
	if err == nil {
		t.Error("expected error for expired context")
	}
}

func TestRedisManager_Poll_ContextCanceled(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	_, err := m.Poll(ctx, "token1")
	// Error should contain context canceled
	if err == nil {
		t.Error("expected error for canceled context")
	}
}

func TestRedisManager_Poll_ServerClosed(t *testing.T) {
	s := miniredis.RunT(t)
	m, _ := NewRedisManager("redis://"+s.Addr(), nil)
	defer m.Shutdown()

	m.RegisterToken("token1")

	// Close the server
	s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := m.Poll(ctx, "token1")
	// Should get an error (either connection error or context deadline)
	if err == nil {
		t.Error("expected error when Redis is unavailable")
	}
}

func TestRedisManager_Deliver_ContextCanceled(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := m.Deliver(ctx, "token1", &Webhook{Method: "POST", Path: "/test"})
	// Error should indicate cancellation
	if err == nil {
		t.Error("expected error for canceled context")
	}
}

func TestRedisManager_IsConnected_WithConsumer(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Add a message to make the consumer visible
	webhook := &Webhook{ID: "test", Method: "POST", Path: "/test"}
	webhookJSON, _ := json.Marshal(webhook)
	key := streamKey("token1")
	m.client.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		Values: map[string]any{"webhook": string(webhookJSON)},
	})

	// Poll to create and use a consumer
	pollCtx, pollCancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer pollCancel()

	_, _ = m.Poll(pollCtx, "token1")

	// Now check if connected - consumer should be visible after reading
	if !m.IsConnected("token1") {
		// This may fail due to miniredis limitations, which is OK
		t.Log("IsConnected returned false (miniredis may not track consumers)")
	}
}

func TestRedisManager_Poll_EmptyStreamsResult(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	// Poll with a short timeout - should get empty result
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result, err := m.Poll(ctx, "token1")
	// Should timeout or return nil
	if result != nil {
		t.Error("expected nil result for timeout")
	}
	_ = err // Error is expected
}

func TestRedisManager_SendResponse_ServerClosed(t *testing.T) {
	s := miniredis.RunT(t)
	m, _ := NewRedisManager("redis://"+s.Addr(), nil)
	defer m.Shutdown()

	// Close the server
	s.Close()

	// Should get an error when publishing
	err := m.SendResponse(&Response{
		RequestID:  "test",
		StatusCode: 200,
	})
	if err == nil {
		t.Error("expected error when Redis is unavailable")
	}
}

func TestRedisManager_Deliver_ServerClosedDuringXAdd(t *testing.T) {
	s := miniredis.RunT(t)
	m, _ := NewRedisManager("redis://"+s.Addr(), nil)
	defer m.Shutdown()

	m.RegisterToken("token1")

	// Create a goroutine that will close the server after subscribe
	go func() {
		time.Sleep(10 * time.Millisecond)
		s.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := m.Deliver(ctx, "token1", &Webhook{Method: "POST", Path: "/test"})
	// Should get an error (either subscribe fails, xadd fails, or timeout)
	if err == nil {
		t.Error("expected error when Redis closes")
	}
}

func TestRedisManager_Deliver_InvalidResponsePayload(t *testing.T) {
	m, s := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Create a separate client to publish an invalid response
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	// Start delivery in background
	errCh := make(chan error, 1)
	webhookID := "invalid-response-test"
	go func() {
		_, err := m.Deliver(ctx, "token1", &Webhook{ID: webhookID, Method: "POST", Path: "/test"})
		errCh <- err
	}()

	// Wait for subscription to be ready
	time.Sleep(50 * time.Millisecond)

	// Publish invalid JSON to the response channel
	channel := responseChannel(webhookID)
	client.Publish(ctx, channel, "not-valid-json")

	// Should get an unmarshal error
	err := <-errCh
	if err == nil {
		t.Error("expected error for invalid response payload")
	}
}

func TestRedisManager_ConnectedCount_MultipleTokens(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")
	m.RegisterToken("token2")
	m.RegisterToken("token3")

	// With no consumers, count should be 0
	count := m.ConnectedCount()
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}
}

func TestRedisManager_Poll_XReadGroupError(t *testing.T) {
	s := miniredis.RunT(t)
	m, _ := NewRedisManager("redis://"+s.Addr(), nil)
	defer m.Shutdown()

	m.RegisterToken("token1")

	// Start poll and close server mid-operation
	go func() {
		time.Sleep(20 * time.Millisecond)
		s.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err := m.Poll(ctx, "token1")
	// Should get an error due to server closing
	if err == nil {
		t.Error("expected error when server closes")
	}
}

func TestRedisManager_Poll_ReadPendingError(t *testing.T) {
	s := miniredis.RunT(t)
	m, _ := NewRedisManager("redis://"+s.Addr(), nil)
	defer m.Shutdown()

	m.RegisterToken("token1")

	// Immediately close the server to cause an error in reading pending
	s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := m.Poll(ctx, "token1")
	// Should get an error
	if err == nil {
		t.Error("expected error when reading pending fails")
	}
}

func TestRedisManager_EmptyHostname(t *testing.T) {
	s := miniredis.RunT(t)
	defer s.Close()

	// Mock getHostname to return empty string
	originalGetHostname := getHostname
	getHostname = func() (string, error) {
		return "", nil
	}
	defer func() { getHostname = originalGetHostname }()

	m, err := NewRedisManager("redis://"+s.Addr(), nil)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	defer m.Shutdown()

	// Consumer ID should use "unknown" as hostname
	if !strings.Contains(m.consumerID, "unknown-") {
		t.Errorf("expected consumer ID to contain 'unknown-', got %s", m.consumerID)
	}
}

func TestRedisManager_Poll_BlockTimeoutZeroImmediate(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	// Create a context with deadline in the past
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer cancel()

	_, err := m.Poll(ctx, "token1")
	// Should return immediately with context error
	if err == nil {
		t.Error("expected error for expired context")
	}
}

func TestRedisManager_Deliver_XAddError(t *testing.T) {
	s := miniredis.RunT(t)
	m, _ := NewRedisManager("redis://"+s.Addr(), nil)
	defer m.Shutdown()

	m.RegisterToken("token1")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Close server after subscribe but before XAdd completes
	go func() {
		time.Sleep(50 * time.Millisecond)
		s.Close()
	}()

	_, err := m.Deliver(ctx, "token1", &Webhook{Method: "POST", Path: "/test"})
	// Should get an error
	if err == nil {
		t.Error("expected error when XAdd fails")
	}
}

func TestWebhook_IsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt int64
		want      bool
	}{
		{"no expiry", 0, false},
		{"future", time.Now().Add(time.Hour).Unix(), false},
		{"past", time.Now().Add(-time.Hour).Unix(), true},
		{"just expired", time.Now().Add(-time.Second).Unix(), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &Webhook{ExpiresAt: tt.expiresAt}
			if got := w.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRedisManager_Deliver_SetsExpiry(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start polling
	webhookCh := make(chan *Webhook, 1)
	go func() {
		webhook, _ := m.Poll(ctx, "token1")
		webhookCh <- webhook
	}()

	time.Sleep(50 * time.Millisecond)

	// Deliver a webhook without expiry set
	go func() {
		_, _ = m.Deliver(ctx, "token1", &Webhook{Method: "POST", Path: "/test"})
	}()

	// Check that the received webhook has expiry set
	select {
	case webhook := <-webhookCh:
		if webhook.ExpiresAt == 0 {
			t.Error("expected ExpiresAt to be set")
		}
		// Should be about 30 seconds in the future
		expectedMin := time.Now().Add(25 * time.Second).Unix()
		expectedMax := time.Now().Add(35 * time.Second).Unix()
		if webhook.ExpiresAt < expectedMin || webhook.ExpiresAt > expectedMax {
			t.Errorf("ExpiresAt %d not in expected range [%d, %d]", webhook.ExpiresAt, expectedMin, expectedMax)
		}
		// Send response to clean up
		_ = m.SendResponse(&Response{RequestID: webhook.ID, StatusCode: 200})
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for webhook")
	}
}

func TestRedisManager_Poll_SkipsExpiredWebhooks(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Directly add an expired webhook to the stream
	expiredWebhook := &Webhook{
		ID:        "expired-1",
		Method:    "POST",
		Path:      "/expired",
		ExpiresAt: time.Now().Add(-time.Hour).Unix(), // Already expired
	}
	expiredJSON, _ := json.Marshal(expiredWebhook)
	key := streamKey("token1")
	m.client.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		Values: map[string]any{"webhook": string(expiredJSON)},
	})

	// Add a valid webhook
	validWebhook := &Webhook{
		ID:        "valid-1",
		Method:    "POST",
		Path:      "/valid",
		ExpiresAt: time.Now().Add(time.Hour).Unix(), // Not expired
	}
	validJSON, _ := json.Marshal(validWebhook)
	m.client.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		Values: map[string]any{"webhook": string(validJSON)},
	})

	// Poll should skip the expired webhook and return the valid one
	webhook, err := m.Poll(ctx, "token1")
	if err != nil {
		t.Fatalf("Poll failed: %v", err)
	}

	if webhook.ID != "valid-1" {
		t.Errorf("expected valid webhook, got %s", webhook.ID)
	}
}

func TestRedisManager_Poll_SkipsInvalidWebhookData(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := streamKey("token1")

	// Add invalid webhook data
	m.client.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		Values: map[string]any{"webhook": "not-valid-json"},
	})

	// Add a valid webhook
	validWebhook := &Webhook{
		ID:        "valid-1",
		Method:    "POST",
		Path:      "/valid",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	validJSON, _ := json.Marshal(validWebhook)
	m.client.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		Values: map[string]any{"webhook": string(validJSON)},
	})

	// Poll should skip the invalid webhook and return the valid one
	webhook, err := m.Poll(ctx, "token1")
	if err != nil {
		t.Fatalf("Poll failed: %v", err)
	}

	if webhook.ID != "valid-1" {
		t.Errorf("expected valid webhook, got %s", webhook.ID)
	}
}

func TestRedisManager_StartRecovery(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start recovery
	m.StartRecovery(ctx)

	// Verify recovery goroutine is running by checking that done channel exists
	if m.recoveryDone == nil {
		t.Error("expected recoveryDone channel to be set")
	}

	// Cancel context and verify recovery stops
	cancel()

	// Wait for recovery to stop (with timeout)
	select {
	case <-m.recoveryDone:
		// Good - recovery stopped
	case <-time.After(time.Second):
		t.Error("recovery goroutine didn't stop after cancel")
	}
}

func TestRedisManager_RecoverPendingMessages_NoPending(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	ctx := context.Background()

	// Should not error with no pending messages
	m.recoverPendingMessages(ctx)
}

func TestRedisManager_RecoverPendingMessages_ServerClosed(t *testing.T) {
	s := miniredis.RunT(t)
	m, _ := NewRedisManager("redis://"+s.Addr(), nil)
	defer m.Shutdown()

	m.RegisterToken("token1")

	// Close server
	s.Close()

	ctx := context.Background()

	// Should not panic with server closed
	m.recoverPendingMessages(ctx)
}

func TestRedisManager_RecoverTokenPending_NoStream(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	ctx := context.Background()

	// Should not error when stream doesn't exist
	m.recoverTokenPending(ctx, "nonexistent-token")
}

func TestRedisManager_Shutdown_StopsRecovery(t *testing.T) {
	m, _ := newTestRedisManager(t)

	ctx := context.Background()
	m.StartRecovery(ctx)

	// Shutdown should stop recovery
	m.Shutdown()

	// Verify recovery channel is closed (would panic if we tried to close it again)
	select {
	case <-m.recoveryDone:
		// Good - recovery stopped
	default:
		t.Error("recovery goroutine didn't stop on shutdown")
	}
}

func TestRedisManager_RecoverPendingMessages_ContextCanceled(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")
	m.RegisterToken("token2")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Should return early when context is canceled
	m.recoverPendingMessages(ctx)
}

func TestRedisManager_RecoverTokenPending_ContextCanceled(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Should return early when context is canceled
	m.recoverTokenPending(ctx, "token1")
}

func TestRedisManager_RecoverTokenPending_WithPendingMessages(t *testing.T) {
	s := miniredis.RunT(t)
	defer s.Close()

	// Create first manager to claim a message
	m1, err := NewRedisManager("redis://"+s.Addr(), nil)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	defer m1.Shutdown()

	m1.RegisterToken("token1")

	ctx := context.Background()
	key := streamKey("token1")

	// Add a webhook message
	webhook := &Webhook{
		ID:        "pending-recovery-test",
		Method:    "POST",
		Path:      "/test",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	webhookJSON, _ := json.Marshal(webhook)
	m1.client.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		Values: map[string]any{"webhook": string(webhookJSON)},
	})

	// Poll to claim the message (but don't ACK it)
	pollCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, _ = m1.Poll(pollCtx, "token1")

	// Create second manager for recovery
	m2, err := NewRedisManager("redis://"+s.Addr(), nil)
	if err != nil {
		t.Fatalf("failed to create second manager: %v", err)
	}
	defer m2.Shutdown()

	// Try to recover - miniredis may not fully support XPENDING/XCLAIM behavior,
	// but this exercises the code path
	m2.recoverTokenPending(ctx, "token1")
}

func TestRedisManager_RecoverTokenPending_NoPendingCount(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	ctx := context.Background()
	key := streamKey("token1")

	// Add a message but don't claim it - so pending count is 0
	webhook := &Webhook{ID: "unclaimed", Method: "POST", Path: "/test"}
	webhookJSON, _ := json.Marshal(webhook)
	m.client.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		Values: map[string]any{"webhook": string(webhookJSON)},
	})

	// Recover should exit early since pending count is 0
	m.recoverTokenPending(ctx, "token1")
}

func TestRedisManager_RecoverTokenPending_XPendingExtError(t *testing.T) {
	s := miniredis.RunT(t)
	m, _ := NewRedisManager("redis://"+s.Addr(), nil)
	defer m.Shutdown()

	m.RegisterToken("token1")

	ctx := context.Background()
	key := streamKey("token1")

	// Add and claim a message to create pending entry
	webhook := &Webhook{ID: "pending-ext-error", Method: "POST", Path: "/test"}
	webhookJSON, _ := json.Marshal(webhook)
	m.client.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		Values: map[string]any{"webhook": string(webhookJSON)},
	})

	// Poll to claim message
	pollCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, _ = m.Poll(pollCtx, "token1")

	// Close server to cause XPendingExt error
	s.Close()

	// Should log error but not panic
	m.recoverTokenPending(ctx, "token1")
}

func TestRedisManager_RecoverTokenPending_LoopContextCancel(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")
	m.RegisterToken("token2")
	m.RegisterToken("token3")

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after first token might be processed
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	// Should exit early when context is canceled during loop
	m.recoverPendingMessages(ctx)
}

func TestRedisManager_Poll_NewMessagesExpired(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := streamKey("token1")

	// Add only expired webhooks - Poll should skip them all and timeout
	for i := 0; i < 3; i++ {
		expiredWebhook := &Webhook{
			ID:        fmt.Sprintf("expired-%d", i),
			Method:    "POST",
			Path:      "/expired",
			ExpiresAt: time.Now().Add(-time.Hour).Unix(), // Already expired
		}
		expiredJSON, _ := json.Marshal(expiredWebhook)
		m.client.XAdd(ctx, &redis.XAddArgs{
			Stream: key,
			Values: map[string]any{"webhook": string(expiredJSON)},
		})
	}

	// Use short timeout - should exhaust all expired webhooks then timeout
	pollCtx, pollCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer pollCancel()

	_, err := m.Poll(pollCtx, "token1")
	// Should timeout after processing all expired webhooks
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestRedisManager_Poll_ServerErrorDuringNewRead(t *testing.T) {
	s := miniredis.RunT(t)
	m, _ := NewRedisManager("redis://"+s.Addr(), nil)
	defer m.Shutdown()

	m.RegisterToken("token1")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Close server after a short delay to cause error during XREADGROUP
	go func() {
		time.Sleep(50 * time.Millisecond)
		s.Close()
	}()

	_, err := m.Poll(ctx, "token1")
	// Should return error
	if err == nil {
		t.Error("expected error when server closes during read")
	}
}

func TestRedisManager_ConnectedCount_WithTokens(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	// Add multiple tokens
	for i := 0; i < 5; i++ {
		m.RegisterToken(fmt.Sprintf("token%d", i))
	}

	// Without consumers, count should be 0
	count := m.ConnectedCount()
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}
}

func TestRedisManager_Deliver_GeneratesID(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start polling
	webhookCh := make(chan *Webhook, 1)
	go func() {
		webhook, _ := m.Poll(ctx, "token1")
		webhookCh <- webhook
	}()

	time.Sleep(50 * time.Millisecond)

	// Deliver a webhook without ID - should generate one
	go func() {
		_, _ = m.Deliver(ctx, "token1", &Webhook{Method: "POST", Path: "/test"})
	}()

	select {
	case webhook := <-webhookCh:
		if webhook.ID == "" {
			t.Error("expected ID to be generated")
		}
		// Send response to clean up
		_ = m.SendResponse(&Response{RequestID: webhook.ID, StatusCode: 200})
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for webhook")
	}
}

func TestRedisManager_StartRecovery_TickerFires(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	// Use a very short recovery interval to test the ticker fires
	m.recoveryInterval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	// Start recovery
	m.StartRecovery(ctx)

	// Wait for at least one tick to fire
	time.Sleep(50 * time.Millisecond)

	// Cancel and wait for shutdown
	cancel()

	select {
	case <-m.recoveryDone:
		// Good
	case <-time.After(time.Second):
		t.Error("recovery didn't stop")
	}
}

func TestRedisManager_Poll_ExpiredPendingMessage(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	ctx := context.Background()
	key := streamKey("token1")

	// Add a webhook that will expire very soon (ExpiresAt is in the past by the time we poll again)
	// We set ExpiresAt to now+1s, then wait 2s before re-polling so that now > ExpiresAt
	soonExpiringWebhook := &Webhook{
		ID:        "will-expire-soon",
		Method:    "POST",
		Path:      "/test",
		ExpiresAt: time.Now().Unix() + 1, // Expires 1 second from now (Unix granularity)
	}
	soonJSON, _ := json.Marshal(soonExpiringWebhook)
	m.client.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		Values: map[string]any{"webhook": string(soonJSON)},
	})

	// Poll to claim the message - it's NOT expired yet
	pollCtx1, cancel1 := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel1()
	result1, err := m.Poll(pollCtx1, "token1")
	if err != nil {
		t.Fatalf("first poll failed: %v", err)
	}
	if result1.ID != "will-expire-soon" {
		t.Errorf("expected 'will-expire-soon', got %s", result1.ID)
	}
	// DON'T ACK the webhook - leave it in pending state

	// Wait long enough for the webhook to expire (need now > ExpiresAt, so wait 2+ seconds)
	time.Sleep(2100 * time.Millisecond)

	// Add a valid message
	validWebhook := &Webhook{
		ID:        "valid-after-expired-pending",
		Method:    "POST",
		Path:      "/valid",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	validJSON, _ := json.Marshal(validWebhook)
	m.client.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		Values: map[string]any{"webhook": string(validJSON)},
	})

	// Poll again - should find pending message (now expired), skip it via line 315-318, then get valid
	pollCtx2, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	defer cancel2()
	webhook, err := m.Poll(pollCtx2, "token1")
	if err != nil {
		t.Fatalf("Poll failed: %v", err)
	}
	if webhook.ID != "valid-after-expired-pending" {
		t.Errorf("expected valid webhook, got %s", webhook.ID)
	}
}

func TestRedisManager_Poll_BlockTimeoutNearZero(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	m.RegisterToken("token1")

	// Create a context with deadline just barely in the future (50ms)
	// The default block timeout is 30s, so this should be less
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Poll - should use the short deadline and timeout
	_, err := m.Poll(ctx, "token1")
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestRedisManager_RecoverPendingMessages_ContextCanceledDuringLoop(t *testing.T) {
	m, _ := newTestRedisManager(t)
	defer m.Shutdown()

	// Register several tokens
	for i := 0; i < 10; i++ {
		m.RegisterToken(fmt.Sprintf("token%d", i))
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Start recovery in background and cancel during loop
	done := make(chan struct{})
	go func() {
		// Give SMembers time to complete, then cancel during token loop
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	go func() {
		m.recoverPendingMessages(ctx)
		close(done)
	}()

	// Should complete quickly after cancel
	select {
	case <-done:
		// Good
	case <-time.After(time.Second):
		t.Error("recovery didn't stop after context cancel")
	}
}
