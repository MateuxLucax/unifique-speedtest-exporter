package exporter

import (
	"context"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// scrape renders the exporter's /metrics output and parses each
// "name value" sample line into a map.
func scrape(t *testing.T, e *Exporter) map[string]float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	e.ServeHTTP(rec, req)

	out := map[string]float64{}
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("malformed metric line: %q", line)
		}
		v, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			t.Fatalf("unparseable value in %q: %v", line, err)
		}
		out[fields[0]] = v
	}
	return out
}

func TestRunOnceUpdatesGauges(t *testing.T) {
	runner := &fakeRunner{result: speedtest.Result{
		DownloadBps: 100e6, UploadBps: 50e6, PingMs: 12, JitterMs: 3,
	}}
	e := New(runner)

	if err := e.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	m := scrape(t, e)
	checkGauge(t, m, "speed_download_bits_per_second", 100e6)
	checkGauge(t, m, "speed_upload_bits_per_second", 50e6)
	checkGauge(t, m, "speed_ping_ms", 12)
	checkGauge(t, m, "speed_jitter_ms", 3)
	checkGauge(t, m, "speed_test_success", 1)
	if m["speed_test_last_run_timestamp_seconds"] == 0 {
		t.Error("lastRun timestamp not set")
	}
}

func TestRunOnceErrorKeepsLastGood(t *testing.T) {
	runner := &fakeRunner{result: speedtest.Result{DownloadBps: 100e6}}
	e := New(runner)

	if err := e.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}

	runner.err = errors.New("backend down")
	if err := e.RunOnce(context.Background()); err == nil {
		t.Fatal("expected error on second run")
	}

	m := scrape(t, e)
	checkGauge(t, m, "speed_test_success", 0)
	checkGauge(t, m, "speed_download_bits_per_second", 100e6) // preserved
}

func TestSingleFlightSkipsOverlap(t *testing.T) {
	runner := &fakeRunner{delay: 200 * time.Millisecond, started: make(chan struct{}, 1)}
	e := New(runner)

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

func TestMetricsEndpointExposesAllNames(t *testing.T) {
	runner := &fakeRunner{result: speedtest.Result{DownloadBps: 100e6, UploadBps: 50e6}}
	e := New(runner)
	if err := e.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	e.ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain...", ct)
	}
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
		if !strings.Contains(body, "# TYPE "+name+" gauge") {
			t.Errorf("/metrics output missing TYPE line for %q", name)
		}
	}
}

func checkGauge(t *testing.T, m map[string]float64, name string, want float64) {
	t.Helper()
	if got, ok := m[name]; !ok {
		t.Errorf("%s missing from /metrics", name)
	} else if got != want {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}
