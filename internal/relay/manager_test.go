package relay

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestManager_RegisterToken(t *testing.T) {
	m := NewManager()

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

func TestManager_IsConnected(t *testing.T) {
	m := NewManager()
	m.RegisterToken("token1")

	// Initially not connected
	if m.IsConnected("token1") {
		t.Error("token1 should not be connected initially")
	}

	// Start a poll in background
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = m.Poll(ctx, "token1")
		close(done)
	}()

	// Wait a bit for poll to start
	time.Sleep(10 * time.Millisecond)

	if !m.IsConnected("token1") {
		t.Error("token1 should be connected while polling")
	}

	// Cancel and wait for poll to finish
	cancel()
	<-done

	// Should no longer be connected
	if m.IsConnected("token1") {
		t.Error("token1 should not be connected after poll canceled")
	}
}

func TestManager_Deliver_NoClient(t *testing.T) {
	m := NewManager()
	m.RegisterToken("token1")

	ctx := context.Background()

	// Deliver without connected client should fail
	_, err := m.Deliver(ctx, "token1", &Webhook{Method: "POST", Path: "/test"})
	if err != ErrNoClient {
		t.Errorf("expected ErrNoClient, got %v", err)
	}
}

func TestManager_Deliver_InvalidToken(t *testing.T) {
	m := NewManager()

	ctx := context.Background()

	// Deliver with invalid token should fail
	_, err := m.Deliver(ctx, "invalid", &Webhook{Method: "POST", Path: "/test"})
	if err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestManager_Deliver_Success(t *testing.T) {
	m := NewManager()
	m.RegisterToken("token1")

	// Start a poll in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	webhookCh := make(chan *Webhook)
	go func() {
		webhook, _ := m.Poll(ctx, "token1")
		webhookCh <- webhook
	}()

	// Wait for poll to start
	time.Sleep(10 * time.Millisecond)

	// Deliver should block until response, so do it in goroutine
	deliverCtx, deliverCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer deliverCancel()

	webhook := &Webhook{Method: "POST", Path: "/test", Body: "dGVzdA=="}
	responseCh := make(chan *Response)
	errCh := make(chan error)

	go func() {
		resp, err := m.Deliver(deliverCtx, "token1", webhook)
		if err != nil {
			errCh <- err
			return
		}
		responseCh <- resp
	}()

	// Webhook should be received by poll
	var receivedWebhook *Webhook
	select {
	case receivedWebhook = <-webhookCh:
		if receivedWebhook.Method != "POST" || receivedWebhook.Path != "/test" {
			t.Errorf("received wrong webhook: %+v", receivedWebhook)
		}
		if receivedWebhook.ID == "" {
			t.Error("webhook should have an ID assigned")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for webhook")
	}

	// Send response back
	err := m.SendResponse(&Response{
		RequestID:  receivedWebhook.ID,
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       "eyJvayI6dHJ1ZX0=",
	})
	if err != nil {
		t.Fatalf("SendResponse failed: %v", err)
	}

	// Deliver should complete with response
	select {
	case resp := <-responseCh:
		if resp.StatusCode != 200 {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}
	case err := <-errCh:
		t.Fatalf("Deliver failed: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for deliver to complete")
	}
}

func TestManager_Deliver_ContextCancelled(t *testing.T) {
	m := NewManager()
	m.RegisterToken("token1")

	// Start a poll in background
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()

	go func() {
		_, _ = m.Poll(pollCtx, "token1")
	}()

	// Wait for poll to start
	time.Sleep(10 * time.Millisecond)

	// Deliver with short timeout (no response will be sent)
	deliverCtx, deliverCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer deliverCancel()

	webhook := &Webhook{Method: "POST", Path: "/test"}
	_, err := m.Deliver(deliverCtx, "token1", webhook)

	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestManager_SendResponse_NotFound(t *testing.T) {
	m := NewManager()

	err := m.SendResponse(&Response{
		RequestID:  "nonexistent",
		StatusCode: 200,
	})

	if err != ErrRequestNotFound {
		t.Errorf("expected ErrRequestNotFound, got %v", err)
	}
}

func TestManager_Poll_InvalidToken(t *testing.T) {
	m := NewManager()

	_, err := m.Poll(context.Background(), "invalid")
	if err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestManager_Poll_ContextCancelled(t *testing.T) {
	m := NewManager()
	m.RegisterToken("token1")

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	var pollErr error
	go func() {
		_, pollErr = m.Poll(ctx, "token1")
		close(done)
	}()

	// Wait for poll to start
	time.Sleep(10 * time.Millisecond)

	// Cancel context
	cancel()

	// Wait for poll to return
	select {
	case <-done:
		if pollErr != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", pollErr)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for poll to return")
	}
}

func TestManager_Poll_NewClientCancelsOld(t *testing.T) {
	m := NewManager()
	m.RegisterToken("token1")

	// Start first poll
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	done1 := make(chan error)
	go func() {
		_, err := m.Poll(ctx1, "token1")
		done1 <- err
	}()

	// Wait for first poll to start
	time.Sleep(10 * time.Millisecond)

	// Start second poll (should cancel first)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	done2 := make(chan error)
	go func() {
		_, err := m.Poll(ctx2, "token1")
		done2 <- err
	}()

	// First poll should be canceled
	select {
	case err := <-done1:
		if err != context.Canceled {
			t.Errorf("expected first poll to be canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for first poll to be canceled")
	}

	// Second poll should still be active
	if !m.IsConnected("token1") {
		t.Error("second poll should still be connected")
	}

	cancel2()
	<-done2
}

func TestManager_ConnectedCount(t *testing.T) {
	m := NewManager()
	m.RegisterToken("token1")
	m.RegisterToken("token2")

	if m.ConnectedCount() != 0 {
		t.Error("expected 0 connected initially")
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())

	done1 := make(chan struct{})
	done2 := make(chan struct{})

	go func() {
		_, _ = m.Poll(ctx1, "token1")
		close(done1)
	}()
	go func() {
		_, _ = m.Poll(ctx2, "token2")
		close(done2)
	}()

	time.Sleep(10 * time.Millisecond)

	if m.ConnectedCount() != 2 {
		t.Errorf("expected 2 connected, got %d", m.ConnectedCount())
	}

	cancel1()
	<-done1
	time.Sleep(10 * time.Millisecond)

	if m.ConnectedCount() != 1 {
		t.Errorf("expected 1 connected, got %d", m.ConnectedCount())
	}

	cancel2()
	<-done2
}

func TestManager_Shutdown(t *testing.T) {
	m := NewManager()
	m.RegisterToken("token1")
	m.RegisterToken("token2")

	ctx1 := context.Background()
	ctx2 := context.Background()

	done1 := make(chan error)
	done2 := make(chan error)

	go func() {
		_, err := m.Poll(ctx1, "token1")
		done1 <- err
	}()
	go func() {
		_, err := m.Poll(ctx2, "token2")
		done2 <- err
	}()

	// Wait for polls to start
	time.Sleep(10 * time.Millisecond)

	if m.ConnectedCount() != 2 {
		t.Fatalf("expected 2 connected, got %d", m.ConnectedCount())
	}

	// Shutdown should cancel all waiters
	m.Shutdown()

	// Both polls should return with context.Canceled
	select {
	case err := <-done1:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled for poll1, got %v", err)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for poll1 to return")
	}

	select {
	case err := <-done2:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled for poll2, got %v", err)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for poll2 to return")
	}
}

func TestManager_DeliverHTTPRequest(t *testing.T) {
	m := NewManager()
	m.RegisterToken("token1")

	// Start a poll in background
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()

	webhookCh := make(chan *Webhook)
	go func() {
		webhook, _ := m.Poll(pollCtx, "token1")
		webhookCh <- webhook
	}()

	// Wait for poll to start
	time.Sleep(10 * time.Millisecond)

	// Create an HTTP request
	req, err := http.NewRequest(http.MethodPost, "http://example.com/webhook", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Custom-Header", "value")

	body := []byte(`{"test": true}`)

	// Deliver in background
	deliverCtx, deliverCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer deliverCancel()

	responseCh := make(chan *Response)
	go func() {
		resp, _ := m.DeliverHTTPRequest(deliverCtx, "token1", req, body, false)
		responseCh <- resp
	}()

	// Receive the webhook
	var receivedWebhook *Webhook
	select {
	case receivedWebhook = <-webhookCh:
		if receivedWebhook.Method != "POST" {
			t.Errorf("expected method POST, got %s", receivedWebhook.Method)
		}
		if receivedWebhook.Path != "/webhook" {
			t.Errorf("expected path /webhook, got %s", receivedWebhook.Path)
		}
		if len(receivedWebhook.Headers["Content-Type"]) == 0 || receivedWebhook.Headers["Content-Type"][0] != "application/json" {
			t.Errorf("expected Content-Type header with value 'application/json', got %v", receivedWebhook.Headers)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for webhook")
	}

	// Send response back
	err = m.SendResponse(&Response{
		RequestID:  receivedWebhook.ID,
		StatusCode: 200,
	})
	if err != nil {
		t.Fatalf("SendResponse failed: %v", err)
	}

	// Wait for deliver to complete
	select {
	case resp := <-responseCh:
		if resp.StatusCode != 200 {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for response")
	}
}

func TestManager_SendResponse_AfterDeliverCompletes(t *testing.T) {
	m := NewManager()
	m.RegisterToken("token1")

	// Start a poll
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()

	webhookCh := make(chan *Webhook)
	go func() {
		webhook, _ := m.Poll(pollCtx, "token1")
		webhookCh <- webhook
	}()

	// Wait for poll to start
	time.Sleep(10 * time.Millisecond)

	// Deliver a webhook
	webhook := &Webhook{ID: "test-id", Method: "POST", Path: "/test"}
	deliverCtx, deliverCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer deliverCancel()

	deliverDone := make(chan struct{})
	go func() {
		_, _ = m.Deliver(deliverCtx, "token1", webhook)
		close(deliverDone)
	}()

	// Receive the webhook
	<-webhookCh

	// Send response - should succeed and complete the Deliver
	err := m.SendResponse(&Response{
		RequestID:  "test-id",
		StatusCode: 200,
	})
	if err != nil {
		t.Fatalf("SendResponse failed: %v", err)
	}

	// Wait for Deliver to complete (which cleans up the pending request)
	select {
	case <-deliverDone:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for Deliver to complete")
	}

	// Response after Deliver completes should fail (pending cleaned up)
	err = m.SendResponse(&Response{
		RequestID:  "test-id",
		StatusCode: 201,
	})
	if err != ErrRequestNotFound {
		t.Errorf("expected ErrRequestNotFound after deliver completed, got %v", err)
	}
}

func TestManager_Deliver_ContextCancelledDuringSend(t *testing.T) {
	m := NewManager()
	m.RegisterToken("token1")

	// Start a poll but don't consume from the channel
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()

	pollStarted := make(chan struct{})
	go func() {
		m.mu.Lock()
		// Mark as connected but don't actually poll (leave channel full)
		m.waiters["token1"] = &waiter{id: "fake", cancel: func() {}}
		m.mu.Unlock()
		close(pollStarted)
		<-pollCtx.Done()
	}()

	<-pollStarted

	// Fill the channel buffer
	m.channels["token1"] <- &Webhook{ID: "blocking"}

	// Now try to deliver with an already-canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	webhook := &Webhook{ID: "test-id", Method: "POST", Path: "/test"}
	_, err := m.Deliver(ctx, "token1", webhook)

	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestManager_SendResponse_DuplicateResponse(t *testing.T) {
	m := NewManager()
	m.RegisterToken("token1")

	// Start a poll
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()

	webhookCh := make(chan *Webhook)
	go func() {
		webhook, _ := m.Poll(pollCtx, "token1")
		webhookCh <- webhook
	}()

	// Wait for poll to start
	time.Sleep(10 * time.Millisecond)

	// Deliver a webhook
	webhook := &Webhook{ID: "dup-test-id", Method: "POST", Path: "/test"}
	deliverCtx, deliverCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer deliverCancel()

	responseCh := make(chan *Response)
	go func() {
		resp, _ := m.Deliver(deliverCtx, "token1", webhook)
		responseCh <- resp
	}()

	// Receive the webhook
	<-webhookCh

	// Send first response - should succeed
	err := m.SendResponse(&Response{
		RequestID:  "dup-test-id",
		StatusCode: 200,
	})
	if err != nil {
		t.Fatalf("first SendResponse failed: %v", err)
	}

	// Wait for Deliver to receive the response
	<-responseCh

	// Send duplicate response while pending still exists (race window)
	// This tests the non-blocking send's default case
	// We need to send before cleanup happens, but after channel is full
	// For this, we'll create a scenario where we can control timing

	// Create a new pending request manually to test duplicate
	responseCh2 := make(chan *Response, 1)
	m.mu.Lock()
	m.pending["dup-test-2"] = &pendingRequest{responseCh: responseCh2}
	m.mu.Unlock()

	// Fill the response channel
	responseCh2 <- &Response{RequestID: "dup-test-2", StatusCode: 200}

	// Now send a "duplicate" - should not block, should return nil
	err = m.SendResponse(&Response{
		RequestID:  "dup-test-2",
		StatusCode: 201, // Different status to distinguish
	})
	if err != nil {
		t.Errorf("duplicate SendResponse should not error, got %v", err)
	}

	// Original response should still be there
	select {
	case resp := <-responseCh2:
		if resp.StatusCode != 200 {
			t.Errorf("expected original response with status 200, got %d", resp.StatusCode)
		}
	default:
		t.Error("expected response in channel")
	}
}

func TestManager_DeliverHTTPRequest_PreserveHost(t *testing.T) {
	m := NewManager()
	m.RegisterToken("token1")

	// Start a poll in background
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()

	webhookCh := make(chan *Webhook)
	go func() {
		webhook, _ := m.Poll(pollCtx, "token1")
		webhookCh <- webhook
	}()

	// Wait for poll to start
	time.Sleep(10 * time.Millisecond)

	// Create an HTTP request with a specific Host
	req, err := http.NewRequest(http.MethodPost, "http://example.com/webhook", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = "webhooks.example.com" // Original host
	req.Header.Set("Content-Type", "application/json")

	body := []byte(`{"test": true}`)

	// Deliver with preserveHost=true
	deliverCtx, deliverCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer deliverCancel()

	responseCh := make(chan *Response)
	go func() {
		resp, _ := m.DeliverHTTPRequest(deliverCtx, "token1", req, body, true)
		responseCh <- resp
	}()

	// Receive the webhook and verify preserve host headers
	var receivedWebhook *Webhook
	select {
	case receivedWebhook = <-webhookCh:
		// Check that preserve host headers are set
		preserveHost := receivedWebhook.Headers["X-Gatekeeperd-Preserve-Host"]
		if len(preserveHost) == 0 || preserveHost[0] != "true" {
			t.Errorf("expected X-Gatekeeperd-Preserve-Host header 'true', got %v", preserveHost)
		}

		originalHost := receivedWebhook.Headers["X-Gatekeeperd-Original-Host"]
		if len(originalHost) == 0 || originalHost[0] != "webhooks.example.com" {
			t.Errorf("expected X-Gatekeeperd-Original-Host header 'webhooks.example.com', got %v", originalHost)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for webhook")
	}

	// Send response back to complete the test
	err = m.SendResponse(&Response{
		RequestID:  receivedWebhook.ID,
		StatusCode: 200,
	})
	if err != nil {
		t.Fatalf("SendResponse failed: %v", err)
	}

	// Wait for deliver to complete
	select {
	case <-responseCh:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for response")
	}
}
