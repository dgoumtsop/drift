package metrics

import (
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

// Prometheus metrics
var (
	RequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "drift_requests_total",
			Help: "Total number of requests received",
		},
		[]string{"method", "path"},
	)

	RateLimitedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "drift_rate_limited_total",
			Help: "Total number of requests rejected by rate limiter",
		},
	)

	RequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "drift_request_duration_seconds",
			Help:    "Request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method"},
	)
)

// Atomic counters consumed by the live dashboard.
// Updated in lockstep with Prometheus metrics
var (
	AtomicRequests     atomic.Int64
	AtomicRateLimited  atomic.Int64
	AtomicLatencyNs    atomic.Int64 // cumulative nanoseconds
	AtomicLatencyCount atomic.Int64
)

func Register() {
	prometheus.MustRegister(RequestsTotal)
	prometheus.MustRegister(RateLimitedTotal)
	prometheus.MustRegister(RequestDuration)
	prometheus.MustRegister(UpstreamErrorsTotal)
}

// UpstreamErrorsTotal counts requests where the backend returned an error
// (connection refused, timeout, etc.) — distinct from 4xx/5xx HTTP status codes.
var UpstreamErrorsTotal = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "drift_upstream_errors_total",
		Help: "Total number of upstream/backend errors (timeouts, connection failures)",
	},
)
