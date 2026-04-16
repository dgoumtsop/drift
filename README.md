<div align="center">

# drift

**A production-grade API gateway built from scratch in Go**

[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-blue?style=flat-square)](LICENSE)
[![Docker](https://img.shields.io/badge/Docker-Compose-2496ED?style=flat-square&logo=docker)](docker-compose.yml)

*Rate limiting · Reverse proxy · Prometheus metrics · Live dashboard · k6 load testing*

</div>

---

## What is this

Drift is a fully custom API gateway — no frameworks, no magic. It started as a learning project and grew into a complete system demonstrating production-level patterns: distributed rate limiting with atomic Redis Lua scripts, Prometheus observability, a real-time SSE dashboard, and multi-scenario load testing.

Everything you'd find in a real gateway, built from first principles.

---

## Demo

> **Run the full stack:** `docker compose up --build`
> Then open [http://localhost:8080/dashboard](http://localhost:8080/dashboard)

The dashboard streams live metrics over SSE — requests/sec, rate-limited/sec, and avg latency — updating every second. Run the k6 load test in a separate terminal and watch the charts respond in real time.

---

## Architecture

```
                        ┌──────────────────────────────────────────┐
                        │              Drift Gateway               │
                        │                                          │
 client request ───────▶│  extractIP()                            │
                        │   └─ X-Forwarded-For / X-Real-IP /      │
                        │      net.SplitHostPort (IPv4 + IPv6)    │
                        │                  │                       │
                        │                  ▼                       │
                        │  ┌───────────────────────────┐           │
                        │  │      Rate Limiter          │           │
                        │  │                           │           │
                        │  │  Redis mode (distributed) │           │
                        │  │  ┌─────────────────────┐  │           │
                        │  │  │  Atomic Lua script  │  │           │
                        │  │  │  (token bucket,     │  │           │
                        │  │  │   no race cond.)    │  │           │
                        │  │  └─────────────────────┘  │           │
                        │  │                           │           │
                        │  │  In-memory fallback       │           │
                        │  │  ┌─────────────────────┐  │           │
                        │  │  │  Per-IP buckets      │  │           │
                        │  │  │  Cleanup goroutine  │  │           │
                        │  │  │  (no memory leak)   │  │           │
                        │  │  └─────────────────────┘  │           │
                        │  └───────────┬───────────────┘           │
                        │             │ allowed                     │
                        │             ▼                             │
                        │  ┌─────────────────────┐                 │
                        │  │   Reverse Proxy     │                 │
                        │  │   20s upstream      │                 │
                        │  │   header timeout    │                 │
                        │  │   error handler     │                 │
                        │  └──────────┬──────────┘                 │
                        │            │                             │
                        │  ┌─────────┴─────────┐                  │
                        │  │  Prometheus        │  SSE hub         │
                        │  │  + atomic mirrors  │◀── /dashboard    │
                        │  └────────────────────┘    (1s tick)     │
                        └──────────────────────────────────────────┘
                                     │
                                     ▼
                              upstream backend
```

---

## Features by phase

| # | Feature | Details |
|---|---------|---------|
| 1 | **Reverse proxy** | `net/http/httputil` · 20s upstream timeout · upstream error handler |
| 2 | **Token bucket rate limiter** | Per-IP · Redis Lua (distributed) or in-memory fallback · cleanup goroutine |
| 3 | **Prometheus metrics** | `drift_requests_total` · `drift_rate_limited_total` · `drift_request_duration_seconds` · `drift_upstream_errors_total` |
| 4 | **Live dashboard** | SSE stream · rolling 60s charts · req/s · limited/s · avg latency |
| 5 | **Docker + k6** | Multi-stage build · full Compose stack (gateway + Redis + Prometheus + Grafana) · 3-scenario load test |

---

## Quick start

### Run locally

```bash
git clone https://github.com/dgoumtsop/drift
cd drift

# dependencies (requires Go 1.25+)
go mod tidy

# run (proxies to httpbin.org by default)
go run ./cmd/gateway
```

```
Drift gateway starting on :8080
  backend   → https://httpbin.org
  dashboard → http://localhost:8080/dashboard
  metrics   → http://localhost:8080/metrics
  rl        → capacity=10 refill=5/s
```

### Run the full stack (Docker)

```bash
docker compose up --build
```

| Service    | URL                             | Notes |
|------------|---------------------------------|-------|
| Gateway    | http://localhost:8080           | Proxy + dashboard + metrics |
| Dashboard  | http://localhost:8080/dashboard | Live SSE metrics |
| Backend    | http://localhost:9999           | httpbin (direct) |
| Redis      | localhost:6379                  | Shared rate-limit state |
| Prometheus | http://localhost:9090           | Time-series DB |
| Grafana    | http://localhost:3000           | Dashboards (anonymous auth) |

Tear down and remove volumes:
```bash
docker compose down -v
```

---

## Configuration

All config is environment-based with safe defaults.

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | Listen port |
| `BACKEND_URL` | `https://httpbin.org` | Upstream target |
| `REDIS_URL` | *(unset)* | Redis address (`host:port`). If unset, uses in-memory limiter |
| `RL_CAPACITY` | `10` | Token bucket capacity (max burst) |
| `RL_REFILL_RATE` | `5` | Tokens added per second |
| `READ_TIMEOUT` | `10s` | HTTP server read timeout |
| `WRITE_TIMEOUT` | `30s` | HTTP server write timeout |
| `IDLE_TIMEOUT` | `60s` | HTTP server idle connection timeout |

---

## Rate limiting

### Redis mode (distributed)

When `REDIS_URL` is set, every gateway instance runs the same atomic Lua script against a shared Redis:

```lua
-- runs atomically in Redis — no race conditions across replicas
local data    = redis.call("HMGET", key, "tokens", "ts")
local elapsed = (now_ms - last_ts) / 1000.0
tokens        = math.min(cap, tokens + elapsed * rate)

if tokens >= cost then
  tokens = tokens - cost
  return 1   -- allow
end
return 0     -- rate limited
```

Keys are auto-expired (TTL = `capacity / refill_rate + 1s`) so idle clients don't accumulate in Redis.

### In-memory mode (single instance)

When Redis is unavailable or `REDIS_URL` is unset, the gateway uses a per-process in-memory map. A background goroutine evicts idle buckets every 5 minutes to prevent memory growth under DDoS.

### Fail-open behavior

If Redis returns an error mid-request, the limiter logs a warning and **allows the request** rather than dropping it. The gateway degrades gracefully — traffic passes through — rather than going dark. The in-memory limiter acts as a secondary guard in this scenario.

---

## Metrics

Scraped by Prometheus at `/metrics` (text/OpenMetrics format).

| Metric | Type | Labels |
|--------|------|--------|
| `drift_requests_total` | Counter | `method`, `path` |
| `drift_rate_limited_total` | Counter | — |
| `drift_request_duration_seconds` | Histogram | `method` |
| `drift_upstream_errors_total` | Counter | — |

### Useful PromQL queries

```promql
# Requests per second (1m window)
rate(drift_requests_total[1m])

# Rate-limited requests as % of total
rate(drift_rate_limited_total[1m]) / rate(drift_requests_total[1m]) * 100

# p95 upstream latency
histogram_quantile(0.95, rate(drift_request_duration_seconds_bucket[5m]))

# Upstream error rate
rate(drift_upstream_errors_total[1m])
```

---

## Load testing

Requires [k6](https://k6.io/docs/get-started/installation/).

```bash
# run against local gateway
k6 run k6/load_test.js

# run against Docker stack with JSON output
GATEWAY_URL=http://localhost:8080 k6 run --out json=k6/results.json k6/load_test.js
```

Three scenarios run sequentially:

| Scenario | VUs | Duration | Purpose |
|----------|-----|----------|---------|
| `warmup` | 5 | 30s | Baseline latency at low load |
| `spike` | 100 | 15s | Validate rate limiter fires under burst |
| `sustained` | 30 | 60s | Steady-state throughput and error rate |

**Thresholds** (test fails if breached):
- `http_req_failed` < 1% (non-429 errors)
- `drift_proxy_latency_ms` p95 < 500ms

---

## Project structure

```
drift/
├── cmd/gateway/
│   └── main.go                    # entrypoint, graceful shutdown, limiter selection
├── internal/
│   ├── config/config.go           # env-based configuration
│   ├── dashboard/dashboard.go     # SSE hub + embedded dashboard HTML
│   ├── metrics/metrics.go         # Prometheus counters + atomic mirrors
│   ├── proxy/proxy.go             # reverse proxy, IP extraction, error handling
│   └── ratelimit/
│       ├── limiter.go             # Limiter interface
│       ├── tokenbucket.go         # in-memory token bucket (cleanup goroutine)
│       └── redis.go               # Redis Lua atomic rate limiter
├── k6/load_test.js                # 3-scenario load test + custom metrics
├── grafana/provisioning/          # auto-wired Prometheus datasource
├── prometheus.yml                 # scrape config
├── docker-compose.yml             # full stack
├── Dockerfile                     # multi-stage build
└── README.md
```

---

## Known edge cases and decisions

**X-Forwarded-For spoofing** — clients can forge `X-Forwarded-For`. In production, only trust this header if the request comes from a known trusted proxy (your LB's IP). For a learning project behind a local Docker network, this is acceptable.

**In-memory limiter and horizontal scaling** — if you run multiple gateway instances without Redis, each has its own bucket map. The effective rate limit per client becomes `N × capacity` across N replicas. Use Redis mode for true horizontal scaling.

**SSE vs WebSocket** — the dashboard uses SSE because it's unidirectional (server pushes metrics, browser reads). SSE is stdlib-only and auto-reconnects. WebSocket would add a library dependency for no additional capability in this use case.

**Fail-open on Redis error** — the gateway allows requests when Redis is down. If you prefer fail-closed (reject all traffic when limiter is unavailable), change the `Allow` error path in `proxy.go`.

---

<div align="center">
Built by <a href="https://github.com/dgoumtsop">@dgoumtsop</a>
</div>
