# Usage

This document covers command-line usage for both gatekeeper binaries.

## gatekeeperd

The main webhook proxy server.

### Command Line Flags

```
gatekeeperd [flags]

Flags:
  -config string
        Path to configuration file (default "./gatekeeperd.yaml")
        Ignored if GATEKEEPERD_CONFIG environment variable is set.

  -listen string
        Address to listen on in HTTP mode (default ":8080")
        Examples: ":8080", "0.0.0.0:8080", "127.0.0.1:8080"

  -tls
        Enable ACME TLS mode. Automatically obtains certificates from
        Let's Encrypt for all configured hostnames. Requires ports 80
        (for ACME challenges) and 443 (for HTTPS traffic).

  -trust-x-forwarded-for
        Trust the X-Forwarded-For header for determining client IP.
        Enable this when running behind an ingress controller or
        L7 load balancer. See docs/X_FORWARDED_FOR.md for details.
```

### Environment Variables

| Variable | Description |
|----------|-------------|
| `GATEKEEPERD_CONFIG` | Full YAML configuration content. When set, the `-config` flag is ignored. Used by Helm charts to inject ConfigMap content. |
| `GATEKEEPERD_TRUST_X_FORWARDED_FOR` | Set to `true` to trust X-Forwarded-For header. Equivalent to `-trust-x-forwarded-for` flag. |

### Configuration File

See `config/example.yaml` for a complete configuration reference. Key sections:

```yaml
global:
  acme_email: "certs@example.com"      # Email for Let's Encrypt notifications
  acme_cache_dir: "/var/cache/certs"   # Directory to cache certificates
  metrics_port: 9090                   # Port for /metrics and /health endpoints
  log_level: info                      # Log level: debug, info, warn, error

ip_allowlists:
  # Static CIDR list
  internal:
    cidrs:
      - "10.0.0.0/8"
      - "192.168.0.0/16"

  # Dynamic list fetched from URL
  aws:
    fetch_url: "https://ip-ranges.amazonaws.com/ip-ranges.json"
    fetch_jq: ".prefixes[].ip_prefix"
    refresh_interval: 24h

verifiers:
  my-slack:
    type: slack
    signing_secret: "${SLACK_SIGNING_SECRET}"
    max_timestamp_age: 5m

# Note: Gatekeeper automatically handles Slack URL verification challenges.
# When Slack sends a url_verification request during webhook setup,
# gatekeeper responds immediately with the challenge - your backend
# does not need to handle this. This works in both direct and relay modes.

validators:
  slack-event:
    type: json_schema
    schema_file: "schemas/slack/event_callback.json"

routes:
  - hostname: webhooks.example.com
    path: /slack
    ip_allowlist: aws
    verifier: my-slack
    validator: slack-event           # Optional: validate payload structure
    destination: http://backend:8080/webhooks/slack
```

### Verifiers vs Validators

Gatekeeper distinguishes between two types of request validation:

| Concept | Purpose | Failure Response |
|---------|---------|------------------|
| **Verifier** | Authenticates requests (proves origin) | HTTP 401 Unauthorized |
| **Validator** | Validates payload structure | HTTP 400 Bad Request |

**Verifiers** answer: "Did this request really come from Slack/GitHub/etc.?"
- Check cryptographic signatures (HMAC)
- Verify API keys or tokens
- Validate timestamps to prevent replay attacks

**Validators** answer: "Is this payload well-formed and safe to process?"
- Check required fields are present
- Validate field types (string, number, array)
- Ensure payload matches expected structure

Both are optional per route. When both are configured, verification runs first, then validation.

### Validator Configuration

Validators use JSON Schema to define expected payload structure.

**Using a schema file:**

```yaml
validators:
  slack-event:
    type: json_schema
    schema_file: "schemas/slack/event_callback.json"
```

**Using an inline schema:**

```yaml
validators:
  simple:
    type: json_schema
    schema: |
      {
        "type": "object",
        "required": ["id", "event"],
        "properties": {
          "id": {"type": "string"},
          "event": {"type": "string"}
        }
      }
```

### Pre-built Schemas

Gatekeeper includes pre-built JSON schemas in the `schemas/` directory:

```
schemas/
├── slack/
│   ├── event_callback.json    # Slack Events API
│   └── url_verification.json  # Slack URL verification challenge
├── github/
│   └── push.json              # GitHub push events
└── shopify/
    └── orders_create.json     # Shopify order created webhook
```

To use a pre-built schema, reference it in your validator configuration:

```yaml
validators:
  github-push:
    type: json_schema
    schema_file: "schemas/github/push.json"
```

### Creating Custom Schemas

For event types not covered by the pre-built schemas, create your own JSON Schema file:

1. Examine sample payloads from your provider's documentation
2. Create a JSON Schema file (use Draft 2020-12 or earlier)
3. Reference it in your validator configuration

