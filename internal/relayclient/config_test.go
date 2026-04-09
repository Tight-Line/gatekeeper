package relayclient

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{
			name:    "missing server",
			config:  Config{},
			wantErr: "server is required",
		},
		{
			name: "invalid server scheme",
			config: Config{
				Server: "ftp://example.com",
			},
			wantErr: "server must start with http:// or https://",
		},
		{
			name: "no channels",
			config: Config{
				Server: "https://example.com",
			},
			wantErr: "at least one channel is required",
		},
		{
			name: "channel missing name",
			config: Config{
				Server: "https://example.com",
				Channels: []ChannelConfig{
					{Token: "token1", Destination: "http://localhost:8080"},
				},
			},
			wantErr: "channel 0: name is required",
		},
		{
			name: "channel missing token",
			config: Config{
				Server: "https://example.com",
				Channels: []ChannelConfig{
					{Name: "test", Destination: "http://localhost:8080"},
				},
			},
			wantErr: "token is required",
		},
		{
			name: "channel missing destination",
			config: Config{
				Server: "https://example.com",
				Channels: []ChannelConfig{
					{Name: "test", Token: "token1"},
				},
			},
			wantErr: "destination is required",
		},
		{
			name: "channel invalid destination scheme",
			config: Config{
				Server: "https://example.com",
				Channels: []ChannelConfig{
					{Name: "test", Token: "token1", Destination: "ftp://localhost:8080"},
				},
			},
			wantErr: "destination must start with http:// or https://",
		},
		{
			name: "duplicate channel name",
			config: Config{
				Server: "https://example.com",
				Channels: []ChannelConfig{
					{Name: "test", Token: "token1", Destination: "http://localhost:8080"},
					{Name: "test", Token: "token2", Destination: "http://localhost:8081"},
				},
			},
			wantErr: "duplicate channel name",
		},
		{
			name: "duplicate token",
			config: Config{
				Server: "https://example.com",
				Channels: []ChannelConfig{
					{Name: "test1", Token: "same-token", Destination: "http://localhost:8080"},
					{Name: "test2", Token: "same-token", Destination: "http://localhost:8081"},
				},
			},
			wantErr: "duplicate token",
		},
		{
			name: "valid config",
			config: Config{
				Server: "https://example.com",
				Channels: []ChannelConfig{
					{Name: "slack", Token: "token1", Destination: "http://localhost:8080/slack"},
					{Name: "github", Token: "token2", Destination: "http://localhost:8080/github"},
				},
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.wantErr)
				} else if !contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
			}
		})
	}
}

