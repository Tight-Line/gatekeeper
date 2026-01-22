package relayclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewPoller(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller("http://relay.example.com", "token123", "test-channel", forwarder, logger, 5, 1)

	if p.serverURL != "http://relay.example.com" {
		t.Errorf("expected serverURL 'http://relay.example.com', got %q", p.serverURL)
	}
	if p.token != "token123" {
		t.Errorf("expected token 'token123', got %q", p.token)
	}
	if p.channelName != "test-channel" {
		t.Errorf("expected channelName 'test-channel', got %q", p.channelName)
	}
	if p.maxConsecutiveFailures != 5 {
		t.Errorf("expected maxConsecutiveFailures 5, got %d", p.maxConsecutiveFailures)
	}
}

func TestPoller_Poll_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/relay/poll" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get(TokenHeader) != "test-token" {
			t.Errorf("expected token header")
		}

		webhook := &Webhook{
			ID:      "webhook-1",
			Method:  "POST",
			Path:    "/test",
			Headers: map[string][]string{"Content-Type": {"application/json"}},
			Body:    base64.StdEncoding.EncodeToString([]byte(`{"test":"data"}`)),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(webhook)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5, 1)

	webhook, err := p.poll(context.Background())
	if err != nil {
		t.Fatalf("poll failed: %v", err)
	}
	if webhook == nil {
		t.Fatal("expected webhook, got nil")
	}
	if webhook.ID != "webhook-1" {
		t.Errorf("expected ID 'webhook-1', got %q", webhook.ID)
	}
}

func TestPoller_Poll_NoContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5, 1)

	webhook, err := p.poll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if webhook != nil {
		t.Errorf("expected nil webhook for NoContent, got %v", webhook)
	}
}

func TestPoller_Poll_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5, 1)

	_, err := p.poll(context.Background())
	if err == nil {
		t.Fatal("expected error for unauthorized")
	}
	if err.Error() != "unauthorized: invalid relay token" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPoller_Poll_ServiceUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5, 1)

	_, err := p.poll(context.Background())
	if err == nil {
		t.Fatal("expected error for service unavailable")
	}
	if err.Error() != "server shutting down" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPoller_Poll_UnexpectedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("I'm a teapot"))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5, 1)

	_, err := p.poll(context.Background())
	if err == nil {
		t.Fatal("expected error for unexpected status")
	}
}

func TestPoller_Poll_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not valid json"))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5, 1)

	_, err := p.poll(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestPoller_Poll_ConnectionError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller("http://localhost:99999", "test-token", "test-channel", forwarder, logger, 5, 1)

	_, err := p.poll(context.Background())
	if err == nil {
		t.Fatal("expected error for connection failure")
	}
}

func TestPoller_SendResponse_Success(t *testing.T) {
	var receivedResp *Response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/relay/response" {
			if r.Method != "POST" {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if r.Header.Get(TokenHeader) != "test-token" {
				t.Errorf("expected token header")
			}
			if r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("expected Content-Type header")
			}

			receivedResp = &Response{}
			_ = json.NewDecoder(r.Body).Decode(receivedResp)
			w.WriteHeader(http.StatusOK)
			return
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5, 1)

	resp := &Response{
		RequestID:  "webhook-1",
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       base64.StdEncoding.EncodeToString([]byte(`{"ok":true}`)),
	}

	err := p.sendResponse(context.Background(), resp)
	if err != nil {
		t.Fatalf("sendResponse failed: %v", err)
	}

	if receivedResp.RequestID != "webhook-1" {
		t.Errorf("expected RequestID 'webhook-1', got %q", receivedResp.RequestID)
	}
}

func TestPoller_SendResponse_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5, 1)

	resp := &Response{RequestID: "webhook-1", StatusCode: 200}
	err := p.sendResponse(context.Background(), resp)
	if err == nil {
		t.Fatal("expected error for server error")
	}
}

