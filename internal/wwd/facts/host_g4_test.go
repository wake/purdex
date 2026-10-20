package facts

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

const meminfoFixture = `MemTotal:       16384000 kB
MemFree:          100000 kB
MemAvailable:   12800000 kB
Buffers:           50000 kB
Cached:          4000000 kB
SwapCached:            0 kB
Active:          5000000 kB
Inactive:        3000000 kB
Active(anon):    2000000 kB
Inactive(anon):   100000 kB
Active(file):    3000000 kB
Inactive(file):  2900000 kB
SwapTotal:       2097152 kB
SwapFree:        2000000 kB
`

// Active, Inactive and Cached are read by their own keys: Active(anon) and the like are other lines.
func TestParseMeminfoBreakdown(t *testing.T) {
	m, err := ParseMeminfo(meminfoFixture)
	if err != nil {
		t.Fatal(err)
	}
	if m.Active != 5000000<<10 || m.Inactive != 3000000<<10 || m.Cached != 4000000<<10 || !m.HasActive || !m.HasInactive || !m.HasCached {
		t.Errorf("%+v", m)
	}
	m, _ = ParseMeminfo("MemTotal: 1 kB\n")
	if m.HasActive || m.HasInactive || m.HasCached {
		t.Errorf("absent keys must read as absent, not 0: %+v", m)
	}
}

func TestRawMemFromMeminfo(t *testing.T) {
	mi, err := ParseMeminfo(meminfoFixture)
	if err != nil {
		t.Fatal(err)
	}
	raw := rawMemFromMeminfo(mi, 4096)
	if !raw.Linux || raw.TotalBytes != 16384000<<10 || raw.AvailableBytes != 12800000<<10 {
		t.Fatalf("%+v", raw)
	}
	if raw.SwapTotalBytes == nil || *raw.SwapTotalBytes != 2097152<<10 || raw.SwapUsedBytes == nil || *raw.SwapUsedBytes != 97152<<10 {
		t.Errorf("swap total %v used %v", raw.SwapTotalBytes, raw.SwapUsedBytes)
	}
	if raw.ActiveBytes == nil || *raw.ActiveBytes != 5000000<<10 || raw.InactiveBytes == nil || *raw.InactiveBytes != 3000000<<10 ||
		raw.FileCacheBytes == nil || *raw.FileCacheBytes != 4000000<<10 {
		t.Errorf("breakdown %v %v %v", raw.ActiveBytes, raw.InactiveBytes, raw.FileCacheBytes)
	}
	if raw.WiredBytes != nil {
		t.Error("Linux has no wired figure: null")
	}
	// a host with no swap configured has a total of 0, which is a measured zero; a meminfo without the swap keys says nothing
	mi, _ = ParseMeminfo("MemTotal: 1024 kB\nMemAvailable: 512 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n")
	if raw = rawMemFromMeminfo(mi, 4096); raw.SwapTotalBytes == nil || *raw.SwapTotalBytes != 0 {
		t.Errorf("swap total 0 is a reading: %v", raw.SwapTotalBytes)
	}
	if raw.ActiveBytes != nil || raw.InactiveBytes != nil || raw.FileCacheBytes != nil {
		t.Error("keys the kernel did not give are null")
	}
}

func TestParseStatmRSS(t *testing.T) {
	got, err := ParseStatmRSS("1000 250 100 5 0 300 0\n", 4096)
	if err != nil || got != 250*4096 {
		t.Fatalf("%d %v", got, err)
	}
	for _, bad := range []string{"", "1000", "1000 x 3"} {
		if _, err := ParseStatmRSS(bad, 4096); err == nil {
			t.Errorf("%q must fail", bad)
		}
	}
}

// The builder turns the byte figures into whole MB and leaves a figure it was not given null.
func TestMemBuilderBreakdown(t *testing.T) {
	raw := fixtureMem(1000, 500)
	raw.SwapTotalBytes, raw.ActiveBytes, raw.WiredBytes = u64p(4096<<20), u64p(3000<<20+5), u64p(0)
	m := NewMemBuilder().Build(t0, raw)
	if m.SwapTotalMB == nil || *m.SwapTotalMB != 4096 || m.ActiveMB == nil || *m.ActiveMB != 3000 {
		t.Errorf("swap total %v active %v", m.SwapTotalMB, m.ActiveMB)
	}
	if m.WiredMB == nil || *m.WiredMB != 0 {
		t.Errorf("wired 0 is a reading: %v", m.WiredMB)
	}
	if m.InactiveMB != nil || m.FileCacheMB != nil {
		t.Errorf("not given: null, got %v %v", m.InactiveMB, m.FileCacheMB)
	}
}

// The process count is the table's raw size: a directory with no readable stat (a process that exited in between, or one we may not
// read) is counted, a name that is not a pid is not.
func TestProcfsReaderTotalCountsUnreadableProcesses(t *testing.T) {
	f := newFakeProc(t, 1_760_000_000)
	f.proc(10, 1, "node", 0, 500, 10, []string{"node"}, nil)
	f.proc(11, 1, "sh", 0, 600, 10, []string{"sh"}, nil)
	f.write("12/environ", "x") // a pid directory whose stat file is missing
	f.write("self/stat", "x")  // not a pid
	f.write("meminfo", "x")    // a file, not a directory
	snap, _, err := NewProcfsReader(f.root, "").Snapshot(t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Procs) != 2 || snap.Total != 3 {
		t.Fatalf("procs %d total %d want 2 and 3", len(snap.Procs), snap.Total)
	}
}

