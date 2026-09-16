# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security
- Upgraded the Alpine packages in the runtime stage of both images. `alpine:3.24.1` still ships `libcrypto3` and `libssl3` at 3.5.7-r0, which carry twenty open advisories including `CVE-2026-14456` (HIGH), and `apk-tools` links against both so they are present whether or not the Dockerfile installs them. The v3.24 apk repository already has the fixed 3.5.8-r0 and there is no newer base image tag to pick it up, so the runtime stage now runs `apk upgrade` at build time. This also keeps taking patched packages whenever the repository gets ahead of the tag, instead of waiting on a 3.24.2 that may never be cut. The cost is that image contents depend on build date as well as the tag, so two builds of the same commit can differ.

## [0.2.16] - 2026-09-14

### Security
- Bumped the runtime base image from `alpine:3.23.5` to `alpine:3.24.1`, picking up the patched OS packages in the current stable Alpine line. 3.23 is supported until 2027-11-01, so this is a forward move rather than an end-of-life rescue, but it is the only way OS-level fixes in the runtime image reach a gatekeeper build.
- Bumped `step-security/harden-runner` from v2.21.0 to v2.21.1 across every workflow job. This is the action that enforces `egress-policy: block`, so it is worth keeping current on its own account.
- Bumped `github/codeql-action/upload-sarif` from v4.37.9 to v4.38.0 in the Snyk workflow.

### Fixed
- `TestServer_Shutdown_ErrorPaths` only waited for one of its two blocking handlers to start, so whichever server had no request in flight returned a nil error from `Shutdown` and left its error branch uncovered. Which of the two lost the race varied by machine, so the 100% coverage gate failed intermittently on CI while passing locally. The marker added for this in 0.2.15 sat on the HTTPS branch and did nothing when the HTTP branch was the one that came up short, which is how the alpine bump above turned up red on a Dockerfile-only diff. Each server now gets its own handler and the test waits for both before shutting down, so both branches are exercised every run. The `coverage:ignore` on the HTTPS branch is gone and `internal/server` is back to a real 100%.

### Changed
- Bumped `golangci/golangci-lint-action` from v7 to v9.3.0. The pinned `golangci-lint` version is unchanged at v2.13.2.

## [0.2.15] - 2026-09-14

### Security
- Raised the `go` directive from 1.25.6 to 1.27.1, clearing six Go standard library advisories that `govulncheck` reported as reachable from gatekeeper code: GO-2026-6218 (quadratic complexity in `net/url` path resolution, reached from webhook forwarding and JSON Schema compilation), GO-2026-6090 and GO-2026-5856 (`crypto/tls` post-handshake message flooding and an Encrypted Client Hello privacy leak, reached from the TLS listener and the Redis relay), GO-2026-6089 (`net/http` not applying `ReadHeaderTimeout` to the unencrypted HTTP/2 check, reached from the webhook and metrics listeners), GO-2026-5972 (unbounded recursion in `encoding/asn1`, reached from SendGrid public key parsing), and GO-2026-5026 (`golang.org/x/net/idna` accepting ASCII-only Punycode labels, reached from outbound HTTP). The 1.25 line is end-of-life now that Go 1.27 has shipped, so it had to move regardless of the advisories. 1.27.1 is the current release, and the `go` directive and both builder images now track it.
- Upgraded `golang.org/x/text` to v0.42.0 for GO-2026-5970, an infinite loop on malformed input in the normalization code reached through `autocert.HostWhitelist`.
- Added a `govulncheck` job to CI. It is tokenless, so unlike Snyk and SonarCloud it runs for real on Dependabot and fork pull requests, where those two skip and report green without scanning anything.
- Pinned every GitHub Actions `uses:` to a full commit SHA, replacing floating tags including `snyk/actions/golang@master`, so a retagged or compromised release cannot run with a job's token.
- Added `step-security/harden-runner` with `egress-policy: block` and an explicit endpoint allowlist to every workflow job.
- Added `.github/dependabot.yml` covering Go modules, GitHub Actions and Docker base images. The repository previously had no version updates configured and no dependency graph, so the backlog was invisible rather than absent.
- Bumped the runtime base image from `alpine:3.23.3` to `alpine:3.23.5`, and the builder image from `golang:1.25-alpine` to `golang:1.27-alpine`.
- Bumped `golangci-lint` in CI from v2.11.1 to v2.13.2. Releases before v2.13.0 are built against Go 1.26 and refuse to run at all against a 1.27 target, so the Go bump forces this one.
- Replaced the deprecated `httputil.ReverseProxy.Director` with `Rewrite` in the forwarding path. `Director` is deprecated as of Go 1.26 and the newer staticcheck flags it. The migration is not mechanical: `ReverseProxy` appended the client IP to the inbound `X-Forwarded-For` chain only for `Director`, and `SetXForwarded` alone would have replaced that chain with just the client IP, silently dropping every upstream hop. The outbound request now copies the inbound header before calling `SetXForwarded` so it appends, and `X-Forwarded-Host` and `X-Forwarded-Proto` are rewritten afterwards to keep the existing behavior of honoring an inbound `X-Forwarded-Proto`. Covered by the existing `TestHandler_ForwardHeaders_XFFChain` and `TestHandler_ForwardHeaders_ProtoDetection` tests.
- Widened the `coverage:ignore` window in `scripts/check-coverage.sh` from one line to two. Go does not attribute an uncovered block to a fixed position: for a marked `if`, the profile sometimes names the condition line and sometimes the body line, depending on test timing. The one-line window caught only the first, so twelve long-standing markers in `internal/relay/redis_manager.go` started failing the gate under Go 1.27 despite being correctly placed. The Codecov filter uses the same window so the two agree.
- Dropped the `tool golang.org/x/vuln/cmd/govulncheck` directive and now invoke govulncheck with `go run ...@v1.8.0`. The directive pulled `golang.org/x/tools` into the module graph, and x/tools requires `goldmark` v1.4.13, which carries CVE-2026-5160 (moderate XSS). Nothing gatekeeper ships imports goldmark, but Snyk resolves dependencies from the full module graph rather than the pruned build list, so the scanner was introducing the finding it then reported. This also drops x/vuln, x/telemetry, x/mod and x/sync from the graph.
- Added a `.snyk` policy ignoring CVE-2024-51744 in `golang-jwt/jwt` (CVSS 2.3, low). Snyk lists every version as affected with no fixed release, so no bump clears it. It arrives through `prometheus/client_golang`, is absent from `go list -deps ./...`, and gatekeeper never parses JWTs. The ignore expires 2027-03-14.

