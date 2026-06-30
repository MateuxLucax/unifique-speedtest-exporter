package speedtest

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

// measurePing performs LibreSpeed's ping/jitter test: it issues PingCount
// sequential GETs to the (empty) ping endpoint over a reused connection and
// times each round trip. Ping is the minimum observed latency; jitter is
// LibreSpeed's asymmetric exponential moving average of the inter-sample delta.
//
// Returns ping and jitter in milliseconds.
func (c Config) measurePing(ctx context.Context) (pingMs, jitterMs float64, err error) {
	client := c.client()
	url := c.BaseURL + "/" + c.PingPath

	var (
		ping     = math.Inf(1)
		jitter   jitterEWMA
		prev     float64
		measured int
	)

	for i := 0; i < c.PingCount; i++ {
		if err = ctx.Err(); err != nil {
			return 0, 0, err
		}

		rtt, perr := c.singlePing(ctx, client, url, i)
		if perr != nil {
			// Ignore transient per-sample errors as LibreSpeed does, but fail
			// if every sample errors out.
			continue
		}
		ms := float64(rtt) / float64(time.Millisecond)
		measured++

		if ms < ping {
			ping = ms
		}
		if measured > 1 {
			jitter.update(math.Abs(ms - prev))
		}
		prev = ms
	}

	if measured == 0 {
		return 0, 0, fmt.Errorf("ping: all %d samples failed", c.PingCount)
	}
	if math.IsInf(ping, 1) {
		ping = 0
	}
	return ping, jitter.val, nil
}

// jitterEWMA reproduces LibreSpeed's asymmetric exponential moving average for
// jitter: the first sample seeds it, then rising jitter is weighted heavily
// (0.7) and falling jitter lightly (0.2), so spikes show but decay slowly.
type jitterEWMA struct {
	val  float64
	init bool
}

func (j *jitterEWMA) update(instJitter float64) {
	if !j.init {
		j.val = instJitter
		j.init = true
		return
	}
	if instJitter > j.val {
		j.val = j.val*0.3 + instJitter*0.7
	} else {
		j.val = j.val*0.8 + instJitter*0.2
	}
}

func (c Config) singlePing(ctx context.Context, client *http.Client, url string, seq int) (time.Duration, error) {
	reqURL := fmt.Sprintf("%s?%s", url, cacheBust(seq))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Cache-Control", "no-cache")

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	// Drain and close so the connection is reused for the next sample.
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if !statusOK(resp.StatusCode) {
		return 0, fmt.Errorf("ping: unexpected status %d", resp.StatusCode)
	}
	return time.Since(start), nil
}
