package facts

import (
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 18, 5, 0, 0, 0, time.UTC)

func u64p(v uint64) *uint64 { return &v }

// fixtureMem is a 16 GiB host with 4 KiB... 16 KiB pages (Apple silicon).
func fixtureMem(pageouts, swapouts uint64) RawMem {
	return RawMem{
		TotalBytes: 16 << 30, PageSize: 16384,
		Free: 20000, Inactive: 32768, // (52768 pages * 16 KiB) = 824.5 MiB available
		Level: 2, Pageouts: pageouts, Swapouts: swapouts,
		SwapUsedBytes: u64p(2100 << 20), CompressorBytes: u64p(2300 << 20),
	}
}

func TestBuildCPU(t *testing.T) {
	prev := &RawCPU{Busy: 1000, Total: 4000}
	cur := RawCPU{Load1: 4.7, Load5: 5.1, NCPU: 10, Busy: 1380, Total: 5000}
	got := BuildCPU(prev, cur)
	if got.NCPU != 10 || got.Load1 != 4.7 || got.Load5 != 5.1 {
		t.Fatalf("%+v", got)
	}
	if got.IdlePct != 62 { // 380 busy of 1000 ticks
		t.Fatalf("idle %v want 62", got.IdlePct)
	}
	// first reading: no earlier ticks, falls back to the share since boot
	got = BuildCPU(nil, RawCPU{NCPU: 10, Busy: 250, Total: 1000})
	if got.IdlePct != 75 {
		t.Fatalf("since-boot idle %v want 75", got.IdlePct)
	}
	// counters that did not advance or went back must not divide by zero
	got = BuildCPU(&RawCPU{Busy: 10, Total: 100}, RawCPU{Busy: 10, Total: 100})
	if got.IdlePct < 0 || got.IdlePct > 100 {
		t.Fatalf("idle out of range: %v", got.IdlePct)
	}
}

func TestBuildMemFields(t *testing.T) {
	c := NewMemBuilder()
	m := c.Build(t0, fixtureMem(1000, 500))
	if m.TotalMB != 16384 {
		t.Errorf("total_mb %d", m.TotalMB)
	}
	if m.AvailableMB != 824 { // 52768*16384/1MiB = 824.5 → 824
		t.Errorf("available_mb %d", m.AvailableMB)
	}
	if m.Pressure != 2 {
		t.Errorf("pressure %d", m.Pressure)
	}
	if m.SwapUsedMB == nil || *m.SwapUsedMB != 2100 {
		t.Errorf("swap_used_mb %v", m.SwapUsedMB)
	}
	if m.CompressorMB == nil || *m.CompressorMB != 2300 {
		t.Errorf("compressor_mb %v", m.CompressorMB)
	}
	if m.PSI != nil {
		t.Errorf("psi must be null on macOS")
	}
	// no earlier sample: rates are unknown, taken as 0 (hostmem.RateOver convention)
	if m.PageOutRate == nil || *m.PageOutRate != 0 || m.SwapOutRate == nil || *m.SwapOutRate != 0 {
		t.Errorf("first rates: %v %v", m.PageOutRate, m.SwapOutRate)
	}
}

func TestBuildMemRatesPerCounter(t *testing.T) {
	c := NewMemBuilder()
	c.Build(t0, fixtureMem(1000, 500))
	m := c.Build(t0.Add(5*time.Second), fixtureMem(1100, 600))
	if *m.PageOutRate != 20 { // 100 pages / 5 s
		t.Errorf("page_out_rate %v", *m.PageOutRate)
	}
	if *m.SwapOutRate != 20 {
		t.Errorf("swap_out_rate %v", *m.SwapOutRate)
	}
	// only swapouts move
	m = c.Build(t0.Add(10*time.Second), fixtureMem(1100, 700))
	// the window baseline is the oldest sample at most RateWindow (10 s) old: t0
	if *m.PageOutRate != 10 { // (1100-1000)/10 s
		t.Errorf("page_out_rate %v want 10", *m.PageOutRate)
	}
	if *m.SwapOutRate != 20 { // (700-500)/10 s
		t.Errorf("swap_out_rate %v want 20", *m.SwapOutRate)
	}
}

