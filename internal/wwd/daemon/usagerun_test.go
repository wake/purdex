package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/loop"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

// The recorder is what the worker's loop takes as Config.Usage.
var _ loop.UsageRecorder = (*UsageRecorder)(nil)

func queueJob() wire.Job {
	return wire.Job{ID: "j_a", Repo: wire.Repo{Name: "purdex"}, Task: "go-test", Class: wire.ClassMerge, State: wire.StateRunning}
}

func f64(v float64) *float64 { return &v }
func u64(v uint64) *uint64   { return &v }

// ran is the Ended of a command that ran for 12 s from ut0 and came out as res.
func ran(res run.Result) runner.Ended {
	res.StartedAt, res.FinishedAt = ut0, ut0.Add(12*time.Second)
	return runner.Ended{Kind: runner.KindRan, Job: queueJob(), Run: res}
}

// The whole path with a real run: run.Run makes the figures (rusage, and the leftover process), the samples make the rest, RecordRun
// makes the row, the writer saves it.
func TestRecordRunFromARealRunThatLeftAProcessBehind(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	for n := 0; n < 3; n++ {
		sampleJob(r, n, wire.ZoneNormal, nil)
	}
	dir := t.TempDir()
	res := run.Run(context.Background(), run.Spec{
		JobID: "j_a", Argv: []string{"sh", "-c", "i=0; while [ $i -lt 20000 ]; do i=$((i+1)); done; sleep 30 >/dev/null 2>&1 & exit 0"},
		Dir: dir, Workspace: dir, ParentEnv: []string{"PATH=/usr/bin:/bin"}, OutDir: filepath.Join(dir, ".out"), KillAfter: 300 * time.Millisecond,
	})
	if res.Outcome != run.Exited || res.ExitCode != 0 || !res.Usage.Leaked {
		t.Fatalf("the run: %+v %+v", res, res.Usage)
	}
	e := runner.Ended{Kind: runner.KindRan, Job: queueJob(), Run: res}
	e.Run.StartedAt, e.Run.FinishedAt = ut0, ut0.Add(12*time.Second) // the samples are on the test's clock
	r.RecordRun(e)
	rows := drain(r, sink)
	if len(rows) != 1 {
		t.Fatalf("%d rows", len(rows))
	}
	u := rows[0]
	if err := u.Validate(); err != nil {
		t.Fatal(err)
	}
	if u.Source != wire.UsageQueue || u.SlotCharged != wire.SlotHeavy || u.Result != wire.StateSucceeded || u.Class != wire.ClassMerge || u.Host != "mlab" {
		t.Fatalf("%+v", u)
	}
	// CPU time is the run's rusage (exact), the peak cores and the memory the samples' (sampled); the leftover process makes the exact one a floor
	if u.CPUSource != wire.CPUFromRusage || u.CPUSeconds == nil || *u.CPUSeconds != *res.Usage.CPUSeconds {
		t.Fatalf("cpu: %+v", u)
	}
	if u.PeakCoresMeasure != wire.MeasureSampled || *u.PeakCores != 2 || u.MemPeakMeasure != wire.MeasureSampled || *u.MemPeakMB != 600 {
		t.Fatalf("peak: %+v", u)
	}
	if !u.LowerBound {
		t.Fatalf("a run that left a process behind has a floor for a CPU time: %+v", u)
	}
}

// A sandbox unit's figures are exact and win over the samples; a unit with its memory but not its CPU time is mixed per figure.
func TestRecordRunUsesTheUnitsExactFigures(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	sampleJob(r, 0, wire.ZoneWarning, nil)
	r.RecordRun(ran(run.Result{Outcome: run.Exited, Usage: run.Usage{From: run.UsageUnit, CPUSeconds: f64(7.5), MemPeakBytes: u64(900 << 20)}}))
	u := drain(r, sink)[0]
	if err := u.Validate(); err != nil {
		t.Fatal(err)
	}
	if u.CPUSource != wire.CPUFromUnit || *u.CPUSeconds != 7.5 || u.MemPeakMeasure != wire.MeasureExact || *u.MemPeakMB != 900 ||
		u.PeakCoresMeasure != wire.MeasureSampled || u.LowerBound || u.ZoneAtStart != wire.ZoneWarning {
		t.Fatalf("%+v", u)
	}
}

