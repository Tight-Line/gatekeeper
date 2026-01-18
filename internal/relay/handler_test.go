package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func newTestHandler() (*Handler, *Manager) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	manager := NewManager()
	handler := NewHandler(manager, logger)
	handler.SetPollTimeout(100 * time.Millisecond) // Short timeout for tests
	return handler, manager
}

func TestHandler_NotFound(t *testing.T) {
	handler, _ := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/relay/unknown", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, rr.Code)
	}
}

func TestHandler_Poll_WrongMethod(t *testing.T) {
	handler, _ := newTestHandler()

	req := httptest.NewRequest(http.MethodPost, "/relay/poll", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, rr.Code)
	}
}

func TestHandler_Poll_MissingToken(t *testing.T) {
	handler, _ := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/relay/poll", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestHandler_Poll_InvalidToken(t *testing.T) {
	handler, _ := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/relay/poll", nil)
	req.Header.Set(TokenHeader, "invalid-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rr.Code)
	}
}

func TestHandler_Poll_Timeout(t *testing.T) {
	handler, manager := newTestHandler()
	manager.RegisterToken("valid-token")

	req := httptest.NewRequest(http.MethodGet, "/relay/poll", nil)
	req.Header.Set(TokenHeader, "valid-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("expected status %d, got %d", http.StatusNoContent, rr.Code)
	}
}

func TestHandler_Poll_WebhookDelivery(t *testing.T) {
	handler, manager := newTestHandler()
	manager.RegisterToken("valid-token")

	// Start poll in background
	req := httptest.NewRequest(http.MethodGet, "/relay/poll", nil)
	req.Header.Set(TokenHeader, "valid-token")
	rr := httptest.NewRecorder()

	pollDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(rr, req)
		close(pollDone)
	}()

	// Wait for poll to start
	time.Sleep(10 * time.Millisecond)

	// Deliver webhook in background (it will block waiting for response)
	webhook := &Webhook{
		ID:      "test-id",
		Method:  "POST",
		Path:    "/events",
		Headers: map[string][]string{"Content-Type": {"application/json"}},
		Body:    "eyJ0ZXN0IjogdHJ1ZX0=", // {"test": true} base64 encoded
	}

	deliverCtx, deliverCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer deliverCancel()

	deliverDone := make(chan error)
	go func() {
		_, err := manager.Deliver(deliverCtx, "valid-token", webhook)
		deliverDone <- err
	}()

	// Wait for poll handler to return with webhook
	select {
	case <-pollDone:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for poll handler")
	}

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	// Check response body
	var received Webhook
	if err := json.Unmarshal(rr.Body.Bytes(), &received); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if received.ID != "test-id" {
		t.Errorf("expected webhook ID 'test-id', got %q", received.ID)
	}
	if received.Method != "POST" {
		t.Errorf("expected method 'POST', got %q", received.Method)
	}
	if received.Path != "/events" {
		t.Errorf("expected path '/events', got %q", received.Path)
	}

	// Send response back to complete the Deliver call
	err := manager.SendResponse(&Response{
		RequestID:  "test-id",
		StatusCode: 200,
	})
	if err != nil {
		t.Fatalf("SendResponse failed: %v", err)
	}

	// Deliver should complete
	select {
	case err := <-deliverDone:
		if err != nil {
			t.Fatalf("Deliver failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for Deliver to complete")
	}
}

func TestHandler_Response_MissingToken(t *testing.T) {
	handler, _ := newTestHandler()

	body := bytes.NewBufferString(`{"request_id":"test","status_code":200}`)
	req := httptest.NewRequest(http.MethodPost, "/relay/response", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestHandler_Response_InvalidToken(t *testing.T) {
	handler, _ := newTestHandler()

	body := bytes.NewBufferString(`{"request_id":"test","status_code":200}`)
	req := httptest.NewRequest(http.MethodPost, "/relay/response", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(TokenHeader, "invalid-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rr.Code)
	}
}

func TestHandler_Response_InvalidJSON(t *testing.T) {
	handler, manager := newTestHandler()
	manager.RegisterToken("valid-token")

	body := bytes.NewBufferString(`not json`)
	req := httptest.NewRequest(http.MethodPost, "/relay/response", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(TokenHeader, "valid-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestHandler_Response_RequestNotFound(t *testing.T) {
	handler, manager := newTestHandler()
	manager.RegisterToken("valid-token")

	body := bytes.NewBufferString(`{"request_id":"nonexistent","status_code":200}`)
	req := httptest.NewRequest(http.MethodPost, "/relay/response", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(TokenHeader, "valid-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, rr.Code)
	}
}

func TestHandler_Response_Success(t *testing.T) {
	handler, manager := newTestHandler()
	manager.RegisterToken("valid-token")

	// Start a poll so we have a connected client
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()

	go func() {
		_, _ = manager.Poll(pollCtx, "valid-token")
	}()

	// Wait for poll to start
	time.Sleep(10 * time.Millisecond)

	// Start a Deliver in background
	webhook := &Webhook{ID: "test-request-id", Method: "POST", Path: "/test"}
	deliverCtx, deliverCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer deliverCancel()

	responseCh := make(chan *Response)
	go func() {
		resp, _ := manager.Deliver(deliverCtx, "valid-token", webhook)
		responseCh <- resp
	}()

	// Wait for deliver to send webhook and be waiting for response
	time.Sleep(20 * time.Millisecond)

	// Now send response via HTTP handler
	respBody := `{"request_id":"test-request-id","status_code":201,"headers":{"X-Custom":["value"]},"body":"dGVzdA=="}`
	req := httptest.NewRequest(http.MethodPost, "/relay/response", bytes.NewBufferString(respBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(TokenHeader, "valid-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	// Deliver should complete with the response
	select {
	case resp := <-responseCh:
		if resp == nil {
			t.Fatal("expected response, got nil")
		}
		if resp.StatusCode != 201 {
			t.Errorf("expected status 201, got %d", resp.StatusCode)
		}
		if len(resp.Headers["X-Custom"]) == 0 || resp.Headers["X-Custom"][0] != "value" {
			t.Errorf("expected X-Custom header with value 'value', got %v", resp.Headers)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for response")
	}
}

func TestHandler_Response_WrongMethod(t *testing.T) {
	handler, _ := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/relay/response", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, rr.Code)
	}
}

// errorWriter is a ResponseWriter that fails on Write
type errorWriter struct {
	header http.Header
}

func (e *errorWriter) Header() http.Header {
	if e.header == nil {
		e.header = make(http.Header)
	}
	return e.header
}

func (e *errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("simulated write error")
}

func (e *errorWriter) WriteHeader(int) {}

func TestHandler_Poll_WriteError(t *testing.T) {
	handler, manager := newTestHandler()
	manager.RegisterToken("valid-token")

	// Start poll in background with error writer
	req := httptest.NewRequest(http.MethodGet, "/relay/poll", nil)
	req.Header.Set(TokenHeader, "valid-token")
	ew := &errorWriter{}

	pollDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(ew, req)
		close(pollDone)
	}()

	// Wait for poll to start
	time.Sleep(10 * time.Millisecond)

	// Deliver webhook
	webhook := &Webhook{
		ID:     "test-id",
		Method: "POST",
		Path:   "/events",
	}

	deliverCtx, deliverCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer deliverCancel()

	go func() {
		// Don't care about result, we're testing the write error path
		_, _ = manager.Deliver(deliverCtx, "valid-token", webhook)
	}()

	// Wait for poll handler to return (it will hit the write error)
	select {
	case <-pollDone:
		// Success - the handler completed (even though write failed)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for poll handler")
	}
}
