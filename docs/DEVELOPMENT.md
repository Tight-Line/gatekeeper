# Development

This guide covers setting up a development environment and common workflows.

## Prerequisites

- Go 1.25 or later
- Docker (for container builds and local testing)
- kubectl and minikube (for Kubernetes testing, optional)

## Initial Setup

Clone the repository and install development tools:

```bash
git clone https://github.com/tight-line/gatekeeper.git
cd gatekeeper

# Install required Go tools (golangci-lint, goimports)
make tools

# Install git pre-commit hook for linting
make setup-hooks
```

## Common Commands

```bash
# Build all binaries (gatekeeperd and gatekeeper-relay)
make build-all

# Build only the server
make build

# Build only the relay client
make build-relay

# Run tests
make test

# Run tests with coverage report
make test-coverage

# Run linter
make lint

# Fix auto-fixable lint issues
make lint-fix

# Format code
make fmt

# Run all checks (lint + test)
make check
```

## Running Locally

### Server Only (Direct Forwarding Mode)

For local development, create a minimal test configuration:

```yaml
# config/dev.yaml
global:
  metrics_port: 9090

verifiers:
  test:
    type: noop  # No signature verification for testing

routes:
  - hostname: localhost
    path: /webhook
    verifier: test
    destination: http://localhost:9999/received
```

Start a simple backend to receive webhooks:

```bash
# Terminal 1: Start a mock backend
python3 -m http.server 9999
# Or use netcat: nc -l 9999
```

```bash
# Terminal 2: Start gatekeeperd
make build && ./bin/gatekeeperd -config config/dev.yaml -listen :8080
```

```bash
# Terminal 3: Send a test webhook
curl -X POST http://localhost:8080/webhook \
  -H "Host: localhost" \
  -H "Content-Type: application/json" \
  -d '{"test": "data"}'
```

### Server with Relay Client

Create server and relay configurations:

```yaml
# config/dev-relay-server.yaml
global:
  metrics_port: 9090

verifiers:
  test:
    type: noop

routes:
  - hostname: localhost
    path: /webhook
    verifier: test
    relay_token: "test-token-12345"
```

```yaml
# config/dev-relay-client.yaml
server: "http://localhost:8080"

channels:
  - name: test
    token: "test-token-12345"
    destination: "http://localhost:9999/received"
```

Run all three components:

```bash
# Terminal 1: Mock backend
python3 -m http.server 9999

# Terminal 2: Server
./bin/gatekeeperd -config config/dev-relay-server.yaml -listen :8080

# Terminal 3: Relay client
./bin/gatekeeper-relay -config config/dev-relay-client.yaml

# Terminal 4: Send webhook
curl -X POST http://localhost:8080/webhook \
  -H "Host: localhost" \
  -H "Content-Type: application/json" \
  -d '{"test": "relay"}'
```

### Testing with Real Provider Signatures

To test with actual Slack signature verification:

```yaml
# config/dev-slack.yaml
global:
  metrics_port: 9090

verifiers:
  slack:
    type: slack
    signing_secret: "your-test-signing-secret"
    max_timestamp_age: 5m

routes:
  - hostname: localhost
    path: /slack
    verifier: slack
    destination: http://localhost:9999/slack
```

Generate a valid Slack signature for testing:

```bash
# Compute signature (requires openssl)
TIMESTAMP=$(date +%s)
BODY='{"type":"event_callback","event":{"type":"message"}}'
SECRET="your-test-signing-secret"
SIG_BASE="v0:${TIMESTAMP}:${BODY}"
SIGNATURE="v0=$(echo -n "$SIG_BASE" | openssl dgst -sha256 -hmac "$SECRET" | cut -d' ' -f2)"

curl -X POST http://localhost:8080/slack \
  -H "Host: localhost" \
  -H "Content-Type: application/json" \
  -H "X-Slack-Request-Timestamp: $TIMESTAMP" \
  -H "X-Slack-Signature: $SIGNATURE" \
  -d "$BODY"
```

## Running with Docker

### Server Only

```bash
# Build images
make docker

# Run with docker-compose
docker-compose up
```

### Server with Relay Client

```bash
docker-compose --profile relay up
```

This starts:
- gatekeeperd on port 8080 (HTTP) and 9090 (metrics)
- gatekeeper-relay connecting to the server
- A mock backend for testing webhook delivery

## Running in Kubernetes (minikube)

```bash
# Start minikube
minikube start

# Apply manifests
kubectl apply -k k8s/

# Get the service URL
minikube service gatekeeperd --url
```

For Helm-based deployment with relay:

```bash
# Add any required secrets
kubectl create secret generic gatekeeper-secrets \
  --from-literal=relay-token=your-token

# Install server
helm install gatekeeperd ./charts/gatekeeperd -f your-values.yaml

# Install relay client
helm install gatekeeper-relay ./charts/gatekeeper-relay -f your-relay-values.yaml
```

## Pre-commit Hook

The pre-commit hook runs automatically after `make setup-hooks`. It checks:

1. Code formatting (gofmt)
2. Linting (golangci-lint)

If the hook fails, fix the issues:

```bash
# Auto-fix formatting and some lint issues
make lint-fix

# Or fix manually, then retry commit
```

To bypass the hook temporarily (not recommended):

```bash
git commit --no-verify
```

## CI Pipeline

The GitHub Actions CI pipeline runs on every push and PR:

1. **Lint**: Runs golangci-lint
2. **Test**: Runs tests with race detection and coverage
3. **Coverage**: Enforces 100% test coverage
4. **Build**: Builds binaries and Docker images

All checks must pass before merging.

## Project Structure

```
cmd/gatekeeperd/       Server entry point, CLI flags
cmd/gatekeeper-relay/  Relay client entry point
internal/config/       YAML parsing, validation, env var interpolation
internal/ipfilter/     CIDR parsing, IP matching, dynamic fetching
internal/verifier/     Webhook signature verification implementations
internal/proxy/        HTTP handler and request forwarding
internal/relay/        Relay server: manager and HTTP handler
internal/relayclient/  Relay client: config, poller, forwarder
internal/server/       ACME TLS server
internal/metrics/      Prometheus metrics
internal/httputil/     HTTP utility functions
config/                Example configuration files
k8s/                   Kubernetes manifests
charts/                Helm charts
docs/                  Documentation
schemas/               JSON schemas for payload validation
testdata/recordings/   VCR-like webhook recordings for tests
```

## Adding New Providers

If you want to add support for a new webhook provider (verifier and/or validator), see [PROVIDER_DEVELOPMENT.md](PROVIDER_DEVELOPMENT.md) for a complete guide covering:

- Deploying gatekeeperd to capture real webhooks from the provider
- Creating test recordings from captured payloads
- Developing and testing verifiers with real signatures
- Creating JSON schemas for payload validation
- Wiring everything into the codebase

This workflow ensures your implementation works with actual provider payloads, not just synthetic test data.
