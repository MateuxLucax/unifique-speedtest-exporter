# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A Prometheus exporter for the Unifique speed test (https://speed.unifique.com.br), which is a stock **LibreSpeed** deployment. The whole speed test is just concurrent HTTP requests against LibreSpeed's `garbage.php` (download), `empty.php` (upload + ping/jitter), and `getIP.php` endpoints — so this exporter speaks that HTTP protocol directly in Go.

Important deployment constraint: the LibreSpeed backend only answers requests originating **inside Unifique's network**, so the exporter (and any real test) must run on a Unifique connection. From anywhere else the `.php` endpoints return `404`, which the exporter surfaces as `speed_test_success 0`.

## Commands

The Go toolchain is pinned via `mise.toml`. If `go` isn't on PATH, prefix with `mise exec go@1.26.4 --`.

```bash
go test ./...          # run all tests (uses httptest fakes, no real network)
go test ./internal/speedtest -run TestMeasureDownload -v   # a single test
go build -o bin/exporter .   # build the binary
go run .                # run locally on :3000 (real results need a Unifique line)
go vet ./... && gofmt -l .   # vet + format check
```

## Architecture

Two internal packages plus a thin `main.go`:

- **`internal/speedtest`** — the native LibreSpeed client. `Config` (seed it with `DefaultConfig`, which carries LibreSpeed's stock parameters) has a `Run(ctx)` that executes ping → download → upload **sequentially** so phases never contend for the link, returning a `Result` (download/upload in bits/sec, ping/jitter in ms). Each phase lives in its own file: `ping.go` (sequential timed GETs; ping = min RTT, jitter via the asymmetric EWMA in `jitterEWMA`), `download.go` and `upload.go` (N concurrent streams summed into the atomic `byteCounter`, discarding a slow-start grace window before measuring). `throughputBps` applies LibreSpeed's overhead-compensation factor (1.06) so numbers line up with the site's UI. Non-2xx responses fail the test rather than counting error-page bytes.

- **`internal/exporter`** — bridges the test to Prometheus. `Exporter` runs the test in a background goroutine on `RUN_INTERVAL`, caches the latest result, and serves it instantly on scrape. It implements `http.Handler` and emits the Prometheus **text exposition format directly from the standard library** (`writeGauge`) — there is intentionally **no `prometheus/client_golang` dependency**, so the module needs zero third-party packages. A `sync.Mutex.TryLock` (`runMu`) provides single-flight: overlapping triggers are skipped (return `ErrBusy`), never queued, so two link-saturating tests can't overlap; a separate `sync.RWMutex` (`mu`) guards the cached snapshot so scrapes never block on a run. Failed runs set `speed_test_success=0` but **leave the last good values intact**. It depends on a `Runner` interface (which `speedtest.Config` satisfies), so tests drive it with a fake runner.

The four original metric names (`speed_download_bits_per_second`, `speed_upload_bits_per_second`, `speed_ping_ms`, `speed_jitter_ms`) are preserved for drop-in compatibility; health metrics (`speed_test_success`, `speed_test_duration_seconds`, `speed_test_last_run_timestamp_seconds`) were added.

## Measurement accuracy

Speed tests are noisy (results vary ~10–30% run to run). The reported numbers are driven by the LibreSpeed parameters in `DefaultConfig` (stream counts, durations, grace times, `CkSize`, overhead factor); those are the knobs to adjust if the readings look off. Real results require running on a Unifique connection.

## Deployment

Pushes to `main` build and publish a multi-tag image to `ghcr.io/mateuxlucax/unifique-speedtest-exporter` via `.github/workflows/docker-publish.yml`. The `Dockerfile` is multi-stage: `golang:1.26-bookworm` build → `gcr.io/distroless/static` runtime with the static binary.
