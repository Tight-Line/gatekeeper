# Coding Standards

This document defines the coding standards for the gatekeeper project.

## Writing Style for Documentation

When writing or updating documentation:

- Do not use emojis
- Do not use emdashes or endashes (use commas, periods, or "to" for ranges)
- Avoid excessive bullet lists; prefer prose where it improves readability
- Keep diagrams minimal; only include them where they clarify complex flows
- Write in a direct, technical tone suitable for engineers
- Avoid marketing language or superlatives

## Go Code Style

- Follow standard Go conventions (gofmt, golint)
- Use meaningful variable names; avoid single-letter names except for loop indices
- Keep functions focused on a single responsibility
- Add comments for exported functions and types
- Error messages should be lowercase and not end with punctuation
- Use structured logging (slog) with consistent field names
- **Cognitive complexity**: Keep functions under 15 cognitive complexity (enforced by SonarCloud). Extract helper methods when complexity grows.

## Package Organization

```
cmd/gatekeeperd/       Server entry point, CLI flags, wiring
cmd/gatekeeper-relay/  Relay client entry point
internal/config/       YAML parsing, validation, env var interpolation
internal/ipfilter/     CIDR parsing and IP matching
internal/verifier/     Webhook signature verification (authentication)
internal/validator/    Payload structure validation (JSON Schema)
internal/proxy/        HTTP handler and request forwarding
internal/relay/        Relay server: manager and HTTP handler
internal/relayclient/  Relay client: config, poller, forwarder
internal/server/       ACME TLS server
internal/metrics/      Prometheus metrics
internal/httputil/     HTTP utility functions
schemas/               Pre-built JSON schemas for common providers
```

### Verifier vs Validator Packages

| Package | Purpose | Interface Method |
|---------|---------|------------------|
| `internal/verifier` | Authenticate requests (prove origin) | `Verify(r *http.Request, body []byte) error` |
| `internal/validator` | Validate payload structure | `Validate(payload []byte) error` |

**Verifiers** check cryptographic signatures, API keys, and timestamps. They answer: "Is this request authentic?"

**Validators** check payload structure against JSON Schema. They answer: "Is this payload well-formed?"

## Testing Requirements

**This project requires 100% test coverage (line and branch).** The CI pipeline enforces this requirement and will fail if coverage drops below 100%.

Guidelines:

- If a code branch is not reachable in tests, remove it from the code
- If a function cannot be meaningfully tested (e.g., main()), exclude it from coverage using build tags or restructure the code
- All verifier implementations must have unit tests covering:
  - Valid signatures
  - Missing headers
  - Invalid signatures
  - Tampered payloads
- Use table-driven tests for comprehensive coverage
- Test helper functions (like signature computation) should mirror the actual provider algorithm

To check coverage locally:

```bash
go test -coverprofile=coverage.out -tags=ci ./...
go tool cover -func=coverage.out | grep -v "100.0%"  # Show uncovered code
go tool cover -html=coverage.out                      # Visual coverage report
```

### CI Test Environment

CI runs tests with additional flags that may reveal issues not seen locally:

- **Race detection** (`-race`): CI runs with race detection enabled. This can cause timing-sensitive tests to behave differently. If a test passes locally but fails in CI with race conditions, the test likely has a race bug.
- **Atomic coverage** (`-covermode=atomic`): Required for accurate coverage with `-race`.

### Testing with miniredis

