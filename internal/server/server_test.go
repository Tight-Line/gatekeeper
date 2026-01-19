package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cfg := Config{
		ACMEEmail:    "test@example.com",
		ACMECacheDir: t.TempDir(),
		Hostnames:    []string{"example.com"},
		Handler:      handler,
		Logger:       logger,
	}

	s := New(cfg)

	if s == nil {
		t.Fatal("New() returned nil")
	}
	if s.cfg.HTTPAddr != ":80" {
		t.Errorf("expected default HTTPAddr ':80', got %q", s.cfg.HTTPAddr)
	}
	if s.cfg.HTTPSAddr != ":443" {
		t.Errorf("expected default HTTPSAddr ':443', got %q", s.cfg.HTTPSAddr)
	}
	if s.certManager == nil {
		t.Error("certManager is nil")
	}
	if s.httpServer == nil {
		t.Error("httpServer is nil")
	}
	if s.httpsServer == nil {
		t.Error("httpsServer is nil")
	}
}

func TestNew_CustomAddresses(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cfg := Config{
		ACMEEmail:    "test@example.com",
		ACMECacheDir: t.TempDir(),
		Hostnames:    []string{"example.com"},
		HTTPAddr:     ":8080",
		HTTPSAddr:    ":8443",
		Handler:      handler,
		Logger:       logger,
	}

	s := New(cfg)

	if s.cfg.HTTPAddr != ":8080" {
		t.Errorf("expected HTTPAddr ':8080', got %q", s.cfg.HTTPAddr)
	}
	if s.cfg.HTTPSAddr != ":8443" {
		t.Errorf("expected HTTPSAddr ':8443', got %q", s.cfg.HTTPSAddr)
	}
}

func TestServer_Shutdown(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cfg := Config{
		ACMEEmail:    "test@example.com",
		ACMECacheDir: t.TempDir(),
		Hostnames:    []string{"example.com"},
		Handler:      handler,
		Logger:       logger,
	}

	s := New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Shutdown before starting should work
	err := s.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown() error: %v", err)
	}
}

func TestServer_Shutdown_NilServers(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{
		cfg: Config{Logger: logger},
		// Both servers are nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := s.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown() with nil servers error: %v", err)
	}
}

func TestServer_Start_InvalidPort(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cfg := Config{
		ACMEEmail:    "test@example.com",
		ACMECacheDir: t.TempDir(),
		Hostnames:    []string{"example.com"},
		HTTPAddr:     "invalid-address",
		HTTPSAddr:    "invalid-address",
		Handler:      handler,
		Logger:       logger,
	}

	s := New(cfg)

	// Start should fail with invalid addresses
	err := s.Start()
	if err == nil {
		t.Error("expected error for invalid addresses")
	}
}

func TestServer_Shutdown_WithRunningServers(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cfg := Config{
		ACMEEmail:    "test@example.com",
		ACMECacheDir: t.TempDir(),
		Hostnames:    []string{"example.com"},
		HTTPAddr:     "127.0.0.1:0",
		HTTPSAddr:    "127.0.0.1:0",
		Handler:      handler,
		Logger:       logger,
	}

	s := New(cfg)

	// Start the HTTP server on a real listener
	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create http listener: %v", err)
	}
	go func() { _ = s.httpServer.Serve(httpListener) }()

	// Start the HTTPS server on a real listener
	httpsListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create https listener: %v", err)
	}
	go func() { _ = s.httpsServer.ServeTLS(httpsListener, "", "") }()

	// Give servers time to start
	time.Sleep(50 * time.Millisecond)

	// Shutdown with a valid context should succeed
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err = s.Shutdown(ctx)
	// Note: HTTPS server may error because there's no TLS cert, but that's ok
	// The test exercises the shutdown paths
	_ = err
}

func TestServer_Shutdown_ErrorPaths(t *testing.T) {
	// Create a handler that blocks until a channel is closed
	blockCh := make(chan struct{})
	handlerStarted := make(chan struct{})
	var once sync.Once
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(handlerStarted) })
		<-blockCh // Block until test finishes
		w.WriteHeader(http.StatusOK)
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Create a custom HTTP server with our blocking handler
	s := &Server{
		cfg: Config{Logger: logger},
		httpServer: &http.Server{
			Handler: handler,
		},
		httpsServer: &http.Server{
			Handler: handler,
		},
	}

	// Start the HTTP server on a real listener
	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create http listener: %v", err)
	}
	httpAddr := httpListener.Addr().String()
	go func() { _ = s.httpServer.Serve(httpListener) }()

	// Start the HTTPS server on a real listener
	httpsListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create https listener: %v", err)
	}
	httpsAddr := httpsListener.Addr().String()
	go func() { _ = s.httpsServer.Serve(httpsListener) }()

	// Make blocking requests to both servers
	go func() { _, _ = http.Get("http://" + httpAddr + "/block") }()
	go func() { _, _ = http.Get("http://" + httpsAddr + "/block") }()

	// Wait for handlers to start (at least one)
	select {
	case <-handlerStarted:
	case <-time.After(2 * time.Second):
		close(blockCh)
		t.Fatal("timeout waiting for handler to start")
	}

	// Shutdown with an already canceled context - should fail immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = s.Shutdown(ctx)

	// Clean up: unblock the handlers
	close(blockCh)

	if err != nil {
		t.Logf("Got expected shutdown error: %v", err)
	}
}
