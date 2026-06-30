package speedtest

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestJitterEWMA(t *testing.T) {
	t.Run("first sample seeds the value", func(t *testing.T) {
		var j jitterEWMA
		j.update(5)
		if j.val != 5 {
			t.Fatalf("seed: got %v, want 5", j.val)
		}
	})

	t.Run("rising jitter weighted 0.7", func(t *testing.T) {
		var j jitterEWMA
		j.update(2)  // seed -> 2
		j.update(12) // rising: 2*0.3 + 12*0.7 = 9
		if math.Abs(j.val-9) > 1e-9 {
			t.Fatalf("rising: got %v, want 9", j.val)
		}
	})

	t.Run("falling jitter weighted 0.2", func(t *testing.T) {
		var j jitterEWMA
		j.update(10) // seed -> 10
		j.update(0)  // falling: 10*0.8 + 0*0.2 = 8
		if math.Abs(j.val-8) > 1e-9 {
			t.Fatalf("falling: got %v, want 8", j.val)
		}
	})

	t.Run("constant samples keep jitter constant", func(t *testing.T) {
		var j jitterEWMA
		j.update(3)
		j.update(3) // 3 is not > 3, falling branch: 3*0.8 + 3*0.2 = 3
		if math.Abs(j.val-3) > 1e-9 {
			t.Fatalf("constant: got %v, want 3", j.val)
		}
	})
}

func TestMeasurePing(t *testing.T) {
	fb := newFakeBackend()
	defer fb.close()
	fb.pingDelay = 5 * time.Millisecond

	c := fb.fastConfig()
	ping, jitter, err := c.measurePing(context.Background())
	if err != nil {
		t.Fatalf("measurePing: %v", err)
	}
	if ping < 5 {
		t.Errorf("ping = %vms, want >= 5 (server delay)", ping)
	}
	if jitter < 0 {
		t.Errorf("jitter = %v, want >= 0", jitter)
	}
}

func TestMeasurePingAllFail(t *testing.T) {
	fb := newFakeBackend()
	defer fb.close()
	fb.failAll = true

	_, _, err := fb.fastConfig().measurePing(context.Background())
	if err == nil {
		t.Fatal("expected error when all ping samples fail, got nil")
	}
}
