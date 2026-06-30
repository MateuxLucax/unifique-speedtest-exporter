// Command unifique-speedtest-exporter runs a LibreSpeed speed test against
// https://speed.unifique.com.br on an interval and exposes the results as
// Prometheus metrics at /metrics.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MateuxLucax/unifique-speedtest-exporter/internal/exporter"
	"github.com/MateuxLucax/unifique-speedtest-exporter/internal/speedtest"
)

func main() {
	var (
		baseURL  = env("SPEEDTEST_URL", "https://speed.unifique.com.br")
		port     = env("PORT", "3000")
		interval = envDuration("RUN_INTERVAL", 10*time.Minute)
	)

	cfg := speedtest.DefaultConfig(baseURL)
	cfg.HTTPClient = speedtest.NewClient()

	exp := exporter.New(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go exp.Start(ctx, interval)

	mux := http.NewServeMux()
	mux.Handle("/metrics", exp)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
		// Bound timeouts so slow clients can't tie up connections; /metrics only
		// serves a small cached payload, so these are generous.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("unifique-speedtest-exporter listening on :%s, testing %s every %s", port, baseURL, interval)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Printf("invalid %s=%q, using default %s", key, v, fallback)
	}
	return fallback
}
