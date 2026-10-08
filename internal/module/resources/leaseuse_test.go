package resourcesmod

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/wake/purdex/internal/resources"
)

const gib = uint64(1) << 30

// useFix is a sweeper fixture whose module also measures leases: the host is
// the 10-core, 16 GiB one of goodRaw.
type useFix struct {
	*sweepFix
}

func newUseFix(t *testing.T) *useFix { return &useFix{newSweepFix(t, resources.ModeLease)} }

// heldLease creates a held row of the scope for the pid, with the baseline
// entries as the row stores them.
func (f *useFix) heldLease(id, scope string, pid int, baseline ...resources.BaselineEntry) {
	f.t.Helper()
	r := baseRow(id, "c-"+id)
	r.Scope, r.HolderPID = scope, pid
	if len(baseline) > 0 {
		b, err := json.Marshal(baseline)
		if err != nil {
			f.t.Fatal(err)
		}
		r.Baseline = string(b)
	}
	at := f.nowMS()
	r.CreatedAt, r.DeadlineAt, r.LeaseUntil = at-1000, at+300000, at+30000
	mustCreate(f.t, f.m.store, r)
	if ok, err := f.m.store.Grant(id, at, false, false); !ok || err != nil {
		f.t.Fatalf("grant %s: %v %v", id, ok, err)
	}
}

// measure runs one measuring step on the process list.
func (f *useFix) measure(procs []resources.Proc) {
	f.m.measureLeases(context.Background(), procs, goodRaw())
}

// advance moves the clock.
func (f *useFix) advance(d time.Duration) { f.clock.ms.Add(d.Milliseconds()) }

func (f *useFix) use(id string) float64 { return f.m.leaseUseSnapshot()[id].Use }

