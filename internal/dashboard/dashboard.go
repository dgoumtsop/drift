// Package dashboard implements Phase 4: a real-time metrics dashboard.
//
// Transport: Server-Sent Events (SSE) over HTTP — stdlib-only, appropriate
// for unidirectional server→client metric streaming. The browser connects to
// /dashboard/stream via the EventSource API; the server pushes a JSON snapshot
// every second. The dashboard page itself is served at /dashboard.
package dashboard

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/dgoumtsop/drift/internal/metrics"
)

// Snapshot is one second of gateway telemetry pushed to every connected client.
type Snapshot struct {
	Timestamp      int64   `json:"ts"`            // Unix ms
	TotalRequests  int64   `json:"totalRequests"` // cumulative
	TotalLimited   int64   `json:"totalLimited"`  // cumulative
	ReqsPerSec     float64 `json:"reqsPerSec"`    // delta vs previous snapshot
	LimitedPerSec  float64 `json:"limitedPerSec"` // delta vs previous snapshot
	AvgLatencyMs   float64 `json:"avgLatencyMs"`  // rolling avg since last snapshot
}

// client represents one connected browser tab.
type client struct {
	ch chan []byte
}

// Hub manages the set of active SSE clients and broadcasts snapshots to all of them.
type Hub struct {
	mu      sync.Mutex
	clients map[*client]struct{}

	// state for delta computation
	prevRequests  int64
	prevLimited   int64
	prevLatencyNs int64
	prevLatCount  int64
}

// NewHub allocates a Hub. Call Run() in a goroutine.
func NewHub() *Hub {
	return &Hub{
		clients: make(map[*client]struct{}),
	}
}

// Run starts the one-second broadcast loop. It blocks forever; call it in a goroutine.
func (h *Hub) Run() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		snap := h.collect()
		data, err := json.Marshal(snap)
		if err != nil {
			log.Printf("[dashboard] marshal error: %v", err)
			continue
		}
		h.broadcast(data)
	}
}

// collect builds a Snapshot by reading the atomic counters in the metrics package.
func (h *Hub) collect() Snapshot {
	now := time.Now()

	totalReqs := metrics.AtomicRequests.Load()
	totalLimited := metrics.AtomicRateLimited.Load()
	totalLatNs := metrics.AtomicLatencyNs.Load()
	totalLatCount := metrics.AtomicLatencyCount.Load()

	deltaReqs := totalReqs - h.prevRequests
	deltaLimited := totalLimited - h.prevLimited
	deltaLatNs := totalLatNs - h.prevLatencyNs
	deltaLatCount := totalLatCount - h.prevLatCount

	h.prevRequests = totalReqs
	h.prevLimited = totalLimited
	h.prevLatencyNs = totalLatNs
	h.prevLatCount = totalLatCount

	var avgLatMs float64
	if deltaLatCount > 0 {
		avgLatMs = float64(deltaLatNs) / float64(deltaLatCount) / 1e6
	}

	return Snapshot{
		Timestamp:     now.UnixMilli(),
		TotalRequests: totalReqs,
		TotalLimited:  totalLimited,
		ReqsPerSec:    float64(deltaReqs),
		LimitedPerSec: float64(deltaLimited),
		AvgLatencyMs:  avgLatMs,
	}
}

// broadcast sends data to every registered client, dropping slow ones.
func (h *Hub) broadcast(data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.ch <- data:
		default:
			// client is too slow — drop this tick rather than block
		}
	}
}

func (h *Hub) add(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// StreamHandler handles GET /dashboard/stream.
// It upgrades the connection to SSE and streams metric snapshots until the client disconnects.
func (h *Hub) StreamHandler(w http.ResponseWriter, r *http.Request) {
	// SSE requires these three headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	c := &client{ch: make(chan []byte, 16)}
	h.add(c)
	defer h.remove(c)

	log.Printf("[dashboard] client connected: %s", r.RemoteAddr)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			log.Printf("[dashboard] client disconnected: %s", r.RemoteAddr)
			return
		case data := <-c.ch:
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// PageHandler serves GET /dashboard — the standalone dashboard UI.
func PageHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, dashboardHTML)
}

// dashboardHTML is the full dashboard SPA, served inline to keep the binary self-contained.
// Uses Chart.js (CDN) + EventSource for live updates. Zero build step required.
const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8"/>
<meta name="viewport" content="width=device-width,initial-scale=1"/>
<title>Drift — Live Dashboard</title>
<script src="https://cdn.jsdelivr.net/npm/chart.js@4.4.3/dist/chart.umd.min.js"></script>
<style>
  :root {
    --bg: #0d1117;
    --surface: #161b22;
    --border: #30363d;
    --accent: #58a6ff;
    --accent2: #f78166;
    --accent3: #3fb950;
    --text: #e6edf3;
    --muted: #8b949e;
    --red: #f85149;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    background: var(--bg);
    color: var(--text);
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', monospace;
    min-height: 100vh;
    padding: 24px;
  }
  header {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 28px;
    border-bottom: 1px solid var(--border);
    padding-bottom: 16px;
  }
  header h1 {
    font-size: 1.4rem;
    font-weight: 600;
    letter-spacing: -0.5px;
  }
  header h1 span { color: var(--accent); }
  .badge {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 0.72rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 20px;
    padding: 3px 10px;
    color: var(--muted);
  }
  .badge .dot {
    width: 7px; height: 7px;
    border-radius: 50%;
    background: var(--red);
    transition: background 0.3s;
  }
  .badge .dot.live { background: var(--accent3); }

  .stats {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
    gap: 16px;
    margin-bottom: 28px;
  }
  .stat-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 18px 20px;
  }
  .stat-card .label {
    font-size: 0.72rem;
    color: var(--muted);
    text-transform: uppercase;
    letter-spacing: 0.06em;
    margin-bottom: 8px;
  }
  .stat-card .value {
    font-size: 2rem;
    font-weight: 700;
    font-variant-numeric: tabular-nums;
  }
  .stat-card .sub {
    font-size: 0.75rem;
    color: var(--muted);
    margin-top: 4px;
  }
  .c-blue  { color: var(--accent); }
  .c-red   { color: var(--accent2); }
  .c-green { color: var(--accent3); }

  .charts {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(380px, 1fr));
    gap: 20px;
  }
  .chart-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 18px 20px;
  }
  .chart-card h3 {
    font-size: 0.8rem;
    color: var(--muted);
    text-transform: uppercase;
    letter-spacing: 0.06em;
    margin-bottom: 14px;
  }
  canvas { display: block; }

  footer {
    margin-top: 28px;
    font-size: 0.7rem;
    color: var(--muted);
    text-align: center;
  }
