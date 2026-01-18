# X-Forwarded-For Header Handling

This document explains how gatekeeper handles the `X-Forwarded-For` header for client IP determination when running behind reverse proxies.

## Background

Gatekeeper uses IP allowlists as one layer of defense, checking the client IP against configured CIDR ranges before processing a request. When gatekeeper runs behind a reverse proxy (ingress controller, gateway, load balancer), the TCP connection's remote address is the proxy's IP, not the original client's. To enforce IP allowlists correctly, gatekeeper can read the `X-Forwarded-For` header to determine the true client IP.

**This behavior must be explicitly enabled** using the `--trust-x-forwarded-for` flag or the `GATEKEEPERD_TRUST_X_FORWARDED_FOR=true` environment variable. By default, gatekeeper only uses the TCP connection's remote address.

## When to Enable

Enable this flag when gatekeeper runs behind a trusted reverse proxy that sets the `X-Forwarded-For` header:

| Deployment | Enable flag? | Reason |
|------------|--------------|--------|
| Behind Ingress Controller | Yes | Ingress sets X-Forwarded-For from real client IP |
| Behind L7 Load Balancer (AWS ALB, GCP HTTP LB) | Yes | L7 LBs terminate HTTP and set X-Forwarded-For |
| Behind L4 Load Balancer with TCP passthrough (AWS NLB, GCP TCP LB) | No | Client IP is preserved in TCP connection; X-Forwarded-For not set |
| Direct exposure with `-tls` mode | No | No proxy; X-Forwarded-For could be spoofed by attacker |
| NodePort Service without Ingress | No | Clients connect directly; X-Forwarded-For could be spoofed |

## Helm Chart Behavior

When using the Helm chart:

- If `ingress.enabled: true`, the flag is automatically enabled
- If `trustXForwardedFor: true` is set explicitly, the flag is enabled
- Otherwise, the flag is disabled (safe default)

```yaml
# values.yaml - automatically trusts X-Forwarded-For when using ingress
ingress:
  enabled: true

# Or explicitly enable for L7 load balancer without ingress
trustXForwardedFor: true
```

## Why Trusting X-Forwarded-For Is Safe (When Properly Configured)

The `X-Forwarded-For` header is inherently untrustworthy when received directly from the internet. An attacker can set it to any value. However, in a reverse-proxied deployment, the proxy sits between the client and gatekeeper, and the proxy controls what headers reach the backend.

Standard behavior of ingress controllers and gateways (nginx-ingress, Traefik, Envoy, AWS ALB, GCP Cloud Load Balancing, etc.):

1. **They set or overwrite X-Forwarded-For** with the actual client IP from the TCP connection
2. **If the header already exists**, they append the client IP to the chain, making the leftmost entry the original client (set by the first trusted proxy)
3. **They are the only entry point** to the cluster. Pods are not directly reachable from outside.

This means an attacker cannot spoof their IP by sending a fake `X-Forwarded-For` header. The ingress controller will either replace it entirely or append the real IP, and gatekeeper reads the leftmost (original client) IP from the chain.

## Requirements for Safe Operation

1. **Gatekeeper must not be directly exposed to the internet.** All external traffic must flow through the ingress controller or gateway. If gatekeeper is directly reachable, attackers can send arbitrary `X-Forwarded-For` values and bypass IP allowlists.

2. **The ingress controller must set X-Forwarded-For correctly.** This is the default behavior for all major ingress controllers, but verify your configuration. For nginx-ingress, this is controlled by `use-forwarded-headers` and `compute-full-forwarded-for` settings.

3. **Network policies should enforce the traffic path.** In Kubernetes, use NetworkPolicy to ensure gatekeeper pods only accept traffic from the ingress controller's pods, not from arbitrary sources.

Example NetworkPolicy:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: gatekeeperd-ingress-only
spec:
  podSelector:
    matchLabels:
      app: gatekeeperd
  policyTypes:
    - Ingress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              name: ingress-nginx
          podSelector:
            matchLabels:
              app.kubernetes.io/name: ingress-nginx
```

## Why We Trust the Leftmost IP

The `X-Forwarded-For` header format is `client, proxy1, proxy2, ...`. Each proxy in the chain appends the IP it received the connection from. The leftmost IP is set by the first proxy that received the request from the actual client. Since that first proxy (your ingress controller) is trusted and sets the value from the real TCP connection, the leftmost IP is the true client IP.

Gatekeeper does not support configuring "trusted proxy" depth (like some frameworks do) because in the expected deployment model, there is exactly one layer of trusted proxies (the ingress controller), and that proxy is responsible for setting the correct client IP.
