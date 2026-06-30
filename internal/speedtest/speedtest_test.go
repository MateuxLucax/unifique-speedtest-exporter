package speedtest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fakeBackend is an httptest stand-in for a LibreSpeed install. It serves
// garbage.php (streams data), empty.php (discards POSTs / replies fast to GETs),
// and records how many bytes it received on upload.
type fakeBackend struct {
	server    *httptest.Server
	uploaded  atomic.Int64
	pingDelay time.Duration
	chunk     []byte
	failAll   bool
}

func newFakeBackend() *fakeBackend {
	fb := &fakeBackend{chunk: make([]byte, 64*1024)}
	mux := http.NewServeMux()
	mux.HandleFunc("/garbage.php", func(w http.ResponseWriter, r *http.Request) {
		if fb.failAll {
			http.Error(w, "nope", http.StatusNotFound)
			return
		}
		// Stream ~1 MiB then return; the client re-requests as needed.
		for i := 0; i < 16; i++ {
			if _, err := w.Write(fb.chunk); err != nil {
				return
			}
		}
	})
	mux.HandleFunc("/empty.php", func(w http.ResponseWriter, r *http.Request) {
		if fb.failAll {
			http.Error(w, "nope", http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPost {
			n, _ := io.Copy(io.Discard, r.Body)
			fb.uploaded.Add(n)
		} else if fb.pingDelay > 0 {
			time.Sleep(fb.pingDelay)
		}
		w.WriteHeader(http.StatusOK)
	})
	fb.server = httptest.NewServer(mux)
	return fb
}

func (fb *fakeBackend) close() { fb.server.Close() }

// fastConfig returns a DefaultConfig pointed at the fake backend with short
// durations so tests finish quickly.
func (fb *fakeBackend) fastConfig() Config {
	c := DefaultConfig(fb.server.URL)
	c.HTTPClient = fb.server.Client()
	c.DownloadStreams = 4
	c.UploadStreams = 2
	c.DownloadDuration = 600 * time.Millisecond
	c.UploadDuration = 600 * time.Millisecond
	c.DownloadGrace = 150 * time.Millisecond
	c.UploadGrace = 150 * time.Millisecond
	c.PingCount = 5
	c.UploadChunkBytes = 256 * 1024
	return c
}

func TestThroughputBps(t *testing.T) {
	tests := []struct {
		name     string
		bytes    int64
		elapsed  time.Duration
		overhead float64
		want     float64
	}{
		{"one megabyte per second no overhead", 1_000_000, time.Second, 1.0, 8_000_000},
		{"with 1.06 overhead", 1_000_000, time.Second, 1.06, 8_480_000},
		{"half second window", 500_000, 500 * time.Millisecond, 1.0, 8_000_000},
		{"zero elapsed guards divide by zero", 1_000_000, 0, 1.06, 0},
		{"zero bytes", 0, time.Second, 1.06, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := throughputBps(tt.bytes, tt.elapsed, tt.overhead)
			if got != tt.want {
				t.Fatalf("throughputBps(%d, %v, %v) = %v, want %v", tt.bytes, tt.elapsed, tt.overhead, got, tt.want)
			}
		})
	}
}

func TestRunEndToEnd(t *testing.T) {
	fb := newFakeBackend()
	defer fb.close()
	fb.pingDelay = 2 * time.Millisecond

	res, err := fb.fastConfig().Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.DownloadBps <= 0 {
		t.Errorf("DownloadBps = %v, want > 0", res.DownloadBps)
	}
	if res.UploadBps <= 0 {
		t.Errorf("UploadBps = %v, want > 0", res.UploadBps)
	}
	if res.PingMs < 2 {
		t.Errorf("PingMs = %v, want >= 2 (ping delay was 2ms)", res.PingMs)
	}
	if res.JitterMs < 0 {
		t.Errorf("JitterMs = %v, want >= 0", res.JitterMs)
	}
	if fb.uploaded.Load() <= 0 {
		t.Errorf("backend received %d upload bytes, want > 0", fb.uploaded.Load())
	}
}

func TestRunFailsWhenBackendDown(t *testing.T) {
	fb := newFakeBackend()
	defer fb.close()
	fb.failAll = true

	_, err := fb.fastConfig().Run(context.Background())
	if err == nil {
		t.Fatal("expected error when backend returns errors, got nil")
	}
}