</style>
</head>
<body>

<header>
  <h1><span>drift</span> gateway</h1>
  <div class="badge">
    <span class="dot" id="dot"></span>
    <span id="status-text">connecting…</span>
  </div>
</header>

<div class="stats">
  <div class="stat-card">
    <div class="label">Total Requests</div>
    <div class="value c-blue" id="s-total">—</div>
    <div class="sub">since gateway start</div>
  </div>
  <div class="stat-card">
    <div class="label">Requests / sec</div>
    <div class="value c-green" id="s-rps">—</div>
    <div class="sub">last second</div>
  </div>
  <div class="stat-card">
    <div class="label">Rate Limited</div>
    <div class="value c-red" id="s-limited">—</div>
    <div class="sub">total rejected</div>
  </div>
  <div class="stat-card">
    <div class="label">Avg Latency</div>
    <div class="value c-blue" id="s-latency">—</div>
    <div class="sub">ms · proxied requests</div>
  </div>
</div>

<div class="charts">
  <div class="chart-card">
    <h3>Requests / sec</h3>
    <canvas id="rpsChart" height="130"></canvas>
  </div>
  <div class="chart-card">
    <h3>Rate Limited / sec</h3>
    <canvas id="limitChart" height="130"></canvas>
  </div>
  <div class="chart-card">
    <h3>Avg Latency (ms)</h3>
    <canvas id="latChart" height="130"></canvas>
  </div>
</div>

<footer>drift · sse stream → /dashboard/stream · prometheus → /metrics</footer>

<script>
const MAX_POINTS = 60; // 60-second rolling window

function makeChart(id, label, color) {
  const ctx = document.getElementById(id).getContext('2d');
  return new Chart(ctx, {
    type: 'line',
    data: {
      labels: [],
      datasets: [{
        label,
        data: [],
        borderColor: color,
        backgroundColor: color + '18',
        borderWidth: 2,
        pointRadius: 0,
        fill: true,
        tension: 0.3,
      }]
    },
    options: {
      animation: false,
      responsive: true,
      plugins: { legend: { display: false } },
      scales: {
        x: {
          display: false,
        },
        y: {
          min: 0,
          grid: { color: '#30363d' },
          ticks: { color: '#8b949e', font: { size: 11 } }
        }
      }
    }
  });
}

const rpsChart   = makeChart('rpsChart',   'req/s',       '#58a6ff');
const limitChart = makeChart('limitChart', 'limited/s',   '#f78166');
const latChart   = makeChart('latChart',   'latency ms',  '#58a6ff');

function push(chart, label, value) {
  chart.data.labels.push(label);
  chart.data.datasets[0].data.push(value);
  if (chart.data.labels.length > MAX_POINTS) {
    chart.data.labels.shift();
    chart.data.datasets[0].data.shift();
  }
  chart.update('none');
}

function fmt(n, decimals = 0) {
  if (n === null || n === undefined) return '—';
  return n.toFixed(decimals);
}

const dot  = document.getElementById('dot');
const statusText = document.getElementById('status-text');

function connect() {
  const es = new EventSource('/dashboard/stream');

  es.onopen = () => {
    dot.classList.add('live');
    statusText.textContent = 'live';
  };

  es.onmessage = (e) => {
    const d = JSON.parse(e.data);
    const label = new Date(d.ts).toLocaleTimeString();

    document.getElementById('s-total').textContent   = d.totalRequests.toLocaleString();
    document.getElementById('s-limited').textContent = d.totalLimited.toLocaleString();
    document.getElementById('s-rps').textContent     = fmt(d.reqsPerSec);
    document.getElementById('s-latency').textContent = d.avgLatencyMs > 0 ? fmt(d.avgLatencyMs, 1) + ' ms' : '—';

    push(rpsChart,   label, d.reqsPerSec);
    push(limitChart, label, d.limitedPerSec);
    if (d.avgLatencyMs > 0) push(latChart, label, d.avgLatencyMs);
  };

  es.onerror = () => {
    dot.classList.remove('live');
    statusText.textContent = 'reconnecting…';
    es.close();
    setTimeout(connect, 3000);
  };
}

connect();
</script>
</body>
</html>`
