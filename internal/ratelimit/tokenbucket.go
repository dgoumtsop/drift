package ratelimit

import (
	"sync"
	"time"
)

type Bucket struct {
	tokens     float64
	capacity   float64
	refillRate  float64
	lastRefillTime  time.Time
	mu             sync.Mutex     // one goRoutine at a time
}

type RateLimiter struct {
	buckets      map[string]*Bucket
	capacity    float64
	refillRate  float64
	mu          sync.RWMutex    // multiple goRoutine
}

func New(capacity, refillRate float64)*RateLimiter {
	return &RateLimiter {
		buckets:       make(map[string]*Bucket),
		capacity:      capacity,
		refillRate:    refillRate,
	}
}

func (rl *RateLimiter) Allow(clientIP string) bool {
	rl.mu.Lock()
	buckets, exists
	bucket, exists := rl.buckets[clientIP]
	if !exists {
		bucket = &Bucket{
			tokens:         rl.capacity,
			capacity:       rl.capacity,
			refillRate:     rl.refillRate,
			lastRefillTime: time.Now(),
		}
		rl.buckets[clientIP] = bucket
	}
	rl.mu.Unlock()

	// Lock this specific bucket
	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	// Calculate tokens to add based on time elapsed
	now := time.Now()
	elapsed := now.Sub(bucket.lastRefillTime).Seconds()
	bucket.tokens += elapsed * bucket.refillRate
	
	// Cap at max capacity
	if bucket.tokens > bucket.capacity {
		bucket.tokens = bucket.capacity
	}
	bucket.lastRefillTime = now
	
	if bucket.tokens >= 1 {
		bucket.tokens -= 1
		return true  
	}

	return false  // Deny request
}
