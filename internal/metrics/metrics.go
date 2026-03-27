package metrics

import "github.com/prometheus/client_golang/prometheus"

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

func Register() {
	prometheus.MustRegister(RequestsTotal)
	prometheus.MustRegister(RateLimitedTotal)
	prometheus.MustRegister(RequestDuration)
}
