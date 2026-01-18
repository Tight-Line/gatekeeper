# gatekeeper

[![CI](https://github.com/tight-line/gatekeeper/actions/workflows/ci.yml/badge.svg)](https://github.com/tight-line/gatekeeper/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/tight-line/gatekeeper/graph/badge.svg)](https://codecov.io/gh/tight-line/gatekeeper)

A webhook authentication proxy for enterprise environments.

## The Problem

Many organizations operate internal networks that are intentionally isolated from the public internet. This is good security practice. However, modern SaaS applications like Slack, Google Workspace, and GitHub rely on webhooks to notify your systems of events. These webhooks originate from the vendor's infrastructure and must reach your internal services.

The traditional approach is to poke holes in your firewall for each webhook source. This becomes problematic because:

1. Major cloud providers like AWS and Google use thousands of IP addresses that change frequently. Slack webhooks, for example, can originate from any EC2 instance in AWS.

2. Even if you maintain an up-to-date allowlist, you are trusting that any traffic from those IPs is legitimate. A compromised service sharing that IP space could send malicious requests to your webhook endpoints.

3. Managing firewall rules across multiple business units, each with their own SaaS integrations, does not scale.

## The Solution

gatekeeper sits at the edge of your network and handles webhook traffic. For each incoming request, it:

1. Checks the source IP against a configured allowlist
2. Validates the webhook signature using the provider's published algorithm
3. Forwards authenticated requests to internal backends

Your internal services never need direct internet exposure. The proxy is the only component that accepts external connections, and it only forwards traffic that passes both IP and cryptographic verification.

## Design

### Configuration-Driven

All routing, verification, and allowlist configuration lives in a single YAML file. Adding a new webhook endpoint means adding a few lines of configuration and deploying. No code changes required.

```yaml
routes:
  - hostname: slack-webhooks.example.com
    path: /events
    ip_allowlist: aws
    verifier: my-slack-verifier
    destination: http://internal-service:8080/webhooks/slack
```

### Path Routing

Routes are matched using segment-aware prefix matching. The route `path` defines the prefix to match, and requests to that path (and any subpaths) are forwarded to the `destination`.

**Matching rules:**

1. Exact matches are tried first
2. Prefix matches require a segment boundary (path separator `/`)
3. A route with `path: /hooks` matches `/hooks` and `/hooks/github`, but NOT `/hookshot`

**Path construction when forwarding:**

The route's `path` is stripped from the incoming request, and the remainder is appended to the destination path:

| Route path | Destination | Request path | Forwarded to |
|------------|-------------|--------------|--------------|
| `/hooks` | `http://backend/api` | `/hooks` | `http://backend/api` |
| `/hooks` | `http://backend/api` | `/hooks/github` | `http://backend/api/github` |
| `/` | `http://backend/api` | `/events` | `http://backend/api/events` |
| `/` | `http://backend` | `/foo/bar` | `http://backend/foo/bar` |

This allows a single route to handle multiple subpaths while preserving the path hierarchy in the forwarded request.

### Relay Client Delivery

Relay client delivery is an alternative to opening firewall ports into private networks. In this mode, gatekeeperd still receives and verifies webhooks at the edge, but it does not connect directly to your internal services. Instead, a relay client inside the private network initiates an outbound connection to gatekeeperd and long polls for verified webhooks. When a webhook arrives, gatekeeperd queues it, the relay client retrieves it, and then forwards it locally.

This means your internal network only needs outbound access to gatekeeperd, and no inbound firewall rules are required. It is useful when direct forwarding is not possible or not desired.

Relay delivery is enabled by configuring a `relay_token` on the route and running `gatekeeper-relay` with the same token. Direct forwarding uses `destination` and requires inbound reachability from gatekeeperd.

### Host Header Preservation

By default, gatekeeper sets the `Host` header of forwarded requests to match the destination hostname. This is standard reverse proxy behavior. However, some backend applications need to see the original `Host` header that the webhook sender used.

Enable `preserve_host` on a route to forward the original `Host` header:

```yaml
routes:
  - hostname: webhooks.example.com
    path: /events
    destination: http://internal-service:8080/webhooks
    preserve_host: true  # Backend receives Host: webhooks.example.com
```

| preserve_host | Incoming Host | Destination | Backend sees Host |
|---------------|---------------|-------------|-------------------|
| `false` (default) | webhooks.example.com | http://internal:8080 | internal:8080 |
| `true` | webhooks.example.com | http://internal:8080 | webhooks.example.com |

This works for both direct forwarding and relay delivery. For relay delivery, gatekeeper sends special headers (`X-Gatekeeperd-Preserve-Host` and `X-Gatekeeperd-Original-Host`) to the relay client, which then sets the appropriate `Host` header when forwarding to the local destination. These internal headers are stripped before the request reaches the final destination.

### Provider-Specific Verification

Different providers use different signature schemes. Slack uses HMAC-SHA256 with a timestamp-prefixed payload. GitHub uses HMAC-SHA256 directly on the body. Google Calendar uses a simple token header. The proxy implements each scheme correctly, including replay attack protection where the provider supports it.

### Multi-Tenant

Enterprise environments often have multiple business units, each with their own SaaS accounts and signing secrets. The proxy supports this naturally through its configuration structure. Each route specifies its own verifier, and verifiers are defined per business unit.

## Security Considerations

### IP Allowlists and X-Forwarded-For

Gatekeeper uses IP allowlists as one layer of defense, checking the client IP against configured CIDR ranges before processing a request. When gatekeeper runs behind a reverse proxy (ingress controller, gateway, load balancer), the TCP connection's remote address is the proxy's IP, not the original client's. To enforce IP allowlists correctly, gatekeeper can read the `X-Forwarded-For` header to determine the true client IP.

**This behavior must be explicitly enabled** using the `--trust-x-forwarded-for` flag or the `GATEKEEPERD_TRUST_X_FORWARDED_FOR=true` environment variable. By default, gatekeeper only uses the TCP connection's remote address.

### When to enable `--trust-x-forwarded-for`

Enable this flag when gatekeeper runs behind a trusted reverse proxy that sets the `X-Forwarded-For` header:

| Deployment | Enable flag? | Reason |
|------------|--------------|--------|
| Behind Ingress Controller | Yes | Ingress sets X-Forwarded-For from real client IP |
| Behind L7 Load Balancer (AWS ALB, GCP HTTP LB) | Yes | L7 LBs terminate HTTP and set X-Forwarded-For |
| Behind L4 Load Balancer with TCP passthrough (AWS NLB, GCP TCP LB) | No | Client IP is preserved in TCP connection; X-Forwarded-For not set |
| Direct exposure with `-tls` mode | No | No proxy; X-Forwarded-For could be spoofed by attacker |
| NodePort Service without Ingress | No | Clients connect directly; X-Forwarded-For could be spoofed |

### Helm chart behavior

When using the Helm chart:

- If `ingress.enabled: true`, the flag is automatically enabled
- If `trustXForwardedFor: true` is set explicitly, the flag is enabled
- Otherwise, the flag is disabled (safe default)

```yaml
# values.yaml - automatically trusts X-Forwarded-For when using ingress
ingress:
  enabled: true

# Or explicitly enable for L7 load balancer without ingress
trustXForwardedFor: true
```

### Why trusting X-Forwarded-For is safe (when properly configured)

The `X-Forwarded-For` header is inherently untrustworthy when received directly from the internet—an attacker can set it to any value. However, in a reverse-proxied deployment, the proxy sits between the client and gatekeeper, and the proxy controls what headers reach the backend.

Standard behavior of ingress controllers and gateways (nginx-ingress, Traefik, Envoy, AWS ALB, GCP Cloud Load Balancing, etc.):

1. **They set or overwrite X-Forwarded-For** with the actual client IP from the TCP connection
2. **If the header already exists**, they append the client IP to the chain, making the leftmost entry the original client (set by the first trusted proxy)
3. **They are the only entry point** to the cluster—pods are not directly reachable from outside

This means an attacker cannot spoof their IP by sending a fake `X-Forwarded-For` header. The ingress controller will either replace it entirely or append the real IP, and gatekeeper reads the leftmost (original client) IP from the chain.

### Requirements for safe operation with X-Forwarded-For

1. **Gatekeeper must not be directly exposed to the internet.** All external traffic must flow through the ingress controller or gateway. If gatekeeper is directly reachable, attackers can send arbitrary `X-Forwarded-For` values and bypass IP allowlists.

2. **The ingress controller must set X-Forwarded-For correctly.** This is the default behavior for all major ingress controllers, but verify your configuration. For nginx-ingress, this is controlled by `use-forwarded-headers` and `compute-full-forwarded-for` settings.

3. **Network policies should enforce the traffic path.** In Kubernetes, use NetworkPolicy to ensure gatekeeper pods only accept traffic from the ingress controller's pods, not from arbitrary sources.

Example NetworkPolicy:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: gatekeeperd-ingress-only
spec:
  podSelector:
    matchLabels:
      app: gatekeeperd
  policyTypes:
    - Ingress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              name: ingress-nginx
          podSelector:
            matchLabels:
              app.kubernetes.io/name: ingress-nginx
```

### Why we trust the leftmost IP

The `X-Forwarded-For` header format is `client, proxy1, proxy2, ...`. Each proxy in the chain appends the IP it received the connection from. The leftmost IP is set by the first proxy that received the request from the actual client. Since that first proxy (your ingress controller) is trusted and sets the value from the real TCP connection, the leftmost IP is the true client IP.

Gatekeeper does not support configuring "trusted proxy" depth (like some frameworks do) because in the expected deployment model, there is exactly one layer of trusted proxies (the ingress controller), and that proxy is responsible for setting the correct client IP.

## Alternatives Considered

### Convoy

Convoy (github.com/frain-dev/convoy) is an open-source webhook gateway with similar goals. It was the starting point for this project. However, Convoy is designed as a full webhook platform with event persistence, retry logic, and delivery tracking. It requires PostgreSQL, Redis, and a message queue.

For our use case, we need a lightweight proxy, not a platform. We do not need to store events or manage retries. The backend services handle that. We just need to authenticate and forward.

### API Gateways (Kong, Traefik)

General-purpose API gateways can terminate TLS and route traffic, but they do not have built-in support for provider-specific webhook signature verification. You would need to write custom plugins for each provider, at which point you have recreated this project with more complexity.

### Cloud Provider Solutions

AWS API Gateway and Google Cloud Endpoints can handle webhook ingestion, but they introduce vendor lock-in and do not solve the fundamental problem: you still need to validate signatures, and you still need to route traffic to internal services that may not be reachable from the cloud provider's network.

## Current Implementation

Phase 1, 2, and 3 are complete.

Features include:

- YAML configuration with environment variable interpolation for secrets
- Static and dynamic IP allowlist filtering with CIDR support
- Dynamic IP range fetching from any JSON endpoint using jq queries
- Verifiers for Slack, GitHub, Shopify, generic HMAC, and API key
- HTTP request forwarding (transparent proxy)
- Automatic TLS certificate provisioning via ACME/Let's Encrypt
- Prometheus metrics endpoint with request counts, latencies, and error tracking
- Health check endpoint for Kubernetes probes
- Structured JSON logging
- Graceful shutdown on SIGINT/SIGTERM
- Kubernetes manifests and Helm chart

## Installation

### Docker

Pre-built images are available from GitHub Container Registry:

```bash
docker pull ghcr.io/tight-line/gatekeeperd:latest
docker pull ghcr.io/tight-line/gatekeeper-relay:latest
```

Run with your config:

```bash
docker run -p 8080:8080 -p 9090:9090 \
  -v /path/to/config.yaml:/etc/gatekeeper/config.yaml \
  ghcr.io/tight-line/gatekeeperd:latest -listen :8080
```

### Helm

```bash
helm repo add gatekeeper https://tight-line.github.io/gatekeeper
helm repo update

# Install the server
helm install gatekeeperd gatekeeper/gatekeeperd -f your-values.yaml

# Install the relay client (in your private network)
helm install gatekeeper-relay gatekeeper/gatekeeper-relay -f your-relay-values.yaml
```

### From Source

```bash
make build
./bin/gatekeeperd -config /path/to/config.yaml -listen :8080
```

## Usage

Metrics are available at http://localhost:9090/metrics (configurable via metrics_port in config). A health check endpoint is available at http://localhost:9090/health.

See config/example.yaml for a complete configuration example.

## Project Structure

```
cmd/gatekeeperd/main.go      Entry point
internal/config/             Configuration loading
internal/ipfilter/           IP allowlist matching and dynamic fetching
internal/verifier/           Signature verification
internal/proxy/              HTTP handling and forwarding
internal/server/             ACME TLS server
internal/metrics/            Prometheus metrics
config/example.yaml          Example configuration
k8s/                         Kubernetes manifests
charts/gatekeeper/           Helm chart
```

## Development

### Prerequisites

- Go 1.25 or later
- Docker (for container builds and local testing)
- kubectl and minikube (for Kubernetes testing, optional)

### Initial Setup

Clone the repository and install development tools:

```bash
git clone https://github.com/tight-line/gatekeeper.git
cd gatekeeper

# Install required Go tools (golangci-lint, goimports)
make tools

# Install git pre-commit hook for linting
make setup-hooks
```

### Common Commands

```bash
# Build all binaries
make build-all

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

### Running Locally

#### Server only (direct forwarding mode)

```bash
# Build and run with example config
make run

# Or manually:
./bin/gatekeeperd -config config/example.yaml -listen :8080
```

The server listens on port 8080. Send test webhooks:

```bash
curl -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -d '{"test": "data"}'
```

#### Server with relay client

Terminal 1 (server):
```bash
make run
```

Terminal 2 (relay client):
```bash
make run-relay
```

### Running with Docker

#### Server only

```bash
# Build images
make docker

# Run with docker-compose
docker-compose up
```

#### Server with relay client

```bash
docker-compose --profile relay up
```

This starts:
- gatekeeperd on port 8080 (HTTP) and 9090 (metrics)
- gatekeeper-relay connecting to the server
- A mock backend for testing webhook delivery

Test it:

```bash
# Direct forwarding
curl -X POST http://localhost:8080/webhook -d '{"test":"direct"}'

# Via relay
curl -X POST http://localhost:8080/relay -d '{"test":"relay"}'
```

### Running in Kubernetes (minikube)

#### Server only

```bash
# Start minikube
minikube start

# Apply manifests
kubectl apply -k k8s/

# Get the service URL
minikube service gatekeeperd --url
```

#### Server with relay client (Helm)

```bash
# Add any required secrets
kubectl create secret generic gatekeeper-secrets \
  --from-literal=relay-token=your-token

# Install server
helm install gatekeeperd ./charts/gatekeeperd \
  -f your-values.yaml

# Install relay client (in the target namespace/cluster)
helm install gatekeeper-relay ./charts/gatekeeper-relay \
  -f your-relay-values.yaml
```

### Pre-commit Hook

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

### CI Pipeline

The GitHub Actions CI pipeline runs on every push and PR:

1. **Lint**: Runs golangci-lint
2. **Test**: Runs tests with race detection and coverage
3. **Coverage**: Enforces 100% test coverage
4. **Build**: Builds binaries and Docker images

All checks must pass before merging.

## Production TLS

For public deployments, TLS termination is required. There are two approaches depending on your infrastructure.

### Baremetal / VM Deployments

Use the `-tls` flag to enable automatic certificate provisioning via Let's Encrypt:

```bash
./bin/gatekeeperd -config /path/to/config.yaml -tls
```

Requirements:
- The server must be publicly accessible on ports 80 and 443
- DNS A records for all configured hostnames must point to the server
- Port 80 is used for ACME HTTP-01 challenges (certificate validation)
- Port 443 serves HTTPS traffic

With Docker:

```bash
docker run -p 80:80 -p 443:443 -p 9090:9090 \
  -v /path/to/config.yaml:/etc/gatekeeper/config.yaml \
  -v /path/to/cert-cache:/var/cache/gatekeeper/certs \
  gatekeeper -tls
```

The certificate cache directory persists certificates across restarts to avoid rate limits.

### Kubernetes Deployments

In Kubernetes, use an Ingress controller with cert-manager for TLS:

1. Install cert-manager:

```bash
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.14.0/cert-manager.yaml
```

2. Create a ClusterIssuer for Let's Encrypt:

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: your-email@example.com
    privateKeySecretRef:
      name: letsencrypt-prod
    solvers:
    - http01:
        ingress:
          class: nginx
```

3. Configure Ingress with TLS:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: gatekeeperd
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
spec:
  ingressClassName: nginx
  tls:
  - hosts:
    - webhooks.example.com
    secretName: gatekeeperd-tls
  rules:
  - host: webhooks.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: gatekeeperd
            port:
              number: 8080
```

The gatekeeperd service runs in HTTP mode (`-listen :8080`), and the Ingress controller handles TLS termination.

## Contributing

See AGENTS.md for code style guidelines and testing requirements.
