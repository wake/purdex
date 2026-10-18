//go:build linux

package facts

import "testing"

// The real reads of the host the test runs on: every number must be sane, and
// two samples must give a whole record.
func TestSystemSourcesReadThisLinuxHost(t *testing.T) {
	src := SystemSources()
	c := NewCollector(src)
	if _, err := c.Sample(); err != nil {
		t.Fatalf("first sample: %v", err)
	}
	r, err := c.Sample()
	if err != nil {
		t.Fatalf("second sample: %v", err)
	}
	if r.CPU.NCPU < 1 || r.CPU.IdlePct < 0 || r.CPU.IdlePct > 100 {
		t.Errorf("cpu: %+v", r.CPU)
	}
	m := r.Mem
	if m.TotalMB <= 0 || m.AvailableMB <= 0 || m.AvailableMB > m.TotalMB {
		t.Errorf("mem: %+v", m)
	}
	if m.Pressure != 1 && m.Pressure != 2 && m.Pressure != 4 {
		t.Errorf("pressure %d is not a level", m.Pressure)
	}
	if m.SwapUsedMB == nil || m.PageOutRate == nil || m.SwapOutRate == nil {
		t.Errorf("swap and paging are read on Linux: %+v", m)
	}
	if m.CompressorMB != nil {
		t.Error("Linux has no compressor")
	}
	if len(r.Disk) == 0 || r.Disk[0].FreeGB < 0 {
		t.Errorf("disk: %+v", r.Disk)
	}
	if r.Owner != nil {
		t.Error("owner is null on Linux")
	}
}
