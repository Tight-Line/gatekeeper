# Concurrency and Multi-Replica Deployment

This document explains how gatekeeper handles concurrent webhook processing and multi-replica deployment.

## Overview

Gatekeeper supports two relay modes:

| Mode | Replicas | Concurrency | Dependency |
|------|----------|-------------|------------|
| In-memory | 1 | Serial per channel | None |
| Valkey | 1+ | Concurrent | Valkey server |

**In-memory mode** is the default. It requires no external dependencies but only supports a single gatekeeperd replica with serial webhook processing per relay channel.

**Valkey mode** enables multiple gatekeeperd replicas and concurrent webhook processing. It requires a Valkey (or Redis) server for coordination.

## How Relay Works

When a webhook arrives for a relay route:

1. Gatekeeperd receives the webhook and verifies it
2. Gatekeeperd queues the webhook for the relay client
3. The relay client (behind a firewall) polls gatekeeperd via HTTPS
4. The relay client receives the webhook and forwards it to the backend
5. The relay client sends the response back to gatekeeperd
6. Gatekeeperd returns the response to the webhook source

The challenge is step 6: the response must reach the same gatekeeperd instance that received the original webhook, because that instance holds the HTTP connection to the webhook source.

## In-Memory Mode

In-memory mode uses Go channels and maps within a single gatekeeperd process:

```
Webhook Source (Slack)
        │
        ▼
   ┌─────────┐
   │Gatekeeperd│
   │         │
   │ Channel ├─────► Relay Client ─────► Backend
   │   Map   │◄─────    (poll)    ◄─────
   │         │
   └─────────┘
        │
        ▼
   HTTP Response
```

- Webhook queue: in-memory channel (buffer size 1)
- Pending responses: in-memory map
- Processing: serial (one webhook at a time per channel)

**Limitations:**
- Single replica only (queue and pending map are not shared)
- Serial processing (relay client processes one webhook, waits for response, then polls again)
- If gatekeeperd restarts, queued webhooks are lost

**When to use:** Development, testing, low-traffic deployments where simplicity is preferred.

## Valkey Mode

Valkey mode uses a Valkey (or Redis) server for coordination between multiple gatekeeperd replicas:

```
Webhook Source (Slack)
        │
        ▼
   ┌─────────┐    ┌─────────┐    ┌─────────┐
   │  GK-A   │    │  GK-B   │    │  GK-C   │
   └────┬────┘    └────┬────┘    └────┬────┘
        │              │              │
        └──────────────┼──────────────┘
                       │
                       ▼
                 ┌──────────┐
                 │  Valkey  │
                 └──────────┘
                       │
                       ▼
                 Relay Client (concurrent workers)
                       │
                       ▼
                    Backend
```

**Flow with Valkey:**

1. Webhook arrives at GK-A
2. GK-A subscribes to pub/sub channel `relay:response:{webhook_id}`
3. GK-A adds webhook to Valkey stream `relay:{token}:webhooks`
4. GK-A blocks waiting on the subscription
5. Relay client polls GK-B (any instance), which reads from Valkey stream
6. Relay client forwards to backend, gets response
7. Relay client posts response to GK-C (any instance)
8. GK-C publishes response to `relay:response:{webhook_id}`
9. GK-A receives response via subscription
10. GK-A returns HTTP response to webhook source

**Valkey data structures:**
- Stream `relay:{token}:webhooks`: webhook queue (one per relay channel)
- Pub/sub `relay:response:{webhook_id}`: response routing (ephemeral, per request)
- Consumer group `relay-clients`: ensures each webhook goes to one consumer

**Benefits:**
- Multiple gatekeeperd replicas (HA, load distribution)
- Concurrent processing (relay client can have multiple workers)
- Webhooks survive gatekeeperd restarts (persisted in Valkey)

**When to use:** Production deployments requiring high availability or high throughput.

## Configuration

### Command Line

```bash
# In-memory mode (default)
gatekeeperd --config config.yaml

# Valkey mode
gatekeeperd --config config.yaml --valkey-uri valkey://localhost:6379

# With authentication
gatekeeperd --config config.yaml --valkey-uri valkey://user:password@localhost:6379

# Redis URI scheme also supported (Valkey is Redis-compatible)
gatekeeperd --config config.yaml --valkey-uri redis://localhost:6379
```

### Environment Variable

```bash
export GATEKEEPERD_VALKEY_URI=valkey://localhost:6379
gatekeeperd --config config.yaml
```

### URI Format

```
valkey://[user:password@]host[:port][/database]
redis://[user:password@]host[:port][/database]
```

Examples:
- `valkey://localhost:6379` - local Valkey, no auth
- `valkey://localhost:6379/1` - database 1
- `valkey://:secretpassword@valkey.example.com:6379` - password only
- `valkey://default:secretpassword@valkey.example.com:6379` - user and password

## Deployment Modes

### Local Development

For local development, use in-memory mode:

