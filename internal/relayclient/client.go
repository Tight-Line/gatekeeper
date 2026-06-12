package relayclient

import (
	"context"
	"log/slog"
	"sync"
)

// ClientOptions configures the relay client
type ClientOptions struct {
	// DebugPayloads enables logging of webhook payloads for debugging
	DebugPayloads bool
}

// Client manages relay connections for multiple channels
type Client struct {
	config  *Config
	logger  *slog.Logger
	pollers []*Poller
}

// NewClient creates a new relay client
func NewClient(cfg *Config, logger *slog.Logger, opts ClientOptions) *Client {
	c := &Client{
		config:  cfg,
		logger:  logger,
		pollers: make([]*Poller, 0, len(cfg.Channels)),
	}

	maxFailures := cfg.GetMaxConsecutiveFailures()

	// Create pollers for each channel
	for _, ch := range cfg.Channels {
		forwarder := NewForwarder(ch.Destination, ch.Name, logger, opts.DebugPayloads)
		forwarder.SetPreservePath(ch.GetPreservePath())
		poller := NewPoller(cfg.Server, ch.Token, ch.Name, forwarder, logger, PollerConfig{
			MaxConsecutiveFailures: maxFailures,
			Workers:                ch.GetWorkers(),
			DebugPayloads:          opts.DebugPayloads,
		})
		c.pollers = append(c.pollers, poller)
	}

	return c
}

// Run starts all channel pollers and blocks until context is canceled or any poller fails.
// Returns ErrMaxConsecutiveFailures if any poller exceeds its failure limit.
func (c *Client) Run(ctx context.Context) error {
	// Create a cancellable context so we can stop all pollers if one fails
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	errCh := make(chan error, len(c.pollers))

	for _, p := range c.pollers {
		wg.Add(1)
		go func(poller *Poller) {
			defer wg.Done()
			c.logger.Info("starting poller", "channel", poller.channelName)
			if err := poller.Run(ctx); err != nil {
				errCh <- err
				cancel() // Stop all other pollers
			}
		}(p)
	}

	c.logger.Info("relay client running", "channels", len(c.pollers))

	// Wait for all pollers to finish
	wg.Wait()
	close(errCh)

	// Check if any poller returned an error
	for err := range errCh {
		if err != nil {
			c.logger.Info("relay client stopped due to error")
			return err
		}
	}

	c.logger.Info("relay client stopped")
	return nil
}

// ChannelCount returns the number of configured channels
func (c *Client) ChannelCount() int {
	return len(c.pollers)
}
