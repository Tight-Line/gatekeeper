# AGENTS.md

This file provides context and instructions for AI coding agents working on gatekeeper.

## Project Summary

gatekeeper is a webhook authentication, authorization, and validation proxy for enterprise environments that cannot expose internal services directly to the internet. It validates incoming webhooks from SaaS providers (Slack, Google, GitHub, Shopify) using provider-specific signature verification, filters by source IP, and forwards authenticated requests to internal backends.

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
3. Rate limiting (if configured) - returns 429 with Retry-After header if exceeded
4. IP validation against configured allowlist
5. Signature verification using provider-specific algorithm
6. Either:
   - Forward to destination (transparent proxy), or
   - Deliver via relay to waiting relay client
7. Log result with minimal information (IP, path, success/failure)

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
| json_field | Microsoft Graph | Token embedded in JSON body at configurable path |
| noop | Testing | Always succeeds |

### Rate Limiting

Rate limiting protects against abuse using a token bucket algorithm. Configure named limiters and reference them from routes or set a global default.

```yaml
rate_limiters:
  default:
    total_rps: 100       # Total requests per second across all IPs
    per_ip_rps: 10       # Per client IP (0 = disabled)
    burst: 20            # Spike allowance
    cleanup_interval: 5m # Stale entry cleanup interval (default: 5m)
    idle_timeout: 10m    # Remove idle per-IP entries after (default: 10m)

global:
  default_rate_limiter: default  # Apply to all routes without explicit limiter

routes:
  - hostname: example.com
    path: /webhook
    rate_limiter: default  # Override or specify per-route
    destination: http://backend:8080
```

When rate limited, returns HTTP 429 with `Retry-After: 1` header. Metrics: `gatekeeper_rate_limited_total{route,limiter,reason}` where reason is `total` or `per_ip`.

### Configuration Loading

Configuration can be loaded from file or from environment variables:

| Binary | Env Var | Default File |
|--------|---------|--------------|
| gatekeeperd | `GATEKEEPERD_CONFIG` | `./gatekeeperd.yaml` |
| gatekeeper-relay | `GATEKEEPER_RELAY_CONFIG` | `./gatekeeper-relay.yaml` |

If the env var is set (contains full YAML), the file path is ignored. The Helm charts use the env var approach to inject ConfigMap content directly.

For multi-replica relay deployments, gatekeeperd also reads `GATEKEEPERD_REDIS_URI` to connect to Redis/Valkey for webhook queue coordination. See [docs/CONCURRENCY.md](docs/CONCURRENCY.md) for details.

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

## Interactive Skills

Gatekeeper includes AI skills for interactive configuration (see [agents/](agents/)):

- **Configure Route** ([agents/configure-route.md](agents/configure-route.md)) - Configure a single webhook route
- **Configure Helm** ([agents/configure-helm.md](agents/configure-helm.md)) - Configure a complete Kubernetes deployment

These are user-facing interactive wizards, not coding agent instructions. In Claude Code, invoke with `/configure-route` or `/configure-helm`.

**Maintenance**: When adding features that affect configuration (new verifier types, new route options, new Helm values), update the relevant skill files in `agents/` before committing. The skills should always reflect the current capabilities.

## File Locations

- Server entry point: cmd/gatekeeperd/main.go
- Relay client entry point: cmd/gatekeeper-relay/main.go
- Config structs: internal/config/config.go
- Relay client config: internal/relayclient/config.go
- Verifier interface: internal/verifier/verifier.go
- HTTP handler: internal/proxy/handler.go
- Rate limiter: internal/ratelimit/limiter.go, internal/ratelimit/set.go
- Relay manager: internal/relay/manager.go
- Redis relay manager: internal/relay/redis_manager.go
- Relay handler: internal/relay/handler.go
- Example config: config/example.yaml
- K8s manifests: k8s/
- Helm charts: charts/gatekeeperd/, charts/gatekeeper-relay/

## Config Directory Policy

The `config/` directory contains example and template files. **Deployment-specific config files should NOT be tracked in git.**

