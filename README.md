# Unifique Speedtest Exporter

![TypeScript](https://img.shields.io/badge/typescript-%23007ACC.svg?style=for-the-badge&logo=typescript&logoColor=white)
![Playwright](https://img.shields.io/badge/-playwright-%232EAD33?style=for-the-badge&logo=playwright&logoColor=white)
![NodeJS](https://img.shields.io/badge/node.js-6DA55F?style=for-the-badge&logo=node.js&logoColor=white)
![Docker](https://img.shields.io/badge/docker-%230db7ed.svg?style=for-the-badge&logo=docker&logoColor=white)
![Grafana](https://img.shields.io/badge/grafana-%23F46800.svg?style=for-the-badge&logo=grafana&logoColor=white)
![Prometheus](https://img.shields.io/badge/Prometheus-E6522C?style=for-the-badge&logo=Prometheus&logoColor=white)

This project turns the [Unifique Speed Test](https://speed.unifique.com.br/) into a Prometheus metrics endpoint.

The Unifique speed test is a stock [LibreSpeed](https://github.com/librespeed/speedtest) deployment, so the whole test is just concurrent HTTP requests against its `garbage.php` / `empty.php` backend. This exporter is a small Go program that reproduces that measurement natively — no headless browser. The test runs in the background on an interval and the results are cached, so every Prometheus scrape returns instantly instead of triggering a fresh, link-saturating test.

> **Note:** the LibreSpeed backend only responds to requests originating from inside Unifique's own network, so this exporter must run on a Unifique connection.

## Why a native Go exporter?

Speaking the LibreSpeed HTTP protocol directly — instead of driving a real browser to scrape the rendered UI — collapses the runtime to a single dependency-free static binary:

| | Playwright + Node | Native Go |
| --- | --- | --- |
| Container image | **2.13 GB** | **14.5 MB** (~150× smaller) |
| Third-party dependencies | Playwright + bundled Chromium + transitive npm tree | **0** — standard library only |
| Runtime | Node.js + a headless Chromium process | one static binary (distroless) |
| Memory | launches a Chromium browser per scrape (hundreds of MB) | ~10 MiB steady |
| Work per scrape | launches a browser and runs a full speed test on **every** scrape | serves a cached result instantly; the test runs on an interval |
| Measurement | screen-scrapes the LibreSpeed UI via DOM selectors | speaks the LibreSpeed HTTP protocol directly |

Zero third-party packages also means a minimal supply-chain attack surface — there is no `go.sum` because there is nothing to verify.

<sub>Image sizes from `docker images`; memory from `docker stats`; measured locally on arm64.</sub>

## How to use

The project can be run locally using Docker or Docker Compose. Follow the instructions below for your preferred method.

### Using Docker

1. Pull the latest image from the GitHub Container Registry:

   ```bash
   docker pull ghcr.io/mateuxlucax/unifique-speedtest-exporter:latest
   ```

2. Run the container:

   ```bash
   docker run -p 3000:3000 ghcr.io/mateuxlucax/unifique-speedtest-exporter:latest
   ```

The exporter will be available at http://localhost:3000/metrics

### Docker Compose

If you wish to setup this in a Docker Compose environment you can check the [docker-compose.yml](docker-compose.yml) file for an example configuration.

## Metrics

The following metrics are exposed:

- `speed_download_bits_per_second`: Download speed in bits per second
- `speed_upload_bits_per_second`: Upload speed in bits per second
- `speed_ping_ms`: Ping time in milliseconds
- `speed_jitter_ms`: Jitter time in milliseconds

Plus exporter health metrics:

- `speed_test_success`: `1` if the last test succeeded, `0` otherwise
- `speed_test_duration_seconds`: how long the last test run took
- `speed_test_last_run_timestamp_seconds`: Unix timestamp of the last run

### Configuration

The exporter is configured via environment variables:

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `3000` | Port the `/metrics` endpoint listens on |
| `SPEEDTEST_URL` | `https://speed.unifique.com.br` | LibreSpeed origin to test against |
| `RUN_INTERVAL` | `10m` | How often to run the test in the background (Go duration, e.g. `5m`, `1h`) |

### Prometheus configuration

```yaml
scrape_configs:
  - job_name: 'unifique_speedtest_exporter'
    scrape_interval: 10m
    scrape_timeout: 2m
    static_configs:
      - targets: ['localhost:3000']
```

### Grafana panel

You can also visualize the metrics in Grafana by importing the [Grafana dashboard JSON file](assets/grafana/unifique-speedtest-grafana-dashboard.json). Here is a preview:

![Grafana dashboard](assets/grafana/grafana-dashboard.png)


## Development & Contribution

The exporter is a standard Go module ([mise](https://mise.jdx.dev/) pins the toolchain via `mise.toml`).

1. Run the tests:
```bash
go test ./...
```

2. Run it locally (must be on a Unifique connection to get real results):
```bash
go run .
# then: curl http://localhost:3000/metrics
```

3. Make your changes, then submit a pull request with a description of them.

## License

This project is licensed under the GPL-3.0 License - see the [LICENSE](LICENSE) file for details.