```bash
# Terminal 1: Run gatekeeperd
make run

# Terminal 2: Run relay client
make run-relay
```

To test Valkey mode locally:

```bash
# Start Valkey (via Docker)
docker run -d --name valkey -p 6379:6379 valkey/valkey:latest

# Run gatekeeperd with Valkey
./bin/gatekeeperd --config config/example.yaml --valkey-uri valkey://localhost:6379

# Run relay client (no changes needed - it still talks HTTP to gatekeeperd)
./bin/gatekeeper-relay --config config/relay-client-example.yaml
```

### Bare Metal / VM Deployment

**Single replica (in-memory):**

```bash
# On the gatekeeperd server
gatekeeperd --config /etc/gatekeeperd/config.yaml --listen :8080

# On the relay client server (behind firewall)
gatekeeper-relay --config /etc/gatekeeper-relay/config.yaml
```

**Multiple replicas (Valkey):**

```bash
# On each gatekeeperd server
gatekeeperd --config /etc/gatekeeperd/config.yaml \
  --listen :8080 \
  --valkey-uri valkey://valkey.internal:6379

# On the relay client server(s)
# No change - relay client doesn't know about Valkey
gatekeeper-relay --config /etc/gatekeeper-relay/config.yaml
```

### Kubernetes with Helm

**Single replica (in-memory):**

```yaml
# values.yaml
replicaCount: 1

relay:
  enabled: true

valkey:
  enabled: false
```

**Multiple replicas (Valkey):**

```yaml
# values.yaml
replicaCount: 3

relay:
  enabled: true

valkey:
  enabled: true
  # Use bundled Valkey (deployed as part of the chart)
  bundled: true

  # Or use external Valkey
  # bundled: false
  # host: valkey.database.svc.cluster.local
  # port: 6379
  # password: secretpassword  # Or use existingSecret
```

The Helm chart validates configuration:
- If `replicaCount > 1` and `relay.enabled`, then `valkey.enabled` must be `true`
- Chart will fail with an error message if this requirement is not met

### Minikube Testing

```bash
# Start minikube
minikube start

# Deploy Valkey (for multi-replica testing)
helm repo add bitnami https://charts.bitnami.com/bitnami
helm install valkey bitnami/valkey --set auth.enabled=false

# Deploy gatekeeperd with Valkey
helm upgrade --install gatekeeperd charts/gatekeeperd \
  -f config/minikube-gatekeeperd.yaml \
  --set replicaCount=2 \
  --set valkey.enabled=true \
  --set valkey.host=valkey-master \
  --set valkey.port=6379
```

## Relay Client Concurrency

The relay client can process webhooks concurrently by running multiple workers:

```yaml
# gatekeeper-relay values.yaml
channels:
  - name: slack
    tokenKey: RELAY_TOKEN_SLACK
    destination: http://backend:8080
    workers: 10  # Process up to 10 webhooks concurrently
```

Each worker independently:
1. Polls gatekeeperd for a webhook
2. Forwards to the backend
3. Sends the response back

With Valkey mode, multiple workers can poll simultaneously and receive different webhooks (Valkey consumer groups ensure each webhook goes to exactly one worker).

With in-memory mode, multiple workers will contend for the single-item channel, effectively serializing processing. Use Valkey mode for true concurrency.

## Failure Handling

### Gatekeeperd Crash

| Mode | Behavior |
|------|----------|
| In-memory | Queued webhooks lost. Webhook source retries. |
| Valkey | Queued webhooks preserved in stream. Another replica can serve relay clients. Original HTTP connection lost, webhook source retries. |

### Relay Client Crash

| Mode | Behavior |
|------|----------|
| In-memory | Webhook stuck in channel. Gatekeeperd times out, returns 504. Webhook source retries. |
| Valkey | Webhook in pending state. Recovery process reclaims after 60s. Another worker processes it. |

### Valkey Crash

Gatekeeperd returns 503 for relay routes. Webhook source retries. Direct forwarding routes are unaffected.

## Monitoring

Prometheus metrics for relay operations:

```
# Webhooks queued for relay
gatekeeper_relay_webhooks_queued_total{token="slack"}

# Webhooks delivered via relay
gatekeeper_relay_webhooks_delivered_total{token="slack"}

# Relay delivery errors
gatekeeper_relay_delivery_errors_total{token="slack", error="timeout"}

# Relay client connections (Valkey mode: consumer count)
gatekeeper_relay_clients_connected{token="slack"}

# Pending webhooks (Valkey mode only)
gatekeeper_relay_webhooks_pending{token="slack"}
```

## Summary

| Feature | In-Memory | Valkey |
|---------|-----------|--------|
| Replicas | 1 | 1+ |
| Processing | Serial | Concurrent |
| Persistence | None | Stream persisted |
| Dependencies | None | Valkey server |
| Complexity | Simple | Moderate |
| Use case | Dev/test, low traffic | Production, HA |
