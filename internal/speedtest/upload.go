package speedtest

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// measureUpload runs LibreSpeed's upload test: UploadStreams concurrent POSTs
// of random data to the (discarding) upload endpoint, re-POSTing as each
// request completes. Bytes handed to the transport are summed; the first
// UploadGrace is discarded as warmup and throughput is measured over the rest.
//
// Returns upload speed in bits per second.
func (c Config) measureUpload(ctx context.Context) (float64, error) {
	url := c.BaseURL + "/" + c.UploadPath

	runCtx, cancel := context.WithTimeout(ctx, c.UploadDuration)
	defer cancel()

	// One shared block of random data is enough — the backend discards it and
	// only the transferred volume matters.
	block := make([]byte, 64*1024)
	if _, err := rand.Read(block); err != nil {
		return 0, fmt.Errorf("upload: seeding random block: %w", err)
	}

	var counter byteCounter
	var wg sync.WaitGroup
	errCh := make(chan error, c.UploadStreams)

	for i := 0; i < c.UploadStreams; i++ {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			if err := uploadStream(runCtx, c.client(), url, seq, block, c.UploadChunkBytes, &counter); err != nil {
				errCh <- err
			}
		}(i)
	}

	startBytes, startAt := sampleAfter(runCtx, c.UploadGrace, &counter)
	<-runCtx.Done()
	endBytes, endAt := counter.load(), time.Now()

	wg.Wait()
	close(errCh)

	failErr, failed := collectErrs(errCh)
	bps := throughputBps(endBytes-startBytes, endAt.Sub(startAt), c.OverheadCompensationFactor)
	// Bytes are counted as they are sent, before the server's status is known, so
	// a backend that drains the upload and then returns a non-2xx status would
	// otherwise report a positive speed. Fail when every stream errored
	// regardless of throughput, and when no throughput was measured.
	if failErr != nil && (failed == c.UploadStreams || bps == 0) {
		return 0, fmt.Errorf("upload: %w", failErr)
	}
	return bps, nil
}

func uploadStream(ctx context.Context, client *http.Client, url string, seq int, block []byte, chunkBytes int, counter *byteCounter) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		reqURL := fmt.Sprintf("%s?%s", url, cacheBust(seq))
		body := &uploadBody{remaining: chunkBytes, block: block, counter: counter}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, body)
		if err != nil {
			return err
		}
		req.ContentLength = int64(chunkBytes)
		req.Header.Set("Content-Type", "application/octet-stream")

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		status := resp.StatusCode
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if !statusOK(status) {
			return fmt.Errorf("unexpected status %d", status)
		}
	}
}

// uploadBody streams up to `remaining` bytes by repeatedly serving `block`,
// counting every byte read by the transport (i.e. handed to the connection).
type uploadBody struct {
	remaining int
	block     []byte
	counter   *byteCounter
}

func (u *uploadBody) Read(p []byte) (int, error) {
	if u.remaining <= 0 {
		return 0, io.EOF
	}
	n := copy(p, u.block)
	if n > u.remaining {
		n = u.remaining
	}
	u.remaining -= n
	u.counter.add(int64(n))
	return n, nil
}
