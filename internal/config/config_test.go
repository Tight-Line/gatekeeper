package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Test string constants used throughout config_test.go.
// Defined once to satisfy SonarCloud S1192 (no duplicate string literals).
const (
	testExampleHost     = "test.example.com"
	testWebhookPath     = "/webhook"
	testBackendURL      = "http://backend:8080"
	errFmtUnexpected    = "unexpected error: %v"
	testSecret          = "my-secret"
	testGoogTokenHeader = "X-Goog-Channel-Token"
	testHostA           = "a.example.com"
	testURLa            = "http://a"
	testHostB           = "b.example.com"
	testHostC           = "c.example.com"
	testNonexistentFile = "/nonexistent/file.yaml"
)

func TestLoad(t *testing.T) {
	// Set up test env vars
	os.Setenv("TEST_SECRET", "my-secret-value")
	defer os.Unsetenv("TEST_SECRET")

	configContent := `
global:
  acme_email: "test@example.com"
  acme_cache_dir: "/tmp/certs"
  metrics_port: 9090
  log_level: info

ip_allowlists:
  test-ips:
    cidrs:
      - "10.0.0.0/8"

verifiers:
  test-verifier:
    type: slack
    signing_secret: "${TEST_SECRET}"

routes:
  - hostname: test.example.com
    path: /webhook
    ip_allowlist: test-ips
    verifier: test-verifier
    destination: http://backend:8080
`

	// Write temp config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Check global settings
	if cfg.Global.ACMEEmail != "test@example.com" {
		t.Errorf("expected acme_email=test@example.com, got %s", cfg.Global.ACMEEmail)
	}
	if cfg.Global.MetricsPort != 9090 {
		t.Errorf("expected metrics_port=9090, got %d", cfg.Global.MetricsPort)
	}

	// Check env var interpolation
	verifier, ok := cfg.Verifiers["test-verifier"]
	if !ok {
		t.Fatal("test-verifier not found")
	}
	if verifier.SigningSecret != "my-secret-value" {
		t.Errorf("expected signing_secret=my-secret-value, got %s", verifier.SigningSecret)
	}

	// Check routes
	if len(cfg.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(cfg.Routes))
	}
	if cfg.Routes[0].Hostname != testExampleHost {
		t.Errorf("expected hostname=test.example.com, got %s", cfg.Routes[0].Hostname)
	}
}

