# Configure Helm Skill

This skill helps users configure a complete Helm deployment of gatekeeper, including multiple routes, secrets, ingress/gateway options, TLS, and relay configuration.

## Usage

- **Claude Code**: `/configure-helm`
- **Any AI assistant**: "I want to deploy gatekeeper to Kubernetes" or "Help me set up gatekeeper with Helm"

## Instructions

This skill guides users through configuring a complete Helm deployment. It wraps the route configuration process and adds Helm-specific settings.

### Overview

A complete gatekeeper Helm deployment includes:

1. **Routes** - One or more webhook routes (use `/configure-route` for each)
2. **Secrets** - Signing secrets, API tokens, relay tokens
3. **Ingress/Gateway** - External traffic routing and TLS termination
4. **Relay** (optional) - If any routes use relay mode, configure gatekeeper-relay

### Step 1: Deployment Context

Ask: "Tell me about your Kubernetes environment:"

- **Namespace**: Where will gatekeeper be deployed?
- **Cluster type**: What ingress/gateway controller do you use?
  - nginx-ingress (traditional Ingress API)
  - Traefik Ingress
  - Traefik Gateway API
  - Istio Gateway
  - Other Gateway API implementation
  - None (LoadBalancer service directly)
- **TLS**: How do you manage certificates?
  - cert-manager with Let's Encrypt
  - cert-manager with internal CA
  - External certificates (manually managed)
  - Built-in ACME (single replica only)

### Step 2: Configure Routes

For each webhook route they need, guide them through route configuration.

Ask: "How many webhook routes do you need to configure?"

For each route, either:
- Run through the `/configure-route` flow interactively
- Or ask them to describe all routes at once if they prefer

Collect for each route:
- Provider type
- Hostname
- Path
- Delivery mode (direct or relay)
- Destination URL

Track which routes use relay mode - this determines whether gatekeeper-relay is needed.

### Step 3: Secrets Configuration

Ask: "How will you manage secrets?"

**Option A: Helm-managed secrets** (simpler)
- Secrets are defined in values.yaml
- Good for development/testing
- Warn: Don't commit secrets to version control

**Option B: External secret management** (production recommended)
- Use `existingSecret` to reference a pre-created Kubernetes Secret
- Works with External Secrets Operator, Sealed Secrets, Vault, etc.
- Provide the secret name they'll use

Generate the secrets section based on the configured routes:
- Signing secrets for each verifier
- Relay tokens for relay routes

### Step 4: Ingress/Gateway Configuration

Based on their cluster type from Step 1:

#### For nginx-ingress or Traefik Ingress

```yaml
ingress:
  enabled: true
  className: "nginx"  # or "traefik"
  annotations:
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
  tls:
    enabled: true
```

#### For Traefik Gateway API

Ask: "Do you want gatekeeper to create its own Gateway, or attach to an existing one?"

**Create own Gateway** (recommended for proper TLS handling):
```yaml
gateway:
  enabled: true
  create: true
  gatewayClassName: "traefik"
  port: 8443
  tls:
    enabled: true
    issuerName: "letsencrypt-prod"
    issuerKind: ClusterIssuer
```

**Attach to existing Gateway**:
```yaml
gateway:
  enabled: true
  create: false
  gatewayName: "traefik-gateway"
  gatewayNamespace: "traefik"
  tls:
    enabled: true
    issuerName: "letsencrypt-prod"
    issuerKind: ClusterIssuer
```

#### For LoadBalancer (no ingress)

```yaml
service:
  type: LoadBalancer
  annotations: {}  # Cloud-specific annotations if needed

# If using built-in ACME (requires ports 80/443):
tls:
  enabled: true
  acmeEmail: "certs@example.com"
```

Note: Built-in ACME only works with single replica.

### Step 5: Replica Count and Redis

Ask: "How many replicas do you need?"

For single replica:
```yaml
replicaCount: 1
```

For multiple replicas:
- If any routes use relay mode, Redis/Valkey is **required** for coordination
- Explain: Without Redis, each replica maintains its own queue, breaking delivery guarantees

```yaml
replicaCount: 2

redis:
  enabled: true
  bundled: true  # Deploy Valkey as subchart
```

Or for external Redis:
```yaml
redis:
  enabled: true
  bundled: false
  host: "redis.example.com"
  port: 6379
  # password: ""  # or use existingSecret
```

### Step 6: Relay Configuration (if needed)

If any routes use relay mode, generate the gatekeeper-relay values.

Ask: "Where will the relay client run?"
- Same cluster as gatekeeperd (different namespace)
- Different cluster (private network)
- Docker/VM in private network

Generate relay values:

```yaml
# gatekeeper-relay values.yaml
server: "https://{gatekeeperd-external-hostname}"

channels:
  - name: {provider}-webhooks
    tokenKey: RELAY_TOKEN_{PROVIDER}
    destination: "http://{internal-service}:{port}{path}"
    workers: 1  # Increase for high-volume webhooks
```

For each relay route, ensure the relay token is defined in both:
1. gatekeeperd secrets (for webhook receipt)
2. gatekeeper-relay secrets (for relay client authentication)

### Step 7: Generate Complete Configuration

Generate the complete values files with clear sections:

#### gatekeeperd/values.yaml

