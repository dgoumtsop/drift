package ratelimit

// Limiter is the rate-limiting interface satisfied by both the in-memory
// token bucket and the Redis-backed implementation.
// proxy.ReverseProxy depends only on this interface so either backend
// can be swapped in (or mocked in tests) without touching the proxy code.
type Limiter interface {
	Allow(ip string) (bool, error)
}
