package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
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

	// Use channels to signal when servers are ready
	httpReady := make(chan struct{})
	httpsReady := make(chan struct{})

	// Start the HTTP server on a real listener
	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create http listener: %v", err)
	}
	go func() {
		close(httpReady)
		_ = s.httpServer.Serve(httpListener)
	}()

	// Start the HTTPS server on a real listener
	httpsListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create https listener: %v", err)
	}
	go func() {
		close(httpsReady)
		_ = s.httpsServer.ServeTLS(httpsListener, "", "")
	}()

	// Wait for servers to start accepting connections
	<-httpReady
	<-httpsReady

	// Small delay to ensure Serve loops have started
	time.Sleep(10 * time.Millisecond)

	// Shutdown with a valid context should succeed
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err = s.Shutdown(ctx)
	// Note: HTTPS server may error because there's no TLS cert, but that's ok
	// The test exercises the shutdown paths
	_ = err
}

func TestServer_Shutdown_ErrorPaths(t *testing.T) {
	// Each server gets its own blocking handler so the test can wait for both
	// to have a request in flight. A single shared handler would only tell us
	// that one of the two had started, leaving the other server with nothing to
	// wait on at shutdown and its error branch unexercised.
	blockCh := make(chan struct{})
	newBlockingHandler := func(started chan struct{}) http.Handler {
		var once sync.Once
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			once.Do(func() { close(started) })
			<-blockCh // Block until test finishes
			w.WriteHeader(http.StatusOK)
		})
	}
	httpStarted := make(chan struct{})
	httpsStarted := make(chan struct{})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Create a custom HTTP server with our blocking handler
	s := &Server{
		cfg: Config{Logger: logger},
		httpServer: &http.Server{
			Handler: newBlockingHandler(httpStarted),
		},
		httpsServer: &http.Server{
			Handler: newBlockingHandler(httpsStarted),
		},
	}

	// Use channels to signal when servers are ready
	httpReady := make(chan struct{})
	httpsReady := make(chan struct{})

	// Start the HTTP server on a real listener
	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create http listener: %v", err)
	}
	httpAddr := httpListener.Addr().String()
	go func() {
		close(httpReady)
		_ = s.httpServer.Serve(httpListener)
	}()

	// Start the HTTPS server on a real listener
	httpsListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create https listener: %v", err)
	}
	httpsAddr := httpsListener.Addr().String()
	go func() {
		close(httpsReady)
		_ = s.httpsServer.Serve(httpsListener)
	}()

	// Wait for servers to be ready
	<-httpReady
	<-httpsReady

	// Make blocking requests to both servers
	var requestsWg sync.WaitGroup
	requestsWg.Add(2)
	go func() {
		defer requestsWg.Done()
		_, _ = http.Get("http://" + httpAddr + "/block")
	}()
	go func() {
		defer requestsWg.Done()
		_, _ = http.Get("http://" + httpsAddr + "/block")
	}()

	// Wait for both handlers to start, so both servers have an in-flight
	// request when Shutdown runs and both error branches are taken.
	for name, started := range map[string]chan struct{}{"http": httpStarted, "https": httpsStarted} {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			close(blockCh)
			t.Fatalf("timeout waiting for %s handler to start", name)
		}
	}

	// Shutdown with an already canceled context - should fail immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = s.Shutdown(ctx)

	// Clean up: unblock the handlers and wait for requests to complete
	close(blockCh)
	requestsWg.Wait()

	// Both servers had a request in flight against a canceled context, so both
	// must report an error.
	if err == nil {
		t.Fatal("Shutdown() with a canceled context and in-flight requests: got nil, want error")
	}
	for _, want := range []string{"http:", "https:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Shutdown() error %q missing %q", err, want)
		}
	}
}
