package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration structure
type Config struct {
	Global       GlobalConfig                 `yaml:"global"`
	IPAllowlists map[string]IPAllowlist       `yaml:"ip_allowlists"`
	Verifiers    map[string]VerifierConfig    `yaml:"verifiers"`
	Validators   map[string]ValidatorConfig   `yaml:"validators"`
	RateLimiters map[string]RateLimiterConfig `yaml:"rate_limiters"`
	Routes       []RouteConfig                `yaml:"routes"`
}

// GlobalConfig contains global settings
type GlobalConfig struct {
	ACMEEmail          string `yaml:"acme_email"`
	ACMECacheDir       string `yaml:"acme_cache_dir"`
	MetricsPort        int    `yaml:"metrics_port"`
	LogLevel           string `yaml:"log_level"`
	MaxBodySize        int64  `yaml:"max_body_size"`        // Maximum request body size in bytes (default: 10MB)
	DefaultRateLimiter string `yaml:"default_rate_limiter"` // Optional default rate limiter for all routes
}

// DefaultMaxBodySize is the default maximum request body size (10MB)
const DefaultMaxBodySize = 10 * 1024 * 1024

// IPAllowlist defines a named set of allowed IP ranges
type IPAllowlist struct {
	// Static CIDRs
	CIDRs []string `yaml:"cidrs,omitempty"`

	// Dynamic fetching
	FetchURL        string        `yaml:"fetch_url,omitempty"`
	FetchJQ         string        `yaml:"fetch_jq,omitempty"` // jq expression to extract CIDRs from JSON
	RefreshInterval time.Duration `yaml:"refresh_interval,omitempty"`
}

// RateLimiterConfig defines a named rate limiter
type RateLimiterConfig struct {
	TotalRPS        float64       `yaml:"total_rps"`        // Total requests per second across all IPs
	PerIPRPS        float64       `yaml:"per_ip_rps"`       // Requests per second per client IP (0 = disabled)
	Burst           int           `yaml:"burst"`            // Burst allowance for spike handling
	CleanupInterval time.Duration `yaml:"cleanup_interval"` // How often to scan for stale per-IP entries (default: 5m)
	IdleTimeout     time.Duration `yaml:"idle_timeout"`     // Remove per-IP limiter after idle time (default: 10m)
}

// VerifierConfig defines a webhook signature verifier
type VerifierConfig struct {
	Type string `yaml:"type"` // slack, github, shopify, api_key, hmac, json_field, query_param, header_query_param, noop

	// For slack verifier
	SigningSecret   string        `yaml:"signing_secret,omitempty"`
	MaxTimestampAge time.Duration `yaml:"max_timestamp_age,omitempty"`

	// For api_key verifier
	Header string `yaml:"header,omitempty"`
	Token  string `yaml:"token,omitempty"`

	// For github/shopify/hmac verifiers
	Secret string `yaml:"secret,omitempty"`

	// For generic hmac verifier
	Hash     string `yaml:"hash,omitempty"`     // SHA256, SHA512
	Encoding string `yaml:"encoding,omitempty"` // hex, base64

	// For json_field verifier
	Path string `yaml:"path,omitempty"` // dot-notation path to field (e.g., "value.0.clientState")

	// For query_param and header_query_param verifiers
	Name string `yaml:"name,omitempty"` // query parameter name or key name within header
}

// ValidatorConfig defines a payload structure validator
type ValidatorConfig struct {
	Type string `yaml:"type"` // json_schema

	// For json_schema validator
	SchemaFile string `yaml:"schema_file,omitempty"` // Path to JSON Schema file
	Schema     string `yaml:"schema,omitempty"`      // Inline JSON Schema content
}

// RouteConfig defines a proxy route
type RouteConfig struct {
	Hostname     string `yaml:"hostname"`
	Path         string `yaml:"path"`
	IPAllowlist  string `yaml:"ip_allowlist"`
	Verifier     string `yaml:"verifier"`
	Validator    string `yaml:"validator,omitempty"`     // Optional payload structure validator
	RateLimiter  string `yaml:"rate_limiter,omitempty"`  // Optional rate limiter (falls back to global default)
	Destination  string `yaml:"destination,omitempty"`   // Direct delivery URL
	RelayToken   string `yaml:"relay_token,omitempty"`   // Relay delivery token (mutually exclusive with destination)
	PreserveHost bool   `yaml:"preserve_host,omitempty"` // Pass original Host header to destination (default: false)
}

// Load reads and parses a config file, interpolating environment variables
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	return parse(data)
}

// LoadFromEnv loads configuration from the GATEKEEPERD_CONFIG environment variable.
// Returns nil if the env var is not set.
func LoadFromEnv() (*Config, error) {
	data := os.Getenv("GATEKEEPERD_CONFIG")
	if data == "" {
		return nil, nil
	}

	return parse([]byte(data))
}

