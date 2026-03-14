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