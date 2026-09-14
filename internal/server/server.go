package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// Config holds server configuration
type Config struct {
	// ACME settings
	ACMEEmail    string
	ACMECacheDir string
	Hostnames    []string

	// Listen addresses
	HTTPAddr  string // For ACME challenges and redirect (default :80)
	HTTPSAddr string // For TLS traffic (default :443)

	// Handler
	Handler http.Handler

	// Logger
	Logger *slog.Logger
}

// Server manages HTTP and HTTPS listeners
type Server struct {
	cfg         Config
	httpServer  *http.Server
	httpsServer *http.Server
	certManager *autocert.Manager
}

// New creates a new server with ACME TLS support
func New(cfg Config) *Server {
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":80"
	}
	if cfg.HTTPSAddr == "" {
		cfg.HTTPSAddr = ":443"
	}

	s := &Server{cfg: cfg}

	// Create autocert manager
	s.certManager = &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Email:      cfg.ACMEEmail,
		HostPolicy: autocert.HostWhitelist(cfg.Hostnames...),
		Cache:      autocert.DirCache(cfg.ACMECacheDir),
	}

	// HTTPS server with autocert TLS config
	// WriteTimeout must be longer than relay poll timeout (30s) to allow
	// responses to be written after long-poll completes
	s.httpsServer = &http.Server{
		Addr:         cfg.HTTPSAddr,
		Handler:      cfg.Handler,
		TLSConfig:    s.certManager.TLSConfig(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Prefer modern TLS
	s.httpsServer.TLSConfig.MinVersion = tls.VersionTLS12

	// HTTP server for ACME challenges only (no redirect - this is a webhook gateway)
	s.httpServer = &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      s.certManager.HTTPHandler(nil),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	return s
}

// Start begins listening on both HTTP and HTTPS
func (s *Server) Start() error {
	s.cfg.Logger.Info("starting HTTP server for ACME challenges",
		"addr", s.cfg.HTTPAddr,
	)
	s.cfg.Logger.Info("starting HTTPS server",
		"addr", s.cfg.HTTPSAddr,
		"hostnames", s.cfg.Hostnames,
		"acme_email", s.cfg.ACMEEmail,
	)

	// Start HTTP server in background (for ACME challenges)
	errCh := make(chan error, 2)
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	// Start HTTPS server (blocks)
	go func() {
		// TLS certs are managed by autocert, so we pass empty strings
		if err := s.httpsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("HTTPS server error: %w", err)
		}
	}()

	// Wait for an error from either server
	return <-errCh
}

// Shutdown gracefully stops both servers
func (s *Server) Shutdown(ctx context.Context) error {
	s.cfg.Logger.Info("shutting down servers")

	var errs []error
	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("http: %w", err))
		}
	}
	if s.httpsServer != nil {
		if err := s.httpsServer.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("https: %w", err))
		}
	}
	return errors.Join(errs...)
}
