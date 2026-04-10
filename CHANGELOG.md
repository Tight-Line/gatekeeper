# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.10] - 2026-04-09

### Added
- `preserve_path` channel option for relay client: when set to `false`, the relay forwarder uses the destination URL verbatim instead of appending the original webhook path. Useful when the destination already contains the full target path. Default behavior (path appending) is unchanged.

## [0.2.9] - 2026-04-09

### Added
- `oidc` verifier type for OIDC JWT bearer token authentication. Supports any OIDC-compliant identity provider (Google, Azure AD, etc.) via issuer discovery or explicit JWKS URI configuration. Handles both standard JWK Set format and Google X.509 certificate map format. Configurable audience, required claims, and automatic key caching with refresh.

## [0.2.8] - 2026-02-17

### Added
- Rate limiting support with token bucket algorithm: configure global and per-route rate limiters with total and per-IP limits, burst allowance, and automatic cleanup of stale entries. Returns HTTP 429 with Retry-After header when exceeded. New metric: `gatekeeper_rate_limited_total{route,limiter,reason}`
- Helm chart support for rate limiting: `rateLimiters`, `defaultRateLimiter`, and per-route `rateLimiter` values

## [0.2.7] - 2026-02-10

### Added
- `gitlab` verifier type for GitLab webhook authentication via `X-Gitlab-Token` header
- `gitlab` predefined IP allowlist for GitLab.com webhook source IPs (`34.74.90.64/28`, `34.74.226.0/24`)

## [0.2.6] - 2026-01-27
### Added
- Microsoft Graph subscription validation handling: automatically responds to `validationToken` query parameter on `json_field` verifier routes, enabling webhook setup without backend involvement (similar to Slack URL verification)

## [0.2.5] - 2026-01-27

### Added
- Debug payload logging for troubleshooting: dumps request/response bodies to stdout when enabled via CLI flag (`-debug-payloads`), environment variable (`GATEKEEPERD_DEBUG_PAYLOADS=true` or `GATEKEEPER_RELAY_DEBUG_PAYLOADS=true`), or Helm values (`debug.payloads: true`)

## [0.2.4] - 2026-01-23

### Added
- `query_param` verifier type for validating a query parameter in the request URL
- `header_query_param` verifier type for parsing a header value as query string format and validating a named parameter (e.g., Google's `X-Goog-Channel-Token` with `secret=...` format)

## [0.2.3] - 2026-01-23

### Fixed
- Helm chart configmap template now supports `json_field` verifier type

## [0.2.2] - 2026-01-23

### Added
- `json_field` verifier type for providers that embed verification tokens in the JSON body (e.g., Microsoft Graph `clientState`). Supports auto-parsing nested JSON strings for paths like `value.0.clientState.secret`
- `microsoft-graph` predefined IP allowlist for Microsoft Graph Change Notifications (Outlook Calendar, OneDrive, etc.)
- `/healthz` health check endpoint on the main HTTP port for ingress/gateway probes

### Fixed
- Case-insensitive hostname matching per RFC 7230 (fixes routing failures with uppercase Host headers from some load balancers)

## [0.2.1] - 2026-01-22

### Fixed
- Strip internal `X-Relay-Stream-ID` header before forwarding to destinations
- Log `client_ip` instead of `remote_addr` for forwarded/relayed requests (shows real client IP, not load balancer)
- Release workflow adds Valkey Helm repo before chart-releaser runs

## [0.2.0] - 2026-01-22

### Added
- Redis/Valkey support for multi-replica relay deployments with at-most-once delivery guarantees
- Pending message recovery for Redis relay mode (reclaims stuck messages after 60s idle)
- Concurrent webhook processing in relay client with configurable worker pool (`workers` config)
- Prometheus metrics for relay operations: `gatekeeper_relay_webhooks_queued_total`, `gatekeeper_relay_webhooks_delivered_total`, `gatekeeper_relay_delivery_errors_total`, `gatekeeper_relay_webhooks_pending`, `gatekeeper_relay_clients_connected`, `gatekeeper_relay_delivery_duration_seconds`
- Helm chart support for bundled Valkey subchart or external Redis connection
- Helm chart validation fails when `replicaCount > 1` with relay routes but Redis not enabled

## [0.1.11] - 2026-01-22

### Changed
- Release workflow now publishes amd64 images first for fast availability, then updates to multi-arch

## [0.1.10] - 2026-01-22

### Added
- `make check` target for pre-release verification (lint, 100% coverage, build)
- `make test-coverage-check` target that fails if coverage is below 100%
- Pre-release checklist in AGENTS.md documentation

### Fixed
- Missing test coverage for X-Forwarded-For edge cases (empty entries, single private IP)

## [0.1.9] - 2026-01-22

### Fixed
- X-Forwarded-For parsing now skips private/internal IPs to find the real public client IP, fixing incorrect client IP detection behind GCP load balancers and similar infrastructure

## [0.1.8] - 2026-01-21

### Added
- Gateway resource template with `gateway.create=true` option for proper per-hostname TLS certificate handling
- Multiple Gateways can share the same Traefik LoadBalancer IP with independent TLS configs

## [0.1.7] - 2026-01-21

### Added
- Certificate resource template for Gateway API with cert-manager integration

## [0.1.6] - 2026-01-21
- Add OCI source label to link GHCR packages to repository

## [0.1.4] - 2026-01-21

### Added
- Gateway API (HTTPRoute) support in gatekeeperd Helm chart as alternative to Ingress
- Slack URL verification challenges are handled directly by gatekeeper, eliminating the need for backend services to respond within Slack's 3-second timeout
- Predefined IP allowlists for common webhook providers (AWS, Google, Azure Bot Service, GitHub, Salesforce)

## [0.1.3] - 2026-01-21

### Changed
- Helm chart default image repositories now point to GHCR (ghcr.io/tight-line/*)

### Removed
- Docker Hub workflow (images are published to GHCR only)

## [0.1.2] - 2026-01-21

### Changed
- Helm charts now publish only on version tags, not on every push to main

## [0.1.1] - 2026-01-21

### Added
- Relay client logs "connected to server" on successful connection
- Relay client logs "connection recovered" after recovering from failures
- Minikube testing guide with step-by-step instructions
- Minikube Helm values files for local development

### Fixed
- Docker security: non-root user with cap_net_bind_service
- SonarCloud security issues and code quality warnings
- Release workflow lowercase repository owner for GHCR

## [0.1.0] - 2025-01-18

### Added
- Initial release
- Webhook proxy server (gatekeeperd) with signature verification
- Support for Slack, GitHub, Shopify, and generic HMAC verification
- IP allowlist filtering with static CIDRs and dynamic fetching
- JSON Schema payload validation
- Direct forwarding to backend services
- Relay delivery for private networks
- Relay client (gatekeeper-relay) for polling and forwarding
- ACME TLS certificate management
- Prometheus metrics
- Helm charts for Kubernetes deployment
- Docker images for both components
