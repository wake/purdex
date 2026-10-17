//go:build darwin

package facts

import (
	"testing"
	"time"
)

// A smoke test against the real host: the sources must produce a plausible
// record (this is what the fixtures stand in for everywhere else).
func TestSystemSourcesSmoke(t *testing.T) {
	c := NewCollector(SystemSources())
	r, err := c.Sample()
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	time.Sleep(1200 * time.Millisecond)
	r, err = c.Sample()
	if err != nil {
		t.Fatalf("second sample: %v", err)
	}
	if r.CPU.NCPU <= 0 || r.CPU.Load1 < 0 || r.CPU.IdlePct < 0 || r.CPU.IdlePct > 100 {
		t.Errorf("cpu: %+v", r.CPU)
	}
	if r.Mem.TotalMB < 1024 || r.Mem.AvailableMB <= 0 || r.Mem.AvailableMB > r.Mem.TotalMB {
		t.Errorf("mem: %+v", r.Mem)
	}
	if r.Mem.Pressure != 1 && r.Mem.Pressure != 2 && r.Mem.Pressure != 4 {
		t.Errorf("pressure %d", r.Mem.Pressure)
	}
	if r.Mem.SwapUsedMB == nil || r.Mem.CompressorMB == nil || r.Mem.PageOutRate == nil || r.Mem.SwapOutRate == nil {
		t.Errorf("counters missing on macOS: %+v", r.Mem)
	}
	if len(r.Disk) == 0 || r.Disk[0].FreeGB <= 0 || r.Disk[0].UsedPct <= 0 || r.Disk[0].UsedPct > 100 {
		t.Errorf("disk: %+v", r.Disk)
	}
}