func TestConfig_Load(t *testing.T) {
	// Create temp config file
	content := `
server: https://webhooks.example.com
channels:
  - name: slack
    token: test-token-1
    destination: http://localhost:8080/slack
  - name: github
    token: test-token-2
    destination: http://localhost:8080/github
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server != "https://webhooks.example.com" {
		t.Errorf("expected server 'https://webhooks.example.com', got %q", cfg.Server)
	}
	if len(cfg.Channels) != 2 {
		t.Errorf("expected 2 channels, got %d", len(cfg.Channels))
	}
	if cfg.Channels[0].Name != "slack" {
		t.Errorf("expected first channel name 'slack', got %q", cfg.Channels[0].Name)
	}
}

func TestConfig_Load_EnvExpansion(t *testing.T) {
	// Set environment variable
	os.Setenv("TEST_RELAY_TOKEN", "secret-token")
	defer os.Unsetenv("TEST_RELAY_TOKEN")

	content := `
server: https://webhooks.example.com
channels:
  - name: test
    token: ${TEST_RELAY_TOKEN}
    destination: http://localhost:8080
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Channels[0].Token != "secret-token" {
		t.Errorf("expected token 'secret-token', got %q", cfg.Channels[0].Token)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestConfig_Load_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestConfig_Load_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("invalid: yaml: content:"), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestConfig_Load_ValidationError(t *testing.T) {
	content := `
server: ftp://invalid-scheme
channels: []
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Error("expected validation error")
	}
}

func TestConfig_LoadFromEnv(t *testing.T) {
	// Test when env var is not set
	os.Unsetenv("GATEKEEPER_RELAY_CONFIG")
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Error("expected nil config when env var not set")
	}

	// Test with valid config
	validConfig := `
server: https://webhooks.example.com
channels:
  - name: test
    token: token1
    destination: http://localhost:8080
`
	os.Setenv("GATEKEEPER_RELAY_CONFIG", validConfig)
	defer os.Unsetenv("GATEKEEPER_RELAY_CONFIG")

	cfg, err = LoadFromEnv()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected config to be loaded")
	}
	if cfg.Server != "https://webhooks.example.com" {
		t.Errorf("expected server 'https://webhooks.example.com', got %q", cfg.Server)
	}
}

func TestConfig_LoadFromEnv_InvalidYAML(t *testing.T) {
	os.Setenv("GATEKEEPER_RELAY_CONFIG", "invalid: yaml: content:")
	defer os.Unsetenv("GATEKEEPER_RELAY_CONFIG")

	_, err := LoadFromEnv()
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestConfig_LoadAuto_FromEnvVar(t *testing.T) {
	validConfig := `
server: https://webhooks.example.com
channels:
  - name: test
    token: token1
    destination: http://localhost:8080
`
	os.Setenv("GATEKEEPER_RELAY_CONFIG", validConfig)
	defer os.Unsetenv("GATEKEEPER_RELAY_CONFIG")

	cfg, err := LoadAuto("/nonexistent/file.yaml")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected config to be loaded from env var")
	}
}

func TestConfig_LoadAuto_FromFile(t *testing.T) {
	os.Unsetenv("GATEKEEPER_RELAY_CONFIG")

	content := `
server: https://webhooks.example.com
channels:
  - name: test
    token: token1
    destination: http://localhost:8080
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadAuto(configPath)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected config to be loaded from file")
	}
}

func TestConfig_LoadAuto_EnvVarError(t *testing.T) {
	os.Setenv("GATEKEEPER_RELAY_CONFIG", "invalid: yaml: content:")
	defer os.Unsetenv("GATEKEEPER_RELAY_CONFIG")

	_, err := LoadAuto("/nonexistent/file.yaml")
	if err == nil {
		t.Error("expected error from invalid env var config")
	}
}

func TestChannelConfig_GetPreservePath(t *testing.T) {
	f := false
	tr := true
	tests := []struct {
		name     string
		channel  ChannelConfig
		expected bool
	}{
		{"nil (default)", ChannelConfig{}, true},
		{"explicit true", ChannelConfig{PreservePath: &tr}, true},
		{"explicit false", ChannelConfig{PreservePath: &f}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.channel.GetPreservePath(); got != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

func TestChannelConfig_GetPreservePath_YAMLRoundTrip(t *testing.T) {
	content := `
server: https://webhooks.example.com
channels:
  - name: test
    token: token1
    destination: http://localhost:8080
    preserve_path: false
`
	cfg, err := parse([]byte(content))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if got := cfg.Channels[0].GetPreservePath(); got != false {
		t.Errorf("expected GetPreservePath() == false after YAML parse, got %v", got)
	}
}

func TestChannelConfig_GetWorkers(t *testing.T) {
	tests := []struct {
		name    string
		workers int
		want    int
	}{
		{"default (0)", 0, 1},
		{"negative", -1, 1},
		{"one", 1, 1},
		{"multiple", 10, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := ChannelConfig{Workers: tt.workers}
			if got := ch.GetWorkers(); got != tt.want {
				t.Errorf("GetWorkers() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestConfig_GetMaxConsecutiveFailures(t *testing.T) {
	// Test default value
	cfg := &Config{
		Server:   "https://example.com",
		Channels: []ChannelConfig{{Name: "test", Token: "token", Destination: "http://localhost"}},
	}
	if cfg.GetMaxConsecutiveFailures() != DefaultMaxConsecutiveFailures {
		t.Errorf("expected default %d, got %d", DefaultMaxConsecutiveFailures, cfg.GetMaxConsecutiveFailures())
	}

	// Test config value
	cfg.MaxConsecutiveFailures = 5
	os.Unsetenv("GATEKEEPER_RELAY_MAX_FAILURES")
	if cfg.GetMaxConsecutiveFailures() != 5 {
		t.Errorf("expected 5, got %d", cfg.GetMaxConsecutiveFailures())
	}

	// Test env var takes precedence
	os.Setenv("GATEKEEPER_RELAY_MAX_FAILURES", "3")
	defer os.Unsetenv("GATEKEEPER_RELAY_MAX_FAILURES")
	if cfg.GetMaxConsecutiveFailures() != 3 {
		t.Errorf("expected 3 from env var, got %d", cfg.GetMaxConsecutiveFailures())
	}

	// Test invalid env var falls back to config
	os.Setenv("GATEKEEPER_RELAY_MAX_FAILURES", "invalid")
	if cfg.GetMaxConsecutiveFailures() != 5 {
		t.Errorf("expected 5 (config) with invalid env var, got %d", cfg.GetMaxConsecutiveFailures())
	}

	// Test negative env var falls back to config
	os.Setenv("GATEKEEPER_RELAY_MAX_FAILURES", "-1")
	if cfg.GetMaxConsecutiveFailures() != 5 {
		t.Errorf("expected 5 (config) with negative env var, got %d", cfg.GetMaxConsecutiveFailures())
	}
}
