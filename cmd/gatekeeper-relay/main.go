//go:build !ci
// +build !ci

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/tight-line/gatekeeper/internal/relayclient"
)

// version is set at link time via -ldflags "-X main.version=...".
// See Dockerfile.relay (ARG VERSION) and the release / pr-images workflows.
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "./gatekeeper-relay.yaml", "Path to configuration file (ignored if GATEKEEPER_RELAY_CONFIG env var is set)")
	debugPayloads := flag.Bool("debug-payloads", false, "Log webhook payloads to stdout for debugging")
	flag.Parse()

	// Environment variables override flags
	if os.Getenv("GATEKEEPER_RELAY_DEBUG_PAYLOADS") == "true" {
		*debugPayloads = true
	}

	// Setup structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("starting gatekeeper-relay", "version", version)

	// Load configuration from env var or file
	cfg, err := relayclient.LoadAuto(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Log config source and settings
	maxFailures := cfg.GetMaxConsecutiveFailures()
	if os.Getenv("GATEKEEPER_RELAY_CONFIG") != "" {
		logger.Info("config loaded from GATEKEEPER_RELAY_CONFIG env var",
			"server", cfg.Server,
			"channels", len(cfg.Channels),
			"max_consecutive_failures", maxFailures,
		)
	} else {
		logger.Info("config loaded from file",
			"path", *configPath,
			"server", cfg.Server,
			"channels", len(cfg.Channels),
			"max_consecutive_failures", maxFailures,
		)
	}

	// Setup signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	if *debugPayloads {
		logger.Info("debug payloads enabled - webhook bodies will be logged")
	}

	// Create and run client
	client := relayclient.NewClient(cfg, logger, relayclient.ClientOptions{
		DebugPayloads: *debugPayloads,
	})
	if err := client.Run(ctx); err != nil {
		return fmt.Errorf("relay client failed: %w", err)
	}

	logger.Info("gatekeeper-relay stopped")
	return nil
}

func init() {
	fmt.Fprintf(os.Stderr, "gatekeeper-relay %s\n", version)
}
