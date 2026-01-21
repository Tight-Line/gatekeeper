# gatekeeper

[![CI](https://github.com/tight-line/gatekeeper/actions/workflows/ci.yml/badge.svg)](https://github.com/tight-line/gatekeeper/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/tight-line/gatekeeper/graph/badge.svg?token=W6O7WA775C)](https://codecov.io/gh/tight-line/gatekeeper)
[![Quality Gate Status](https://sonarcloud.io/api/project_badges/measure?project=Tight-Line_gatekeeper&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=Tight-Line_gatekeeper)
[![Security Rating](https://sonarcloud.io/api/project_badges/measure?project=Tight-Line_gatekeeper&metric=security_rating)](https://sonarcloud.io/summary/new_code?id=Tight-Line_gatekeeper)
[![Snyk](https://snyk.io/test/github/tight-line/gatekeeper/badge.svg)](https://snyk.io/test/github/tight-line/gatekeeper)

A webhook authentication proxy for enterprise environments.

## Table of Contents

- [Motivation](#motivation)
- [Solution](#solution)
  - [Overview](#overview)
  - [How Webhooks Are Delivered](#how-webhooks-are-delivered)
  - [Validating Incoming Webhooks](#validating-incoming-webhooks)
  - [Configuration Features](#configuration-features)
- [Security Considerations](#security-considerations)
  - [Overview](#security-overview)
  - [Mitigations](#mitigations)
- [Alternatives Considered](#alternatives-considered)
- [Implementation Status](#implementation-status)
- [Installation](#installation)
- [Usage](#usage)
- [Development](#development)
- [Configuring TLS](#configuring-tls)
- [Contributing](#contributing)

---

## Motivation

Development and staging environments are often isolated on private networks or behind VPNs, intentionally unreachable from the public internet. Production applications that handle sensitive internal workflows may be similarly restricted. This is good security practice.

However, modern SaaS applications like Slack, Google Workspace, and GitHub rely on webhooks to notify your systems of events. These webhooks originate from the vendor's infrastructure and must somehow reach your private services. Production applications exposed to the public internet rarely face this problem, but private environments do.

Some vendors provide their own solutions. Stripe CLI can forward webhooks to localhost during development. BrowserStack provides a binary that tunnels browser traffic into private networks. These tools work, but only for their specific vendor.

The traditional general-purpose approach is to open firewall ports for each webhook source. This becomes problematic because:

1. Major cloud providers like AWS and Google use thousands of IP addresses that change frequently. Slack webhooks, for example, can originate from any EC2 instance in AWS.

2. Even if you maintain an up-to-date allowlist, you are trusting that any traffic from those IPs is legitimate. A compromised service sharing that IP space could send malicious requests to your webhook endpoints.

3. Managing firewall rules across multiple teams, each with their own SaaS integrations, does not scale. Webhook sprawl becomes a security and operational burden.

---

## Solution

Gatekeeper is a webhook authentication proxy. It is the only component exposed to the public internet. Your internal services never need direct internet exposure.

For each incoming webhook request, gatekeeper:

1. Checks the source IP against a configured allowlist
2. Validates the webhook signature using the provider's published algorithm
3. Optionally validates the payload structure against a schema
4. Forwards authenticated requests to internal backends

### Overview

Gatekeeper supports two delivery modes depending on your network architecture.

**Direct Forwarding** requires an inbound firewall rule from gatekeeperd to your internal service:

```
                                        Firewall
    +--------+   HTTPS   +-------------+    :    +-----------------+
    | Slack  |---------->| gatekeeperd |--(*->)->| Internal API    |
    | GitHub |           |             |    :    | 10.1.2.3:8080   |
    +--------+           |  verify     |    :    +-----------------+
                         |  + forward  |    :
                         +-------------+    :

    ----> = direction of connection initiation
    (*->) = direction of firewall hole (ingress)
```

**Relay Mode** requires no inbound firewall rules. A relay client inside your private network connects outbound to gatekeeperd and polls for verified webhooks:

```
                                        Firewall
    +--------+   HTTPS   +-------------+    :    +------------------+
    | Slack  |---------->| gatekeeperd |    :    | gatekeeper-relay |
    | GitHub |           |             |<-(<-*)--|                  |
    +--------+           |  verify     |    :    |  poll + forward  |
                         |  + queue    |    :    +--------+---------+
                         +-------------+    :             |
                                            :             v
                                            :    +-----------------+
    No inbound firewall rule needed.        :    | Internal API    |
    Only outbound HTTPS from relay client.  :    | localhost:8080  |
                                            :    +-----------------+

    ----> = direction of connection initiation
    (<-*) = direction of firewall hole (egress)
```

### How Webhooks Are Delivered

#### Direct Forwarding

In direct mode, configure a `destination` URL on your route. Gatekeeperd forwards verified webhooks immediately to that URL. This requires network reachability from gatekeeperd to the destination, typically via a firewall rule.

```yaml
routes:
  - hostname: webhooks.example.com
    path: /slack
    verifier: my-slack-verifier
    destination: http://10.1.2.3:8080/webhooks/slack
```

#### Relay Client Delivery

In relay mode, configure a `relay_token` instead of a destination. Gatekeeperd queues verified webhooks. A relay client (`gatekeeper-relay`) running inside your private network connects outbound to gatekeeperd and long-polls for webhooks matching its token. When a webhook arrives, the relay client retrieves it and forwards it to a local destination.

This architecture means your private network only needs outbound HTTPS access to gatekeeperd. No inbound firewall rules are required.

Server configuration:
```yaml
routes:
  - hostname: webhooks.example.com
    path: /slack
    verifier: my-slack-verifier
    relay_token: "${RELAY_TOKEN_SLACK}"
```

Relay client configuration:
```yaml
server: "https://gatekeeperd.example.com"
channels:
  - name: slack
    token: "${RELAY_TOKEN_SLACK}"
    destination: "http://localhost:8080/webhooks/slack"
```

### Validating Incoming Webhooks

#### Signature Verification

Different providers use different signature schemes. Slack uses HMAC-SHA256 with a timestamp-prefixed payload. GitHub uses HMAC-SHA256 directly on the body. Google Calendar uses a simple token header. Gatekeeper implements each scheme correctly, including replay attack protection where the provider supports it.

Supported verifier types:

| Type | Provider | Algorithm |
|------|----------|-----------|
| `slack` | Slack | HMAC-SHA256 of "v0:{timestamp}:{body}" with replay protection |
| `github` | GitHub | HMAC-SHA256 of body, hex encoded |
| `shopify` | Shopify | HMAC-SHA256 of body, base64 encoded |
| `hmac` | Generic | Configurable HMAC (SHA256/SHA512, hex/base64) |
| `api_key` | Google Calendar, etc. | Header token comparison |
| `noop` | Testing | Always succeeds (testing only) |

#### Payload Schema Validation

Gatekeeper supports optional JSON Schema validation of webhook payloads. This is **independent of signature verification** and provides an additional layer of defense against malformed or malicious payloads.

**Key distinction:**
- **Verifiers** authenticate requests (e.g., HMAC signature verification proves the request came from the claimed provider)
- **Validators** check payload structure (e.g., JSON Schema validation ensures the payload contains required fields with correct types)

A request must pass both verifier and validator checks (if configured) before being forwarded. Validation runs after signature verification succeeds.

Example configuration:

```yaml
validators:
  slack-event:
    type: json_schema
    schema_file: "schemas/slack/event_callback.json"

routes:
  - hostname: webhooks.example.com
    path: /slack
    verifier: my-slack-verifier    # First: verify signature
    validator: slack-event          # Then: validate payload structure
    destination: http://backend:8080/webhooks/slack
```

Schema validation is off by default. See [docs/USAGE.md](docs/USAGE.md) for detailed configuration options.

### Configuration Features

#### Configuration-Driven

All routing, verification, and allowlist configuration lives in a single YAML file. Adding a new webhook endpoint means adding a few lines of configuration. No code changes required.

```yaml
routes:
  - hostname: slack-webhooks.example.com
    path: /events
    ip_allowlist: aws
    verifier: my-slack-verifier
    destination: http://internal-service:8080/webhooks/slack
```

#### Multi-Tenant Support

Enterprise environments often have multiple teams, each with their own SaaS accounts and signing secrets. Gatekeeper supports this through its configuration structure. Each route specifies its own verifier, and verifiers are defined per team or application.

#### Path Routing

Routes are matched using segment-aware prefix matching. The route `path` defines the prefix to match, and requests to that path (and any subpaths) are forwarded.

Matching rules:

1. Exact matches are tried first
2. Prefix matches require a segment boundary (path separator `/`)
3. A route with `path: /hooks` matches `/hooks` and `/hooks/github`, but NOT `/hookshot`

Path construction when forwarding:

| Route path | Destination | Request path | Forwarded to |
|------------|-------------|--------------|--------------|
| `/hooks` | `http://backend/api` | `/hooks` | `http://backend/api` |
| `/hooks` | `http://backend/api` | `/hooks/github` | `http://backend/api/github` |
| `/` | `http://backend/api` | `/events` | `http://backend/api/events` |

#### Host Header Preservation

By default, gatekeeper sets the `Host` header of forwarded requests to match the destination hostname. Some backend applications need to see the original `Host` header. Enable `preserve_host` on a route:

```yaml
routes:
  - hostname: webhooks.example.com
    path: /events
    destination: http://internal-service:8080/webhooks
    preserve_host: true  # Backend receives Host: webhooks.example.com
```

---

## Security Considerations

### Security Overview

A webhook proxy is an attack surface on your internal network. Any service accepting traffic from the internet must be treated with care. Gatekeeper is designed to minimize this attack surface through multiple layers of validation before any request reaches your internal services.

### Mitigations

#### IP Allowlists

Gatekeeper filters incoming requests by source IP before any other processing. Allowlists can be configured as static CIDR ranges or fetched dynamically from provider IP range endpoints.

Static allowlist:
```yaml
ip_allowlists:
  internal:
    cidrs:
      - "10.0.0.0/8"
      - "192.168.0.0/16"
```

Dynamic allowlist (automatically refreshed):
```yaml
ip_allowlists:
  aws:
    fetch_url: "https://ip-ranges.amazonaws.com/ip-ranges.json"
    fetch_jq: ".prefixes[].ip_prefix"
    refresh_interval: 24h
```

When running behind an ingress controller or load balancer, gatekeeper must read the `X-Forwarded-For` header to determine the true client IP. This requires explicit opt-in via the `--trust-x-forwarded-for` flag. See [docs/X_FORWARDED_FOR.md](docs/X_FORWARDED_FOR.md) for configuration details and security considerations.

#### Payload Signature Verification

Most webhook providers sign their payloads using HMAC or similar algorithms. Gatekeeper verifies these signatures using provider-specific implementations that follow each provider's documented algorithm exactly.

Signature verification proves that:
- The payload originated from the claimed provider
- The payload has not been modified in transit
- (For providers with timestamp validation) The request is not a replay attack

For providers without a native signature scheme, the generic `hmac` verifier supports configurable algorithms (SHA256, SHA512) and encodings (hex, base64).

#### Header Token Verification

Some webhook providers use simpler authentication: a shared secret sent in a header. While less sophisticated than cryptographic signatures, header tokens still provide strong protection against attackers who do not possess the secret.

The `api_key` verifier supports this pattern. Combined with IP allowlists, token verification blocks unauthorized requests effectively.

#### Payload Schema Validation

Even with valid signatures, a compromised provider account or leaked signing key could allow an attacker to send malicious payloads that exploit vulnerabilities in your webhook handlers.

Payload schema validation provides defense against injection attacks and malformed input by validating the structure of incoming payloads against known-good schemas for each provider and event type. This limits the attack surface even if authentication is somehow bypassed.

**How it works:**

1. Define validators in your configuration (using JSON Schema)
2. Associate validators with routes
3. After signature verification passes, gatekeeper validates the payload against the schema
4. Invalid payloads are rejected with HTTP 400 before reaching your backend

Pre-built schemas are included in the `schemas/` directory for common providers (Slack, GitHub, Shopify). You can also provide inline schemas or create custom schema files for your specific event types.

#### Relay Mode (Zero Inbound Attack Surface)

Relay mode eliminates inbound firewall rules entirely. The relay client initiates all connections outbound from your private network. This means:

- No listening ports exposed to the internet on your internal network
- Webhooks must pass all configured validations (IP, signature, schema) before the relay client ever sees them
- The only attack surface is gatekeeperd itself, which performs all validation before queuing

---

## Alternatives Considered

### Convoy

Convoy (github.com/frain-dev/convoy) is an open-source webhook gateway, but it solves the opposite problem: Convoy is for *sending* webhooks, not receiving them. It helps you deliver webhooks to your customers with retry logic, event persistence, and delivery tracking. If you are building a SaaS product that needs to notify customers via webhooks, Convoy is excellent.

Gatekeeper solves the receiving side: validating and forwarding incoming webhooks from providers like Slack and GitHub to your internal services.

### API Gateways (Kong, Traefik)

General-purpose API gateways can terminate TLS and route traffic, but they do not have built-in support for provider-specific webhook signature verification. You would need to write custom plugins for each provider, at which point you have recreated this project with more complexity.

### Cloud Provider Solutions

AWS API Gateway and Google Cloud Endpoints can handle webhook ingestion, but they introduce vendor lock-in and do not solve the fundamental problem: you still need to validate signatures, and you still need to route traffic to internal services that may not be reachable from the cloud provider's network.

### Smee.io

Smee.io is a webhook relay service created by the Probot team for GitHub App development. It receives webhooks at a public URL and forwards them to a local client via Server-Sent Events.

The relay concept is similar to gatekeeper's relay mode, but Smee is a pure pass-through. It performs no signature verification, IP filtering, or payload validation. Anyone who discovers your channel URL can send arbitrary payloads to your application. Smee channels are also not authenticated, so webhook contents are visible to anyone with the channel ID.

Smee is explicitly intended for development only and is hosted as a third-party service. Gatekeeper provides proper provider-specific signature verification, is self-hosted, and supports both development and production use cases.

### Tunnel Services (ngrok, Cloudflare Tunnel, localtunnel)

General-purpose tunnel services expose your localhost to the internet via a public URL. They are useful for development and testing, but they perform no webhook-specific validation. Any traffic that reaches the public URL is forwarded to your application.

These tools solve network reachability but not authentication. You still need to verify signatures in your application code. Gatekeeper handles both: it provides network reachability (via relay mode) and authentication (via provider-specific verification) in one package.

### Webhook Relay

Webhook Relay (webhookrelay.com) is a commercial relay service with a self-hosted option. It can forward webhooks to private networks and provides a Lua scripting layer for custom logic.

However, Webhook Relay does not include built-in provider-specific verification. To validate signatures, you must write custom Lua functions for each provider, implementing the correct header parsing, algorithm, and comparison logic yourself. This is similar to the API gateway approach: possible, but you end up reimplementing provider-specific verification from scratch.

Gatekeeper provides built-in verifiers for each supported provider. You specify `type: slack` or `type: github` and the correct algorithm is applied automatically.

---

## Implementation Status

See [docs/IMPLEMENTATION_STATUS.md](docs/IMPLEMENTATION_STATUS.md) for current features and planned work.

See [docs/PROVIDER_TODO.md](docs/PROVIDER_TODO.md) for the list of webhook providers we plan to support.

---

## Installation

See [docs/INSTALL.md](docs/INSTALL.md) for installation options:

- Docker images from GitHub Container Registry
- Helm charts for Kubernetes
- Building from source
- Kubernetes manifests (kustomize)

---

## Usage

See [docs/USAGE.md](docs/USAGE.md) for detailed command-line usage:

- `gatekeeperd` flags and environment variables
- `gatekeeper-relay` configuration
- Configuration file reference
- Endpoint documentation
- Interactive route configuration with Claude Code (`/configure-route`)

---

## Development

See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for:

- Development environment setup
- Running locally (server only, with relay, with real signatures)
- Docker and Kubernetes testing
- CI pipeline

---

## Configuring TLS

See [docs/TLS.md](docs/TLS.md) for TLS configuration:

- Kubernetes with Ingress and cert-manager (recommended for production)
- Baremetal with built-in ACME/Let's Encrypt

---

## Contributing

We welcome contributions:

- **New provider implementations** - See [docs/PROVIDER_TODO.md](docs/PROVIDER_TODO.md) for the wishlist and [docs/PROVIDER_DEVELOPMENT.md](docs/PROVIDER_DEVELOPMENT.md) for a step-by-step guide to developing providers
- **Bug fixes** - Please include a test case that reproduces the bug
- **Performance improvements** - Include benchmarks showing the improvement
- **New features** - Open an issue first to discuss the design

Before submitting:

- Read [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for setup and workflow
- Read [docs/CODING_STANDARDS.md](docs/CODING_STANDARDS.md) for code style and testing requirements
- All PRs must pass CI with 100% test coverage

**A note on AI-assisted contributions**: We use AI tools in our own development and welcome others who do the same. However, PRs must demonstrate human understanding of the changes. Include clear motivation explaining *why* the change is needed, not just what it does. Explain your testing approach. Low-effort submissions that appear to be unreviewed AI output will be declined. We value quality over quantity.
