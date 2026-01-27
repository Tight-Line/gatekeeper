package relayclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	cfg := &Config{
		Server: "https://relay.example.com",
		Channels: []ChannelConfig{
			{Name: "slack", Token: "token1", Destination: "http://localhost:8080/slack"},
			{Name: "github", Token: "token2", Destination: "http://localhost:8080/github"},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := NewClient(cfg, logger, ClientOptions{})

	if client.ChannelCount() != 2 {
		t.Errorf("expected 2 channels, got %d", client.ChannelCount())
	}
}

func TestClient_Run_ContextCancelled(t *testing.T) {
	// Create a relay server that returns timeouts
	relayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/relay/poll" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}))
	defer relayServer.Close()

	cfg := &Config{
		Server: relayServer.URL,
		Channels: []ChannelConfig{
			{Name: "test", Token: "token1", Destination: "http://localhost:8080"},
		},
		MaxConsecutiveFailures: 3,
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := NewClient(cfg, logger, ClientOptions{})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := client.Run(ctx)
	if err != nil {
		t.Errorf("expected nil error on graceful shutdown, got %v", err)
	}
}

func TestClient_Run_MaxConsecutiveFailures(t *testing.T) {
	// Create a relay server that always returns errors
	relayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer relayServer.Close()

	cfg := &Config{
		Server: relayServer.URL,
		Channels: []ChannelConfig{
			{Name: "test", Token: "token1", Destination: "http://localhost:8080"},
		},
		MaxConsecutiveFailures: 2,
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := NewClient(cfg, logger, ClientOptions{})

	// Give it enough time to fail multiple times
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.Run(ctx)
	if err != ErrMaxConsecutiveFailures {
		t.Errorf("expected ErrMaxConsecutiveFailures, got %v", err)
	}
}

func TestClient_Run_WebhookDelivery(t *testing.T) {
	// Create local destination that receives webhooks
	webhookReceived := make(chan *http.Request, 1)
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookReceived <- r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer localServer.Close()

	// Create relay server that sends a webhook, then returns timeout
	var callCount int32
	relayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/relay/poll" {
			count := atomic.AddInt32(&callCount, 1)
			if count == 1 {
				// First call: send a webhook
				w.Header().Set("Content-Type", "application/json")
				webhook := map[string]interface{}{
					"id":      "test-webhook-id",
					"method":  "POST",
					"path":    "/webhook/test",
					"headers": map[string][]string{"Content-Type": {"application/json"}},
					"body":    base64.StdEncoding.EncodeToString([]byte(`{"test":"data"}`)),
				}
				_ = json.NewEncoder(w).Encode(webhook)
				return
			}
			// Subsequent calls: return timeout
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/relay/response" {
			// Accept the response
			w.WriteHeader(http.StatusOK)
			return
		}
	}))
	defer relayServer.Close()

	cfg := &Config{
		Server: relayServer.URL,
		Channels: []ChannelConfig{
			{Name: "test", Token: "token1", Destination: localServer.URL},
		},
		MaxConsecutiveFailures: 5,
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := NewClient(cfg, logger, ClientOptions{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Run client in background
	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx)
	}()

	// Wait for webhook to be received
	select {
	case req := <-webhookReceived:
		if req.Method != "POST" {
			t.Errorf("expected POST, got %s", req.Method)
		}
		if req.Header.Get("X-Relay-Webhook-ID") != "test-webhook-id" {
			t.Errorf("expected webhook ID header")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for webhook")
	}

	// Cancel and wait for client to stop
	cancel()
	<-done
}