```yaml
# ==============================================================================
# GATEKEEPER HELM VALUES
# Generated by /configure-helm
# ==============================================================================

replicaCount: {replica-count}

# ------------------------------------------------------------------------------
# INGRESS / GATEWAY
# ------------------------------------------------------------------------------
{ingress-or-gateway-config}

# ------------------------------------------------------------------------------
# REDIS (required for multi-replica with relay)
# ------------------------------------------------------------------------------
{redis-config-if-needed}

# ------------------------------------------------------------------------------
# IP ALLOWLISTS
# (predefined lists are already included, add custom ones here)
# ------------------------------------------------------------------------------
ipAllowlists: {}

# ------------------------------------------------------------------------------
# VERIFIERS
# ------------------------------------------------------------------------------
verifiers:
{verifier-configs}

# ------------------------------------------------------------------------------
# ROUTES
# ------------------------------------------------------------------------------
routes:
{route-configs}

# ------------------------------------------------------------------------------
# SECRETS
# ------------------------------------------------------------------------------
{secrets-or-existingSecret}
```

#### gatekeeper-relay/values.yaml (if relay routes exist)

```yaml
# ==============================================================================
# GATEKEEPER-RELAY HELM VALUES
# Generated by /configure-helm
# ==============================================================================

server: "https://{external-hostname}"

maxConsecutiveFailures: 10

channels:
{channel-configs}

{secrets-or-existingSecret}
```

### Step 8: Deployment Instructions

Provide deployment commands:

```bash
# Create namespace
kubectl create namespace {namespace}

# If using external secrets, create the secret first:
kubectl create secret generic gatekeeper-secrets \
  --from-literal=SLACK_SIGNING_SECRET='...' \
  --from-literal=RELAY_TOKEN_SLACK='...' \
  -n {namespace}

# Deploy gatekeeperd
helm upgrade --install gatekeeperd ./charts/gatekeeperd \
  -f gatekeeperd-values.yaml \
  -n {namespace}

# If using relay, deploy relay client:
helm upgrade --install gatekeeper-relay ./charts/gatekeeper-relay \
  -f relay-values.yaml \
  -n {namespace}

# Verify deployment
kubectl get pods -n {namespace}
kubectl logs -l app.kubernetes.io/name=gatekeeperd -n {namespace}
```

### Step 9: Post-Deployment Checklist

Provide a checklist for the user:

- [ ] DNS records point to the ingress/gateway/LoadBalancer IP
- [ ] TLS certificates are issued (check `kubectl get certificate -n {namespace}`)
- [ ] Pods are running (`kubectl get pods -n {namespace}`)
- [ ] Health endpoint responds (`curl https://{hostname}/healthz`)
- [ ] Test webhook delivery from provider
- [ ] If using relay, verify relay client is connected (check logs)

### Conversation Style

- Be concise and direct
- Ask one question at a time
- For route configuration, offer to run through `/configure-route` for each or collect all at once
- Generate complete, ready-to-use values files
- Explain trade-offs (e.g., bundled vs external Redis)
- Use code blocks for all configuration snippets
- Provide copy-pasteable deployment commands

### Common Configurations

#### Minimal Single-Route (Direct Mode)

```yaml
# gatekeeperd values.yaml
replicaCount: 1

ingress:
  enabled: true
  className: "nginx"
  annotations:
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
  tls:
    enabled: true

verifiers:
  slack:
    type: slack
    signingSecretKey: SLACK_SIGNING_SECRET

routes:
  - hostname: webhooks.example.com
    path: /slack
    ipAllowlist: aws
    verifier: slack
    destination: http://backend:8080/webhooks/slack

secrets:
  SLACK_SIGNING_SECRET: "xoxb-your-secret"
```

#### Multi-Route with Relay

```yaml
# gatekeeperd values.yaml
replicaCount: 2

ingress:
  enabled: true
  className: "nginx"
  annotations:
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
  tls:
    enabled: true

redis:
  enabled: true
  bundled: true

verifiers:
  slack:
    type: slack
    signingSecretKey: SLACK_SIGNING_SECRET
  github:
    type: github
    secretKey: GITHUB_WEBHOOK_SECRET

routes:
  # Direct route
  - hostname: webhooks.example.com
    path: /slack
    ipAllowlist: aws
    verifier: slack
    destination: http://backend:8080/webhooks/slack

  # Relay route (for private network)
  - hostname: webhooks.example.com
    path: /github
    ipAllowlist: github
    verifier: github
    relayTokenKey: RELAY_TOKEN_GITHUB

existingSecret: "gatekeeper-secrets"
```

```yaml
# gatekeeper-relay values.yaml
server: "https://webhooks.example.com"

channels:
  - name: github-webhooks
    tokenKey: RELAY_TOKEN_GITHUB
    destination: "http://localhost:8080/webhooks/github"
    workers: 5

existingSecret: "relay-secrets"
```

#### Gateway API with Traefik

```yaml
# gatekeeperd values.yaml
replicaCount: 2

gateway:
  enabled: true
  create: true
  gatewayClassName: "traefik"
  port: 8443
  tls:
    enabled: true
    issuerName: "letsencrypt-prod"
    issuerKind: ClusterIssuer

redis:
  enabled: true
  bundled: true

verifiers:
  ms-graph:
    type: json_field
    path: "value.0.clientState.tpVerificationToken"
    tokenKey: MS_GRAPH_CLIENT_STATE

routes:
  - hostname: graph-webhooks.example.com
    path: /notifications
    ipAllowlist: microsoft-graph
    verifier: ms-graph
    relayTokenKey: RELAY_TOKEN_GRAPH

existingSecret: "gatekeeper-secrets"
```
