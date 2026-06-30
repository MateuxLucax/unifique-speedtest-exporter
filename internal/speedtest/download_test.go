package speedtest

import (
	"context"
	"testing"
)

func TestMeasureDownload(t *testing.T) {
	fb := newFakeBackend()
	defer fb.close()

	bps, err := fb.fastConfig().measureDownload(context.Background())
	if err != nil {
		t.Fatalf("measureDownload: %v", err)
	}
	if bps <= 0 {
		t.Fatalf("download bps = %v, want > 0", bps)
	}
}

func TestMeasureDownloadBackendDown(t *testing.T) {
	fb := newFakeBackend()
	defer fb.close()
	fb.failAll = true

	_, err := fb.fastConfig().measureDownload(context.Background())
	if err == nil {
		t.Fatal("expected error when garbage.php returns 404, got nil")
	}
}
