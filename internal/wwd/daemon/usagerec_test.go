package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/wire"
)

var ut0 = time.Date(2026, 10, 18, 14, 0, 0, 0, time.UTC)

type memUsage struct {
	mu   sync.Mutex
	rows []wire.JobUsage
}

func (m *memUsage) SaveUsage(u wire.JobUsage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows = append(m.rows, u)
	return nil
}

func (m *memUsage) got() []wire.JobUsage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]wire.JobUsage(nil), m.rows...)
}

func newRec(started time.Time) (*UsageRecorder, *memUsage) {
	sink := &memUsage{}
	r := NewUsageRecorder("mlab", facts.NewJobUsageTracker(started, 5*time.Second), sink)
	r.logf = func(string, ...any) {}
	return r, sink
}

// sampleJob feeds one 5 s sample at ut0+n*5s in which job j_a (pid 300, with a child 301) uses 2 cores and 600 MB.
func sampleJob(r *UsageRecorder, n int, zone wire.Zone, owner *wire.OwnerState) {
	snap := facts.ProcSnapshot{Procs: []facts.ProcRec{
		{PID: 300, PPID: 1, Name: "go", CPUPct: 50, FootprintBytes: 100 << 20},
		{PID: 301, PPID: 300, Name: "compile", CPUPct: 150, FootprintBytes: 500 << 20},
	}}
	ctx := facts.ProcContext{JobOf: func(pid int) (string, bool) { return "j_a", pid == 300 }}
	now := ut0.Add(time.Duration(n) * 5 * time.Second)
	r.Observe(now, snap, ctx)
	r.OnFacts(wire.Facts{At: now, Owner: owner, Derived: wire.Derived{Zone: zone}})
}

func finalJob(state wire.JobState, start, end time.Time) wire.Job {
	return wire.Job{
		ID: "j_a", Repo: wire.Repo{Name: "purdex"}, Task: "go-test", Class: wire.ClassDev, State: state,
		Result: &wire.Result{Host: "mlab", StartedAt: &start, FinishedAt: &end, Reason: "oom"},
	}
}

func drain(r *UsageRecorder, sink *memUsage) []wire.JobUsage {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Run writes what waits, then returns
	r.Run(ctx)
	return sink.got()
}

func TestUsageRecorderWritesTheRowOfAJobThatRanHere(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	here := &wire.OwnerState{Active: false}
	for n := 0; n < 3; n++ {
		sampleJob(r, n, wire.ZoneNormal, here)
	}
	r.OnJobEvent(wire.JobEvent{Host: "mlab", Job: finalJob(wire.StateFailed, ut0, ut0.Add(12*time.Second))})
	rows := drain(r, sink)
	if len(rows) != 1 {
		t.Fatalf("%d rows", len(rows))
	}
	u := rows[0]
	if err := u.Validate(); err != nil {
		t.Fatal(err)
	}
	if u.JobID != "j_a" || u.Host != "mlab" || u.Repo != "purdex" || u.Task != "go-test" || u.SlotCharged != wire.SlotHeavy ||
		u.Source != wire.UsageQueue || u.Result != wire.StateFailed || u.Reason != "oom" || u.Class != wire.ClassDev {
		t.Fatalf("%+v", u)
	}
	// 3 samples x 5 s x 2 cores, 600 MB, sampled; the job began after wwd did: not partial
	if u.CPUSeconds == nil || *u.CPUSeconds < 29.9 || *u.CPUSeconds > 30.1 || u.CPUSource != wire.CPUSampled ||
		*u.PeakCores != 2 || u.PeakCoresMeasure != wire.MeasureSampled || *u.MemPeakMB != 600 || u.MemPeakMeasure != wire.MeasureSampled {
		t.Fatalf("%+v", u)
	}
	if u.Partial || u.JobsAtStart != 1 || u.ZoneAtStart != wire.ZoneNormal || u.OwnerAtStart == nil || *u.OwnerAtStart {
		t.Fatalf("%+v", u)
	}
}

func TestUsageRecorderMarksAJobThatBeganBeforeWwdPartial(t *testing.T) {
	r, sink := newRec(ut0.Add(5 * time.Second)) // wwd began 5 s into the job
	sampleJob(r, 1, wire.ZoneNormal, nil)
	r.OnJobEvent(wire.JobEvent{Job: finalJob(wire.StateSucceeded, ut0, ut0.Add(20*time.Second))})
	rows := drain(r, sink)
	if len(rows) != 1 || !rows[0].Partial {
		t.Fatalf("%+v", rows)
	}
}

func TestUsageRecorderAJobNoSampleSawHasUnknownFigures(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	r.OnJobEvent(wire.JobEvent{Job: finalJob(wire.StateSucceeded, ut0, ut0.Add(2*time.Second))}) // ended between two samples
	rows := drain(r, sink)
	if len(rows) != 1 {
		t.Fatalf("%d rows", len(rows))
	}
	u := rows[0]
	if err := u.Validate(); err != nil {
		t.Fatal(err)
	}
	if u.CPUSeconds != nil || u.CPUSource != wire.CPUNone || u.PeakCores != nil || u.MemPeakMB != nil || u.Partial {
		t.Fatalf("%+v", u)
	}
}

