package speedtest

import (
	"math/rand/v2"
	"strconv"
	"sync/atomic"
)

// byteCounter is a concurrency-safe total of bytes transferred across all
// streams of a phase.
type byteCounter struct {
	n atomic.Int64
}

func (b *byteCounter) add(delta int64) { b.n.Add(delta) }
func (b *byteCounter) load() int64     { return b.n.Load() }

// countingWriter increments a byteCounter for every byte written and otherwise
// discards the data. Used as the io.Copy sink for download streams.
type countingWriter struct{ c *byteCounter }

func (w countingWriter) Write(p []byte) (int, error) {
	w.c.add(int64(len(p)))
	return len(p), nil
}

// cacheBust returns a query string LibreSpeed appends to defeat caching/proxies.
// Each request gets a fresh pseudo-random value plus the sequence number.
func cacheBust(seq int) string {
	return "r=" + strconv.FormatFloat(rand.Float64(), 'f', -1, 64) +
		"&n=" + strconv.Itoa(seq)
}
