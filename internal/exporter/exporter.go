// Package exporter wires the speed test into Prometheus. It runs the test in
// the background on an interval, caches the results as gauges, and serves them
// instantly on every scrape — so Prometheus scrape timing is decoupled from the
// multi-minute test, and a single-flight guard ensures only one link-saturating
// test runs at a time.
package exporter

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/MateuxLucax/unifique-speedtest-exporter/internal/speedtest"
)

// DefaultRunTimeout bounds a single test run so a hung request can't block all
// future runs. It matches the scrape_timeout suggested in the README.
const DefaultRunTimeout = 2 * time.Minute

// ErrBusy is returned by RunOnce when a test is already in progress; the
// trigger is skipped rather than queued.
var ErrBusy = errors.New("speed test already in progress")

// Runner executes a single speed test. speedtest.Config satisfies it.
type Runner interface {
	Run(ctx context.Context) (speedtest.Result, error)
}

// Exporter holds the metric gauges and the single-flight guard.
type Exporter struct {
	runner Runner

	// RunTimeout bounds each individual test run. Defaults to DefaultRunTimeout.
	RunTimeout time.Duration

	mu sync.Mutex // single-flight: held for the duration of a run

	download prometheus.Gauge
	upload   prometheus.Gauge
	ping     prometheus.Gauge
	jitter   prometheus.Gauge
	success  prometheus.Gauge
	duration prometheus.Gauge
	lastRun  prometheus.Gauge
}

// New builds an Exporter and registers its metrics on reg.
func New(runner Runner, reg prometheus.Registerer) *Exporter {
	e := &Exporter{
		runner:     runner,
		RunTimeout: DefaultRunTimeout,
		download:   gauge("speed_download_bits_per_second", "Download speed in bits per second"),
		upload:     gauge("speed_upload_bits_per_second", "Upload speed in bits per second"),
		ping:       gauge("speed_ping_ms", "Ping in milliseconds"),
		jitter:     gauge("speed_jitter_ms", "Jitter in milliseconds"),
		success:    gauge("speed_test_success", "1 if the last speed test succeeded, 0 otherwise"),
		duration:   gauge("speed_test_duration_seconds", "Duration of the last speed test run in seconds"),
		lastRun:    gauge("speed_test_last_run_timestamp_seconds", "Unix timestamp of the last speed test run"),
	}
	reg.MustRegister(e.download, e.upload, e.ping, e.jitter, e.success, e.duration, e.lastRun)
	return e
}

func gauge(name, help string) prometheus.Gauge {
	return prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
}

// RunOnce runs a single test under the single-flight guard and updates the
// gauges. If a run is already in progress it returns ErrBusy without starting
// another. On test failure it sets speed_test_success to 0 and leaves the last
// good speed gauges untouched, so stale-but-valid data remains scrapeable.
func (e *Exporter) RunOnce(ctx context.Context) error {
	if !e.mu.TryLock() {
		return ErrBusy
	}
	defer e.mu.Unlock()

	timeout := e.RunTimeout
	if timeout <= 0 {
		timeout = DefaultRunTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	res, err := e.runner.Run(runCtx)
	e.duration.Set(time.Since(start).Seconds())
	e.lastRun.Set(float64(time.Now().Unix()))
	if err != nil {
		e.success.Set(0)
		return err
	}

	e.download.Set(res.DownloadBps)
	e.upload.Set(res.UploadBps)
	e.ping.Set(res.PingMs)
	e.jitter.Set(res.JitterMs)
	e.success.Set(1)
	return nil
}

// Start runs the test once immediately, then on every interval tick until ctx
// is cancelled. It blocks, so callers typically run it in a goroutine. Runs
// that coincide with an in-progress run are skipped (ErrBusy).
func (e *Exporter) Start(ctx context.Context, interval time.Duration) {
	e.runAndLog(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.runAndLog(ctx)
		}
	}
}

func (e *Exporter) runAndLog(ctx context.Context) {
	if err := e.RunOnce(ctx); err != nil && !errors.Is(err, ErrBusy) && ctx.Err() == nil {
		log.Printf("speed test failed: %v", err)
	}
}
