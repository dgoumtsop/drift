package ratelimit

import (
	"sync"
	"time"
)

// bucket holds the token state for a single client IP.
type bucket struct {
	tokens         float64
	capacity       float64
	refillRate     float64
	lastRefillTime time.Time
	mu             sync.Mutex
}

// InMemoryLimiter is a per-IP token bucket rate limiter backed by a Go map.
// It satisfies the Limiter interface and is safe for concurrent use.
//
// Use this when running a single gateway instance. For distributed deployments
// (multiple gateway replicas behind a load balancer) use RedisLimiter instead,
// which keeps shared state in Redis via an atomic Lua script.
type InMemoryLimiter struct {
	buckets    map[string]*bucket
	capacity   float64
	refillRate float64
	mu         sync.RWMutex // protects the buckets map
}

// NewInMemory returns a rate limiter with the given bucket capacity and
// per-second refill rate. It starts a background goroutine that evicts
// stale buckets every 5 minutes to prevent unbounded memory growth.
func NewInMemory(capacity, refillRate float64) *InMemoryLimiter {
	rl := &InMemoryLimiter{
		buckets:    make(map[string]*bucket),
		capacity:   capacity,
		refillRate: refillRate,
	}
	go rl.cleanup()
	return rl
}

// New is a convenience alias kept for backwards compatibility.
func New(capacity, refillRate float64) *InMemoryLimiter {
	return NewInMemory(capacity, refillRate)
}

// Allow returns true if the request from ip should be allowed.
// The error return is always nil for the in-memory backend; it exists
// to satisfy the Limiter interface shared with RedisLimiter.
func (rl *InMemoryLimiter) Allow(ip string) (bool, error) {
	// Fast path: read lock — bucket already exists for this IP
	rl.mu.RLock()
	b, exists := rl.buckets[ip]
	rl.mu.RUnlock()

	if !exists {
		// Slow path: write lock — new IP, create a bucket
		rl.mu.Lock()
		// Double-check: another goroutine might have inserted while we waited
		b, exists = rl.buckets[ip]
		if !exists {
			b = &bucket{
				tokens:         rl.capacity,
				capacity:       rl.capacity,
				refillRate:     rl.refillRate,
				lastRefillTime: time.Now(),
			}
			rl.buckets[ip] = b
		}
		rl.mu.Unlock()
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.lastRefillTime).Seconds()
	b.tokens += elapsed * b.refillRate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.lastRefillTime = now

	if b.tokens >= 1 {
		b.tokens--
		return true, nil
	}
	return false, nil
}

// cleanup evicts buckets that have been fully refilled (idle clients) every
// 5 minutes. This prevents the map from growing without bound under a
// sustained DDoS with many unique source IPs.
func (rl *InMemoryLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		for ip, b := range rl.buckets {
			b.mu.Lock()
			// A fully-refilled bucket means the client has been idle for at
			// least capacity/refillRate seconds — safe to evict.
			elapsed := time.Since(b.lastRefillTime).Seconds()
			fullRefillTime := b.capacity / b.refillRate
			if elapsed > fullRefillTime {
				delete(rl.buckets, ip)
			}
			b.mu.Unlock()
		}
		rl.mu.Unlock()
	}
}
