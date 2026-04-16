package ratelimit

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// tokenBucketLua implements the token bucket algorithm as a single atomic
// Redis Lua script. Because Redis executes Lua scripts serially, there is no
// race condition — no two scripts can read-modify-write the same key
// simultaneously, even across multiple gateway replicas.
//
// KEYS[1] — hash key for this client IP's bucket
// ARGV[1] — bucket capacity (max tokens)
// ARGV[2] — refill rate (tokens per second)
// ARGV[3] — current time in milliseconds (passed in from Go for testability)
// ARGV[4] — cost (tokens to consume; always 1 for our gateway)
//
// Returns: 1 if allowed, 0 if rate limited.
var tokenBucketLua = redis.NewScript(`
local key      = KEYS[1]
local cap      = tonumber(ARGV[1])
local rate     = tonumber(ARGV[2])
local now_ms   = tonumber(ARGV[3])
local cost     = tonumber(ARGV[4])

local data     = redis.call("HMGET", key, "tokens", "ts")
local tokens   = tonumber(data[1])
local last_ts  = tonumber(data[2])

-- First access: initialise bucket at full capacity
if tokens == nil then
  tokens  = cap
  last_ts = now_ms
end

-- Refill proportional to elapsed time, capped at capacity
local elapsed_sec = math.max(0, (now_ms - last_ts) / 1000.0)
tokens  = math.min(cap, tokens + elapsed_sec * rate)
last_ts = now_ms

-- Attempt to consume 'cost' tokens
local allowed = 0
if tokens >= cost then
  tokens  = tokens - cost
  allowed = 1
end

-- TTL = time to fully refill from 0 + 1s buffer
-- This auto-expires idle keys so Redis memory stays clean.
local ttl = math.ceil(cap / rate) + 1
redis.call("HMSET",  key, "tokens", tokens, "ts", last_ts)
redis.call("EXPIRE", key, ttl)

return allowed
`)

// RedisLimiter is a distributed token bucket rate limiter backed by Redis.
// All gateway replicas share the same bucket state, so the per-IP rate limit
// is enforced globally even when traffic is spread across multiple instances.
//
// If Redis is unreachable, Allow falls back to permitting the request and
// logs a warning — the gateway degrades gracefully rather than going dark.
type RedisLimiter struct {
	client     *redis.Client
	capacity   float64
	refillRate float64
	keyPrefix  string
}

// NewRedis connects to the Redis instance at addr and returns a RedisLimiter.
// addr format: "host:port" (e.g. "localhost:6379" or "redis:6379" in Docker).
func NewRedis(addr string, capacity, refillRate float64) (*RedisLimiter, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  1 * time.Second,
		WriteTimeout: 1 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	return &RedisLimiter{
		client:     client,
		capacity:   capacity,
		refillRate: refillRate,
		keyPrefix:  "drift:rl:",
	}, nil
}

// Allow runs the Lua token bucket script and returns whether the request
// from ip should be permitted.
func (rl *RedisLimiter) Allow(ip string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	key := rl.keyPrefix + ip
	nowMs := time.Now().UnixMilli()

	result, err := tokenBucketLua.Run(ctx, rl.client,
		[]string{key},
		rl.capacity,
		rl.refillRate,
		nowMs,
		1, // cost
	).Int()

	if err != nil {
		// Redis unavailable — fail open so the gateway stays up.
		// The in-memory limiter in main.go acts as a secondary guard.
		log.Printf("[ratelimit] redis error (failing open): %v", err)
		return true, fmt.Errorf("redis error: %w", err)
	}

	return result == 1, nil
}

// Close shuts down the Redis client connection pool.
func (rl *RedisLimiter) Close() error {
	return rl.client.Close()
}
