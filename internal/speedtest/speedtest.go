// Package speedtest implements a native client for a LibreSpeed backend
// (https://github.com/librespeed/speedtest). It reproduces the measurement the
// LibreSpeed browser client performs — concurrent HTTP download/upload streams
// plus a timed-request ping/jitter test — without driving a real browser.
//
// The target deployment is https://speed.unifique.com.br, a stock LibreSpeed
// install whose default, root-relative endpoints are:
//
//	garbage.php?ckSize=N  download: server streams N MiB of incompressible data
//	empty.php             upload (POST body discarded) and ping/jitter (timed GET)
//	getIP.php             client IP / ISP info (not needed for the core metrics)
package speedtest

import (
	"context"
	"net"
	"net/http"
	"time"
)

// Result holds one speed test outcome. Download and Upload are in bits per
// second; Ping and Jitter are in milliseconds — matching the Prometheus metrics
// the exporter exposes.
type Result struct {
	DownloadBps float64
	UploadBps   float64
	PingMs      float64
	JitterMs    float64
}

// Config describes the LibreSpeed backend and how to run the test. The zero
// value is not usable; start from DefaultConfig and override as needed.
type Config struct {
	// BaseURL is the LibreSpeed origin, without a trailing slash, e.g.
	// "https://speed.unifique.com.br".
	BaseURL string

	// Endpoint paths, relative to BaseURL. These are LibreSpeed defaults.
	DownloadPath string // garbage.php
	UploadPath   string // empty.php
	PingPath     string // empty.php

	// DownloadStreams / UploadStreams are the number of concurrent HTTP
	// streams, mirroring LibreSpeed's xhr_dlMultistream / xhr_ulMultistream.
	DownloadStreams int
	UploadStreams   int

	// DownloadDuration / UploadDuration are the total measurement windows
	// (LibreSpeed time_dl_max / time_ul_max).
	DownloadDuration time.Duration
	UploadDuration   time.Duration

	// DownloadGrace / UploadGrace are the slow-start warmup periods discarded
	// before measurement begins (LibreSpeed time_dlGraceTime / time_ulGraceTime).
	DownloadGrace time.Duration
	UploadGrace   time.Duration

	// CkSize is the garbage.php ckSize parameter in MiB (LibreSpeed
	// garbagePhp_chunkSize).
	CkSize int

	// UploadChunkBytes is the size of each random buffer streamed per upload
	// request (LibreSpeed xhr_ul_blob_megabytes, in bytes here).
	UploadChunkBytes int

	// PingCount is the number of ping samples (LibreSpeed count_ping).
	PingCount int

	// OverheadCompensationFactor scales measured throughput to estimate
	// line-rate including protocol overhead (LibreSpeed overheadCompensationFactor).
	OverheadCompensationFactor float64

	// HTTPClient is used for all requests. If nil, NewClient() is used.
	HTTPClient *http.Client
}

// DefaultConfig returns a Config populated with LibreSpeed's stock defaults,
// which is what speed.unifique.com.br uses (its page sets only telemetry_level).
func DefaultConfig(baseURL string) Config {
	return Config{
		BaseURL:                    baseURL,
		DownloadPath:               "garbage.php",
		UploadPath:                 "empty.php",
		PingPath:                   "empty.php",
		DownloadStreams:            6,
		UploadStreams:              3,
		DownloadDuration:           15 * time.Second,
		UploadDuration:             15 * time.Second,
		DownloadGrace:              1500 * time.Millisecond,
		UploadGrace:                3 * time.Second,
		CkSize:                     100,
		UploadChunkBytes:           20 * 1024 * 1024,
		PingCount:                  10,
		OverheadCompensationFactor: 1.06,
	}
}

// NewClient builds an http.Client tuned for many concurrent, long-lived
// streams to a single host with connection reuse.
func NewClient() *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          64,
		MaxConnsPerHost:       64,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		// Disable compression so garbage data is measured at wire size.
		DisableCompression: true,
		// HTTP/2 is disabled deliberately: LibreSpeed saturates the link with
		// multiple parallel TCP connections. Over HTTP/2 the streams would
		// multiplex onto a single connection and under-measure throughput on
		// HTTP/2-capable servers. Do not "simplify" this away.
		ForceAttemptHTTP2: false,
	}
	return &http.Client{Transport: transport}
}

func (c Config) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return NewClient()
}

// Run executes the full test sequence — ping/jitter, then download, then
// upload — and returns the combined Result. The phases run sequentially so they
// never contend for the link. ctx bounds the whole run.
func (c Config) Run(ctx context.Context) (Result, error) {
	var res Result

	ping, jitter, err := c.measurePing(ctx)
	if err != nil {
		return res, err
	}
	res.PingMs = ping
	res.JitterMs = jitter

	dl, err := c.measureDownload(ctx)
	if err != nil {
		return res, err
	}
	res.DownloadBps = dl

	ul, err := c.measureUpload(ctx)
	if err != nil {
		return res, err
	}
	res.UploadBps = ul

	return res, nil
}

// statusOK reports whether an HTTP response is a 2xx success. Non-2xx
// responses (e.g. a 404 from a misconfigured or unreachable backend) must fail
// the test rather than have their error-page bodies counted as throughput.
func statusOK(code int) bool { return code >= 200 && code < 300 }

// throughputBps converts a transferred byte count over an elapsed window into
// bits per second, applying the overhead compensation factor — the same
// computation LibreSpeed reports to its UI.
func throughputBps(bytes int64, elapsed time.Duration, overhead float64) float64 {
	if elapsed <= 0 {
		return 0
	}
	return float64(bytes) * 8 * overhead / elapsed.Seconds()
}