func TestValidate_RouteRequiresHostname(t *testing.T) {
	cfg := &Config{
		Routes: []RouteConfig{
			{
				Path:        testWebhookPath,
				Destination: testBackendURL,
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for missing hostname")
	}
}

func TestValidate_RouteRequiresPath(t *testing.T) {
	cfg := &Config{
		Routes: []RouteConfig{
			{
				Hostname:    testExampleHost,
				Destination: testBackendURL,
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for missing path")
	}
}

func TestValidate_RouteRequiresDestination(t *testing.T) {
	cfg := &Config{
		Routes: []RouteConfig{
			{
				Hostname: testExampleHost,
				Path:     testWebhookPath,
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for missing destination")
	}
}

func TestValidate_RouteReferencesInvalidIPAllowlist(t *testing.T) {
	cfg := &Config{
		Routes: []RouteConfig{
			{
				Hostname:    testExampleHost,
				Path:        testWebhookPath,
				Destination: testBackendURL,
				IPAllowlist: "nonexistent",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for invalid ip_allowlist reference")
	}
}

func TestValidate_RouteReferencesInvalidVerifier(t *testing.T) {
	cfg := &Config{
		Routes: []RouteConfig{
			{
				Hostname:    testExampleHost,
				Path:        testWebhookPath,
				Destination: testBackendURL,
				Verifier:    "nonexistent",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for invalid verifier reference")
	}
}

func TestValidate_SlackVerifierRequiresSigningSecret(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type: "slack",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for slack verifier without signing_secret")
	}
}

func TestValidate_GitHubVerifierRequiresSecret(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type: "github",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for github verifier without secret")
	}
}

func TestValidate_APIKeyVerifierRequiresHeaderAndToken(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type: "api_key",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for api_key verifier without header")
	}

	cfg.Verifiers["test"] = VerifierConfig{
		Type:   "api_key",
		Header: "X-API-Key",
	}

	err = cfg.Validate()
	if err == nil {
		t.Error("expected validation error for api_key verifier without token")
	}
}

func TestValidate_IPAllowlistRequiresCIDRsOrFetchURL(t *testing.T) {
	cfg := &Config{
		IPAllowlists: map[string]IPAllowlist{
			"test": {},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for ip_allowlist without cidrs or fetch_url")
	}
}

func TestValidate_IPAllowlistFetchURLRequiresFetchJQ(t *testing.T) {
	cfg := &Config{
		IPAllowlists: map[string]IPAllowlist{
			"test": {
				FetchURL: "https://example.com/ips.json",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for ip_allowlist with fetch_url but no fetch_jq")
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	cfg := &Config{
		IPAllowlists: map[string]IPAllowlist{
			"static": {
				CIDRs: []string{testCIDRPrivate8},
			},
			"dynamic": {
				FetchURL:        "https://example.com/ips.json",
				FetchJQ:         ".prefixes[].ip",
				RefreshInterval: 24 * time.Hour,
			},
		},
		Verifiers: map[string]VerifierConfig{
			"slack": {
				Type:          "slack",
				SigningSecret: "secret",
			},
			"github": {
				Type:   "github",
				Secret: "secret",
			},
			"apikey": {
				Type:   "api_key",
				Header: "X-API-Key",
				Token:  "token",
			},
			"noop": {
				Type: "noop",
			},
			"gitlab": {
				Type:  "gitlab",
				Token: "token",
			},
		},
		Routes: []RouteConfig{
			{
				Hostname:    testExampleHost,
				Path:        testWebhookPath,
				IPAllowlist: "static",
				Verifier:    "slack",
				Destination: testBackendURL,
			},
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}

func TestGetHostnames(t *testing.T) {
	cfg := &Config{
		Routes: []RouteConfig{
			{Hostname: testHostA, Path: "/1", Destination: testURLa},
			{Hostname: testHostB, Path: "/2", Destination: "http://b"},
			{Hostname: testHostA, Path: "/3", Destination: testURLa}, // duplicate
			{Hostname: testHostC, Path: "/4", Destination: "http://c"},
		},
	}

	hostnames := cfg.GetHostnames()
	if len(hostnames) != 3 {
		t.Errorf("expected 3 unique hostnames, got %d", len(hostnames))
	}

	expected := map[string]bool{
		testHostA: true,
		testHostB: true,
		testHostC: true,
	}
	for _, h := range hostnames {
		if !expected[h] {
			t.Errorf("unexpected hostname: %s", h)
		}
	}
}

func TestInterpolateEnvVars(t *testing.T) {
	os.Setenv("TEST_VAR1", "value1")
	os.Setenv("TEST_VAR2", "value2")
	defer os.Unsetenv("TEST_VAR1")
	defer os.Unsetenv("TEST_VAR2")

	input := "secret: ${TEST_VAR1}, other: ${TEST_VAR2}, missing: ${NONEXISTENT}"
	expected := "secret: value1, other: value2, missing: "

	result := interpolateEnvVars(input)
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLoadFromEnv(t *testing.T) {
	// Test when env var is not set
	os.Unsetenv("GATEKEEPERD_CONFIG")
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Errorf(errFmtUnexpected, err)
	}
	if cfg != nil {
		t.Error("expected nil config when env var not set")
	}

	// Test when env var is set with valid config
	validConfig := `
routes:
  - hostname: test.example.com
    path: /webhook
    destination: http://backend:8080
`
	os.Setenv("GATEKEEPERD_CONFIG", validConfig)
	defer os.Unsetenv("GATEKEEPERD_CONFIG")

	cfg, err = LoadFromEnv()
	if err != nil {
		t.Errorf(errFmtUnexpected, err)
	}
	if cfg == nil {
		t.Fatal("expected config to be loaded")
	}
	if len(cfg.Routes) != 1 {
		t.Errorf("expected 1 route, got %d", len(cfg.Routes))
	}
}

func TestLoadFromEnv_InvalidYAML(t *testing.T) {
	os.Setenv("GATEKEEPERD_CONFIG", "invalid: yaml: content:")
	defer os.Unsetenv("GATEKEEPERD_CONFIG")

	_, err := LoadFromEnv()
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoadAuto_FromEnvVar(t *testing.T) {
	validConfig := `
routes:
  - hostname: test.example.com
    path: /webhook
    destination: http://backend:8080
`
	os.Setenv("GATEKEEPERD_CONFIG", validConfig)
	defer os.Unsetenv("GATEKEEPERD_CONFIG")

	cfg, err := LoadAuto(testNonexistentFile)
	if err != nil {
		t.Errorf(errFmtUnexpected, err)
	}
	if cfg == nil {
		t.Fatal("expected config to be loaded from env var")
	}
}

func TestLoadAuto_FromFile(t *testing.T) {
	os.Unsetenv("GATEKEEPERD_CONFIG")

	configContent := `
routes:
  - hostname: test.example.com
    path: /webhook
    destination: http://backend:8080
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := LoadAuto(configPath)
	if err != nil {
		t.Errorf(errFmtUnexpected, err)
	}
	if cfg == nil {
		t.Fatal("expected config to be loaded from file")
	}
}

func TestGetRelayTokens(t *testing.T) {
	cfg := &Config{
		Routes: []RouteConfig{
			{Hostname: testHostA, Path: "/1", Destination: testURLa},
			{Hostname: testHostB, Path: "/2", RelayToken: "token1"},
			{Hostname: testHostC, Path: "/3", RelayToken: "token2"},
			{Hostname: "d.example.com", Path: "/4", RelayToken: "token1"}, // duplicate
		},
	}

	tokens := cfg.GetRelayTokens()
	if len(tokens) != 2 {
		t.Errorf("expected 2 unique tokens, got %d", len(tokens))
	}

	expected := map[string]bool{
		"token1": true,
		"token2": true,
	}
	for _, tk := range tokens {
		if !expected[tk] {
			t.Errorf("unexpected token: %s", tk)
		}
	}
}

func TestValidate_RouteWithRelayToken(t *testing.T) {
	cfg := &Config{
		Routes: []RouteConfig{
			{
				Hostname:   testExampleHost,
				Path:       testWebhookPath,
				RelayToken: "my-token",
			},
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf("unexpected error for route with relay_token: %v", err)
	}
}

func TestValidate_RouteBothDestinationAndRelayToken(t *testing.T) {
	cfg := &Config{
		Routes: []RouteConfig{
			{
				Hostname:    testExampleHost,
				Path:        testWebhookPath,
				Destination: testBackendURL,
				RelayToken:  "my-token",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for route with both destination and relay_token")
	}
}

func TestValidate_ShopifyVerifierRequiresSecret(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type: "shopify",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for shopify verifier without secret")
	}
}

func TestValidate_HMACVerifierRequiresFields(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type: "hmac",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for hmac verifier without header")
	}

	cfg.Verifiers["test"] = VerifierConfig{
		Type:   "hmac",
		Header: "X-Sig",
	}
	err = cfg.Validate()
	if err == nil {
		t.Error("expected validation error for hmac verifier without secret")
	}

	cfg.Verifiers["test"] = VerifierConfig{
		Type:   "hmac",
		Header: "X-Sig",
		Secret: "secret",
	}
	err = cfg.Validate()
	if err == nil {
		t.Error("expected validation error for hmac verifier without hash")
	}

	cfg.Verifiers["test"] = VerifierConfig{
		Type:   "hmac",
		Header: "X-Sig",
		Secret: "secret",
		Hash:   "SHA256",
	}
	err = cfg.Validate()
	if err == nil {
		t.Error("expected validation error for hmac verifier without encoding")
	}
}

func TestValidate_UnknownVerifierType(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type: "unknown",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for unknown verifier type")
	}
}

func TestValidate_ValidHMACVerifier(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type:     "hmac",
				Header:   "X-Sig",
				Secret:   "secret",
				Hash:     "SHA256",
				Encoding: "hex",
			},
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf(errFmtUnexpected, err)
	}
}

func TestValidate_ValidShopifyVerifier(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type:   "shopify",
				Secret: "secret",
			},
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf(errFmtUnexpected, err)
	}
}

func TestValidate_VerifierEmptyType(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				// Type is empty
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for verifier with empty type")
	}
}

func TestLoadAuto_EnvVarError(t *testing.T) {
	// Set invalid YAML in env var to trigger error in LoadFromEnv
	os.Setenv("GATEKEEPERD_CONFIG", "invalid: yaml: content:")
	defer os.Unsetenv("GATEKEEPERD_CONFIG")

	_, err := LoadAuto(testNonexistentFile)
	if err == nil {
		t.Error("expected error from invalid env var config")
	}
}

func TestLoadAuto_FileNotFound(t *testing.T) {
	// Ensure env var is not set
	os.Unsetenv("GATEKEEPERD_CONFIG")

	_, err := LoadAuto(testNonexistentFile)
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLoadFromEnv_ValidationError(t *testing.T) {
	// Valid YAML but invalid config (route without hostname)
	invalidConfig := `
routes:
  - path: /webhook
    destination: http://backend:8080
`
	os.Setenv("GATEKEEPERD_CONFIG", invalidConfig)
	defer os.Unsetenv("GATEKEEPERD_CONFIG")

	_, err := LoadFromEnv()
	if err == nil {
		t.Error("expected validation error")
	}
}

func TestValidate_HMACVerifierMissingHeader(t *testing.T) {
	// HMAC verifier with secret but missing header
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type:   "hmac",
				Secret: "secret",
				// Header is missing
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for hmac verifier without header")
	}
}

func TestValidate_RouteReferencesInvalidValidator(t *testing.T) {
	cfg := &Config{
		Routes: []RouteConfig{
			{
				Hostname:    testExampleHost,
				Path:        testWebhookPath,
				Destination: testBackendURL,
				Validator:   "nonexistent",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for invalid validator reference")
	}
}

func TestValidate_JSONSchemaValidatorRequiresSchema(t *testing.T) {
	cfg := &Config{
		Validators: map[string]ValidatorConfig{
			"test": {
				Type: "json_schema",
				// Neither SchemaFile nor Schema specified
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for json_schema validator without schema")
	}
}

func TestValidate_ValidatorEmptyType(t *testing.T) {
	cfg := &Config{
		Validators: map[string]ValidatorConfig{
			"test": {
				// Type is empty
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for validator with empty type")
	}
}

func TestValidate_UnknownValidatorType(t *testing.T) {
	cfg := &Config{
		Validators: map[string]ValidatorConfig{
			"test": {
				Type: "unknown",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for unknown validator type")
	}
}

func TestValidate_ValidJSONSchemaValidator_WithSchemaFile(t *testing.T) {
	cfg := &Config{
		Validators: map[string]ValidatorConfig{
			"test": {
				Type:       "json_schema",
				SchemaFile: "/path/to/schema.json",
			},
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf(errFmtUnexpected, err)
	}
}

func TestValidate_ValidJSONSchemaValidator_WithInlineSchema(t *testing.T) {
	cfg := &Config{
		Validators: map[string]ValidatorConfig{
			"test": {
				Type:   "json_schema",
				Schema: `{"type": "object"}`,
			},
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf(errFmtUnexpected, err)
	}
}

func TestValidate_RouteWithValidator(t *testing.T) {
	cfg := &Config{
		Validators: map[string]ValidatorConfig{
			"my-validator": {
				Type:   "json_schema",
				Schema: `{"type": "object"}`,
			},
		},
		Routes: []RouteConfig{
			{
				Hostname:    testExampleHost,
				Path:        testWebhookPath,
				Destination: testBackendURL,
				Validator:   "my-validator",
			},
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf(errFmtUnexpected, err)
	}
}

func TestValidate_JSONFieldVerifier_MissingPath(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type:  "json_field",
				Token: "secret",
				// Path is missing
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for json_field verifier without path")
	}
}

func TestValidate_JSONFieldVerifier_MissingToken(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type: "json_field",
				Path: "value.0.clientState",
				// Token is missing
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for json_field verifier without token")
	}
}

func TestValidate_ValidJSONFieldVerifier(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type:  "json_field",
				Path:  "value.0.clientState",
				Token: "my-secret-state",
			},
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf(errFmtUnexpected, err)
	}
}

func TestValidate_QueryParamVerifier_MissingName(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type:  "query_param",
				Token: "secret",
				// Name is missing
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for query_param verifier without name")
	}
}

func TestValidate_QueryParamVerifier_MissingToken(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type: "query_param",
				Name: "token",
				// Token is missing
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for query_param verifier without token")
	}
}

func TestValidate_ValidQueryParamVerifier(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type:  "query_param",
				Name:  "token",
				Token: testSecret,
			},
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf(errFmtUnexpected, err)
	}
}

func TestValidate_HeaderQueryParamVerifier_MissingHeader(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type:  "header_query_param",
				Name:  "secret",
				Token: testSecret,
				// Header is missing
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for header_query_param verifier without header")
	}
}

func TestValidate_HeaderQueryParamVerifier_MissingName(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type:   "header_query_param",
				Header: testGoogTokenHeader,
				Token:  testSecret,
				// Name is missing
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for header_query_param verifier without name")
	}
}

func TestValidate_HeaderQueryParamVerifier_MissingToken(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type:   "header_query_param",
				Header: testGoogTokenHeader,
				Name:   "secret",
				// Token is missing
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for header_query_param verifier without token")
	}
}

func TestValidate_ValidHeaderQueryParamVerifier(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type:   "header_query_param",
				Header: testGoogTokenHeader,
				Name:   "secret",
				Token:  testSecret,
			},
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf(errFmtUnexpected, err)
	}
}

func TestValidate_GitLabVerifierRequiresToken(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type: "gitlab",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for gitlab verifier without token")
	}
}

func TestValidate_SendGridVerifierRequiresPublicKey(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type: "sendgrid",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for sendgrid verifier without public_key")
	}
}

func TestValidate_RateLimiter_MissingTotalRPS(t *testing.T) {
	cfg := &Config{
		RateLimiters: map[string]RateLimiterConfig{
			"test": {
				PerIPRPS: 10,
				Burst:    20,
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for rate_limiter without total_rps")
	}
}

func TestValidate_RateLimiter_NegativePerIPRPS(t *testing.T) {
	cfg := &Config{
		RateLimiters: map[string]RateLimiterConfig{
			"test": {
				TotalRPS: 100,
				PerIPRPS: -1,
				Burst:    20,
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for rate_limiter with negative per_ip_rps")
	}
}

func TestValidate_RateLimiter_MissingBurst(t *testing.T) {
	cfg := &Config{
		RateLimiters: map[string]RateLimiterConfig{
			"test": {
				TotalRPS: 100,
				PerIPRPS: 10,
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for rate_limiter without burst")
	}
}

func TestValidate_ValidRateLimiter(t *testing.T) {
	cfg := &Config{
		RateLimiters: map[string]RateLimiterConfig{
			"test": {
				TotalRPS: 100,
				PerIPRPS: 10,
				Burst:    20,
			},
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf(errFmtUnexpected, err)
	}
}

func TestValidate_RateLimiter_ZeroPerIPRPS_Allowed(t *testing.T) {
	cfg := &Config{
		RateLimiters: map[string]RateLimiterConfig{
			"test": {
				TotalRPS: 100,
				PerIPRPS: 0, // Zero means per-IP limiting disabled
				Burst:    20,
			},
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf("unexpected error (per_ip_rps=0 should be allowed): %v", err)
	}
}

func TestValidate_RouteReferencesInvalidRateLimiter(t *testing.T) {
	cfg := &Config{
		Routes: []RouteConfig{
			{
				Hostname:    testExampleHost,
				Path:        testWebhookPath,
				Destination: testBackendURL,
				RateLimiter: "nonexistent",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for invalid rate_limiter reference")
	}
}

func TestValidate_RouteWithValidRateLimiter(t *testing.T) {
	cfg := &Config{
		RateLimiters: map[string]RateLimiterConfig{
			"default": {
				TotalRPS: 100,
				PerIPRPS: 10,
				Burst:    20,
			},
		},
		Routes: []RouteConfig{
			{
				Hostname:    testExampleHost,
				Path:        testWebhookPath,
				Destination: testBackendURL,
				RateLimiter: "default",
			},
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf(errFmtUnexpected, err)
	}
}

func TestValidate_GlobalDefaultRateLimiter_NotFound(t *testing.T) {
	cfg := &Config{
		Global: GlobalConfig{
			DefaultRateLimiter: "nonexistent",
		},
		Routes: []RouteConfig{
			{
				Hostname:    testExampleHost,
				Path:        testWebhookPath,
				Destination: testBackendURL,
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for invalid global default_rate_limiter")
	}
}

func TestValidate_GlobalDefaultRateLimiter_Valid(t *testing.T) {
	cfg := &Config{
		Global: GlobalConfig{
			DefaultRateLimiter: "default",
		},
		RateLimiters: map[string]RateLimiterConfig{
			"default": {
				TotalRPS: 100,
				PerIPRPS: 10,
				Burst:    20,
			},
		},
		Routes: []RouteConfig{
			{
				Hostname:    testExampleHost,
				Path:        testWebhookPath,
				Destination: testBackendURL,
			},
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf(errFmtUnexpected, err)
	}
}

func TestValidate_OIDCVerifierMissingIssuer(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type:     "oidc",
				Audience: "myapp",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for oidc verifier without issuer")
	}
}

func TestValidate_OIDCVerifierMissingAudience(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type:   "oidc",
				Issuer: "https://accounts.example.com",
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected validation error for oidc verifier without audience")
	}
}

func TestValidate_ValidOIDCVerifier(t *testing.T) {
	cfg := &Config{
		Verifiers: map[string]VerifierConfig{
			"test": {
				Type:     "oidc",
				Issuer:   "https://accounts.example.com",
				Audience: "myapp",
			},
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf(errFmtUnexpected, err)
	}
}
