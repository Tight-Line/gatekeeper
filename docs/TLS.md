# TLS Configuration

For production deployments, TLS termination is required. There are two approaches depending on your infrastructure.

## Kubernetes Deployments (Recommended)

In Kubernetes, use an Ingress controller with cert-manager for TLS. This is the standard pattern and works well with multiple replicas.

### Architecture

```
                                      cert-manager
                                      (cluster add-on)
                                      +-----------------+
                                      | 1. Watches for  |
                                      |    Ingress with |
                                      |    annotation   |
                                      |                 |
                                      | 2. Performs     |
                                      |    ACME challenge|
                                      |                 |
                                      | 3. Stores cert  |
                                      |    in Secret    |
                                      +--------+--------+
                                               |
                                               v
+--------+       +--------------+       +-------------+
| Slack  |--HTTPS|   Ingress    |--HTTP-| gatekeeperd | x N replicas
| GitHub |       |  Controller  |       | (port 8080) |
+--------+       |              |       +-------------+
                 | TLS from     |
                 | Secret       |
                 +--------------+
```

### Setup Steps

1. Install cert-manager in your cluster:

```bash
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.14.0/cert-manager.yaml
```

2. Create a ClusterIssuer for Let's Encrypt:

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: your-email@example.com
    privateKeySecretRef:
      name: letsencrypt-prod
    solvers:
    - http01:
        ingress:
          class: nginx
```

3. Configure Ingress with TLS:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: gatekeeperd
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
spec:
  ingressClassName: nginx
  tls:
  - hosts:
    - webhooks.example.com
    secretName: gatekeeperd-tls
  rules:
  - host: webhooks.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: gatekeeperd
            port:
              number: 8080
```

The gatekeeperd service runs in HTTP mode (`-listen :8080`), and the Ingress controller handles TLS termination.

### Helm Chart Configuration

```yaml
# values.yaml
replicaCount: 3

tls:
  enabled: false  # Disable internal ACME

ingress:
  enabled: true
  className: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
  tls:
    enabled: true
    # secretName: optional - if omitted, each host gets "{hostname}-tls"
```

The Ingress hostnames are automatically derived from your routes configuration.

## Baremetal / VM Deployments

Use the `-tls` flag to enable automatic certificate provisioning via Let's Encrypt. This is suitable for single-instance deployments where you have direct control over ports 80 and 443.

### Requirements

- The server must be publicly accessible on ports 80 and 443
- DNS A records for all configured hostnames must point to the server
- Port 80 is used for ACME HTTP-01 challenges (certificate validation)
- Port 443 serves HTTPS traffic

### Configuration

```yaml
# gatekeeperd.yaml
global:
  acme_email: "certs@example.com"
  acme_cache_dir: "/var/cache/gatekeeper/certs"

routes:
  - hostname: webhooks.example.com
    # ... rest of route config
```

### Running

```bash
./bin/gatekeeperd -config /path/to/config.yaml -tls
```

### Docker

```bash
docker run -p 80:80 -p 443:443 -p 9090:9090 \
  -v /path/to/config.yaml:/etc/gatekeeper/config.yaml \
  -v /path/to/cert-cache:/var/cache/gatekeeper/certs \
  ghcr.io/tight-line/gatekeeperd:latest -tls
```

The certificate cache directory persists certificates across restarts to avoid rate limits.

### Limitations

**The internal ACME implementation requires a singleton instance.** Certificate state is stored in a local directory, and running multiple instances would cause:

- Each instance attempting to obtain its own certificate
- Let's Encrypt rate limits being hit
- Certificate cache conflicts

For high availability, use the Kubernetes/Ingress approach instead.

We welcome contributions to improve the baremetal TLS architecture, such as:
- Shared certificate storage backends (Redis, etcd, S3)
- Leader election for certificate renewal
- External certificate management integration
