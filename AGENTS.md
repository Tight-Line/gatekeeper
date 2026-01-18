# AGENTS.md

This file provides context and instructions for AI coding agents working on gatekeeper.

## Project Summary

gatekeeper is a webhook authentication proxy for enterprise environments that cannot expose internal services directly to the internet. It validates incoming webhooks from SaaS providers (Slack, Google, GitHub, Shopify) using provider-specific signature verification, filters by source IP, and forwards authenticated requests to internal backends.

## Build and Test Commands

```bash
# Build all binaries (gatekeeperd and gatekeeper-relay)
make build-all

# Build only the server
make build

# Build only the relay client
make build-relay

# Run all tests
make test

# Run tests with coverage report
make test-coverage

# Run locally with example config
make run

# Build Docker image
make docker
```

## Code Style Guidelines

### Writing Style for Documentation

When writing or updating documentation in this project:

- Do not use emojis
- Do not use emdashes or endashes (use commas, periods, or "to" for ranges)
- Avoid excessive bullet lists; prefer prose where it improves readability
- Keep diagrams minimal; only include them where they clarify complex flows
- Write in a direct, technical tone suitable for engineers
- Avoid marketing language or superlatives

### Go Code Style

- Follow standard Go conventions (gofmt, golint)
- Use meaningful variable names; avoid single-letter names except for loop indices
- Keep functions focused on a single responsibility
- Add comments for exported functions and types
- Error messages should be lowercase and not end with punctuation
- Use structured logging (slog) with consistent field names

### Package Organization

```
cmd/gatekeeperd/       Server entry point, CLI flags, wiring
cmd/gatekeeper-relay/  Relay client entry point
internal/config/       YAML parsing, validation, env var interpolation
internal/ipfilter/     CIDR parsing and IP matching
internal/verifier/     Webhook signature verification implementations
internal/proxy/        HTTP handler and request forwarding
internal/relay/        Relay server: manager and HTTP handler
internal/relayclient/  Relay client: config, poller, forwarder
internal/server/       ACME TLS server
internal/metrics/      Prometheus metrics
```

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
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | grep -v "100.0%"  # Show uncovered code
go tool cover -html=coverage.out                      # Visual coverage report
```

## Security Considerations

- Never log request bodies or secrets
- Use constant-time comparison for all signature verification
- Validate timestamps to prevent replay attacks (where supported by provider)
- IP allowlists default to deny (fail closed) when misconfigured

## Architecture Notes

### Request Flow

1. TLS termination (autocert)
2. Route lookup by hostname and path
3. IP validation against configured allowlist
4. Signature verification using provider-specific algorithm
5. Either:
   - Forward to destination (transparent proxy), or
   - Deliver via relay to waiting relay client
6. Log result with minimal information (IP, path, success/failure)

### Delivery Modes

Gatekeeper supports two delivery modes: direct forwarding and relay.

```
(a) Direct Forwarding (destination:)
    Requires inbound firewall rule to allow traffic from gatekeeperd

                              Firewall
    ┌────────┐   HTTPS   ┌─────────────┐    :    ┌─────────────────┐
    │ Slack  │──────────>│ gatekeeperd │--(*)-->│ Internal API    │
    │ GitHub │           │             │    :    │ 10.1.2.3:8080   │
    └────────┘           │  verify     │    :    └─────────────────┘
                         │  + forward  │    :
                         └─────────────┘    :
                                            :
    Route config:                           :
      destination: http://10.1.2.3:8080     :


(b) Relay Mode (relay_token:)
    No inbound firewall rule needed. Relay client connects outbound.

                              Firewall
    ┌────────┐   HTTPS   ┌─────────────┐    :    ┌──────────────────┐
    │ Slack  │──────────>│ gatekeeperd │    :    │ gatekeeper-relay │
    │ GitHub │           │             │<--(*)-──│                  │
    └────────┘           │  verify     │    :    │  poll + forward  │
                         │  + queue    │    :    └────────┬─────────┘
                         └─────────────┘    :             │
                                            :             v
    Route config:                           :    ┌─────────────────┐
      relay_token: ${TOKEN}                 :    │ Internal API    │
                                            :    │ localhost:8080  │
                                            :    └─────────────────┘

    (*) = direction of connection initiation
```

In direct mode, gatekeeperd forwards verified webhooks through an open firewall port to the internal API.

In relay mode, the relay client inside the private network initiates an outbound HTTPS connection to gatekeeperd and long-polls for webhooks. When a webhook arrives, gatekeeperd queues it until the relay client retrieves it, then the relay client forwards it to the local destination.

Configuration example for gatekeeperd (relay mode):
```yaml
routes:
  - hostname: webhooks.example.com
    path: /slack
    verifier: slack
    relay_token: "${RELAY_TOKEN_SLACK}"
```

Configuration example for gatekeeper-relay:
```yaml
server: https://webhooks.example.com
channels:
  - name: slack
    token: "${RELAY_TOKEN_SLACK}"
    destination: http://localhost:8080/webhooks/slack
```

### Verifier Types

| Type | Provider | Algorithm |
|------|----------|-----------|
| slack | Slack | HMAC-SHA256 of "v0:{timestamp}:{body}" |
| github | GitHub | HMAC-SHA256 of body, hex encoded |
| shopify | Shopify | HMAC-SHA256 of body, base64 encoded |
| hmac | Generic | Configurable HMAC (SHA256/SHA512, hex/base64) |
| api_key | Google Calendar | Header token comparison |
| noop | Testing | Always succeeds |

### TLS Certificate Management

Gatekeeperd supports two TLS modes depending on your deployment environment.

**Option 1: Internal ACME (single instance or bare metal)**

Gatekeeperd has built-in ACME support using the `-tls` flag. It automatically obtains and renews certificates from Let's Encrypt.

```bash
# Command line
./gatekeeperd -config ./gatekeeperd.yaml -tls