func TestRecordRunMixesExactAndSampledPerFigure(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	sampleJob(r, 0, wire.ZoneNormal, nil)
	// rusage gives the CPU time only (a plain run has no memory figure): the memory is the samples'
	r.RecordRun(ran(run.Result{Outcome: run.Exited, Usage: run.Usage{From: run.UsageRusage, CPUSeconds: f64(3)}}))
	u := drain(r, sink)[0]
	if u.CPUSource != wire.CPUFromRusage || *u.CPUSeconds != 3 || u.MemPeakMeasure != wire.MeasureSampled || *u.MemPeakMB != 600 {
		t.Fatalf("%+v", u)
	}
}

// Without a sample the exact figures stand alone and everything else is unknown (null, not 0); without an exact figure either, it is all unknown.
func TestRecordRunWithoutSamples(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	r.RecordRun(ran(run.Result{Outcome: run.Exited, Usage: run.Usage{From: run.UsageRusage, CPUSeconds: f64(3)}}))
	u := drain(r, sink)[0]
	if err := u.Validate(); err != nil {
		t.Fatal(err)
	}
	if u.CPUSource != wire.CPUFromRusage || u.PeakCores != nil || u.PeakCoresMeasure != wire.MeasureNone || u.MemPeakMB != nil || u.Partial {
		t.Fatalf("%+v", u)
	}
	r2, sink2 := newRec(ut0.Add(-time.Hour))
	r2.RecordRun(ran(run.Result{Outcome: run.Exited}))
	u = drain(r2, sink2)[0]
	if err := u.Validate(); err != nil || u.CPUSeconds != nil || u.CPUSource != wire.CPUNone {
		t.Fatalf("%v %+v", err, u)
	}
}

// The floor flag goes with an exact figure only: a leftover process says nothing about a figure the samples made.
func TestRecordRunLowerBoundNeedsAnExactFigure(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	sampleJob(r, 0, wire.ZoneNormal, nil)
	r.RecordRun(ran(run.Result{Outcome: run.Exited, Usage: run.Usage{Leaked: true}}))
	if u := drain(r, sink)[0]; u.LowerBound || u.CPUSource != wire.CPUSampled {
		t.Fatalf("%+v", u)
	}
}

func TestRecordRunMarksAJobThatBeganBeforeWwdPartial(t *testing.T) {
	r, sink := newRec(ut0.Add(5 * time.Second))
	sampleJob(r, 1, wire.ZoneNormal, nil)
	r.RecordRun(ran(run.Result{Outcome: run.Exited}))
	if u := drain(r, sink)[0]; !u.Partial {
		t.Fatalf("%+v", u)
	}
}

// What the run turned into is what the reporter posts, and only a result of the job is a row.
func TestRecordRunResultMatchesWhatTheReporterPosts(t *testing.T) {
	exit := func(code int) run.Result { return run.Result{Outcome: run.Exited, ExitCode: code} }
	type want struct {
		state  wire.JobState
		reason string
		ok     bool
	}
	cases := map[string]struct {
		e runner.Ended
		w want
	}{
		"succeeded":        {ran(exit(0)), want{wire.StateSucceeded, "", true}},
		"failed":           {ran(exit(3)), want{wire.StateFailed, "", true}},
		"killed by signal": {ran(run.Result{Outcome: run.Exited, ExitCode: -1, Signal: "killed"}), want{wire.StateFailed, "", true}},
		"timeout":          {ran(run.Result{Outcome: run.TimedOut}), want{wire.StateFailed, "timeout", true}},
		"unit timeout":     {ran(run.Result{Outcome: run.Exited, Reason: "timeout"}), want{wire.StateFailed, "timeout", true}},
		"oom":              {ran(run.Result{Outcome: run.Exited, ExitCode: 137, Reason: "oom"}), want{wire.StateFailed, "oom", true}},
		"cancelled":        {runner.Ended{Kind: runner.KindCancelled, Job: queueJob(), Run: run.Result{Outcome: run.Cancelled}}, want{wire.StateCancelled, "", true}},
		// the job goes back to the queue, or it is not this worker's result: no row (the next run of the job is the job's)
		"log not kept":     {ran(run.Result{Outcome: run.Exited, CaptureErr: context.Canceled}), want{"", "", false}},
		"unit not stopped": {ran(run.Result{Outcome: run.Exited, Err: context.Canceled}), want{"", "", false}},
		"quarantined":      {runner.Ended{Kind: runner.KindRan, Job: queueJob(), Quarantined: true, Run: run.Result{Outcome: run.Exited}}, want{"", "", false}},
		"never started":    {ran(run.Result{Outcome: run.StartFailed}), want{"", "", false}},
		"lost":             {runner.Ended{Kind: runner.KindLost, Job: queueJob(), Run: run.Result{Outcome: run.Cancelled}}, want{"", "", false}},
		"infra":            {runner.Ended{Kind: runner.KindInfra, Job: queueJob()}, want{"", "", false}},
		"shutdown":         {runner.Ended{Kind: runner.KindShutdown, Job: queueJob(), Run: run.Result{Outcome: run.Cancelled}}, want{"", "", false}},
		"reclaimed":        {runner.Ended{Kind: runner.KindReclaimed, Job: queueJob(), Run: run.Result{Outcome: run.Cancelled}}, want{"", "", false}},
		"rejected":         {runner.Ended{Kind: runner.KindRejected, Job: queueJob()}, want{"", "", false}},
		"skipped":          {runner.Ended{Kind: runner.KindSkipped, Job: queueJob()}, want{"", "", false}},
	}
	for name, c := range cases {
		state, reason, ok := runResult(c.e)
		if state != c.w.state || reason != c.w.reason || ok != c.w.ok {
			t.Errorf("%s: %q %q %v, want %+v", name, state, reason, ok, c.w)
		}
	}
}

