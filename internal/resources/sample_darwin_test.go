//go:build darwin

package resources

import (
	"context"
	"testing"
	"time"
)

// TestSampler_DarwinLive takes one real sample: it forks vm_stat and ps.
func TestSampler_DarwinLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, procs, err := NewSampler().Sample(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if raw.NCPU <= 0 || raw.MemBytes == 0 || raw.PageSize == 0 || len(procs) == 0 {
		t.Fatalf("implausible sample: %+v, %d procs", raw, len(procs))
	}
	if !raw.Usable() || raw.Load1 < 0 {
		t.Fatalf("unusable sample: %+v", raw)
	}
	h := ComputeHost(raw)
	if h.Measured < 0 || h.Measured > Capacity {
		t.Fatalf("Measured out of range: %+v", h)
	}
}
