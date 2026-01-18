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
