// k6 load test for the drift gateway.
//
//   k6 run k6/load_test.js
//   GATEWAY_URL=http://localhost:8080 k6 run --out json=k6/results.json k6/load_test.js
//
// Three scenarios run back to back: warmup -> spike -> sustained.
// 429s are the rate limiter doing its job, so they count as expected
// responses, not failures. Only real errors (5xx, timeouts) fail the run.

import http from "k6/http";
import { check } from "k6";
import { Counter, Rate, Trend } from "k6/metrics";

const GATEWAY_URL = __ENV.GATEWAY_URL || "http://localhost:8080";

const proxyLatency = new Trend("drift_proxy_latency_ms", true);
const rateLimited = new Counter("drift_rate_limited");
const rateLimitedRatio = new Rate("drift_rate_limited_ratio");

http.setResponseCallback(http.expectedStatuses({ min: 200, max: 399 }, 429));

export const options = {
  scenarios: {
    warmup: {
      executor: "constant-vus",
      vus: 5,
      duration: "30s",
      tags: { scenario: "warmup" },
    },
    spike: {
      executor: "constant-vus",
      vus: 100,
      duration: "15s",
      startTime: "30s",
      tags: { scenario: "spike" },
    },
    sustained: {
      executor: "constant-vus",
      vus: 30,
      duration: "60s",
      startTime: "45s",
      tags: { scenario: "sustained" },
    },
  },
  thresholds: {
    http_req_failed: ["rate<0.01"],
    // only successful proxied requests -- a 429 returns instantly and
    // would make the gateway look faster than it is
    drift_proxy_latency_ms: ["p(95)<500"],
  },
};

export default function () {
  const res = http.get(`${GATEWAY_URL}/get`);

  check(res, {
    "status is 200 or 429": (r) => r.status === 200 || r.status === 429,
    "429 sets Retry-After": (r) => r.status !== 429 || r.headers["Retry-After"] !== undefined,
  });

  const limited = res.status === 429;
  rateLimitedRatio.add(limited);
  if (limited) {
    rateLimited.add(1);
  } else if (res.status === 200) {
    proxyLatency.add(res.timings.duration);
  }
}