// cpuProc is a process that uses pct host percent of cpu on the 10-core host
// and no memory.
func cpuProc(pid, ppid int, pct float64) resources.Proc {
	return resources.Proc{PID: pid, PPID: ppid, Pcpu: pct * 10}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestLeaseUse_ProcessScopeTree(t *testing.T) {
	f := newUseFix(t)
	f.heldLease("a", resources.ScopeProcess, 100)
	f.measure([]resources.Proc{
		{PID: 100, PPID: 1, Pcpu: 20, RSSBytes: gib},
		{PID: 101, PPID: 100, Pcpu: 30, RSSBytes: gib},
		{PID: 200, PPID: 1, Pcpu: 500, RSSBytes: 8 * gib}, // not the holder's
	})
	// cpu 50/10 = 5 %, memory 2/16 = 12.5 %.
	if got := f.use("a"); !approx(got, 12.5) {
		t.Fatalf("use = %v, want 12.5", got)
	}
	r := f.row("a")
	if r.Samples != 1 || r.EmptySamples != 0 || !approx(r.EWMA, 12.5) || !approx(r.PeakUse, 12.5) || !approx(r.MeanUse, 12.5) {
		t.Fatalf("row = samples %d empty %d ewma %v peak %v mean %v", r.Samples, r.EmptySamples, r.EWMA, r.PeakUse, r.MeanUse)
	}
}

// sessionTable is a session: the agent 50, an MCP server 60 that was there at
// grant, and a shell 70 running two processes that were not.
func sessionTable(shellPct float64) []resources.Proc {
	return []resources.Proc{
		cpuProc(50, 1, 1),
		cpuProc(60, 50, 9),
		cpuProc(70, 50, shellPct/2),
		cpuProc(71, 70, shellPct/2),
	}
}

func TestLeaseUse_SessionNewExcludesBaseline(t *testing.T) {
	f := newUseFix(t)
	mcp := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	f.alive(60, mcp)
	f.alive(70, mcp.Add(time.Hour))
	f.alive(71, mcp.Add(time.Hour))
	f.heldLease("a", resources.ScopeSessionNew, 50, resources.BaselineEntry{PID: 60, StartMS: mcp.UnixMilli()})
	f.measure(sessionTable(30))
	if got := f.use("a"); !approx(got, 30) {
		t.Fatalf("use = %v, want 30 (the agent and the MCP server are not charged)", got)
	}
	if r := f.row("a"); r.EmptySamples != 0 {
		t.Fatalf("empty_samples = %d, want 0", r.EmptySamples)
	}
}

func TestLeaseUse_BadBaselineJSONIsEmptyBaseline(t *testing.T) {
	f := newUseFix(t)
	f.alive(60, procStart)
	f.heldLease("a", resources.ScopeSessionNew, 50)
	if _, err := f.m.store.db.Exec(`UPDATE resource_leases SET baseline = '{not json' WHERE id = 'a'`); err != nil {
		t.Fatal(err)
	}
	f.measure(sessionTable(30))
	if got := f.use("a"); !approx(got, 39) { // 9 + 30: nothing is excluded
		t.Fatalf("use = %v, want 39", got)
	}
}

func TestLeaseUse_TwoLeasesOneSessionNotDoubleCounted(t *testing.T) {
	f := newUseFix(t)
	mcp := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	f.alive(60, mcp)
	f.alive(70, mcp.Add(time.Hour))
	f.alive(71, mcp.Add(time.Hour))
	base := resources.BaselineEntry{PID: 60, StartMS: mcp.UnixMilli()}
	f.heldLease("a", resources.ScopeSessionNew, 50, base)
	f.heldLease("b", resources.ScopeSessionNew, 50, base)
	f.measure(sessionTable(40))
	if a, b := f.use("a"), f.use("b"); !approx(a, 20) || !approx(b, 20) {
		t.Fatalf("a = %v, b = %v, want 20 each (one new tree of 40)", a, b)
	}
}

func TestLeaseUse_EWMAAndPeakPersist(t *testing.T) {
	f := newUseFix(t)
	f.heldLease("a", resources.ScopeProcess, 100)
	half := f.m.settings().HalfLife()

	var ewma, mean, peak float64
	for i, pct := range []float64{10, 30, 20} {
		if i > 0 {
			f.advance(5 * time.Second)
		}
		f.measure([]resources.Proc{cpuProc(100, 1, pct)})
		if i == 0 {
			ewma, mean = pct, pct
		} else {
			ewma = resources.UpdateEWMA(ewma, pct, 5*time.Second, half, false)
			mean += (pct - mean) / float64(i+1)
		}
		peak = math.Max(peak, pct)
		r := f.row("a")
		if r.Samples != i+1 || !approx(r.EWMA, ewma) || !approx(r.MeanUse, mean) || !approx(r.PeakUse, peak) {
			t.Fatalf("after %d: samples %d ewma %v (want %v) mean %v (want %v) peak %v (want %v)",
				i+1, r.Samples, r.EWMA, ewma, r.MeanUse, mean, r.PeakUse, peak)
		}
	}
	if !approx(peak, 30) || !approx(mean, 20) {
		t.Fatalf("fixture drifted: peak %v mean %v", peak, mean)
	}
	// The map holds the latest raw use, not the average.
	if got := f.use("a"); !approx(got, 20) {
		t.Fatalf("use = %v, want the raw 20", got)
	}
}

// A failed UpdateUse must not move the lease's clock: the next good write
// weighs the whole time since the last figure that reached the database.
func TestLeaseUse_FailedWriteKeepsEWMAClock(t *testing.T) {
	f := newUseFix(t)
	f.heldLease("a", resources.ScopeProcess, 100)
	half := f.m.settings().HalfLife()
	f.measure([]resources.Proc{cpuProc(100, 1, 40)}) // ewma 40 persisted

	if _, err := f.m.store.db.Exec(`CREATE TRIGGER use_fail BEFORE UPDATE ON resource_leases
		BEGIN SELECT RAISE(ABORT, 'busy'); END`); err != nil {
		t.Fatal(err)
	}
	f.advance(60 * time.Second)
	f.measure([]resources.Proc{cpuProc(100, 1, 100)}) // write fails
	if _, err := f.m.store.db.Exec(`DROP TRIGGER use_fail`); err != nil {
		t.Fatal(err)
	}
	if r := f.row("a"); !approx(r.EWMA, 40) {
		t.Fatalf("failed write changed the row: ewma %v", r.EWMA)
	}

	f.advance(5 * time.Second)
	f.measure([]resources.Proc{cpuProc(100, 1, 100)})
	want := resources.UpdateEWMA(40, 100, 65*time.Second, half, false)
	if r := f.row("a"); !approx(r.EWMA, want) {
		t.Fatalf("ewma = %v, want %v (dt 65s since the last persisted figure)", r.EWMA, want)
	}
}

func TestLeaseUse_ResumesPersistedAverage(t *testing.T) {
	f := newUseFix(t)
	f.heldLease("a", resources.ScopeProcess, 100)
	if err := f.m.store.UpdateUse("a", 40, 55, 38, 9, 0); err != nil {
		t.Fatal(err)
	}
	f.measure([]resources.Proc{cpuProc(100, 1, 10)})
	r := f.row("a")
	// Not the first sample: the average moves from the persisted 40, by one
	// sampling interval (no earlier update is known in memory).
	want := resources.UpdateEWMA(40, 10, f.m.interval, f.m.settings().HalfLife(), false)
	if r.Samples != 10 || !approx(r.EWMA, want) || !approx(r.PeakUse, 55) {
		t.Fatalf("row = samples %d ewma %v (want %v) peak %v", r.Samples, r.EWMA, want, r.PeakUse)
	}
}

func TestLeaseUse_EmptySamplesCountsAndResets(t *testing.T) {
	f := newUseFix(t)
	mcp := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	f.alive(60, mcp)
	f.alive(70, mcp.Add(time.Hour))
	f.heldLease("a", resources.ScopeSessionNew, 50, resources.BaselineEntry{PID: 60, StartMS: mcp.UnixMilli()})
	idle := []resources.Proc{cpuProc(50, 1, 1), cpuProc(60, 50, 9)}

	empties := func() int { return f.row("a").EmptySamples }
	f.measure(idle)
	f.advance(5 * time.Second)
	f.measure(idle)
	if got := empties(); got != 2 {
		t.Fatalf("after two idle samples empty_samples = %d, want 2", got)
	}
	f.advance(5 * time.Second)
	f.measure(sessionTable(30)) // the command starts
	if got := empties(); got != 0 {
		t.Fatalf("after a busy sample empty_samples = %d, want 0 (consecutive only)", got)
	}
	f.advance(5 * time.Second)
	f.measure(idle)
	if got := empties(); got != 1 {
		t.Fatalf("empty_samples = %d, want 1", got)
	}
	if r := f.row("a"); r.Samples != 4 {
		t.Fatalf("samples = %d, want 4", r.Samples)
	}
}

func TestLeaseUse_NoHeldLeasesNoSnapshotRead(t *testing.T) {
	f := newUseFix(t)
	f.measure([]resources.Proc{cpuProc(100, 1, 10)})
	if n := f.snaps.Load(); n != 0 {
		t.Fatalf("%d process table reads with no held lease, want 0", n)
	}
	// A waiting row is not held.
	f.waiting("w", f.nowMS()+30000)
	f.measure(nil)
	if n := f.snaps.Load(); n != 0 {
		t.Fatalf("%d process table reads with only a waiter, want 0", n)
	}
	// Held, then ended: the figures are dropped at the next step.
	f.heldLease("a", resources.ScopeProcess, 100)
	f.measure([]resources.Proc{cpuProc(100, 1, 10)})
	if f.use("a") == 0 || f.snaps.Load() != 1 {
		t.Fatalf("held: use %v, reads %d", f.use("a"), f.snaps.Load())
	}
	if _, err := f.m.store.End("a", resources.EndReleased, f.nowMS()); err != nil {
		t.Fatal(err)
	}
	f.measure([]resources.Proc{cpuProc(100, 1, 10)})
	if got := f.m.leaseUseSnapshot(); len(got) != 0 {
		t.Fatalf("after the end the map is %v, want empty", got)
	}
	if n := f.snaps.Load(); n != 1 {
		t.Fatalf("%d reads, want still 1", n)
	}
}

func TestLeaseUse_SnapshotUnavailableKeepsLastValues(t *testing.T) {
	f := newUseFix(t)
	f.heldLease("a", resources.ScopeProcess, 100)
	f.measure([]resources.Proc{cpuProc(100, 1, 10)})
	before := f.row("a")

	f.snapErr = context.DeadlineExceeded
	f.advance(5 * time.Second)
	f.measure([]resources.Proc{cpuProc(100, 1, 90)})
	if got := f.use("a"); !approx(got, 10) {
		t.Fatalf("use = %v, want the last 10", got)
	}
	if after := f.row("a"); after.Samples != before.Samples || after.EWMA != before.EWMA || after.PeakUse != before.PeakUse {
		t.Fatalf("row changed: %+v -> %+v", before, after)
	}
	if f.logs.count("process table") != 1 {
		t.Fatalf("log lines about the table = %d, want 1", f.logs.count("process table"))
	}
	f.measure([]resources.Proc{cpuProc(100, 1, 90)})
	if f.logs.count("process table") != 1 {
		t.Fatal("a standing failure logged again")
	}

	f.snapErr = nil
	f.advance(5 * time.Second)
	f.measure([]resources.Proc{cpuProc(100, 1, 20)})
	if got := f.use("a"); !approx(got, 20) {
		t.Fatalf("use = %v after recovery, want 20", got)
	}
}

func TestLeaseUse_ModeOffDoesNotMeasure(t *testing.T) {
	f := newUseFix(t)
	f.heldLease("a", resources.ScopeProcess, 100)
	f.m.sampler = &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		return goodRaw(), []resources.Proc{cpuProc(100, 1, 10)}, nil
	}}
	f.set.set(resources.Settings{Mode: resources.ModeOff})
	f.m.tick(context.Background())
	if r := f.row("a"); r.Samples != 0 {
		t.Fatalf("samples = %d in mode off, want 0", r.Samples)
	}
	if n := f.snaps.Load(); n != 0 {
		t.Fatalf("%d table reads in mode off, want 0", n)
	}
	f.set.set(resources.Settings{Mode: resources.ModeLease})
	f.m.tick(context.Background())
	if r := f.row("a"); r.Samples != 1 {
		t.Fatalf("samples = %d after switching back, want 1", r.Samples)
	}
}

