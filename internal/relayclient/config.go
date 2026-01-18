package relayclient

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// DefaultMaxConsecutiveFailures is the default number of consecutive failures before exiting
	DefaultMaxConsecutiveFailures = 10
)

// Config represents the relay client configuration
type Config struct {
	Server                 string          `yaml:"server"`
	Channels               []ChannelConfig `yaml:"channels"`
	MaxConsecutiveFailures int             `yaml:"max_consecutive_failures"` // 0 means use default
}

// ChannelConfig represents a single relay channel configuration
type ChannelConfig struct {
	Name        string `yaml:"name"`
	Token       string `yaml:"token"`
	Destination string `yaml:"destination"`
}

// Load reads and parses the configuration file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	return parse(data)
}

// LoadFromEnv loads configuration from the GATEKEEPER_RELAY_CONFIG environment variable.
// Returns nil if the env var is not set.
func LoadFromEnv() (*Config, error) {
	data := os.Getenv("GATEKEEPER_RELAY_CONFIG")
	if data == "" {
		return nil, nil
	}

	return parse([]byte(data))
}

// LoadAuto loads configuration from env var GATEKEEPER_RELAY_CONFIG if set,
// otherwise falls back to the specified file path.
func LoadAuto(defaultPath string) (*Config, error) {
	cfg, err := LoadFromEnv()
	if err != nil {
		return nil, err
	}
	if cfg != nil {
		return cfg, nil
	}
	return Load(defaultPath)
}

// parse parses YAML config data with env var expansion
func parse(data []byte) (*Config, error) {
	// Expand environment variables
	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// GetMaxConsecutiveFailures returns the max consecutive failures setting.
// Priority: GATEKEEPER_RELAY_MAX_FAILURES env var > config value > default (10)
func (c *Config) GetMaxConsecutiveFailures() int {
	// Check env var first
	if envVal := os.Getenv("GATEKEEPER_RELAY_MAX_FAILURES"); envVal != "" {
		if n, err := strconv.Atoi(envVal); err == nil && n > 0 {
			return n
		}
	}

	// Use config value if set
	if c.MaxConsecutiveFailures > 0 {
		return c.MaxConsecutiveFailures
	}

	// Fall back to default
	return DefaultMaxConsecutiveFailures
}

// Validate checks the configuration for errors
func (c *Config) Validate() error {
	if c.Server == "" {
		return fmt.Errorf("server is required")
	}

	if !strings.HasPrefix(c.Server, "http://") && !strings.HasPrefix(c.Server, "https://") {
		return fmt.Errorf("server must start with http:// or https://")
	}

	if len(c.Channels) == 0 {
		return fmt.Errorf("at least one channel is required")
	}

	names := make(map[string]bool)
	tokens := make(map[string]bool)

	for i, ch := range c.Channels {
		if ch.Name == "" {
			return fmt.Errorf("channel %d: name is required", i)
		}
		if ch.Token == "" {
			return fmt.Errorf("channel %q: token is required", ch.Name)
		}
		if ch.Destination == "" {
			return fmt.Errorf("channel %q: destination is required", ch.Name)
		}

		if !strings.HasPrefix(ch.Destination, "http://") && !strings.HasPrefix(ch.Destination, "https://") {
			return fmt.Errorf("channel %q: destination must start with http:// or https://", ch.Name)
		}

		if names[ch.Name] {
			return fmt.Errorf("duplicate channel name: %q", ch.Name)
		}
		names[ch.Name] = true

		if tokens[ch.Token] {
			return fmt.Errorf("duplicate token in channel %q", ch.Name)
		}
		tokens[ch.Token] = true
	}

	return nil
}
