# Testing Guide

This guide covers how to test gatekeeper locally and in cloud environments.

## Local Testing

### Bare Metal (Go Binaries)

Build and run the binaries directly:

```bash
# Build both binaries
make build-all

# Run gatekeeperd with example config
./bin/gatekeeperd -config config/example.yaml -listen :8080

# In another terminal, run the relay client (if testing relay mode)
./bin/gatekeeper-relay -config config/relay-client-example.yaml
```

Test with curl:

```bash
# Direct forwarding test
curl -X POST http://localhost:8080/webhook \
  -H "Host: localhost" \
  -H "Content-Type: application/json" \
  -d '{"test": "data"}'

# Relay test (requires relay client running)
curl -X POST http://localhost:8080/relay \
  -H "Host: localhost" \
  -H "Content-Type: application/json" \
  -d '{"test": "relay"}'
```

### Docker Compose

Docker Compose provides a more realistic test environment with networking.

**Build and run locally:**

```bash
# Start server only
docker-compose up

# Start server + relay client
docker-compose --profile relay up

# Run in background
docker-compose --profile relay up -d

# View logs
docker-compose logs -f

# Stop
docker-compose down
```

**Using pre-built images (PR or release):**

```bash
# Using PR images
GATEKEEPERD_IMAGE=ghcr.io/tight-line/gatekeeperd:pr-123-abc1234 \
RELAY_IMAGE=ghcr.io/tight-line/gatekeeper-relay:pr-123-abc1234 \
docker-compose --profile relay up

# Using release images
GATEKEEPERD_IMAGE=ghcr.io/tight-line/gatekeeperd:0.2.0 \
RELAY_IMAGE=ghcr.io/tight-line/gatekeeper-relay:0.2.0 \
docker-compose --profile relay up
```

Alternatively, create a `.env` file:

```bash
# .env
GATEKEEPERD_IMAGE=ghcr.io/tight-line/gatekeeperd:pr-123-abc1234
RELAY_IMAGE=ghcr.io/tight-line/gatekeeper-relay:pr-123-abc1234
```

Then run normally:

```bash
docker-compose --profile relay up
```

## Cloud Testing (Kubernetes)

### PR Image Builds

When you push to a PR, GitHub Actions automatically builds Docker images tagged with `pr-<number>-<sha>`. A comment is posted on the PR with the image tags and usage instructions.

**Example PR tags:**
- `ghcr.io/tight-line/gatekeeperd:pr-123-abc1234`
- `ghcr.io/tight-line/gatekeeper-relay:pr-123-abc1234`

PR images are automatically cleaned up:
- When the PR is closed (merged or abandoned)
- After 15 days (scheduled cleanup for orphaned images)

### Manual Image Builds

If you need to test a branch that doesn't have a PR, you can build and push manually:

```bash
# Set your tag
export TAG=my-test-$(git rev-parse --short HEAD)
export REGISTRY=ghcr.io/tight-line

# Log in to GHCR
echo $GITHUB_TOKEN | docker login ghcr.io -u USERNAME --password-stdin

# Build and push gatekeeperd
docker build -t $REGISTRY/gatekeeperd:$TAG -f Dockerfile .
docker push $REGISTRY/gatekeeperd:$TAG

# Build and push gatekeeper-relay
docker build -t $REGISTRY/gatekeeper-relay:$TAG -f Dockerfile.relay .
docker push $REGISTRY/gatekeeper-relay:$TAG
```

### Deploying with Helm

Create a values override file for your test deployment:

```yaml
# values-test.yaml
image:
  repository: ghcr.io/tight-line/gatekeeperd
  tag: "pr-123-abc1234"
  pullPolicy: Always  # Ensure fresh pulls during testing

# Your other test-specific configuration...
```

Deploy with the override:

```bash
# gatekeeperd
helm upgrade --install gatekeeperd ./charts/gatekeeperd \
  -f charts/gatekeeperd/values.yaml \
  -f values-test.yaml \
  -n your-namespace

# gatekeeper-relay
helm upgrade --install gatekeeper-relay ./charts/gatekeeper-relay \
  -f charts/gatekeeper-relay/values.yaml \
  --set image.repository=ghcr.io/tight-line/gatekeeper-relay \
  --set image.tag=pr-123-abc1234 \
  --set image.pullPolicy=Always \
  -n your-namespace
```

Or use `--set` for quick testing without a values file:

```bash
helm upgrade --install gatekeeperd ./charts/gatekeeperd \
  --set image.tag=pr-123-abc1234 \
  --set image.pullPolicy=Always \
  -n your-namespace
```

### Verifying the Deployment

Check the running images:

```bash
# Check pod images
kubectl get pods -n your-namespace -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.containers[*].image}{"\n"}{end}'

# Check gatekeeperd logs
kubectl logs -l app.kubernetes.io/name=gatekeeperd -n your-namespace

# Check relay logs
kubectl logs -l app.kubernetes.io/name=gatekeeper-relay -n your-namespace
```

### Rolling Back

To revert to a release version:

```bash
# Use specific version
helm upgrade --install gatekeeperd ./charts/gatekeeperd \
  --set image.tag=0.1.0 \
  -n your-namespace

# Or use 'latest' (most recent release)
helm upgrade --install gatekeeperd ./charts/gatekeeperd \
  --set image.tag=latest \
  -n your-namespace
```

## Test Configuration Examples

### Minimal Test Config (gatekeeperd)

```yaml
# config/test.yaml
global:
  log_level: debug
  metrics_port: 9090

routes:
  - hostname: localhost
    path: /webhook
    verifier: noop
    destination: http://httpbin.org/post

verifiers:
  noop:
    type: noop
```

### Relay Test Config

**gatekeeperd:**

```yaml
global:
  log_level: debug

routes:
  - hostname: localhost
    path: /relay
    verifier: noop
    relay_token: "test-token-12345"

verifiers:
  noop:
    type: noop
```

**gatekeeper-relay:**

```yaml
server: http://localhost:8080
max_consecutive_failures: 10

channels:
  - name: test
    token: "test-token-12345"
    destination: http://httpbin.org/post
```

## Troubleshooting

### Image Pull Errors

If you see `ImagePullBackOff`:

1. Check if the image exists: `docker pull ghcr.io/tight-line/gatekeeperd:your-tag`
2. Verify GHCR authentication if using private images
3. Check the PR comment for the correct tag

### Container Won't Start

Check logs for configuration errors:

```bash
# Docker Compose
docker-compose logs gatekeeperd

# Kubernetes
kubectl logs -l app.kubernetes.io/name=gatekeeperd -n your-namespace --previous
```

### Relay Connection Issues

1. Ensure gatekeeperd is running and accessible
2. Check the relay token matches between server and client
3. Verify network connectivity (firewall rules, service mesh, etc.)

```bash
# Test connectivity from relay pod
kubectl exec -it deploy/gatekeeper-relay -n your-namespace -- wget -qO- http://gatekeeperd:8080/health
```
