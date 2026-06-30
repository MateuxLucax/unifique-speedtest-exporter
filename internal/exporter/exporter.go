// Package exporter wires the speed test into Prometheus. It runs the test in
// the background on an interval, caches the results, and serves them instantly
// on every scrape — so Prometheus scrape timing is decoupled from the
// multi-minute test, and a single-flight guard ensures only one link-saturating
// test runs at a time.
//
// It deliberately depends on nothing outside the standard library: the four
// label-less gauges plus health metrics are emitted directly in the Prometheus
// text exposition format, keeping the supply-chain attack surface at zero
// third-party packages.
package exporter

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

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

// snapshot is the cached result set served on every scrape.
type snapshot struct {
	download float64
	upload   float64
	ping     float64
	jitter   float64

	success         float64
	durationSeconds float64
	lastRunUnix     float64
}

// Exporter caches the latest test result and serves it as Prometheus metrics.
// It implements http.Handler for the /metrics endpoint.
type Exporter struct {
	runner Runner

	// RunTimeout bounds each individual test run. Defaults to DefaultRunTimeout.
	RunTimeout time.Duration

	runMu sync.Mutex // single-flight: held for the whole duration of a run

	mu  sync.RWMutex // guards cur; held only briefly
	cur snapshot
}

// New builds an Exporter for the given runner.
func New(runner Runner) *Exporter {
	return &Exporter{runner: runner, RunTimeout: DefaultRunTimeout}
}

// RunOnce runs a single test under the single-flight guard and updates the
// cached snapshot. If a run is already in progress it returns ErrBusy without
// starting another. On test failure it sets speed_test_success to 0 and leaves
// the last good speed values intact, so stale-but-valid data stays scrapeable.
func (e *Exporter) RunOnce(ctx context.Context) error {
	if !e.runMu.TryLock() {
		return ErrBusy
	}
	defer e.runMu.Unlock()

	timeout := e.RunTimeout
	if timeout <= 0 {
		timeout = DefaultRunTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	res, err := e.runner.Run(runCtx)
	elapsed := time.Since(start).Seconds()
	now := float64(time.Now().Unix())

	e.mu.Lock()
	defer e.mu.Unlock()
	e.cur.durationSeconds = elapsed
	e.cur.lastRunUnix = now
	if err != nil {
		e.cur.success = 0
		return err
	}
	e.cur.download = res.DownloadBps
	e.cur.upload = res.UploadBps
	e.cur.ping = res.PingMs
	e.cur.jitter = res.JitterMs
	e.cur.success = 1
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

// ServeHTTP writes the cached metrics in the Prometheus text exposition format.
func (e *Exporter) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	e.mu.RLock()
	m := e.cur
	e.mu.RUnlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	writeGauge(w, "speed_download_bits_per_second", "Download speed in bits per second", m.download)
	writeGauge(w, "speed_upload_bits_per_second", "Upload speed in bits per second", m.upload)
	writeGauge(w, "speed_ping_ms", "Ping in milliseconds", m.ping)
	writeGauge(w, "speed_jitter_ms", "Jitter in milliseconds", m.jitter)
	writeGauge(w, "speed_test_success", "1 if the last speed test succeeded, 0 otherwise", m.success)
	writeGauge(w, "speed_test_duration_seconds", "Duration of the last speed test run in seconds", m.durationSeconds)
	writeGauge(w, "speed_test_last_run_timestamp_seconds", "Unix timestamp of the last speed test run", m.lastRunUnix)
}

func writeGauge(w io.Writer, name, help string, value float64) {
	io.WriteString(w, "# HELP "+name+" "+help+"\n")
	io.WriteString(w, "# TYPE "+name+" gauge\n")
	io.WriteString(w, name+" "+strconv.FormatFloat(value, 'g', -1, 64)+"\n")
}
