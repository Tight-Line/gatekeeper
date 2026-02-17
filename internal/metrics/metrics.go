package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// RequestsTotal counts total requests by hostname, path, and status
	RequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gatekeeper_requests_total",
			Help: "Total number of requests processed",
		},
		[]string{"hostname", "path", "status"},
	)

	// RequestDuration measures request latency
	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gatekeeper_request_duration_seconds",
			Help:    "Request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"hostname", "path"},
	)

	// VerificationFailures counts verification failures by type
	VerificationFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gatekeeper_verification_failures_total",
			Help: "Total number of verification failures",
		},
		[]string{"hostname", "verifier", "reason"},
	)

	// ValidationFailures counts payload validation failures
	ValidationFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gatekeeper_validation_failures_total",
			Help: "Total number of payload validation failures",
		},
		[]string{"hostname", "validator"},
	)

	// IPFilterDenied counts requests denied by IP filter
	IPFilterDenied = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gatekeeper_ip_filter_denied_total",
			Help: "Total number of requests denied by IP filter",
		},
		[]string{"hostname", "allowlist"},
	)

	// IPRangesLoaded tracks the number of CIDRs loaded per allowlist
	IPRangesLoaded = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gatekeeper_ip_ranges_loaded",
			Help: "Number of CIDR ranges loaded per allowlist",
		},
		[]string{"allowlist"},
	)

	// IPRangeFetchErrors counts errors fetching IP ranges
	IPRangeFetchErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gatekeeper_ip_range_fetch_errors_total",
			Help: "Total number of errors fetching IP ranges",
		},
		[]string{"allowlist"},
	)

	// ForwardErrors counts errors forwarding requests
	ForwardErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gatekeeper_forward_errors_total",
			Help: "Total number of errors forwarding requests",
		},
		[]string{"hostname", "destination"},
	)

	// Relay metrics

	// RelayWebhooksQueued counts webhooks added to the relay queue
	RelayWebhooksQueued = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gatekeeper_relay_webhooks_queued_total",
			Help: "Total number of webhooks added to the relay queue",
		},
		[]string{"token"},
	)

	// RelayWebhooksDelivered counts webhooks successfully delivered via relay
	RelayWebhooksDelivered = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gatekeeper_relay_webhooks_delivered_total",
			Help: "Total number of webhooks successfully delivered via relay",
		},
		[]string{"token"},
	)

	// RelayDeliveryErrors counts relay delivery errors
	RelayDeliveryErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gatekeeper_relay_delivery_errors_total",
			Help: "Total number of relay delivery errors",
		},
		[]string{"token", "reason"},
	)

	// RelayWebhooksPending tracks the number of pending webhooks in the relay queue
	// Only available in Redis mode
	RelayWebhooksPending = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gatekeeper_relay_webhooks_pending",
			Help: "Number of webhooks pending in the relay queue (Redis mode only)",
		},
		[]string{"token"},
	)

	// RelayClientsConnected tracks the number of connected relay clients
	RelayClientsConnected = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gatekeeper_relay_clients_connected",
			Help: "Number of connected relay clients per token",
		},
		[]string{"token"},
	)

	// RelayDeliveryDuration measures relay delivery latency
	RelayDeliveryDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gatekeeper_relay_delivery_duration_seconds",
			Help:    "Relay delivery duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"token"},
	)

	// RateLimitedTotal counts requests denied by rate limiting
	RateLimitedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gatekeeper_rate_limited_total",
			Help: "Total number of requests denied by rate limiting",
		},
		[]string{"route", "limiter", "reason"},
	)
)

// Handler returns the Prometheus metrics HTTP handler
func Handler() http.Handler {
	return promhttp.Handler()
}

// RecordRequest records a request metric
func RecordRequest(hostname, path, status string, durationSeconds float64) {
	RequestsTotal.WithLabelValues(hostname, path, status).Inc()
	RequestDuration.WithLabelValues(hostname, path).Observe(durationSeconds)
}

// RecordVerificationFailure records a verification failure
func RecordVerificationFailure(hostname, verifier, reason string) {
	VerificationFailures.WithLabelValues(hostname, verifier, reason).Inc()
}

// RecordValidationFailure records a payload validation failure
func RecordValidationFailure(hostname, validator string) {
	ValidationFailures.WithLabelValues(hostname, validator).Inc()
}

// RecordIPDenied records an IP filter denial
func RecordIPDenied(hostname, allowlist string) {
	IPFilterDenied.WithLabelValues(hostname, allowlist).Inc()
}

// RecordIPRangesLoaded records the number of IP ranges loaded
func RecordIPRangesLoaded(allowlist string, count int) {
	IPRangesLoaded.WithLabelValues(allowlist).Set(float64(count))
}

// RecordIPRangeFetchError records an error fetching IP ranges
func RecordIPRangeFetchError(allowlist string) {
	IPRangeFetchErrors.WithLabelValues(allowlist).Inc()
}

// RecordForwardError records a forwarding error
func RecordForwardError(hostname, destination string) {
	ForwardErrors.WithLabelValues(hostname, destination).Inc()
}

// RecordRelayWebhookQueued records a webhook added to the relay queue
func RecordRelayWebhookQueued(token string) {
	RelayWebhooksQueued.WithLabelValues(token).Inc()
}

// RecordRelayWebhookDelivered records a successful relay delivery
func RecordRelayWebhookDelivered(token string, durationSeconds float64) {
	RelayWebhooksDelivered.WithLabelValues(token).Inc()
	RelayDeliveryDuration.WithLabelValues(token).Observe(durationSeconds)
}

// RecordRelayDeliveryError records a relay delivery error
func RecordRelayDeliveryError(token, reason string) {
	RelayDeliveryErrors.WithLabelValues(token, reason).Inc()
}

// RecordRelayWebhooksPending updates the pending webhooks gauge
func RecordRelayWebhooksPending(token string, count int) {
	RelayWebhooksPending.WithLabelValues(token).Set(float64(count))
}

// RecordRelayClientsConnected updates the connected clients gauge
func RecordRelayClientsConnected(token string, count int) {
	RelayClientsConnected.WithLabelValues(token).Set(float64(count))
}

// RecordRateLimited records a request denied by rate limiting
func RecordRateLimited(route, limiter, reason string) {
	RateLimitedTotal.WithLabelValues(route, limiter, reason).Inc()
}