Example custom schema for a webhook with `id` and `timestamp`:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["id", "timestamp"],
  "properties": {
    "id": {
      "type": "string",
      "pattern": "^[a-f0-9-]{36}$"
    },
    "timestamp": {
      "type": "string",
      "format": "date-time"
    }
  }
}
```

### Examples

**HTTP mode (development or behind ingress):**

```bash
./bin/gatekeeperd -config ./config.yaml -listen :8080
```

**ACME TLS mode (direct internet exposure):**

```bash
./bin/gatekeeperd -config ./config.yaml -tls
```

**Behind an ingress controller:**

```bash
./bin/gatekeeperd -config ./config.yaml -listen :8080 -trust-x-forwarded-for
```

**Using environment variable for config:**

```bash
export GATEKEEPERD_CONFIG="$(cat config.yaml)"
./bin/gatekeeperd -listen :8080
```

### Endpoints

**Main HTTP port (8080 or -listen):**

| Endpoint | Description |
|----------|-------------|
| `/*` | Webhook routes (configured paths) |
| `/healthz` | Health check for ingress/gateway probes (returns 200 OK, hostname-agnostic) |
| `/relay/poll` | Relay client polling endpoint |
| `/relay/response` | Relay client response endpoint |

**Metrics port (9090, configurable via `global.metrics_port`):**

| Endpoint | Description |
|----------|-------------|
| `/metrics` | Prometheus metrics |
| `/health` | Health check (returns 200 OK) |

**Health Check Configuration:**

When deploying behind an ingress controller or gateway, configure health probes to use `/healthz` on the main HTTP port. This endpoint responds with 200 OK regardless of the Host header, making it suitable for load balancer health checks.

For Kubernetes deployments:
- **Liveness/readiness probes** use `/health` on the metrics port (configured automatically by the Helm chart)
- **Ingress/gateway backend health checks** should use `/healthz` on the main HTTP port

---

## gatekeeper-relay

The relay client for receiving webhooks in private networks.

### Command Line Flags

```
gatekeeper-relay [flags]

Flags:
  -config string
        Path to configuration file (default "./gatekeeper-relay.yaml")
        Ignored if GATEKEEPER_RELAY_CONFIG environment variable is set.
```

### Environment Variables

| Variable | Description |
|----------|-------------|
| `GATEKEEPER_RELAY_CONFIG` | Full YAML configuration content. When set, the `-config` flag is ignored. |

### Configuration File

See `config/relay-client-example.yaml` for a complete reference:

```yaml
# The gatekeeper server to connect to
server: "https://webhooks.example.com"

# Optional: Maximum consecutive failures before giving up (default: 10)
# Set to 0 for unlimited retries
max_consecutive_failures: 10

# Channels to poll for webhooks
channels:
  - name: slack-webhooks           # Descriptive name for logging
    token: "${RELAY_TOKEN_SLACK}"  # Must match relay_token in server config
    destination: "http://localhost:8080/webhooks/slack"

  - name: github-webhooks
    token: "${RELAY_TOKEN_GITHUB}"
    destination: "http://localhost:8080/webhooks/github"
```

### How It Works

1. The relay client connects outbound to the gatekeeper server (HTTPS)
2. For each configured channel, it long-polls the `/relay/poll` endpoint
3. When a webhook arrives at the server and passes validation, it's queued
4. The relay client receives the webhook and forwards it to the local destination
5. The relay client reports success/failure back to the server via `/relay/response`
6. The server returns the appropriate HTTP status to the original webhook sender

This architecture means:
- No inbound firewall rules required in the private network
- Only outbound HTTPS connections from relay client to server
- Webhooks are validated before entering the private network

### Examples

**Basic usage:**

```bash
./bin/gatekeeper-relay -config ./relay-config.yaml
```

**Using environment variable for config:**

```bash
export GATEKEEPER_RELAY_CONFIG="$(cat relay-config.yaml)"
./bin/gatekeeper-relay
```

### Logging

Both binaries output structured JSON logs to stdout:

```json
{"time":"2024-01-15T10:30:00Z","level":"INFO","msg":"starting gatekeeperd","version":"0.1.0"}
{"time":"2024-01-15T10:30:00Z","level":"INFO","msg":"config loaded from file","path":"./config.yaml","routes":3,"verifiers":2,"ip_allowlists":2}
```

Use log aggregation tools (Loki, Elasticsearch, CloudWatch) to collect and query logs in production.

---

## Interactive Route Configuration (Claude Code)

If you use [Claude Code](https://github.com/anthropics/claude-code), gatekeeper includes an interactive skill to help configure new webhook routes.

### Usage

Invoke the skill with:

```
/configure-route
```

Or simply ask: "I want to configure a webhook for Slack"

### What It Does

The skill walks you through configuring a new webhook route step by step:

1. **Provider selection** - Slack, GitHub, Shopify, Google Calendar, or generic HMAC/API key
2. **External hostname** - The public DNS name for receiving webhooks
3. **Delivery mode** - Direct forwarding or relay client
4. **Internal destination** - Where webhooks should be delivered

After gathering this information, it generates:

- Complete `gatekeeperd.yaml` configuration stanza
- Relay client configuration (if using relay mode)
- Provider-specific setup instructions (where to find signing secrets, etc.)
- Environment variable summary

### Supported Providers

| Provider | Verifier Type | Setup Guidance |
|----------|---------------|----------------|
| Slack | `slack` | Events API, signing secret location. URL verification handled automatically. |
| GitHub | `github` | Repository/org webhook setup |
| Shopify | `shopify` | Admin panel webhook configuration |
| Google Calendar | `api_key` | Calendar API watch requests |
| Generic HMAC | `hmac` | Configurable algorithm/encoding |
| API Key | `api_key` | Simple header token |

### Example Session

```
You: /configure-route

Claude: Which webhook provider do you want to configure?
- Slack
- GitHub
- Shopify
- Google Calendar
- Generic HMAC
- API Key

You: Slack

Claude: What hostname will Slack send webhooks to?

You: slack-webhooks.example.com

Claude: How should webhooks be delivered to your internal service?
- Direct forwarding (requires firewall rule)
- Relay client (no inbound firewall needed)

You: Relay client

Claude: What is the internal URL where webhooks should be delivered?
Suggested: http://your-app:8080/webhooks/slack

You: http://internal.example.com:8080/webhooks/slack

Claude: Here's your configuration...
[generates complete config for both server and relay client]
```