func TestUsageRecorderLeavesOutWhatDidNotRunHere(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	running := finalJob(wire.StateRunning, ut0, ut0)
	r.OnJobEvent(wire.JobEvent{Job: running}) // not final
	elsewhere := finalJob(wire.StateSucceeded, ut0, ut0.Add(time.Second))
	elsewhere.Result.Host = "air26" // that host's worker records it
	r.OnJobEvent(wire.JobEvent{Job: elsewhere})
	expired := finalJob(wire.StateExpired, ut0, ut0)
	expired.Result = &wire.Result{Host: "mlab"} // never started
	r.OnJobEvent(wire.JobEvent{Job: expired})
	cancelled, at := finalJob(wire.StateCancelled, ut0, ut0), ut0.Add(time.Second)
	cancelled.Result = &wire.Result{Host: "mlab", FinishedAt: &at} // stopped in the queue: a finish time and no start
	r.OnJobEvent(wire.JobEvent{Job: cancelled})
	if rows := drain(r, sink); len(rows) != 0 {
		t.Fatalf("%+v", rows)
	}
}

// A result with no time (a lease that ran out, a release) still gets a row when the samples saw the job: they say when it was there.
func TestUsageRecorderAResultWithoutTimesUsesWhatTheSamplesSaw(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	sampleJob(r, 0, wire.ZoneNormal, nil)
	sampleJob(r, 1, wire.ZoneNormal, nil)
	j := finalJob(wire.StateFailed, ut0, ut0)
	j.Result = &wire.Result{Host: "mlab", Reason: "lease_lost"}
	r.OnJobEvent(wire.JobEvent{Job: j})
	rows := drain(r, sink)
	if len(rows) != 1 || rows[0].Validate() != nil {
		t.Fatalf("%+v", rows)
	}
	u := rows[0]
	if !u.StartedAt.Equal(ut0) || !u.EndedAt.Equal(ut0.Add(5*time.Second)) || !u.Partial || u.CPUSeconds == nil {
		t.Fatalf("%+v", u)
	}
}

// A full writer queue drops rows, counts them, and Run says so: the caller logs nothing.
func TestUsageRecorderCountsTheRowsItDropped(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	var said []string
	r.logf = func(f string, a ...any) { said = append(said, f) }
	for i := 0; i < usageQueueLen+3; i++ {
		j := finalJob(wire.StateSucceeded, ut0, ut0.Add(time.Second))
		j.ID = "j_" + time.Duration(i).String()
		r.OnJobEvent(wire.JobEvent{Job: j})
	}
	if len(said) != 0 {
		t.Fatalf("the callback logged: %v", said)
	}
	if rows := drain(r, sink); len(rows) != usageQueueLen {
		t.Fatalf("%d rows", len(rows))
	}
	if len(said) != 1 || r.dropped.Load() != 0 { // Run told once, and reset the count
		t.Fatalf("dropped %d said %v", r.dropped.Load(), said)
	}
}

func TestUsageRecorderFastLaneAndAdHocJobs(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	sampleJob(r, 0, wire.ZoneNormal, nil)
	j := finalJob(wire.StateSucceeded, ut0, ut0.Add(5*time.Second))
	j.Local, j.AdHoc = true, true
	j.Result.Host = "" // a fast-lane result has no worker host
	r.OnJobEvent(wire.JobEvent{Job: j})
	rows := drain(r, sink)
	if len(rows) != 1 || rows[0].Source != wire.UsageFastLane || rows[0].SlotCharged != wire.SlotHeavy {
		t.Fatalf("%+v", rows)
	}
}

// A declared task through the fast lane takes the light slot; a queue job the worker runs is charged a heavy one.
func TestUsageRecorderSlotIsWhatTheHostCharged(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	j := finalJob(wire.StateSucceeded, ut0, ut0.Add(time.Second))
	j.Local, j.Result.Host = true, ""
	r.OnJobEvent(wire.JobEvent{Job: j})
	if rows := drain(r, sink); len(rows) != 1 || rows[0].SlotCharged != wire.SlotLight || rows[0].Source != wire.UsageFastLane {
		t.Fatalf("%+v", rows)
	}
}

func TestUsageRecorderEveryFinalStateRowValidates(t *testing.T) {
	for _, st := range []wire.JobState{wire.StateSucceeded, wire.StateFailed, wire.StateCancelled, wire.StateSuperseded, wire.StateExpired} {
		r, sink := newRec(ut0.Add(-time.Hour))
		sampleJob(r, 0, wire.ZoneWarning, nil)
		r.OnJobEvent(wire.JobEvent{Job: finalJob(st, ut0, ut0.Add(time.Second))})
		rows := drain(r, sink)
		if len(rows) != 1 || rows[0].Validate() != nil || rows[0].Result != st {
			t.Errorf("%s: %+v", st, rows)
		}
	}
}

// The owner first known after the job began has no "at start": the row keeps both null (wire: both or neither).
func TestUsageRecorderOwnerKnownLateGivesNullOwnerFigures(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	sampleJob(r, 0, wire.ZoneNormal, nil)
	sampleJob(r, 1, wire.ZoneNormal, &wire.OwnerState{Active: true})
	r.OnJobEvent(wire.JobEvent{Job: finalJob(wire.StateSucceeded, ut0, ut0.Add(10*time.Second))})
	rows := drain(r, sink)
	if len(rows) != 1 || rows[0].Validate() != nil || rows[0].OwnerAtStart != nil || rows[0].OwnerSeen != nil {
		t.Fatalf("%+v", rows)
	}
}

// A full writer queue drops the row and never blocks the queue's publish.
func TestUsageRecorderNeverBlocksTheCaller(t *testing.T) {
	r, _ := newRec(ut0.Add(-time.Hour))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < usageQueueLen+10; i++ {
			j := finalJob(wire.StateSucceeded, ut0, ut0.Add(time.Second))
			j.ID = "j_" + time.Duration(i).String()
			r.OnJobEvent(wire.JobEvent{Job: j})
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("OnJobEvent blocked")
	}
}
