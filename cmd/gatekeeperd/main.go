//go:build !ci
// +build !ci

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tight-line/gatekeeper/internal/config"
	"github.com/tight-line/gatekeeper/internal/ipfilter"
	"github.com/tight-line/gatekeeper/internal/metrics"
	"github.com/tight-line/gatekeeper/internal/proxy"
	"github.com/tight-line/gatekeeper/internal/relay"
	"github.com/tight-line/gatekeeper/internal/server"
)

var version = "0.1.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "./gatekeeperd.yaml", "Path to configuration file (ignored if GATEKEEPERD_CONFIG env var is set)")
	listenAddr := flag.String("listen", "", "Address to listen on (HTTP mode, no TLS)")
	enableTLS := flag.Bool("tls", false, "Enable ACME TLS (requires ports 80 and 443)")
	trustXFF := flag.Bool("trust-x-forwarded-for", false, "Trust X-Forwarded-For header for client IP (set when behind ingress/proxy)")
	flag.Parse()

	// Environment variable can also enable trust-x-forwarded-for
	if os.Getenv("GATEKEEPERD_TRUST_X_FORWARDED_FOR") == "true" {
		*trustXFF = true
	}

	// Setup structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("starting gatekeeperd", "version", version)

	// Load configuration from env var or file
	cfg, err := config.LoadAuto(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logConfigSource(logger, cfg, *configPath)

	// Setup signal handling for graceful shutdown
	ctx, cancel := setupSignalHandler(logger)
	defer cancel()

	// Build IP filters (needs ctx for background fetching)
	filters, fetcher, err := buildFilters(ctx, cfg, logger)
	if err != nil {
		return fmt.Errorf("building IP filters: %w", err)
	}
	defer fetcher.Stop()

	// Create proxy handler
	handler, err := proxy.NewHandler(cfg, filters, logger, proxy.HandlerOptions{
		TrustXForwardedFor: *trustXFF,
		MaxBodySize:        cfg.Global.MaxBodySize,
	})
	if err != nil {
		return fmt.Errorf("creating handler: %w", err)
	}

	if *trustXFF {
		logger.Info("trusting X-Forwarded-For header for client IP")
	}

	// Setup relay manager if any routes use relay tokens
	relayHandler, cleanup := setupRelayManager(cfg, handler, logger)
	if cleanup != nil {
		defer cleanup()
	}

	// Start metrics server
	startMetricsServer(cfg, logger)

	// Build main HTTP handler (with relay if enabled)
	mainHandler := buildMainHandler(handler, relayHandler)

	// Start server
	if *enableTLS {
		return runTLSServer(ctx, cfg, mainHandler, logger)
	}

	addr := *listenAddr
	if addr == "" {
		addr = ":8080"
	}
	return runHTTPServer(ctx, addr, mainHandler, logger)
}

// logConfigSource logs information about the loaded config
func logConfigSource(logger *slog.Logger, cfg *config.Config, configPath string) {
	if os.Getenv("GATEKEEPERD_CONFIG") != "" {
		logger.Info("config loaded from GATEKEEPERD_CONFIG env var",
			"routes", len(cfg.Routes),
			"verifiers", len(cfg.Verifiers),
			"ip_allowlists", len(cfg.IPAllowlists),
		)
	} else {
		logger.Info("config loaded from file",
			"path", configPath,
			"routes", len(cfg.Routes),
			"verifiers", len(cfg.Verifiers),
			"ip_allowlists", len(cfg.IPAllowlists),
		)
	}
}

// setupSignalHandler creates a context that is canceled on SIGINT or SIGTERM
func setupSignalHandler(logger *slog.Logger) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	return ctx, cancel
}

// setupRelayManager configures relay if any routes use relay tokens
// Returns the relay handler (or nil) and a cleanup function
func setupRelayManager(cfg *config.Config, handler *proxy.Handler, logger *slog.Logger) (relayHandler *relay.Handler, cleanup func()) {
	relayTokens := cfg.GetRelayTokens()
	if len(relayTokens) == 0 {
		return nil, nil
	}

	relayManager := relay.NewManager()
	for _, token := range relayTokens {
		relayManager.RegisterToken(token)
	}
	handler.SetRelayManager(relayManager)
	relayHandler = relay.NewHandler(relayManager, logger)
	cleanup = relayManager.Shutdown
	logger.Info("relay enabled", "tokens", len(relayTokens))

	return
}

// startMetricsServer starts the metrics server if configured
func startMetricsServer(cfg *config.Config, logger *slog.Logger) {
	if cfg.Global.MetricsPort <= 0 {
		return
	}

	metricsAddr := fmt.Sprintf(":%d", cfg.Global.MetricsPort)
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", metrics.Handler())
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		})
		logger.Info("starting metrics server", "addr", metricsAddr)
		if err := http.ListenAndServe(metricsAddr, mux); err != nil {
			logger.Error("metrics server error", "error", err)
		}
	}()
}

// buildMainHandler builds the main HTTP handler, combining proxy and relay if needed
func buildMainHandler(handler http.Handler, relayHandler *relay.Handler) http.Handler {
	if relayHandler == nil {
		return handler
	}

	mux := http.NewServeMux()
	// Specific relay protocol endpoints
	mux.Handle("/relay/poll", relayHandler)
	mux.Handle("/relay/response", relayHandler)
	// Everything else goes to proxy handler (including webhook routes)
	mux.Handle("/", handler)
	return mux
}

func buildFilters(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*ipfilter.FilterSet, *ipfilter.Fetcher, error) {
	filters := ipfilter.NewFilterSet()
	fetcher := ipfilter.NewFetcher(filters, logger)

	for name, al := range cfg.IPAllowlists {
		// Load static CIDRs
		if len(al.CIDRs) > 0 {
			filter, err := ipfilter.NewFilter(name, al.CIDRs)
			if err != nil {
				return nil, nil, fmt.Errorf("creating filter %q: %w", name, err)
			}
			filters.Add(name, filter)
			logger.Info("ip filter loaded", "name", name, "cidrs", filter.Count())
		}

		// Configure dynamic fetching
		if al.FetchURL != "" {
			fetcher.AddSource(&ipfilter.FetchSource{
				Name:            name,
				URL:             al.FetchURL,
				JQQuery:         al.FetchJQ,
				RefreshInterval: al.RefreshInterval,
			})
			logger.Info("dynamic IP fetching configured",
				"name", name,
				"url", al.FetchURL,
				"jq", al.FetchJQ,
				"refresh_interval", al.RefreshInterval,
			)
		}
	}

	// Start fetching in background
	fetcher.Start(ctx)

	return filters, fetcher, nil
}

func runTLSServer(ctx context.Context, cfg *config.Config, handler http.Handler, logger *slog.Logger) error {
	hostnames := cfg.GetHostnames()
	if len(hostnames) == 0 {
		return fmt.Errorf("no hostnames configured for TLS")
	}

	srv := server.New(server.Config{
		ACMEEmail:    cfg.Global.ACMEEmail,
		ACMECacheDir: cfg.Global.ACMECacheDir,
		Hostnames:    hostnames,
		Handler:      handler,
		Logger:       logger,
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func runHTTPServer(ctx context.Context, addr string, handler http.Handler, logger *slog.Logger) error {
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second, // Must be longer than relay poll timeout (30s)
		IdleTimeout:  120 * time.Second,
	}

	logger.Info("starting HTTP server", "addr", addr)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func init() {
	fmt.Fprintf(os.Stderr, "gatekeeperd %s\n", version)
}
