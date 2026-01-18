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

## Coding Standards

See [docs/CODING_STANDARDS.md](docs/CODING_STANDARDS.md) for:
- Documentation writing style
- Go code style guidelines
- Package organization
- Testing requirements (100% coverage)
- Security considerations
- Common workflows (adding verifiers, modifying config)

## Architecture Notes

### Request Flow

1. TLS termination (autocert or ingress)
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
    +--------+   HTTPS   +-------------+    :    +-----------------+
    | Slack  |---------->| gatekeeperd |--(*)-->| Internal API    |
    | GitHub |           |             |    :    | 10.1.2.3:8080   |
    +--------+           |  verify     |    :    +-----------------+
                         |  + forward  |    :
                         +-------------+    :
                                            :
    Route config:                           :
      destination: http://10.1.2.3:8080     :


(b) Relay Mode (relay_token:)
    No inbound firewall rule needed. Relay client connects outbound.

                              Firewall
    +--------+   HTTPS   +-------------+    :    +------------------+
    | Slack  |---------->| gatekeeperd |    :    | gatekeeper-relay |
    | GitHub |           |             |<--(*)----|                  |
    +--------+           |  verify     |    :    |  poll + forward  |
                         |  + queue    |    :    +--------+---------+
                         +-------------+    :             |
                                            :             v
    Route config:                           :    +-----------------+
      relay_token: ${TOKEN}                 :    | Internal API    |
                                            :    | localhost:8080  |
                                            :    +-----------------+

    (*) = direction of connection initiation
```

In direct mode, gatekeeperd forwards verified webhooks through an open firewall port to the internal API.

In relay mode, the relay client inside the private network initiates an outbound HTTPS connection to gatekeeperd and long-polls for webhooks. When a webhook arrives, gatekeeperd queues it until the relay client retrieves it, then the relay client forwards it to the local destination.

### Verifier Types

| Type | Provider | Algorithm |
|------|----------|-----------|
| slack | Slack | HMAC-SHA256 of "v0:{timestamp}:{body}" |
| github | GitHub | HMAC-SHA256 of body, hex encoded |
| shopify | Shopify | HMAC-SHA256 of body, base64 encoded |
| hmac | Generic | Configurable HMAC (SHA256/SHA512, hex/base64) |
| api_key | Google Calendar | Header token comparison |
| noop | Testing | Always succeeds |

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

## Current Implementation Status

See [docs/IMPLEMENTATION_STATUS.md](docs/IMPLEMENTATION_STATUS.md) for current status and planned work.

See [docs/PROVIDER_TODO.md](docs/PROVIDER_TODO.md) for the list of webhook providers we plan to support.

## Provider Development

See [docs/PROVIDER_DEVELOPMENT.md](docs/PROVIDER_DEVELOPMENT.md) for the step-by-step guide to developing new webhook providers or improving existing ones. This includes:

- Deploying gatekeeperd to capture real webhooks
- Creating and organizing test recordings
- Developing verifiers and validators from real payloads
- End-to-end testing workflow

**Maintenance note**: Keep PROVIDER_DEVELOPMENT.md up-to-date when adding new features that affect the provider development workflow (e.g., new recording formats, new verifier types, changes to the testutil package).