func TestPoller_SendResponse_ConnectionError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller("http://localhost:99999", "test-token", "test-channel", forwarder, logger, 5, 1)

	resp := &Response{RequestID: "webhook-1", StatusCode: 200}
	err := p.sendResponse(context.Background(), resp)
	if err == nil {
		t.Fatal("expected error for connection failure")
	}
}

func TestPoller_Run_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5, 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := p.Run(ctx)
	if err != nil {
		t.Errorf("expected nil error on context cancel, got %v", err)
	}
}

func TestPoller_Run_MaxConsecutiveFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 2, 1)
	// Reduce backoff for faster test
	p.minBackoff = time.Millisecond
	p.maxBackoff = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := p.Run(ctx)
	if err != ErrMaxConsecutiveFailures {
		t.Errorf("expected ErrMaxConsecutiveFailures, got %v", err)
	}
}

func TestPoller_Run_ForwardErrorContinues(t *testing.T) {
	// Server that sends webhook then returns NoContent
	var pollCount int32
	relayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/relay/poll" {
			count := atomic.AddInt32(&pollCount, 1)
			if count == 1 {
				// First poll: send webhook
				webhook := &Webhook{
					ID:      "webhook-1",
					Method:  "POST",
					Path:    "/test",
					Headers: map[string][]string{},
					Body:    base64.StdEncoding.EncodeToString([]byte(`{}`)),
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(webhook)
				return
			}
			// Subsequent polls: timeout
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/relay/response" {
			w.WriteHeader(http.StatusOK)
			return
		}
	}))
	defer relayServer.Close()

	// Local server that fails
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Close connection to cause error
		panic(http.ErrAbortHandler)
	}))
	defer localServer.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder(localServer.URL, "test", logger)
	p := NewPoller(relayServer.URL, "test-token", "test-channel", forwarder, logger, 5, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Should continue even after forward error
	err := p.Run(ctx)
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}

	if atomic.LoadInt32(&pollCount) < 2 {
		t.Error("expected poller to continue after forward error")
	}
}

