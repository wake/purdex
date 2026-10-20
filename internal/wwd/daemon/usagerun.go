package daemon

import (
	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/ident"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

// RecordRun is loop.UsageRecorder: the worker tells it how each run it made went (after the result was delivered, before the
// workspace goes). A job the worker ran here, for this host's queue or a peer's, is recorded from here and only from here: the exact
// figures (the process's rusage, the sandbox unit's cgroup) are in run.Result, which neither the queue's final event nor a peer
// has. It never blocks (the row is queued for the writer, like OnJobEvent's).
//
// The caller must call it only for a run whose result the origin accepted (the loop's finish, once the reporter says delivered): a row
// is the job's first and only one, so a run the origin did not take, which the job may be given again after, must not write it.
func (r *UsageRecorder) RecordRun(e runner.Ended) {
	u, ok := r.runRow(e)
	if !ok {
		return
	}
	select {
	case r.out <- u:
	default:
		r.dropped.Add(1)
	}
}

// runResult is the final state and reason a run's Ended becomes, the same the reporter posts (report.Reporter.ran, and cancelled);
// ok is false when the run is not a result of the job: the job goes back to the queue (released as infra or reclaim), the lease was
// lost, or it never started. A job that goes back runs again, and the row of its next run is the job's: this one is not written.
func runResult(e runner.Ended) (state wire.JobState, reason string, ok bool) {
	rn := e.Run
	if e.Quarantined || (rn.Err != nil && e.Kind != runner.KindLost) {
		return "", "", false // the unit was not proven stopped: released as infra whatever the outcome says
	}
	switch e.Kind {
	case runner.KindCancelled:
		return wire.StateCancelled, "", true
	case runner.KindRan:
		if rn.CaptureErr != nil {
			return "", "", false
		}
		switch {
		case rn.Outcome == run.TimedOut || rn.Reason == "timeout":
			return wire.StateFailed, "timeout", true
		case rn.Reason == "oom":
			return wire.StateFailed, "oom", true
		case rn.Outcome == run.Exited && rn.Signal == "" && rn.ExitCode == 0:
			return wire.StateSucceeded, "", true
		case rn.Outcome == run.Exited:
			return wire.StateFailed, "", true
		}
	}
	return "", "", false
}

// runRow is the usage row of a run the worker made: the exact figures of run.Result where it has them, and what the 5 s samples saw
// for the rest (the peak cores, the host around the job, and a CPU time or a memory peak the run could not give).
func (r *UsageRecorder) runRow(e runner.Ended) (wire.JobUsage, bool) {
	state, reason, ok := runResult(e)
	rn := e.Run
	if !ok || rn.StartedAt.IsZero() || rn.FinishedAt.IsZero() {
		// not the job's end: it may run again, and its next run collects its own samples. What this run left is dropped, not taken (a taken
		// job is one the tracker ignores from then on).
		r.tr.DiscardMark(ident.OriginTag(e.Source), e.Job.ID)
		return wire.JobUsage{}, false
	}
	sm, seen := r.tr.TakeMark(ident.OriginTag(e.Source), e.Job.ID)
	started, ended := rn.StartedAt, rn.FinishedAt
	if ended.Before(started) {
		ended = started
	}
	j := e.Job
	u := wire.JobUsage{
		JobID: j.ID, Host: r.host, Repo: j.Repo.Name, Task: j.Task, SlotCharged: wire.JobSlot(j), Class: j.Class, Source: wire.UsageQueue,
		Result: state, Reason: reason, StartedAt: started, EndedAt: ended,
		CPUSource: wire.CPUNone, PeakCoresMeasure: wire.MeasureNone, MemPeakMeasure: wire.MeasureNone,
		ZoneAtStart: wire.ZoneUnknown, ZoneWorst: wire.ZoneUnknown,
	}
	exact := false
	if cpu := rn.Usage.CPUSeconds; cpu != nil {
		v := *cpu
		u.CPUSeconds, exact = &v, true
		u.CPUSource = wire.CPUFromRusage
		if rn.Usage.From == run.UsageUnit {
			u.CPUSource = wire.CPUFromUnit
		}
	}
	if mem := rn.Usage.MemPeakBytes; mem != nil {
		mb := int(*mem >> 20)
		u.MemPeakMB, u.MemPeakMeasure, exact = &mb, wire.MeasureExact, true
	}
	if seen {
		if u.CPUSeconds == nil {
			v := sm.CPUSeconds
			u.CPUSeconds, u.CPUSource = &v, wire.CPUSampled
		}
		cores := sm.PeakCores
		u.PeakCores, u.PeakCoresMeasure = &cores, wire.MeasureSampled
		if u.MemPeakMB == nil {
			mb := int(sm.MemPeakBytes >> 20)
			u.MemPeakMB, u.MemPeakMeasure = &mb, wire.MeasureSampled
		}
		u.JobsAtStart, u.JobsPeak = sm.JobsAtStart, sm.JobsPeak
		u.ZoneAtStart, u.ZoneWorst = sm.ZoneAtStart, sm.ZoneWorst
		if sm.OwnerAtStart != nil && sm.OwnerSeen != nil { // both or neither (wire)
			u.OwnerAtStart, u.OwnerSeen = sm.OwnerAtStart, sm.OwnerSeen
		}
		// the samples before a restart of wwd are gone: the sampled figures miss the beginning
		u.Partial = started.Before(r.tr.StartedAt())
	}
	// the exact figures are read when the command returns: processes it left behind are in none of them
	u.LowerBound = exact && rn.Usage.Leaked
	return u, true
}
