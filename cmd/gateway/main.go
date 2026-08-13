package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dgoumtsop/drift/internal/config"
	"github.com/dgoumtsop/drift/internal/dashboard"
	"github.com/dgoumtsop/drift/internal/metrics"
	"github.com/dgoumtsop/drift/internal/proxy"
	"github.com/dgoumtsop/drift/internal/ratelimit"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	metrics.Register()

	// Rate limiter setup 
	// If REDIS_URL is set, use the distributed Redis limiter so all gateway
	// replicas share a single rate-limit state. Otherwise fall back to the
	// in-memory limiter 
	var limiter ratelimit.Limiter

	if cfg.RedisURL != "" {
		rl, err := ratelimit.NewRedis(cfg.RedisURL, cfg.RLCapacity, cfg.RLRefillRate)
		if err != nil {
			log.Printf("[WARNING] Redis unavailable (%v) — falling back to in-memory limiter", err)
			limiter = ratelimit.NewInMemory(cfg.RLCapacity, cfg.RLRefillRate)
		} else {
			log.Printf("Rate limiter: Redis (%s)", cfg.RedisURL)
			limiter = rl
		}
	} else {
		log.Printf("Rate limiter: in-memory (set REDIS_URL for distributed mode)")
		limiter = ratelimit.NewInMemory(cfg.RLCapacity, cfg.RLRefillRate)
	}

	// Proxy 
	reverseProxy, err := proxy.New(cfg.BackendURL, limiter)
	if err != nil {
		log.Fatalf("proxy: %v", err)
	}

	// Dashboard 
	hub := dashboard.NewHub()
	go hub.Run()

	// ── Routes ───────────────────────────────────────────────────────────────
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/dashboard", dashboard.PageHandler)
	mux.HandleFunc("/dashboard/stream", hub.StreamHandler)
	mux.HandleFunc("/", reverseProxy.ServeHTTP)

	// ── HTTP server with timeouts ─────────────────────────────────────────────
	// Never use http.ListenAndServe directly — it has no timeouts so a slow
	// client can hold a connection open forever and exhaust file descriptors.
	addr := fmt.Sprintf(":%s", cfg.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	log.Printf("Drift gateway starting on %s", addr)
	log.Printf("  backend   → %s", cfg.BackendURL)
	log.Printf("  dashboard → http://localhost%s/dashboard", addr)
	log.Printf("  metrics   → http://localhost%s/metrics", addr)
	log.Printf("  rl        → capacity=%.0f refill=%.0f/s", cfg.RLCapacity, cfg.RLRefillRate)

	// Start server in a goroutine so we can block on the signal channel below.
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	// Block until SIGINT or SIGTERM, then give in-flight requests 30s to finish.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Printf("Signal received (%s) — shutting down gracefully...", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}
	log.Println("Server stopped cleanly")
}