// A run that is not recorded still takes its samples out, so nothing is kept for a job that goes back to the queue.
func TestRecordRunForgetsTheSamplesOfARunItDoesNotRecord(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	sampleJob(r, 0, wire.ZoneNormal, nil)
	r.RecordRun(runner.Ended{Kind: runner.KindInfra, Job: queueJob(), Run: run.Result{FinishedAt: ut0.Add(time.Second)}})
	if rows := drain(r, sink); len(rows) != 0 || r.tr.Len() != 0 {
		t.Fatalf("%d rows, %d kept", len(rows), r.tr.Len())
	}
}

// Until the worker's loop reports its runs, a queue job whose result names this host is written from its final event, as before.
func TestAQueueJobsFinalEventWritesTheRowUntilTheLoopReportsItsRuns(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	sampleJob(r, 0, wire.ZoneNormal, nil)
	j := queueJob()
	j.State = wire.StateSucceeded
	start, end := ut0, ut0.Add(12*time.Second)
	j.Result = &wire.Result{Host: "mlab", StartedAt: &start, FinishedAt: &end}
	r.OnJobEvent(wire.JobEvent{Job: j})
	rows := drain(r, sink)
	if len(rows) != 1 || rows[0].Source != wire.UsageQueue || rows[0].SlotCharged != wire.SlotHeavy || rows[0].CPUSource != wire.CPUSampled {
		t.Fatalf("%+v", rows)
	}
}

// A run that goes back to the queue is not the job's end: the same job's next run collects its own samples and writes the row.
func TestRecordRunAJobThatRunsAgainKeepsTheSecondRunsSamples(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	sampleJob(r, 0, wire.ZoneDanger, nil) // first run: released as infra
	r.RecordRun(runner.Ended{Kind: runner.KindInfra, Job: queueJob(), Run: run.Result{FinishedAt: ut0.Add(time.Second)}})
	sampleJob(r, 1, wire.ZoneNormal, nil) // the same job id again
	sampleJob(r, 2, wire.ZoneNormal, nil)
	r.RecordRun(ran(run.Result{Outcome: run.Exited}))
	rows := drain(r, sink)
	if len(rows) != 1 {
		t.Fatalf("%d rows", len(rows))
	}
	u := rows[0]
	if u.CPUSource != wire.CPUSampled || u.CPUSeconds == nil || *u.CPUSeconds < 19.9 || *u.CPUSeconds > 20.1 || u.ZoneWorst != wire.ZoneNormal ||
		u.PeakCores == nil || *u.PeakCores != 2 {
		t.Fatalf("the second run's own samples (2 samples x 5 s x 2 cores = 20 s, zone normal): %+v", u)
	}
}

// Once the loop reports its runs (ForWorker), the final event of a queue job the worker ran does not write a row: the run's is the one.
func TestAQueueJobsFinalEventWritesNoRowOnceTheLoopReportsItsRuns(t *testing.T) {
	r, sink := newRec(ut0.Add(-time.Hour))
	r.ForWorker()
	sampleJob(r, 0, wire.ZoneNormal, nil)
	j := queueJob()
	j.State = wire.StateSucceeded
	start, end := ut0, ut0.Add(12*time.Second)
	j.Result = &wire.Result{Host: "mlab", StartedAt: &start, FinishedAt: &end}
	r.OnJobEvent(wire.JobEvent{Job: j})
	if rows := drain(r, sink); len(rows) != 0 {
		t.Fatalf("%+v", rows)
	}
	if r.tr.Len() != 1 {
		t.Fatalf("the samples are the run's to take: %d kept", r.tr.Len())
	}
}
