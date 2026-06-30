package speedtest

import (
	"context"
	"net/http"
	"testing"
)

func TestMeasureUpload(t *testing.T) {
	fb := newFakeBackend()
	defer fb.close()

	bps, err := fb.fastConfig().measureUpload(context.Background())
	if err != nil {
		t.Fatalf("measureUpload: %v", err)
	}
	if bps <= 0 {
		t.Fatalf("upload bps = %v, want > 0", bps)
	}
	if fb.uploaded.Load() <= 0 {
		t.Fatalf("backend received %d bytes, want > 0", fb.uploaded.Load())
	}
}

func TestMeasureUploadBackendDown(t *testing.T) {
	fb := newFakeBackend()
	defer fb.close()
	fb.failAll = true

	_, err := fb.fastConfig().measureUpload(context.Background())
	if err == nil {
		t.Fatal("expected error when empty.php returns 404, got nil")
	}
}

// TestMeasureUploadDrainsBodyThenFails covers the case where the backend reads
// the whole upload body (so bytes are counted) but then returns a non-2xx
// status. Throughput is positive, so the failure must be surfaced on the error
// signal, not gated on zero bytes.
func TestMeasureUploadDrainsBodyThenFails(t *testing.T) {
	fb := newFakeBackend()
	defer fb.close()
	fb.uploadStatus = http.StatusInternalServerError

	_, err := fb.fastConfig().measureUpload(context.Background())
	if err == nil {
		t.Fatal("expected error when upload backend drains the body then returns 500")
	}
	if fb.uploaded.Load() == 0 {
		t.Fatal("precondition: backend should have received upload bytes (the bug scenario)")
	}
}