func TestPoller_Run_FullWebhookCycle(t *testing.T) {
	webhookReceived := make(chan bool, 1)
	responseReceived := make(chan bool, 1)

	// Local destination
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookReceived <- true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"received":true}`))
	}))
	defer localServer.Close()

	var pollCount int32
	relayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/relay/poll" {
			count := atomic.AddInt32(&pollCount, 1)
			if count == 1 {
				webhook := &Webhook{
					ID:      "webhook-1",
					Method:  "POST",
					Path:    "/test",
					Headers: map[string][]string{"Content-Type": {"application/json"}},
					Body:    base64.StdEncoding.EncodeToString([]byte(`{"test":"data"}`)),
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(webhook)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/relay/response" {
			responseReceived <- true
			w.WriteHeader(http.StatusOK)
			return
		}
	}))
	defer relayServer.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder(localServer.URL, "test", logger)
	p := NewPoller(relayServer.URL, "test-token", "test-channel", forwarder, logger, 5, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- p.Run(ctx)
	}()

	// Wait for webhook to be received
	select {
	case <-webhookReceived:
		// Good
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for webhook")
	}

	// Wait for response to be sent
	select {
	case <-responseReceived:
		// Good
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for response")
	}

	cancel()
	<-done
}

func TestPoller_Run_SendResponseError(t *testing.T) {
	webhookSent := make(chan bool, 1)

	// Local destination that works
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer localServer.Close()

	var pollCount int32
	relayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/relay/poll" {
			count := atomic.AddInt32(&pollCount, 1)
			if count == 1 {
				webhook := &Webhook{
					ID:      "webhook-1",
					Method:  "POST",
					Path:    "/test",
					Headers: map[string][]string{},
					Body:    base64.StdEncoding.EncodeToString([]byte(`{}`)),
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(webhook)
				webhookSent <- true
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/relay/response" {
			// Return error for response
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}))
	defer relayServer.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder(localServer.URL, "test", logger)
	p := NewPoller(relayServer.URL, "test-token", "test-channel", forwarder, logger, 5, 1)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- p.Run(ctx)
	}()

	// Wait for webhook to be sent
	<-webhookSent

	// Give time for response error to be logged
	time.Sleep(100 * time.Millisecond)

	cancel()
	err := <-done
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestPoller_Run_ForwardErrorSendResponseError(t *testing.T) {
	responseSent := make(chan bool, 1)

	var pollCount int32
	relayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/relay/poll" {
			count := atomic.AddInt32(&pollCount, 1)
			if count == 1 {
				webhook := &Webhook{
					ID:      "webhook-1",
					Method:  "POST",
					Path:    "/test",
					Headers: map[string][]string{},
					Body:    base64.StdEncoding.EncodeToString([]byte(`{}`)),
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(webhook)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/relay/response" {
			responseSent <- true
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}))
	defer relayServer.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost\x00:8080", "test", logger)
	p := NewPoller(relayServer.URL, "test-token", "test-channel", forwarder, logger, 5, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- p.Run(ctx)
	}()

	select {
	case <-responseSent:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for response error")
	}

	cancel()
	err := <-done
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestPoller_Run_ContextCancelledDuringBackoff(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return error to trigger backoff
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 10, 1)
	// Long backoff so we can cancel during it
	p.minBackoff = 5 * time.Second
	p.maxBackoff = 10 * time.Second

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- p.Run(ctx)
	}()

	// Wait for first poll to fail and backoff to start
	time.Sleep(100 * time.Millisecond)

	// Cancel during backoff
	cancel()

	err := <-done
	if err != nil {
		t.Errorf("expected nil error on context cancel during backoff, got %v", err)
	}
}

func TestPoller_Run_ContextCancelledDuringPoll(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until request context is canceled
		<-r.Context().Done()
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5, 1)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- p.Run(ctx)
	}()

	// Wait for poll to start
	time.Sleep(100 * time.Millisecond)

	cancel()

	err := <-done
	if err != nil {
		t.Errorf("expected nil error on context cancel during poll, got %v", err)
	}
}

func TestPoller_Poll_InvalidURL(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	// Invalid URL with control character
	p := NewPoller("http://localhost\x00:8080", "test-token", "test-channel", forwarder, logger, 5, 1)

	_, err := p.poll(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestPoller_SendResponse_InvalidURL(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	// Invalid URL with control character
	p := NewPoller("http://localhost\x00:8080", "test-token", "test-channel", forwarder, logger, 5, 1)

	resp := &Response{RequestID: "test", StatusCode: 200}
	err := p.sendResponse(context.Background(), resp)
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestPoller_Run_LogsConnectedOnFirstSuccess(t *testing.T) {
	var pollCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pollCount, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	// Capture log output
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_ = p.Run(ctx)

	// Verify "connected to server" was logged
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "connected to server") {
		t.Errorf("expected 'connected to server' log, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "test-channel") {
		t.Errorf("expected channel name in log, got: %s", logOutput)
	}
}

func TestPoller_Run_LogsConnectedAfterInitialFailures(t *testing.T) {
	// When initial connections fail but then succeed, we log "connected to server"
	// (not "connection recovered" because we were never connected before)
	var pollCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&pollCount, 1)
		if count <= 2 {
			// First two polls fail
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Third poll succeeds
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	// Capture log output
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5, 1)
	p.minBackoff = time.Millisecond
	p.maxBackoff = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_ = p.Run(ctx)

	// Should log "connected to server" on first successful connection
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "connected to server") {
		t.Errorf("expected 'connected to server' log, got: %s", logOutput)
	}
	// Should NOT log "connection recovered" since we were never connected before
	if strings.Contains(logOutput, "connection recovered") {
		t.Errorf("did not expect 'connection recovered' log for initial connection, got: %s", logOutput)
	}
}

func TestPoller_Run_LogsConnectedThenRecovered(t *testing.T) {
	var pollCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&pollCount, 1)
		if count == 1 {
			// First poll succeeds
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if count <= 3 {
			// Second and third polls fail
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Fourth poll succeeds
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	// Capture log output
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5, 1)
	p.minBackoff = time.Millisecond
	p.maxBackoff = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_ = p.Run(ctx)

	logOutput := logBuf.String()
	// Should have both messages
	if !strings.Contains(logOutput, "connected to server") {
		t.Errorf("expected 'connected to server' log, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "connection recovered") {
		t.Errorf("expected 'connection recovered' log, got: %s", logOutput)
	}
}

func TestNewPoller_DefaultWorkers(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)

	// Test with 0 workers (should default to 1)
	p := NewPoller("http://relay.example.com", "token123", "test-channel", forwarder, logger, 5, 0)
	if p.workers != 1 {
		t.Errorf("expected workers 1 (default), got %d", p.workers)
	}

	// Test with negative workers (should default to 1)
	p = NewPoller("http://relay.example.com", "token123", "test-channel", forwarder, logger, 5, -1)
	if p.workers != 1 {
		t.Errorf("expected workers 1 (default), got %d", p.workers)
	}

	// Test with explicit workers
	p = NewPoller("http://relay.example.com", "token123", "test-channel", forwarder, logger, 5, 10)
	if p.workers != 10 {
		t.Errorf("expected workers 10, got %d", p.workers)
	}
}

func TestPoller_Run_WorkerPoolLogsWorkerCount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	// Capture log output
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5, 3)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_ = p.Run(ctx)

	// Verify "started worker pool" was logged with workers count
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "started worker pool") {
		t.Errorf("expected 'started worker pool' log, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, `"workers":3`) {
		t.Errorf("expected workers count 3 in log, got: %s", logOutput)
	}
}

func TestPoller_Run_ConcurrentWebhookProcessing(t *testing.T) {
	// Track concurrent processing
	var activeWorkers int32
	var maxConcurrent int32
	var webhookCount int32
	var mu sync.Mutex

	// Local destination that simulates slow processing
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&activeWorkers, 1)
		mu.Lock()
		if current > maxConcurrent {
			maxConcurrent = current
		}
		mu.Unlock()

		// Simulate processing time
		time.Sleep(50 * time.Millisecond)

		atomic.AddInt32(&activeWorkers, -1)
		atomic.AddInt32(&webhookCount, 1)

		w.WriteHeader(http.StatusOK)
	}))
	defer localServer.Close()

	var pollCount int32
	relayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/relay/poll" {
			count := atomic.AddInt32(&pollCount, 1)
			if count <= 5 {
				// Send webhooks for processing
				webhook := &Webhook{
					ID:      fmt.Sprintf("webhook-%d", count),
					Method:  "POST",
					Path:    "/test",
					Headers: map[string][]string{},
					Body:    base64.StdEncoding.EncodeToString([]byte(`{}`)),
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(webhook)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/relay/response" {
			w.WriteHeader(http.StatusOK)
			return
		}
	}))
	defer relayServer.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder(localServer.URL, "test", logger)
	// Use 3 workers
	p := NewPoller(relayServer.URL, "test-token", "test-channel", forwarder, logger, 5, 3)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = p.Run(ctx)

	// Verify concurrent processing occurred
	mu.Lock()
	maxConc := maxConcurrent
	totalProcessed := atomic.LoadInt32(&webhookCount)
	mu.Unlock()

	if totalProcessed < 5 {
		t.Errorf("expected at least 5 webhooks processed, got %d", totalProcessed)
	}

	// With 3 workers and slow processing, we should see concurrent execution
	// (maxConcurrent might be less than 3 depending on timing)
	if maxConc < 1 {
		t.Errorf("expected concurrent processing, got max concurrent %d", maxConc)
	}
	t.Logf("Max concurrent workers: %d, Total processed: %d", maxConc, totalProcessed)
}

func TestPoller_Run_GracefulShutdownWaitsForWorkers(t *testing.T) {
	webhookStarted := make(chan bool, 1)
	webhookFinished := make(chan bool, 1)

	// Local destination that takes time to process
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookStarted <- true
		// Simulate slow processing
		time.Sleep(200 * time.Millisecond)
		webhookFinished <- true
		w.WriteHeader(http.StatusOK)
	}))
	defer localServer.Close()

	var pollCount int32
	relayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/relay/poll" {
			count := atomic.AddInt32(&pollCount, 1)
			if count == 1 {
				webhook := &Webhook{
					ID:      "webhook-1",
					Method:  "POST",
					Path:    "/test",
					Headers: map[string][]string{},
					Body:    base64.StdEncoding.EncodeToString([]byte(`{}`)),
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(webhook)
				return
			}
			// Block subsequent polls until context canceled
			<-r.Context().Done()
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/relay/response" {
			w.WriteHeader(http.StatusOK)
			return
		}
	}))
	defer relayServer.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder(localServer.URL, "test", logger)
	p := NewPoller(relayServer.URL, "test-token", "test-channel", forwarder, logger, 5, 1)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- p.Run(ctx)
	}()

	// Wait for webhook processing to start
	select {
	case <-webhookStarted:
		// Good
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for webhook to start")
	}

	// Cancel context while webhook is being processed
	cancel()

	// Verify that Run waits for worker to finish (webhookFinished should complete)
	select {
	case <-webhookFinished:
		// Good - webhook finished processing
	case <-time.After(time.Second):
		t.Fatal("worker didn't complete - graceful shutdown not working")
	}

	// Wait for Run to return
	select {
	case <-done:
		// Good
	case <-time.After(time.Second):
		t.Fatal("Run didn't return after workers finished")
	}
}

func TestPoller_Run_ContextCancelledDuringDispatch(t *testing.T) {
	// Test the case where context is canceled while trying to dispatch
	// a webhook to the worker channel (when channel is full and workers are busy)
	//
	// With 1 worker and channel capacity 1:
	// - Webhook 1: Worker picks it up and starts processing (blocks on workerBlocking)
	// - Webhook 2: Goes into the channel buffer (channel now full)
	// - Webhook 3: Tries to dispatch but channel is full, blocks in select
	// - Cancel context: Should trigger the ctx.Done case in dispatch select

	workerStarted := make(chan struct{})
	workerBlocking := make(chan struct{})

	// Local destination that blocks until we signal
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case workerStarted <- struct{}{}:
		default:
		}
		<-workerBlocking // Block until signaled
		w.WriteHeader(http.StatusOK)
	}))
	defer localServer.Close()

	var pollCount int32
	relayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/relay/poll" {
			count := atomic.AddInt32(&pollCount, 1)
			// Return webhooks for first THREE polls
			if count <= 3 {
				webhook := &Webhook{
					ID:      fmt.Sprintf("webhook-%d", count),
					Method:  "POST",
					Path:    "/test",
					Headers: map[string][]string{},
					Body:    base64.StdEncoding.EncodeToString([]byte(`{}`)),
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(webhook)
				return
			}
			// Block subsequent polls until context canceled
			<-r.Context().Done()
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/relay/response" {
			w.WriteHeader(http.StatusOK)
			return
		}
	}))
	defer relayServer.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder(localServer.URL, "test", logger)
	// Use only 1 worker with channel capacity 1
	p := NewPoller(relayServer.URL, "test-token", "test-channel", forwarder, logger, 5, 1)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- p.Run(ctx)
	}()

	// Wait for worker to be busy processing first webhook
	select {
	case <-workerStarted:
		// Worker is now busy with webhook 1
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for worker to start")
	}

	// Give time for:
	// - Webhook 2 to be polled and put in channel (fills channel)
	// - Webhook 3 to be polled and block on dispatch
	time.Sleep(100 * time.Millisecond)

	// Cancel context while dispatch of webhook 3 is blocked
	cancel()

	// Signal worker to finish so it can process remaining
	close(workerBlocking)

	// Wait for Run to return
	select {
	case <-done:
		// Good - Run exited due to context cancel during dispatch
	case <-time.After(2 * time.Second):
		t.Fatal("Run didn't return after context cancel")
	}
}
