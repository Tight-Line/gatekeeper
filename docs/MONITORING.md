# Monitoring

Gatekeeper exposes Prometheus metrics on the metrics port (default `9090`) at `/metrics`. A pre-built Grafana dashboard is included for visualizing all metrics.

## Grafana Dashboard

The dashboard JSON is available at [`dashboards/grafana-gatekeeperd.json`](../dashboards/grafana-gatekeeperd.json). It covers all 14 metrics exported by gatekeeperd, organized into four sections:

- **Overview** - Request rate, success rate, error rate, connected relay clients
- **Requests** - Rate by hostname, by status code, latency percentiles (p50/p95/p99), latency heatmap
- **Security** - Verification failures by verifier/reason, IP filter denials, validation failures
- **Relay** - Webhooks queued vs delivered, delivery latency, delivery errors, pending webhooks, clients per token
- **System** - IP ranges loaded per allowlist, IP range fetch errors, forward errors by hostname/destination

### Template Variables

The dashboard includes four template variables for filtering:

| Variable | Description |
|----------|-------------|
| `datasource` | Prometheus datasource to query |
| `namespace` | Kubernetes namespace (supports "All") |
| `instance` | Instance selector (supports "All") |
| `hostname` | Route hostname filter (supports multi-select) |

### Manual Import (Docker, Bare Metal)

1. Open Grafana and navigate to **Dashboards > Import**
2. Upload `dashboards/grafana-gatekeeperd.json` or paste its contents
3. Select your Prometheus datasource
4. Click **Import**

Ensure your Prometheus instance is scraping the gatekeeperd metrics endpoint (`<host>:9090/metrics`).

### Helm / Kubernetes

#### Dashboard ConfigMap (Grafana Sidecar)

If you use the [Grafana sidecar](https://github.com/grafana/helm-charts/tree/main/charts/grafana) to auto-provision dashboards, enable the ConfigMap in your Helm values:

```yaml
grafana:
  dashboard:
    enabled: true
```

This creates a ConfigMap with the `grafana_dashboard` label, which the Grafana sidecar picks up automatically. You can customize the label, namespace, and annotations:

```yaml
grafana:
  dashboard:
    enabled: true
    sidecarLabel: grafana_dashboard  # default
    namespace: monitoring            # deploy ConfigMap to a specific namespace
    labels: {}
    annotations: {}
```

#### ServiceMonitor (Prometheus Operator)

If you use the [Prometheus Operator](https://prometheus-operator.dev/), enable the ServiceMonitor:

```yaml
serviceMonitor:
  enabled: true
```

This creates a ServiceMonitor that tells Prometheus to scrape the gatekeeperd metrics port. You can customize the scrape interval, namespace, and labels:

```yaml
serviceMonitor:
  enabled: true
  interval: 30s    # default
  namespace: ""    # deploy to a specific namespace
  labels: {}       # additional labels for ServiceMonitor selection
```

## Metrics Reference

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `gatekeeper_requests_total` | Counter | `hostname`, `status`, `namespace`, `instance` | Total HTTP requests processed |
| `gatekeeper_request_duration_seconds` | Histogram | `hostname`, `namespace`, `instance` | Request processing duration |
| `gatekeeper_verification_failures_total` | Counter | `verifier`, `reason`, `hostname`, `namespace`, `instance` | Webhook signature verification failures |
| `gatekeeper_validation_failures_total` | Counter | `validator`, `hostname`, `namespace`, `instance` | Payload schema validation failures |
| `gatekeeper_ip_filter_denied_total` | Counter | `allowlist`, `hostname`, `namespace`, `instance` | Requests denied by IP allowlist |
| `gatekeeper_ip_ranges_loaded` | Gauge | `allowlist`, `namespace`, `instance` | Number of IP ranges currently loaded per allowlist |
| `gatekeeper_ip_range_fetch_errors_total` | Counter | `allowlist`, `namespace`, `instance` | Errors fetching IP range updates |
| `gatekeeper_forward_errors_total` | Counter | `hostname`, `destination`, `namespace`, `instance` | Errors forwarding requests to backends |
| `gatekeeper_relay_webhooks_queued_total` | Counter | `namespace`, `instance` | Total webhooks queued for relay delivery |
| `gatekeeper_relay_webhooks_delivered_total` | Counter | `namespace`, `instance` | Total webhooks delivered via relay |
| `gatekeeper_relay_delivery_errors_total` | Counter | `reason`, `namespace`, `instance` | Relay delivery errors |
| `gatekeeper_relay_webhooks_pending` | Gauge | `token`, `namespace`, `instance` | Webhooks currently pending delivery per relay token |
| `gatekeeper_relay_clients_connected` | Gauge | `token`, `namespace`, `instance` | Relay clients currently connected per token |
| `gatekeeper_relay_delivery_duration_seconds` | Histogram | `namespace`, `instance` | Relay webhook delivery duration |
