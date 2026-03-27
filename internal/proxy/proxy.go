package proxy

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/dgoumtsop/drift/internal/metrics"
	"github.com/dgoumtsop/drift/internal/ratelimit"
)

type ReverseProxy struct {
	proxy       *httputil.ReverseProxy
	rateLimiter *ratelimit.RateLimiter
}

func New(backendURL string, rateLimiter *ratelimit.RateLimiter) (*ReverseProxy, error) {
	target, err := url.Parse(backendURL)
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)

	return &ReverseProxy{
		proxy:       proxy,
		rateLimiter: rateLimiter,
	}, nil
}

func (rp *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	metrics.RequestsTotal.WithLabelValues(r.Method, r.URL.Path).Inc()

	clientIP := strings.Split(r.RemoteAddr, ":")[0]
	if !rp.rateLimiter.Allow(clientIP) {
		metrics.RateLimitedTotal.Inc()
		http.Error(w, "Rate Limit exceeded", http.StatusTooManyRequests)
		return
	}

	log.Printf("[PROXY] %s %s -> forwarding to backend", r.Method, r.URL.Path)
	rp.proxy.ServeHTTP(w, r)

	metrics.RequestDuration.WithLabelValues(r.Method).Observe(time.Since(start).Seconds())
}