func TestLeaseUse_TickMeasuresAfterPublish(t *testing.T) {
	f := newUseFix(t)
	f.m.latest.Store(nil) // no snapshot yet
	f.heldLease("a", resources.ScopeProcess, 100)
	f.m.sampler = &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		return goodRaw(), []resources.Proc{cpuProc(100, 1, 10)}, nil
	}}
	var publishedAtRead bool
	f.m.sweepView = func(context.Context) (procView, error) {
		publishedAtRead = f.m.latest.Load() != nil
		return f.view, nil
	}
	f.m.tick(context.Background())
	if !publishedAtRead {
		t.Fatal("the lease measure ran before the snapshot was published")
	}
	if got := f.use("a"); !approx(got, 10) {
		t.Fatalf("use = %v, want 10", got)
	}
}

func TestLeaseUse_FailedSampleDoesNotMeasure(t *testing.T) {
	f := newUseFix(t)
	f.heldLease("a", resources.ScopeProcess, 100)
	f.m.sampler = &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		return resources.HostRaw{}, nil, context.DeadlineExceeded
	}}
	f.m.tick(context.Background())
	if r := f.row("a"); r.Samples != 0 {
		t.Fatalf("samples = %d after a failed sample, want 0", r.Samples)
	}
}

func TestLeaseUse_SnapshotIsACopy(t *testing.T) {
	f := newUseFix(t)
	f.heldLease("a", resources.ScopeProcess, 100)
	f.measure([]resources.Proc{cpuProc(100, 1, 10)})
	got := f.m.leaseUseSnapshot()
	got["a"] = resources.LeaseUsage{Use: 99}
	got["zzz"] = resources.LeaseUsage{Use: 1}
	if again := f.m.leaseUseSnapshot(); len(again) != 1 || again["a"].Use != 10 {
		t.Fatalf("snapshot = %v after the caller edited its copy", again)
	}
	if empty := newUseFix(t).m.leaseUseSnapshot(); empty == nil || len(empty) != 0 {
		t.Fatalf("nothing measured: %v, want an empty non-nil map", empty)
	}
}
