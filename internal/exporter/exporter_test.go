package exporter

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/MateuxLucax/unifique-speedtest-exporter/internal/speedtest"
)

type fakeRunner struct {
	calls   atomic.Int32
	delay   time.Duration
	err     error
	result  speedtest.Result
	started chan struct{}
}

func (f *fakeRunner) Run(ctx context.Context) (speedtest.Result, error) {
	f.calls.Add(1)
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return speedtest.Result{}, ctx.Err()
		}
	}
	return f.result, f.err
}

func TestRunOnceUpdatesGauges(t *testing.T) {
	runner := &fakeRunner{result: speedtest.Result{
		DownloadBps: 100e6, UploadBps: 50e6, PingMs: 12, JitterMs: 3,
	}}
	e := New(runner, prometheus.NewRegistry())

	if err := e.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	checkGauge(t, "download", e.download, 100e6)
	checkGauge(t, "upload", e.upload, 50e6)
	checkGauge(t, "ping", e.ping, 12)
	checkGauge(t, "jitter", e.jitter, 3)
	checkGauge(t, "success", e.success, 1)
	if testutil.ToFloat64(e.lastRun) == 0 {
		t.Error("lastRun timestamp not set")
	}
}

func TestRunOnceErrorKeepsLastGood(t *testing.T) {
	runner := &fakeRunner{result: speedtest.Result{DownloadBps: 100e6}}
	e := New(runner, prometheus.NewRegistry())

	if err := e.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}

	runner.err = errors.New("backend down")
	if err := e.RunOnce(context.Background()); err == nil {
		t.Fatal("expected error on second run")
	}

	checkGauge(t, "success", e.success, 0)
	checkGauge(t, "download last-good", e.download, 100e6) // preserved
}

func TestSingleFlightSkipsOverlap(t *testing.T) {
	runner := &fakeRunner{delay: 200 * time.Millisecond, started: make(chan struct{}, 1)}
	e := New(runner, prometheus.NewRegistry())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = e.RunOnce(context.Background())
	}()

	<-runner.started // ensure the first run holds the lock

	if err := e.RunOnce(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("overlapping RunOnce = %v, want ErrBusy", err)
	}
	wg.Wait()

	if got := runner.calls.Load(); got != 1 {
		t.Fatalf("runner called %d times, want 1 (overlap should be skipped)", got)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	runner := &fakeRunner{result: speedtest.Result{DownloadBps: 100e6, UploadBps: 50e6}}
	reg := prometheus.NewRegistry()
	e := New(runner, reg)
	if err := e.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, name := range []string{
		"speed_download_bits_per_second",
		"speed_upload_bits_per_second",
		"speed_ping_ms",
		"speed_jitter_ms",
		"speed_test_success",
		"speed_test_duration_seconds",
		"speed_test_last_run_timestamp_seconds",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("/metrics output missing %q", name)
		}
	}
}

func checkGauge(t *testing.T, name string, g prometheus.Gauge, want float64) {
	t.Helper()
	if got := testutil.ToFloat64(g); got != want {
		t.Errorf("%s gauge = %v, want %v", name, got, want)
	}
}