### Changed
- Updated dependencies to current releases: `miniredis` v2.39.0, `gojq` v0.12.19, `prometheus/client_golang` v1.24.1, `go-redis/v9` v9.22.0, `golang.org/x/crypto` v0.57.0, `golang.org/x/time` v0.16.0.
- Workflows now read the Go version from `go.mod` via `go-version-file` instead of hardcoding it in each job, so the `go` directive is the single place to bump it.

## [0.2.14] - 2026-06-16

### Fixed
- Relay recovery loop could not reclaim stuck messages after a pod restart because `DefaultWebhookExpiry` (30s) was shorter than `pendingIdleTimeout` (60s). Fixed by raising `DefaultWebhookExpiry` to 2 minutes and lowering `pendingIdleTimeout` to 30s, ensuring the recovery loop always has time to reclaim a stuck message before it expires.

## [0.2.13] - 2026-06-11

### Changed
- Reduced SonarQube cognitive complexity in `validateRoute`, `getClientIP`, `TestExtractJSONPath`, and `TestForwarder_Forward_PreserveHost` by extracting helper functions
- Replaced duplicated `"text/plain"` content-type literal with a named constant
- Refactored `NewPoller` to accept a `PollerConfig` struct instead of 8 positional parameters
- CI lint step now uses the official `golangci/golangci-lint-action` instead of `go install`, eliminating the unpinned-dependency security warning

## [0.2.12] - 2026-06-11

### Added
- `sendgrid` verifier type for SendGrid Event Webhook authentication. Verifies the ECDSA P-256 signature in `X-Twilio-Email-Event-Webhook-Signature` over `timestamp + payload`, with optional `max_timestamp_age` replay protection. The public key may be supplied as PEM or as the base64-encoded DER shown in the SendGrid UI.

## [0.2.11] - 2026-04-30

### Added
- Grafana dashboard for monitoring all gatekeeperd Prometheus metrics (request rates, latency, security events, relay operations, system health)
- Helm ConfigMap template for Grafana sidecar dashboard auto-provisioning (`grafana.dashboard.enabled`)
- Helm ServiceMonitor template for Prometheus Operator (`serviceMonitor.enabled`)
- Monitoring documentation (`docs/MONITORING.md`) covering dashboard setup, ServiceMonitor, and metrics reference
- Root-level `dashboards/grafana-gatekeeperd.json` for non-Kubernetes users (Docker, bare metal)

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
