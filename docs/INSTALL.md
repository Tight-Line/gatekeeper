# Installation

## Docker

Pre-built images are available from GitHub Container Registry:

```bash
docker pull ghcr.io/tight-line/gatekeeperd:latest
docker pull ghcr.io/tight-line/gatekeeper-relay:latest
```

Run the server with your configuration:

```bash
docker run -p 8080:8080 -p 9090:9090 \
  -v /path/to/config.yaml:/etc/gatekeeper/config.yaml \
  ghcr.io/tight-line/gatekeeperd:latest -listen :8080
```

Run the relay client:

```bash
docker run \
  -v /path/to/relay-config.yaml:/etc/gatekeeper/relay.yaml \
  ghcr.io/tight-line/gatekeeper-relay:latest -config /etc/gatekeeper/relay.yaml
```

## Helm

Add the Helm repository:

```bash
helm repo add gatekeeper https://tight-line.github.io/gatekeeper
helm repo update
```

Install the server:

```bash
helm install gatekeeperd gatekeeper/gatekeeperd -f your-values.yaml
```

Install the relay client (in your private network):

```bash
helm install gatekeeper-relay gatekeeper/gatekeeper-relay -f your-relay-values.yaml
```

See the chart values files for all configuration options:
- `charts/gatekeeperd/values.yaml`
- `charts/gatekeeper-relay/values.yaml`

## From Source

Requirements:
- Go 1.25 or later

Build both binaries:

```bash
git clone https://github.com/tight-line/gatekeeper.git
cd gatekeeper
make build-all
```

This produces:
- `bin/gatekeeperd` - the webhook proxy server
- `bin/gatekeeper-relay` - the relay client for private networks

Run the server:

```bash
./bin/gatekeeperd -config /path/to/config.yaml -listen :8080
```

Run the relay client:

```bash
./bin/gatekeeper-relay -config /path/to/relay-config.yaml
```

## Kubernetes (without Helm)

Apply the kustomize manifests:

```bash
kubectl apply -k k8s/
```

This creates:
- ConfigMap for configuration
- Secret for sensitive values (you must populate this)
- Deployment for gatekeeperd
- Service exposing ports 8080 (HTTP) and 9090 (metrics)
- PersistentVolumeClaim for ACME certificate cache (if using built-in TLS)

Edit `k8s/configmap.yaml` with your route configuration before applying.