// LoadAuto loads configuration from env var GATEKEEPERD_CONFIG if set,
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

// parse parses YAML config data with env var interpolation
func parse(data []byte) (*Config, error) {
	// Interpolate environment variables
	data = []byte(interpolateEnvVars(string(data)))

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

// envVarPattern matches ${VAR_NAME} patterns
var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// interpolateEnvVars replaces ${VAR_NAME} with the value of the environment variable
func interpolateEnvVars(input string) string {
	return envVarPattern.ReplaceAllStringFunc(input, func(match string) string {
		// Extract variable name from ${VAR_NAME}
		varName := match[2 : len(match)-1]
		if value, exists := os.LookupEnv(varName); exists {
			return value
		}
		// Return empty string if env var not set
		return ""
	})
}

// Validate checks that the configuration is valid
func (c *Config) Validate() error {
	if err := c.validateRoutes(); err != nil {
		return err
	}
	if err := c.validateVerifiers(); err != nil {
		return err
	}
	if err := c.validateIPAllowlists(); err != nil {
		return err
	}
	if err := c.validateValidators(); err != nil {
		return err
	}
	if err := c.validateRateLimiters(); err != nil {
		return err
	}
	return nil
}

// validateRoutes checks that all routes are properly configured
func (c *Config) validateRoutes() error {
	for i, route := range c.Routes {
		if err := c.validateRoute(i, route); err != nil {
			return err
		}
	}
	return nil
}

// validateRoute validates a single route configuration
func (c *Config) validateRoute(i int, route RouteConfig) error {
	if route.Hostname == "" {
		return fmt.Errorf("route %d: hostname is required", i)
	}
	if route.Path == "" {
		return fmt.Errorf("route %d: path is required", i)
	}
	if route.Destination == "" && route.RelayToken == "" {
		return fmt.Errorf("route %d: either destination or relay_token is required", i)
	}
	if route.Destination != "" && route.RelayToken != "" {
		return fmt.Errorf("route %d: destination and relay_token are mutually exclusive", i)
	}
	if route.IPAllowlist != "" {
		if _, ok := c.IPAllowlists[route.IPAllowlist]; !ok {
			return fmt.Errorf("route %d: ip_allowlist %q not found", i, route.IPAllowlist)
		}
	}
	if route.Verifier != "" {
		if _, ok := c.Verifiers[route.Verifier]; !ok {
			return fmt.Errorf("route %d: verifier %q not found", i, route.Verifier)
		}
	}
	if route.Validator != "" {
		if _, ok := c.Validators[route.Validator]; !ok {
			return fmt.Errorf("route %d: validator %q not found", i, route.Validator)
		}
	}
	if route.RateLimiter != "" {
		if _, ok := c.RateLimiters[route.RateLimiter]; !ok {
			return fmt.Errorf("route %d: rate_limiter %q not found", i, route.RateLimiter)
		}
	}
	return nil
}

// validateVerifiers checks that all verifier configs are valid
func (c *Config) validateVerifiers() error {
	for name, v := range c.Verifiers {
		if err := validateVerifier(name, v); err != nil {
			return err
		}
	}
	return nil
}

// validateVerifier validates a single verifier configuration
func validateVerifier(name string, v VerifierConfig) error {
	switch v.Type {
	case "slack":
		if v.SigningSecret == "" {
			return fmt.Errorf("verifier %q: signing_secret is required for slack verifier", name)
		}
	case "github", "shopify":
		if v.Secret == "" {
			return fmt.Errorf("verifier %q: secret is required for %s verifier", name, v.Type)
		}
	case "api_key":
		return validateAPIKeyVerifier(name, v)
	case "hmac":
		return validateHMACVerifier(name, v)
	case "json_field":
		return validateJSONFieldVerifier(name, v)
	case "query_param":
		return validateQueryParamVerifier(name, v)
	case "header_query_param":
		return validateHeaderQueryParamVerifier(name, v)
	case "noop":
		// No validation needed
	case "":
		return fmt.Errorf("verifier %q: type is required", name)
	default:
		return fmt.Errorf("verifier %q: unknown type %q", name, v.Type)
	}
	return nil
}

// validateAPIKeyVerifier validates api_key verifier config
func validateAPIKeyVerifier(name string, v VerifierConfig) error {
	if v.Header == "" {
		return fmt.Errorf("verifier %q: header is required for api_key verifier", name)
	}
	if v.Token == "" {
		return fmt.Errorf("verifier %q: token is required for api_key verifier", name)
	}
	return nil
}

// validateHMACVerifier validates hmac verifier config
func validateHMACVerifier(name string, v VerifierConfig) error {
	if v.Secret == "" {
		return fmt.Errorf("verifier %q: secret is required for hmac verifier", name)
	}
	if v.Header == "" {
		return fmt.Errorf("verifier %q: header is required for hmac verifier", name)
	}
	if v.Hash == "" {
		return fmt.Errorf("verifier %q: hash is required for hmac verifier (SHA256 or SHA512)", name)
	}
	if v.Encoding == "" {
		return fmt.Errorf("verifier %q: encoding is required for hmac verifier (hex or base64)", name)
	}
	return nil
}

// validateJSONFieldVerifier validates json_field verifier config
func validateJSONFieldVerifier(name string, v VerifierConfig) error {
	if v.Path == "" {
		return fmt.Errorf("verifier %q: path is required for json_field verifier", name)
	}
	if v.Token == "" {
		return fmt.Errorf("verifier %q: token is required for json_field verifier", name)
	}
	return nil
}

// validateQueryParamVerifier validates query_param verifier config
func validateQueryParamVerifier(name string, v VerifierConfig) error {
	if v.Name == "" {
		return fmt.Errorf("verifier %q: name is required for query_param verifier", name)
	}
	if v.Token == "" {
		return fmt.Errorf("verifier %q: token is required for query_param verifier", name)
	}
	return nil
}

// validateHeaderQueryParamVerifier validates header_query_param verifier config
func validateHeaderQueryParamVerifier(name string, v VerifierConfig) error {
	if v.Header == "" {
		return fmt.Errorf("verifier %q: header is required for header_query_param verifier", name)
	}
	if v.Name == "" {
		return fmt.Errorf("verifier %q: name is required for header_query_param verifier", name)
	}
	if v.Token == "" {
		return fmt.Errorf("verifier %q: token is required for header_query_param verifier", name)
	}
	return nil
}

// validateIPAllowlists checks that all IP allowlist configs are valid
func (c *Config) validateIPAllowlists() error {
	for name, al := range c.IPAllowlists {
		if len(al.CIDRs) == 0 && al.FetchURL == "" {
			return fmt.Errorf("ip_allowlist %q: must have either cidrs or fetch_url", name)
		}
		if al.FetchURL != "" && al.FetchJQ == "" {
			return fmt.Errorf("ip_allowlist %q: fetch_jq is required when fetch_url is set", name)
		}
	}
	return nil
}

// validateValidators checks that all validator configs are valid
func (c *Config) validateValidators() error {
	for name, v := range c.Validators {
		if err := validateValidator(name, v); err != nil {
			return err
		}
	}
	return nil
}

// validateValidator validates a single validator configuration
func validateValidator(name string, v ValidatorConfig) error {
	switch v.Type {
	case "json_schema":
		if v.SchemaFile == "" && v.Schema == "" {
			return fmt.Errorf("validator %q: either schema_file or schema is required for json_schema validator", name)
		}
	case "":
		return fmt.Errorf("validator %q: type is required", name)
	default:
		return fmt.Errorf("validator %q: unknown type %q", name, v.Type)
	}
	return nil
}

// validateRateLimiters checks that all rate limiter configs are valid
func (c *Config) validateRateLimiters() error {
	// Validate global default rate limiter reference
	if c.Global.DefaultRateLimiter != "" {
		if _, ok := c.RateLimiters[c.Global.DefaultRateLimiter]; !ok {
			return fmt.Errorf("global: default_rate_limiter %q not found", c.Global.DefaultRateLimiter)
		}
	}

	// Validate each rate limiter config
	for name, rl := range c.RateLimiters {
		if err := validateRateLimiter(name, rl); err != nil {
			return err
		}
	}
	return nil
}

// validateRateLimiter validates a single rate limiter configuration
func validateRateLimiter(name string, rl RateLimiterConfig) error {
	if rl.TotalRPS <= 0 {
		return fmt.Errorf("rate_limiter %q: total_rps must be positive", name)
	}
	if rl.PerIPRPS < 0 {
		return fmt.Errorf("rate_limiter %q: per_ip_rps cannot be negative", name)
	}
	if rl.Burst <= 0 {
		return fmt.Errorf("rate_limiter %q: burst must be positive", name)
	}
	return nil
}

// GetHostnames returns all unique hostnames configured in routes
func (c *Config) GetHostnames() []string {
	seen := make(map[string]bool)
	var hostnames []string
	for _, route := range c.Routes {
		if !seen[route.Hostname] {
			seen[route.Hostname] = true
			hostnames = append(hostnames, route.Hostname)
		}
	}
	return hostnames
}

// GetRelayTokens returns all unique relay tokens configured in routes
func (c *Config) GetRelayTokens() []string {
	seen := make(map[string]bool)
	var tokens []string
	for _, route := range c.Routes {
		if route.RelayToken != "" && !seen[route.RelayToken] {
			seen[route.RelayToken] = true
			tokens = append(tokens, route.RelayToken)
		}
	}
	return tokens
}
