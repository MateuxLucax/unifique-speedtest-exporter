package speedtest

import (
	"context"
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
