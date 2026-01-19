package relayclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewPoller(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller("http://relay.example.com", "token123", "test-channel", forwarder, logger, 5)

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
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5)

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
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5)

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
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5)

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
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5)

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
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5)

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
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5)

	_, err := p.poll(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestPoller_Poll_ConnectionError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller("http://localhost:99999", "test-token", "test-channel", forwarder, logger, 5)

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
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5)

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
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5)

	resp := &Response{RequestID: "webhook-1", StatusCode: 200}
	err := p.sendResponse(context.Background(), resp)
	if err == nil {
		t.Fatal("expected error for server error")
	}
}

func TestPoller_SendResponse_ConnectionError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	p := NewPoller("http://localhost:99999", "test-token", "test-channel", forwarder, logger, 5)

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
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5)

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
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 2)
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
	p := NewPoller(relayServer.URL, "test-token", "test-channel", forwarder, logger, 5)

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
	p := NewPoller(relayServer.URL, "test-token", "test-channel", forwarder, logger, 5)

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
	p := NewPoller(relayServer.URL, "test-token", "test-channel", forwarder, logger, 5)

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
	p := NewPoller(relayServer.URL, "test-token", "test-channel", forwarder, logger, 5)

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
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 10)
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
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5)

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
	p := NewPoller("http://localhost\x00:8080", "test-token", "test-channel", forwarder, logger, 5)

	_, err := p.poll(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestPoller_SendResponse_InvalidURL(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	forwarder := NewForwarder("http://localhost:8080", "test", logger)
	// Invalid URL with control character
	p := NewPoller("http://localhost\x00:8080", "test-token", "test-channel", forwarder, logger, 5)

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
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5)

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
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5)
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
	p := NewPoller(server.URL, "test-token", "test-channel", forwarder, logger, 5)
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
