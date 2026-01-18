# Implementation Status

Current state of gatekeeper development and planned work.

## Completed Features

Core functionality is complete and production-ready:

- YAML configuration with environment variable interpolation for secrets
- Static IP allowlist filtering with CIDR support
- Dynamic IP range fetching from any JSON endpoint using jq queries (AWS, Google Cloud, etc.)
- Provider-specific verifiers: Slack, GitHub, Shopify
- Generic verifiers: HMAC (configurable algorithm/encoding), API key
- **Payload schema validation**: JSON Schema validation of webhook payloads (optional per route)
- HTTP request forwarding (transparent proxy)
- Relay mode for private networks without inbound firewall rules
- Host header preservation option
- Segment-aware path routing with prefix matching
- Automatic TLS certificate provisioning via ACME/Let's Encrypt
- Prometheus metrics endpoint with request counts, latencies, and error tracking
- Health check endpoint for Kubernetes probes
- Structured JSON logging
- Graceful shutdown on SIGINT/SIGTERM
- Kubernetes manifests and Helm charts for both server and relay client
- 100% test coverage
- VCR-like test recording system for provider payloads (internal/testutil)
- Interactive route configuration via Claude Code skill (/configure-route)

## Verifiers vs Validators

Gatekeeper distinguishes between two types of request validation:

| Component | Purpose | Checks | Failure Response |
|-----------|---------|--------|------------------|
| **Verifier** | Authentication | Signature, token, timestamp | HTTP 401 Unauthorized |
| **Validator** | Structure validation | JSON Schema compliance | HTTP 400 Bad Request |

**Request processing order:**
1. IP allowlist check → 403 Forbidden if blocked
2. Signature verification (verifier) → 401 Unauthorized if invalid
3. Payload validation (validator) → 400 Bad Request if malformed
4. Forward to destination

Both verifiers and validators are optional per route. A route can have neither, either, or both.

## Planned Providers

See [PROVIDER_TODO.md](PROVIDER_TODO.md) for the full list of webhook providers we plan to support, organized by priority.

High-priority providers include:
- Stripe (payments)
- GitLab (DevOps)
- Twilio (communication)
- Linear (project management)
- Discord (communication)

## Contributing

We welcome contributions for new provider implementations, bug fixes, and feature improvements. See the main README for contribution guidelines.