# Config file
global:
  acme_email: "certs@example.com"
  acme_cache_dir: "/var/cache/gatekeeper/certs"
```

Use this when:
- Running a single instance (VM, bare metal, single container)
- You have direct control over ports 80 and 443
- You don't have an external load balancer or ingress controller

**Option 2: External TLS with Ingress and cert-manager (Kubernetes)**

For Kubernetes deployments, especially with multiple replicas, use an Ingress controller with cert-manager. Gatekeeperd runs in HTTP mode behind the Ingress.

```
                                      cert-manager
                                      (cluster add-on)
                                      ┌─────────────────┐
                                      │ 1. Watches for  │
                                      │    Ingress with │
                                      │    annotation   │
                                      │                 │
                                      │ 2. Performs     │
                                      │    ACME challenge│
                                      │                 │
                                      │ 3. Stores cert  │
                                      │    in Secret    │
                                      └────────┬────────┘
                                               │
                                               v
┌────────┐       ┌──────────────┐       ┌─────────────┐
│ Slack  │─HTTPS─│   Ingress    │──HTTP─│ gatekeeperd │ x N replicas
│ GitHub │       │  Controller  │       │ (port 8080) │
└────────┘       │              │       └─────────────┘
                 │ TLS from     │
                 │ Secret       │
                 └──────────────┘
```

Setup steps:

1. Install cert-manager in your cluster:
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
       email: certs@example.com
       privateKeySecretRef:
         name: letsencrypt-prod-key
       solvers:
         - http01:
             ingress:
               class: nginx
   ```

3. Deploy gatekeeperd with Ingress enabled:
   ```yaml
   # values.yaml
   replicaCount: 3

   tls:
     enabled: false  # Disable internal ACME

   ingress:
     enabled: true
     className: nginx
     annotations:
       cert-manager.io/cluster-issuer: letsencrypt-prod
     tls:
       enabled: true
       # secretName: optional - if omitted, each host gets "{hostname}-tls"

   routes:
     - hostname: webhooks.example.com
       path: /slack
       verifier: slack-verifier
       destination: http://backend:8080/slack
   ```

The Ingress hostnames are automatically derived from your routes configuration - no need to duplicate them. cert-manager watches for Ingress resources with its annotation, performs the ACME challenge, and stores the certificate in the Secret. The Ingress controller reads from that Secret to terminate TLS. Gatekeeperd replicas run plain HTTP on port 8080.

**Why internal ACME doesn't work with multiple replicas:**

Internal ACME stores certificates in a local directory (PVC in Kubernetes). With multiple replicas:
- Each replica would try to obtain its own certificate
- Let's Encrypt has rate limits that would be hit
- ReadWriteOnce PVC can only be mounted by one pod

You could use ReadWriteMany storage (NFS, EFS) to share the cert cache, but Ingress + cert-manager is the standard Kubernetes pattern.

### Configuration Loading

Configuration can be loaded from file or from environment variables:

| Binary | Env Var | Default File |
|--------|---------|--------------|
| gatekeeperd | `GATEKEEPERD_CONFIG` | `./gatekeeperd.yaml` |
| gatekeeper-relay | `GATEKEEPER_RELAY_CONFIG` | `./gatekeeper-relay.yaml` |

If the env var is set (contains full YAML), the file path is ignored. The Helm charts use the env var approach to inject ConfigMap content directly.

### Configuration

Configuration uses YAML with environment variable interpolation. Secrets should never appear in config files directly; use ${VAR_NAME} syntax.

Dynamic IP allowlists use jq queries to extract CIDR strings from JSON endpoints:

```yaml
ip_allowlists:
  aws:
    fetch_url: "https://ip-ranges.amazonaws.com/ip-ranges.json"
    fetch_jq: ".prefixes[].ip_prefix"
    refresh_interval: 24h
```

## Current Implementation Status

All phases complete:
- Config loading with env var interpolation
- Static and dynamic IP allowlist filtering (jq-based extraction)
- Slack, GitHub, Shopify, generic HMAC, and API key verifiers
- HTTP forwarding (transparent proxy)
- Relay mode for private networks (gatekeeper-relay client)
- Structured JSON logging
- ACME TLS with autocert (use -tls flag)
- Prometheus metrics on configurable port
- Health check endpoint at /health on metrics port
- Kubernetes manifests
- Helm charts (gatekeeperd and gatekeeper-relay)
- Integration tests

## File Locations

- Server entry point: cmd/gatekeeperd/main.go
- Relay client entry point: cmd/gatekeeper-relay/main.go
- Config structs: internal/config/config.go
- Relay client config: internal/relayclient/config.go
- Verifier interface: internal/verifier/verifier.go
- HTTP handler: internal/proxy/handler.go
- Relay manager: internal/relay/manager.go
- Relay handler: internal/relay/handler.go
- Example config: config/example.yaml
- K8s manifests: k8s/
- Helm charts: charts/gatekeeperd/, charts/gatekeeper-relay/

## Common Workflows

### Adding a New Verifier

1. Create internal/verifier/{provider}.go implementing the Verifier interface
2. Create internal/verifier/{provider}_test.go with table-driven tests
3. Add the verifier type to internal/config/config.go Validate() function
4. Wire it up in internal/proxy/handler.go buildVerifier function
5. Update the verifier table in this file
6. Add an example to config/example.yaml

### Modifying Configuration

1. Update internal/config/config.go structs
2. Update validation in Config.Validate()
3. Update config/example.yaml with examples
4. Document new fields in this file

### Before Submitting Changes

- Run `make test` to verify tests pass
- Run `make build` to verify compilation
- Check config/example.yaml remains valid