func TestBuildMemMissingCounters(t *testing.T) {
	c := NewMemBuilder()
	raw := fixtureMem(0, 0)
	raw.SwapUsedBytes, raw.CompressorBytes = nil, nil
	raw.NoPageouts, raw.NoSwapouts = true, true
	m := c.Build(t0, raw)
	if m.SwapUsedMB != nil || m.CompressorMB != nil || m.PageOutRate != nil || m.SwapOutRate != nil {
		t.Fatalf("unreadable counters must be null: %+v", m)
	}
}

// One counter failing must not hide the other (R2: a swapouts sysctl error
// used to null the still-valid pageout rate).
func TestBuildMemCountersAreIndependent(t *testing.T) {
	c := NewMemBuilder()
	raw := fixtureMem(1000, 0)
	raw.NoSwapouts = true
	c.Build(t0, raw)
	raw = fixtureMem(1100, 0)
	raw.NoSwapouts = true
	m := c.Build(t0.Add(5*time.Second), raw)
	if m.SwapOutRate != nil {
		t.Errorf("swap_out_rate must be null: %v", *m.SwapOutRate)
	}
	if m.PageOutRate == nil || *m.PageOutRate != 20 {
		t.Errorf("page_out_rate lost: %v", m.PageOutRate)
	}
	raw = fixtureMem(0, 700)
	raw.NoPageouts = true
	m = c.Build(t0.Add(10*time.Second), raw)
	if m.PageOutRate != nil || m.SwapOutRate == nil {
		t.Errorf("mirror case: %v %v", m.PageOutRate, m.SwapOutRate)
	}
}

func TestBuildDisk(t *testing.T) {
	got := BuildDisk([]RawDisk{{Mount: "/System/Volumes/Data", TotalBytes: 245 << 30, AvailBytes: 17 << 30, FreeBytes: 18 << 30}})
	if len(got) != 1 || got[0].Mount != "/System/Volumes/Data" {
		t.Fatalf("%+v", got)
	}
	// used = total - free; pct of (used+avail), like df
	if got[0].FreeGB != 17 {
		t.Errorf("free_gb %v", got[0].FreeGB)
	}
	want := 100 * float64(245-18) / float64(245-18+17)
	if d := got[0].UsedPct - want; d > 0.05 || d < -0.05 {
		t.Errorf("used_pct %v want %v", got[0].UsedPct, want)
	}
	if got[0].TotalBytes != 245<<30 || got[0].AvailBytes != 17<<30 {
		t.Errorf("statfs sizes: total %d avail %d", got[0].TotalBytes, got[0].AvailBytes)
	}
	if BuildDisk(nil) == nil {
		t.Errorf("empty disk list must be [] not null")
	}
}

func TestCollectorSampleAndPartialFailure(t *testing.T) {
	n := 0
	cpuErr := error(nil)
	src := Sources{
		Now: func() time.Time { n++; return t0.Add(time.Duration(n-1) * 5 * time.Second) },
		CPU: func() (RawCPU, error) {
			if cpuErr != nil {
				return RawCPU{}, cpuErr
			}
			return RawCPU{NCPU: 10, Load1: 1, Busy: uint64(n) * 100, Total: uint64(n) * 1000}, nil
		},
		Mem: func() (RawMem, error) { return fixtureMem(uint64(n)*100, 0), nil },
		Disks: func() ([]RawDisk, error) {
			return []RawDisk{{Mount: "/", TotalBytes: 100 << 30, AvailBytes: 50 << 30, FreeBytes: 50 << 30}}, nil
		},
	}
	c := NewCollector(src)
	r, err := c.Sample()
	if err != nil || r.At != t0 || r.CPU.NCPU != 10 || r.Mem.TotalMB != 16384 || len(r.Disk) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = c.Sample()
	if err != nil || r.CPU.IdlePct != 90 {
		t.Fatalf("second: %+v %v", r, err)
	}
	// a failing source is reported, the rest of the record still comes back
	cpuErr = errors.New("boom")
	r, err = c.Sample()
	if err == nil || r.Mem.TotalMB != 16384 {
		t.Fatalf("partial: %+v %v", r, err)
	}
}