**Tracked files (examples/templates):**
- `config/example.yaml` - Example gatekeeperd config
- `config/relay-client-example.yaml` - Example relay client config
- `config/minikube-*.yaml` - Local development templates
- `config/test*.yaml` - Test fixtures

**Not tracked (deployment-specific):**
- Any file with environment names (e.g., `*-prod-*.yaml`, `*-staging-*.yaml`)
- Cluster-specific configs (e.g., `ike-cloud-nonprod-*.yaml`, `tinypulse-dev-*.yaml`)

When creating deployment configs, store them locally or in a separate deployment repo. Do not commit them to this repository.

## Planned Work

See [docs/PROVIDER_TODO.md](docs/PROVIDER_TODO.md) for the list of webhook providers we plan to support.

## Provider Development

See [docs/PROVIDER_DEVELOPMENT.md](docs/PROVIDER_DEVELOPMENT.md) for the step-by-step guide to developing new webhook providers or improving existing ones. This includes:

- Deploying gatekeeperd to capture real webhooks
- Creating and organizing test recordings
- Developing verifiers and validators from real payloads
- End-to-end testing workflow

**Maintenance note**: Keep PROVIDER_DEVELOPMENT.md up-to-date when adding new features that affect the provider development workflow (e.g., new recording formats, new verifier types, changes to the testutil package).

## Changelog Maintenance

**IMPORTANT**: Always update CHANGELOG.md when making changes to the codebase.

### When to Update

Add an entry to the `[Unreleased]` section of CHANGELOG.md for:
- New features or capabilities
- Bug fixes
- Breaking changes
- Significant refactorings that affect behavior
- Dependency updates that affect functionality

Do NOT add entries for:
- Minor code cleanup or formatting
- Internal refactoring with no behavior change
- Documentation-only changes
- Test-only changes

### How to Update

1. Add entries under the appropriate subsection in `[Unreleased]`:
   - `### Added` - new features
   - `### Changed` - changes to existing functionality
   - `### Deprecated` - features that will be removed
   - `### Removed` - removed features
   - `### Fixed` - bug fixes
   - `### Security` - security-related changes

2. Write entries from the user's perspective, not the developer's:
   - Good: "Relay client logs connection status on startup and recovery"
   - Bad: "Added logging calls to poller.go"

3. Keep entries concise - one line per change when possible.

### Cleaning Up the Changelog

Before adding new entries, review and clean up existing `[Unreleased]` content:
- Combine related entries (e.g., multiple iterations on the same feature)
- Remove entries that were superseded by later work
- Check `git log $(git describe --tags --abbrev=0)..HEAD --oneline` to see all commits since the last tag and ensure significant changes are captured

### Creating Releases

**IMPORTANT: Before creating any release, you MUST verify 100% test coverage.**

Run `make check` before every release. This single command verifies:
- Linting passes
- Tests pass with 100% coverage (will fail if coverage < 100%)
- All binaries build successfully

```bash
# Run ALL pre-release checks (REQUIRED before tagging)
make check

# Only after make check passes, create the release
scripts/make-tag <version>
```

The `scripts/make-tag` script:
1. Runs tests to ensure CI will pass
2. Updates Helm chart versions
3. Moves `[Unreleased]` content to a dated release section
4. Commits and creates the git tag

**DO NOT skip `make check`. CI will reject releases with less than 100% coverage.**

## Test Coverage Exclusions

The project enforces 100% test coverage. In rare cases, specific lines may be marked as excluded from coverage requirements using `// coverage:ignore - <reason>` comments.

