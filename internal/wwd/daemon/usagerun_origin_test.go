package daemon

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/ident"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

// sampleMarked is sampleJob for a job whose mark has the origin tag (pid 300 carries it, 301 is its child).
func sampleMarked(r *UsageRecorder, n int, tag string) {
	snap := facts.ProcSnapshot{Procs: []facts.ProcRec{
		{PID: 300, PPID: 1, Name: "go", CPUPct: 50, FootprintBytes: 100 << 20},
		{PID: 301, PPID: 300, Name: "compile", CPUPct: 150, FootprintBytes: 500 << 20},
	}}
	ctx := facts.ProcContext{
		JobOf:    func(pid int) (string, bool) { return "j_a", pid == 300 },
		OriginOf: func(pid int) (string, bool) { return tag, pid == 300 },
	}
	now := ut0.Add(time.Duration(n) * 5 * time.Second)
	r.Observe(now, snap, ctx)
	r.OnFacts(wire.Facts{At: now, Derived: wire.Derived{Zone: wire.ZoneNormal}})
}

func endedFrom(source string) runner.Ended {
	e := ran(run.Result{Outcome: run.Exited})
	e.Source = source
	return e
}

// The samples of a job are found by the connection it came in on: the run of air26's job j_a gets the samples of air26's j_a, and a run
// of mserv's job with the same id gets none (they are another job's).
func TestRecordRunFindsTheSamplesOfItsOwnOrigin(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	for n := 0; n < 3; n++ {
		sampleMarked(r, n, ident.OriginTag("air26"))
	}
	r.RecordRun(endedFrom("mserv"))
	other := drain(r, sink)
	if len(other) != 1 || other[0].PeakCores != nil {
		t.Fatalf("mserv's job j_a must not get air26's samples: %+v", other)
	}
	r, sink = newRec(ut0.Add(-time.Hour))
	for n := 0; n < 3; n++ {
		sampleMarked(r, n, ident.OriginTag("air26"))
	}
	r.RecordRun(endedFrom("air26"))
	own := drain(r, sink)
	if len(own) != 1 || own[0].PeakCores == nil || *own[0].PeakCores != 2 {
		t.Fatalf("air26's job j_a must get its own samples (2 cores): %+v", own)
	}
}

// A run that is not the job's end drops what its origin's samples saw, and not another origin's.
func TestRecordRunDiscardsOnlyItsOwnOriginsSamples(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	for n := 0; n < 3; n++ {
		sampleMarked(r, n, ident.OriginTag("air26"))
	}
	// a run of mserv's j_a that is not the end of the job (no start time) discards under mserv's tag: air26's samples stay
	notEnd := endedFrom("mserv")
	notEnd.Run.StartedAt = time.Time{}
	r.RecordRun(notEnd)
	r.RecordRun(endedFrom("air26"))
	rows := drain(r, sink)
	if len(rows) != 1 || rows[0].PeakCores == nil {
		t.Fatalf("air26's samples must have survived the other origin's discard: %+v", rows)
	}
}

// A run of air26's job that is not the job's end drops air26's samples of it (the job runs again and collects its own).
func TestRecordRunDiscardsTheSamplesOfItsOwnOriginWhenTheRunIsNotTheEnd(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	for n := 0; n < 3; n++ {
		sampleMarked(r, n, ident.OriginTag("air26"))
	}
	notEnd := endedFrom("air26")
	notEnd.Run.StartedAt = time.Time{}
	r.RecordRun(notEnd)
	r.RecordRun(endedFrom("air26"))
	rows := drain(r, sink)
	if len(rows) != 1 || rows[0].PeakCores != nil {
		t.Fatalf("the samples of the run that was not the end must be gone: %+v", rows)
	}
}