Redis-dependent code uses [miniredis](https://github.com/alicebob/miniredis) for testing. miniredis simulates most Redis commands but has limitations:

- Does not track message idle time in streams (XPendingExt)
- Does not track delivery counts accurately
- XCLAIM behavior differs from real Redis

Code paths that depend on these behaviors may need `// coverage:ignore - miniredis limitation` comments. See AGENTS.md for the coverage:ignore policy.

### Making Intervals Configurable

For code with time-based behavior (polling intervals, recovery timers, timeouts), make intervals configurable so tests can use short values:

```go
type Manager struct {
    recoveryInterval time.Duration  // Configurable for testing
}

func NewManager(opts ...Option) *Manager {
    m := &Manager{
        recoveryInterval: 30 * time.Second,  // Production default
    }
    for _, opt := range opts {
        opt(m)
    }
    return m
}

func WithRecoveryInterval(d time.Duration) Option {
    return func(m *Manager) { m.recoveryInterval = d }
}
```

Tests can then use millisecond intervals to run quickly without flakiness.

## Webhook Recording System (VCR)

The project includes a VCR-like system for recording and replaying webhook payloads in tests. This makes it easy to test verifiers with realistic payloads and to generate failure test cases.

### Directory Structure

```
testdata/recordings/
  slack/
    event_callback/
      message.json
    url_verification/
      challenge.json
  github/
    push/
      single_commit.json
  shopify/
    orders/
      created.json
```

### Recording Format

Each recording is a JSON file with this structure:

```json
{
  "metadata": {
    "provider": "slack",
    "event_type": "event_callback",
    "description": "Standard message event from Slack Events API"
  },
  "request": {
    "method": "POST",
    "path": "/webhooks/slack",
    "headers": {
      "Content-Type": "application/json",
      "X-Slack-Request-Timestamp": "1234567890",
      "X-Slack-Signature": "v0=..."
    },
    "body": { ... }
  },
  "signing_secret": "test-signing-secret"
}
```

### Using Recordings in Tests

```go
import "github.com/tight-line/gatekeeper/internal/testutil"

func TestSlackVerifier(t *testing.T) {
    // Load a recording
    rec, err := testutil.LoadRecording("slack", "event_callback", "message")
    if err != nil {
        t.Fatal(err)
    }

    // Convert to HTTP request
    req, body := rec.ToHTTPRequest()

    // Test with the verifier
    verifier := NewSlackVerifier(rec.SigningSecret, 5*time.Minute)
    err = verifier.Verify(req, body)
    // ...
}
```

### Generating Failure Test Cases

The recording type provides mutation methods for generating failure test cases:

```go
// Invalid signature
invalid := rec.InvalidateSignature()

// Expired timestamp (10 minutes old)
expired := rec.ExpireTimestamp(10 * time.Minute)

// Future timestamp (clock skew)
future := rec.FutureTimestamp(10 * time.Minute)

// Missing header
noSig := rec.WithoutHeader("X-Slack-Signature")

// Modified body field
tampered := rec.WithBodyField("event.type", "malicious_event")

// Different signing secret
wrongSecret := rec.WithSigningSecret("wrong-secret")
```

### Creating New Recordings

#### Manual Recording

1. Set up a test endpoint that logs the full request (headers + body)
2. Configure the provider to send webhooks to your test endpoint
3. Trigger the event you want to record
4. Create a JSON file in the appropriate directory
5. Replace real secrets with test values
6. Recompute the signature using the test secret (for provider-specific tests)

For test recordings, you can use placeholder signatures since tests typically either:
- Recompute valid signatures using the test signing secret
- Use the mutation methods to test invalid signatures

#### Recording Checklist for New Providers

When adding a new provider, create recordings for:

1. **Happy path**: A typical valid webhook payload
2. **Edge cases**: Different event types the provider sends
3. **URL verification** (if applicable): Challenge/handshake requests

Document in the recording's description field what the payload represents.

## Security Considerations

- Never log request bodies or secrets
- Use constant-time comparison for all signature verification
- Validate timestamps to prevent replay attacks (where supported by provider)
- IP allowlists default to deny (fail closed) when misconfigured

## Common Workflows

### Adding a New Verifier

1. Create `internal/verifier/{provider}.go` implementing the `Verifier` interface
2. Create `internal/verifier/{provider}_test.go` with table-driven tests
3. Add the verifier type to `internal/config/config.go` Validate() function
4. Wire it up in `internal/proxy/handler.go` buildVerifier function
5. Update the verifier table in AGENTS.md
6. Add an example to `config/example.yaml`

### Adding a New JSON Schema

1. Create the schema file in `schemas/{provider}/{event_type}.json`
2. Follow JSON Schema Draft 2020-12 specification
3. Include `$schema` and `$id` fields for clarity
4. Test the schema with sample payloads from the provider's documentation
5. Document the schema in USAGE.md if it's commonly used

### Modifying Configuration

1. Update `internal/config/config.go` structs
2. Update validation in `Config.Validate()`
3. Update `config/example.yaml` with examples
4. Document new fields in AGENTS.md

### Before Submitting Changes

- Run `make test` to verify tests pass
- Run `make lint` to verify linting passes
- Run `make build` to verify compilation
- Check that `config/example.yaml` remains valid
- Ensure 100% test coverage