**When to use `coverage:ignore`:**
1. Code that is genuinely unreachable with the test infrastructure (e.g., Redis behaviors that miniredis doesn't simulate)
2. Only when the developer explicitly requests it

**When NOT to use `coverage:ignore`:**
- Code that is merely difficult to test - write the test instead
- Code that could be tested with better test design
- As a shortcut to avoid writing tests

**Never suggest or add `coverage:ignore` comments unless:**
1. You have thoroughly investigated and confirmed the code path is truly untestable with available tools, OR
2. The developer explicitly asks you to mark specific code as untestable

The `scripts/check-coverage.sh` script validates that all uncovered lines have a `coverage:ignore` comment with a reason. For Codecov integration, use `--codecov` flag to generate a filtered coverage file.

## Testing Deployments

See [docs/TESTING.md](docs/TESTING.md) for comprehensive testing instructions. This section covers what AI agents need to know for common deployment testing tasks.

### PR Image Builds

When code is pushed to a PR, GitHub Actions automatically builds Docker images:

- **gatekeeperd**: `ghcr.io/tight-line/gatekeeperd:pr-<number>-<sha>`
- **gatekeeper-relay**: `ghcr.io/tight-line/gatekeeper-relay:pr-<number>-<sha>`

The PR will have a comment with the exact tags. Images are cleaned up automatically when the PR closes or after 15 days.

### Testing with docker-compose

To test PR images locally with docker-compose:

```bash
# Get the PR image tag (from PR comment or construct it)
PR_TAG="pr-123-abc1234"

# Run with pre-built images
GATEKEEPERD_IMAGE=ghcr.io/tight-line/gatekeeperd:$PR_TAG \
RELAY_IMAGE=ghcr.io/tight-line/gatekeeper-relay:$PR_TAG \
docker-compose --profile relay up
```

Or create/update a `.env` file in the repo root:

```bash
# .env (not tracked in git)
GATEKEEPERD_IMAGE=ghcr.io/tight-line/gatekeeperd:pr-123-abc1234
RELAY_IMAGE=ghcr.io/tight-line/gatekeeper-relay:pr-123-abc1234
```

### Testing with Kubernetes/Helm

To deploy PR images to a Kubernetes cluster, update the Helm values:

**Option 1: Command line overrides**

```bash
# gatekeeperd
helm upgrade --install gatekeeperd ./charts/gatekeeperd \
  --set image.tag=pr-123-abc1234 \
  --set image.pullPolicy=Always \
  -n your-namespace

# gatekeeper-relay
helm upgrade --install gatekeeper-relay ./charts/gatekeeper-relay \
  --set image.tag=pr-123-abc1234 \
  --set image.pullPolicy=Always \
  -n your-namespace
```

**Option 2: Values file override**

Create a temporary values file (do not commit):

```yaml
# values-test.yaml
image:
  tag: "pr-123-abc1234"
  pullPolicy: Always
```

Then deploy:

```bash
helm upgrade --install gatekeeperd ./charts/gatekeeperd \
  -f charts/gatekeeperd/values.yaml \
  -f values-test.yaml \
  -n your-namespace
```

### AI Agent Deployment Workflow

When a user says "push this branch and test it in my k8s env", follow this workflow:

1. **Commit and push the branch** (if not already done):
   ```bash
   git add -A && git commit -m "..." && git push origin HEAD
   ```

2. **Create or update the PR** (if needed):
   ```bash
   gh pr create --title "..." --body "..." --head $(git branch --show-current)
   ```

3. **Wait for the PR images workflow** to complete:
   ```bash
   gh run list --workflow=pr-images.yml --branch $(git branch --show-current) --limit 1
   gh run watch <run-id>  # Watch until complete
   ```

4. **Get the image tag** from the workflow or construct it:
   ```bash
   PR_NUMBER=$(gh pr view --json number -q .number)
   SHORT_SHA=$(git rev-parse --short HEAD)
   TAG="pr-${PR_NUMBER}-${SHORT_SHA}"
   ```

5. **Deploy to Kubernetes**:
   ```bash
   helm upgrade --install gatekeeperd ./charts/gatekeeperd \
     --set image.tag=$TAG \
     --set image.pullPolicy=Always \
     -n <namespace>
   ```

6. **Verify the deployment**:
   ```bash
   kubectl rollout status deployment/gatekeeperd -n <namespace>
   kubectl get pods -n <namespace> -l app.kubernetes.io/name=gatekeeperd
   ```

### Reverting to Release Version

To roll back from a test deployment:

```bash
helm upgrade --install gatekeeperd ./charts/gatekeeperd \
  --set image.tag=0.1.0 \
  -n your-namespace
```

Or use `latest` for the most recent release:

```bash
helm upgrade --install gatekeeperd ./charts/gatekeeperd \
  --set image.tag=latest \
  -n your-namespace
```
