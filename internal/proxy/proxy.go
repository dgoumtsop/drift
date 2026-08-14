package proxy

import (
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/dgoumtsop/drift/internal/metrics"
	"github.com/dgoumtsop/drift/internal/ratelimit"
)

// ReverseProxy wraps httputil.ReverseProxy with rate limiting and observability.
type ReverseProxy struct {
	proxy       *httputil.ReverseProxy
	rateLimiter ratelimit.Limiter // interface — works with in-memory or Redis
}

// New creates a gateway proxy that forwards traffic to backendURL
// rateLimiter can be either *ratelimit.InMemoryLimiter or *ratelimit.RedisLimiter.
func New(backendURL string, rateLimiter ratelimit.Limiter) (*ReverseProxy, error) {
	target, err := url.Parse(backendURL)
	if err != nil {
		return nil, err
	}

	rp := httputil.NewSingleHostReverseProxy(target)

	// Give the upstream a 20-second response-header deadline so a hung
	// backend doesn't hold a connection open indefinitely.
	rp.Transport = &http.Transport{
		ResponseHeaderTimeout: 20 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:    100,
		IdleConnTimeout: 90 * time.Second,
	}

	// Log upstream errors without panicking.
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("[PROXY] upstream error for %s %s: %v", r.Method, r.URL.Path, err)
		metrics.UpstreamErrorsTotal.Inc()
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}

	return &ReverseProxy{
		proxy:       rp,
		rateLimiter: rateLimiter,
	}, nil
}

// ServeHTTP implements http.Handler.
func (rp *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	metrics.RequestsTotal.WithLabelValues(r.Method, r.URL.Path).Inc()
	metrics.AtomicRequests.Add(1)

	clientIP := extractIP(r)
	allowed, err := rp.rateLimiter.Allow(clientIP)
	if err != nil {
		// Limiter returned an error (e.g. Redis down). We already logged it
		// inside the limiter; 'allowed' will be true (fail-open). Continue.
		log.Printf("[PROXY] rate limiter error (failing open): %v", err)
	}

	if !allowed {
		metrics.RateLimitedTotal.Inc()
		metrics.AtomicRateLimited.Add(1)
		w.Header().Set("Retry-After", "1")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	log.Printf("[PROXY] %s %s → backend (client: %s)", r.Method, r.URL.Path, clientIP)
	rp.proxy.ServeHTTP(w, r)

	elapsed := time.Since(start)
	metrics.RequestDuration.WithLabelValues(r.Method).Observe(elapsed.Seconds())
	metrics.AtomicLatencyNs.Add(elapsed.Nanoseconds())
	metrics.AtomicLatencyCount.Add(1)
}

// extractIP returns the real client IP from the request.
//
// Priority:
//  1. X-Forwarded-For (set by load balancers / reverse proxies)
//  2. X-Real-IP (set by Nginx)
//  3. RemoteAddr (direct connection)
//
// Using strings.Split(RemoteAddr, ":")[0] is broken for IPv6 addresses
// like [::1]:8080 — net.SplitHostPort handles both IPv4 and IPv6.
func extractIP(r *http.Request) string {
	// X-Forwarded-For may contain a comma-separated list; take the first
	// (leftmost) entry which is the original client.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[0]); ip != "" {
			return ip
		}
	}

	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// SplitHostPort failed (no port?), use RemoteAddr as-is.
		return r.RemoteAddr
	}
	return host
}
