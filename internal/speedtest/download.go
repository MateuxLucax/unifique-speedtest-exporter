package speedtest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// measureDownload runs LibreSpeed's download test: DownloadStreams concurrent
// GETs to garbage.php, each re-requesting as soon as it finishes, with all
// bytes summed into a shared counter. The first DownloadGrace is discarded as
// slow-start warmup; throughput is measured over the remaining window and
// scaled by OverheadCompensationFactor.
//
// Returns download speed in bits per second.
func (c Config) measureDownload(ctx context.Context) (float64, error) {
	url := fmt.Sprintf("%s/%s?ckSize=%d", c.BaseURL, c.DownloadPath, c.CkSize)

	runCtx, cancel := context.WithTimeout(ctx, c.DownloadDuration)
	defer cancel()

	var counter byteCounter
	var wg sync.WaitGroup
	errCh := make(chan error, c.DownloadStreams)

	for i := 0; i < c.DownloadStreams; i++ {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			if err := downloadStream(runCtx, c.client(), url, seq, &counter); err != nil {
				errCh <- err
			}
		}(i)
	}

	startBytes, startAt := sampleAfter(runCtx, c.DownloadGrace, &counter)
	<-runCtx.Done()
	endBytes, endAt := counter.load(), time.Now()

	wg.Wait()
	close(errCh)

	bps := throughputBps(endBytes-startBytes, endAt.Sub(startAt), c.OverheadCompensationFactor)
	if bps == 0 {
		// No measured throughput almost always means every stream errored
		// (e.g. backend unreachable); surface that rather than reporting 0.
		if err := firstErr(errCh); err != nil {
			return 0, fmt.Errorf("download: %w", err)
		}
	}
	return bps, nil
}

// downloadStream repeatedly downloads garbage, counting every byte, until ctx
// is cancelled (the measurement window closing). A cancelled context is the
// normal stop signal, not an error.
func downloadStream(ctx context.Context, client *http.Client, baseURL string, seq int, counter *byteCounter) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		reqURL := fmt.Sprintf("%s&%s", baseURL, cacheBust(seq))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if !statusOK(resp.StatusCode) {
			resp.Body.Close()
			return fmt.Errorf("download: unexpected status %d", resp.StatusCode)
		}
		_, err = io.Copy(countingWriter{counter}, resp.Body)
		resp.Body.Close()
		if err != nil && ctx.Err() == nil {
			return err
		}
	}
}

// sampleAfter waits for the grace period (or ctx end), then snapshots the
// counter and timestamp marking the start of the measurement window.
func sampleAfter(ctx context.Context, grace time.Duration, counter *byteCounter) (int64, time.Time) {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
	return counter.load(), time.Now()
}

func firstErr(ch <-chan error) error {
	for err := range ch {
		if err != nil {
			return err
		}
	}
	return nil
}