func TestProcfsReaderBootTime(t *testing.T) {
	f := newFakeProc(t, 1_760_000_000)
	got, ok := NewProcfsReader(f.root, "").BootTime()
	if !ok || !got.Equal(time.Unix(1_760_000_000, 0)) {
		t.Fatalf("%v %v", got, ok)
	}
	if err := os.Remove(filepath.Join(f.root, "stat")); err != nil {
		t.Fatal(err)
	}
	if _, ok := NewProcfsReader(f.root, "").BootTime(); ok {
		t.Error("no btime: unknown, not the epoch")
	}
}

// wwd's own CPU is the time it spent over the wall time between two samples, one core = 100; the first sample has nothing to compare
// with, and a clock that did not move or a counter that went back is null, not 0 or negative.
func TestSelfBuilder(t *testing.T) {
	b := NewSelfBuilder()
	first := b.Build(t0, RawSelf{CPUTime: 2 * time.Second, RSSBytes: 50 << 20})
	if first.CPUPct != nil || first.RSSMB == nil || *first.RSSMB != 50 {
		t.Fatalf("first %+v", first)
	}
	// 0.35 s of CPU in 7 s (not the 5 s of the sampling period)
	got := b.Build(t0.Add(7*time.Second), RawSelf{CPUTime: 2*time.Second + 350*time.Millisecond, RSSBytes: 60 << 20})
	if got.CPUPct == nil || *got.CPUPct < 4.99 || *got.CPUPct > 5.01 {
		t.Errorf("cpu_pct %v want 5", got.CPUPct)
	}
	if got := b.Build(t0.Add(7*time.Second), RawSelf{CPUTime: 3 * time.Second}); got.CPUPct != nil {
		t.Errorf("no time passed: null, got %v", *got.CPUPct)
	}
	if got := b.Build(t0.Add(12*time.Second), RawSelf{CPUTime: time.Second}); got.CPUPct != nil {
		t.Errorf("counter went back: null, got %v", *got.CPUPct)
	}
	// the baseline is renewed after a bad sample: the next good pair gives a figure
	if got := b.Build(t0.Add(17*time.Second), RawSelf{CPUTime: time.Second + 500*time.Millisecond}); got.CPUPct == nil || *got.CPUPct < 9.99 || *got.CPUPct > 10.01 {
		t.Errorf("after a reset %v want 10", got.CPUPct)
	}
	// more than one core is above 100
	if got := b.Build(t0.Add(18*time.Second), RawSelf{CPUTime: 3*time.Second + 500*time.Millisecond}); got.CPUPct == nil || *got.CPUPct < 199 {
		t.Errorf("two cores %v want 200", got.CPUPct)
	}
}

// The collector hands the three figures to the record: procs_total from the snapshot, boot_at from its source, self over two samples.
func TestCollectorHostFields(t *testing.T) {
	boot := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	now := t0
	cpu := 2 * time.Second
	src := Sources{
		Now:   func() time.Time { return now },
		CPU:   func() (RawCPU, error) { return RawCPU{NCPU: 2, Busy: 1, Total: 2}, nil },
		Mem:   func() (RawMem, error) { return fixtureMem(1, 1), nil },
		Disks: func() ([]RawDisk, error) { return nil, nil },
		Procs: func(time.Time) (ProcSnapshot, ProcContext, error) {
			return ProcSnapshot{Procs: []ProcRec{{PID: 1, Name: "a"}}, Total: 431}, ProcContext{}, nil
		},
		Boot: func() (time.Time, bool) { return boot, true },
		Self: func() (RawSelf, error) { return RawSelf{CPUTime: cpu, RSSBytes: 80 << 20}, nil },
	}
	c := NewCollector(src)
	r, err := c.Sample()
	if err != nil {
		t.Fatal(err)
	}
	if r.ProcsTotal == nil || *r.ProcsTotal != 431 {
		t.Errorf("procs_total %v", r.ProcsTotal)
	}
	if r.BootAt == nil || !r.BootAt.Equal(boot) {
		t.Errorf("boot_at %v", r.BootAt)
	}
	if r.Self == nil || r.Self.CPUPct != nil || *r.Self.RSSMB != 80 {
		t.Errorf("first self %+v", r.Self)
	}
	now, cpu = t0.Add(5*time.Second), 3*time.Second // 1 s of CPU in 5 s
	r, _ = c.Sample()
	if r.Self == nil || r.Self.CPUPct == nil || *r.Self.CPUPct < 19.99 || *r.Self.CPUPct > 20.01 {
		t.Errorf("second self %+v", r.Self)
	}
	var f wire.Facts
	r.Apply(&f)
	if f.ProcsTotal == nil || f.BootAt == nil || f.Self == nil {
		t.Errorf("Apply left a field out: %+v", f)
	}
}

// A source that is absent or fails leaves its field null and does not fail the sample.
func TestCollectorHostFieldsNullWhenUnread(t *testing.T) {
	src := Sources{
		Now:   func() time.Time { return t0 },
		CPU:   func() (RawCPU, error) { return RawCPU{NCPU: 2, Busy: 1, Total: 2}, nil },
		Mem:   func() (RawMem, error) { return fixtureMem(1, 1), nil },
		Disks: func() ([]RawDisk, error) { return nil, nil },
		Boot:  func() (time.Time, bool) { return time.Time{}, false },
		Self:  func() (RawSelf, error) { return RawSelf{}, os.ErrPermission },
	}
	r, err := NewCollector(src).Sample()
	if err != nil {
		t.Fatalf("a failed self read is not a failed sample: %v", err)
	}
	if r.ProcsTotal != nil || r.BootAt != nil || r.Self != nil {
		t.Errorf("%v %v %v", r.ProcsTotal, r.BootAt, r.Self)
	}
}
