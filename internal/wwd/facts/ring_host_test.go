package facts

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func ip(v int) *int { return &v }

// hostRec is a record of the minute with the G-4 fields set; v is the record's proto version.
func hostRec(i, v int, procs int, selfCPU *float64, rss int, swapTotal int, wired, active *int) wire.Facts {
	r := rec(t0.Add(time.Duration(i)*5*time.Second), 1, 1000, 1, wire.ZoneNormal)
	r.V = v
	boot := t0.Add(-time.Duration(i) * time.Hour) // differs per record: the last one is the one that counts
	r.ProcsTotal, r.BootAt = ip(procs), &boot
	r.Self = &wire.Self{CPUPct: selfCPU, RSSMB: ip(rss)}
	r.Mem.SwapTotalMB, r.Mem.WiredMB, r.Mem.ActiveMB = ip(swapTotal), wired, active
	return r
}

// The minute's rules (host-metrics spec §2.4): procs_total, self.rss_mb and swap_total_mb are the peak, self.cpu_pct and the memory
// breakdown the average of the samples that have a value, boot_at the last record's. The last record is v1 here, and the new
// fields are not in the proto 2 set the rollup clears.
func TestRollupHostFieldsMixedProto(t *testing.T) {
	recs := []wire.Facts{
		hostRec(0, wire.FactsProto, 400, fp(10), 50, 1024, ip(1000), ip(2000)),
		hostRec(1, wire.FactsProto, 450, nil, 70, 2048, ip(2000), nil),
		hostRec(2, 1, 420, fp(30), 60, 1024, ip(3000), ip(4000)),
	}
	got := Rollup(recs)
	if got.V != 1 {
		t.Fatalf("test setup: last record must be v1, got %d", got.V)
	}
	if got.ProcsTotal == nil || *got.ProcsTotal != 450 {
		t.Errorf("procs_total %v want the peak 450", got.ProcsTotal)
	}
	if got.Self == nil || got.Self.RSSMB == nil || *got.Self.RSSMB != 70 {
		t.Errorf("self.rss_mb %+v want the peak 70", got.Self)
	}
	if got.Self.CPUPct == nil || *got.Self.CPUPct != 20 { // (10+30)/2: the sample with no value does not count as 0
		t.Errorf("self.cpu_pct %v want 20", got.Self.CPUPct)
	}
	if got.Mem.SwapTotalMB == nil || *got.Mem.SwapTotalMB != 2048 {
		t.Errorf("swap_total_mb %v want the peak 2048", got.Mem.SwapTotalMB)
	}
	if got.Mem.WiredMB == nil || *got.Mem.WiredMB != 2000 {
		t.Errorf("wired_mb %v want the mean 2000", got.Mem.WiredMB)
	}
	if got.Mem.ActiveMB == nil || *got.Mem.ActiveMB != 3000 { // (2000+4000)/2
		t.Errorf("active_mb %v want 3000", got.Mem.ActiveMB)
	}
	if got.Mem.InactiveMB != nil || got.Mem.FileCacheMB != nil {
		t.Errorf("no sample had inactive/file cache: null, got %v %v", got.Mem.InactiveMB, got.Mem.FileCacheMB)
	}
	if got.BootAt == nil || !got.BootAt.Equal(*recs[2].BootAt) {
		t.Errorf("boot_at %v want the last record's %v", got.BootAt, recs[2].BootAt)
	}
}

// A minute with no value for a field says null, not 0; and a minute of records from before the fields existed rolls up to nil.
func TestRollupHostFieldsAbsent(t *testing.T) {
	recs := []wire.Facts{rec(t0, 1, 1000, 1, wire.ZoneNormal), rec(t0.Add(5*time.Second), 1, 1000, 1, wire.ZoneNormal)}
	got := Rollup(recs)
	if got.ProcsTotal != nil || got.BootAt != nil || got.Self != nil || got.Mem.SwapTotalMB != nil || got.Mem.WiredMB != nil {
		t.Fatalf("an old minute must stay null: %+v %+v", got.Self, got.Mem)
	}
	// samples with a self record but no cpu in any of them (the first sample after a start): cpu_pct null, rss still the peak
	r1 := hostRec(0, wire.FactsProto, 1, nil, 40, 0, nil, nil)
	r2 := hostRec(1, wire.FactsProto, 1, nil, 45, 0, nil, nil)
	got = Rollup([]wire.Facts{r1, r2})
	if got.Self == nil || got.Self.CPUPct != nil || *got.Self.RSSMB != 45 {
		t.Fatalf("self %+v", got.Self)
	}
	// the rollup shares nothing with its inputs
	if aliased(got, r1) || aliased(got, r2) {
		t.Fatal("rollup aliases an input record")
	}
}

// A zero is a measured zero: swap_total 0 (no swap configured) stays 0, it is not turned into null.
func TestRollupKeepsMeasuredZero(t *testing.T) {
	got := Rollup([]wire.Facts{hostRec(0, wire.FactsProto, 0, fp(0), 0, 0, ip(0), ip(0))})
	if got.Mem.SwapTotalMB == nil || *got.Mem.SwapTotalMB != 0 || got.Self.CPUPct == nil || *got.Self.CPUPct != 0 || got.ProcsTotal == nil {
		t.Fatalf("zero lost: %+v %+v", got.Mem, got.Self)
	}
}
