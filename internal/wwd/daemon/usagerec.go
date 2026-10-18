package daemon

import (
	"context"
	"log"
	"time"

	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/wire"
)

// usageQueueLen is how many finished jobs' rows wait for the writer; a full queue drops the row (logged), never blocks the caller.
const usageQueueLen = 256

// UsageSaver is the part of the store the recorder writes to.
type UsageSaver interface {
	SaveUsage(u wire.JobUsage) error
}

// UsageRecorder writes the usage row of every job that ran on this host and ended (spec section on job usage). The 5 s samples reach it
// through Observe and OnFacts (the same two hooks the sampler already has); the queue's job events reach it through OnJobEvent. The
// row is built from the job's final event and what the samples saw of it. It is recording only: nothing here decides anything.
//
// OnJobEvent runs inside the queue's publish: it takes no queue method and waits for nothing. The store write is done by Run on its
// own goroutine.
type UsageRecorder struct {
	host string
	tr   *facts.JobUsageTracker
	sink UsageSaver
	out  chan wire.JobUsage
	logf func(format string, args ...any)
}

// NewUsageRecorder records for host, from tr's samples, into sink.
func NewUsageRecorder(host string, tr *facts.JobUsageTracker, sink UsageSaver) *UsageRecorder {
	return &UsageRecorder{host: host, tr: tr, sink: sink, out: make(chan wire.JobUsage, usageQueueLen), logf: log.Printf}
}

// Observe is facts.Sources.Observe for the recorder: the sample's process table.
func (r *UsageRecorder) Observe(now time.Time, snap facts.ProcSnapshot, c facts.ProcContext) {
	r.tr.Observe(now, snap, c)
}

// OnFacts takes the finished facts record of the sample Observe just saw (the daemon's OnFacts calls it).
func (r *UsageRecorder) OnFacts(f wire.Facts) { r.tr.Commit(f) }

// OnJobEvent is told of every job state change. A job that ran on this host and reached a final state gets its row queued.
func (r *UsageRecorder) OnJobEvent(ev wire.JobEvent) {
	u, ok := r.row(ev.Job)
	if !ok {
		return
	}
	select {
	case r.out <- u:
	default:
		r.logf("wwd: usage of %s dropped: the writer is behind", u.JobID)
	}
}

// Run writes the queued rows until ctx ends, then the ones still waiting.
func (r *UsageRecorder) Run(ctx context.Context) {
	for {
		select {
		case u := <-r.out:
			r.save(u)
		case <-ctx.Done():
			for {
				select {
				case u := <-r.out:
					r.save(u)
				default:
					return
				}
			}
		}
	}
}

func (r *UsageRecorder) save(u wire.JobUsage) {
	if err := r.sink.SaveUsage(u); err != nil {
		r.logf("wwd: usage of %s not saved: %v", u.JobID, err)
	}
}

// ranHere says the job's process tree was on this host: a fast-lane job always, a queue job when its result names this host (a job
// another host's worker ran is recorded by that host).
func (r *UsageRecorder) ranHere(j wire.Job) bool {
	return j.Local || (j.Result != nil && j.Result.Host == r.host)
}

// row is the usage row of a job's event; ok is false when there is nothing to record (not final, ran elsewhere, never ran).
func (r *UsageRecorder) row(j wire.Job) (wire.JobUsage, bool) {
	if !j.State.Terminal() || !r.ranHere(j) {
		return wire.JobUsage{}, false
	}
	sm, seen := r.tr.Take(j.ID) // takes it out even for a job that is not recorded, so nothing is kept for it
	if j.Result == nil || j.Result.StartedAt == nil || j.Result.FinishedAt == nil {
		return wire.JobUsage{}, false // it never started: expired, superseded or cancelled in the queue
	}
	started, ended := *j.Result.StartedAt, *j.Result.FinishedAt
	if ended.Before(started) {
		ended = started
	}
	u := wire.JobUsage{
		JobID: j.ID, Host: r.host, Repo: j.Repo.Name, Task: j.Task, Slot: slotOf(j), Class: j.Class, Source: wire.UsageQueue,
		Result: j.State, Reason: j.Result.Reason, StartedAt: started, EndedAt: ended,
		CPUSource: wire.CPUNone, PeakCoresMeasure: wire.MeasureNone, MemPeakMeasure: wire.MeasureNone,
		ZoneAtStart: wire.ZoneUnknown, ZoneWorst: wire.ZoneUnknown,
	}
	if j.Local {
		u.Source = wire.UsageFastLane
	}
	if !seen {
		return u, true // the job ended between two samples, or its processes were never seen: every figure is unknown
	}
	cpu, cores, mem := sm.CPUSeconds, sm.PeakCores, int(sm.MemPeakBytes>>20)
	u.CPUSeconds, u.CPUSource = &cpu, wire.CPUSampled
	u.PeakCores, u.PeakCoresMeasure = &cores, wire.MeasureSampled
	u.MemPeakMB, u.MemPeakMeasure = &mem, wire.MeasureSampled
	u.JobsAtStart, u.JobsPeak = sm.JobsAtStart, sm.JobsPeak
	u.ZoneAtStart, u.ZoneWorst = sm.ZoneAtStart, sm.ZoneWorst
	if sm.OwnerAtStart != nil && sm.OwnerSeen != nil { // the row has both or neither: an owner first known after the start has no "at start"
		u.OwnerAtStart, u.OwnerSeen = sm.OwnerAtStart, sm.OwnerSeen
	}
	// wwd was restarted while the job ran: the samples before the restart are gone, the sampled figures miss the beginning
	u.Partial = started.Before(r.tr.StartedAt())
	return u, true
}

// slotOf is the gate slot the job took: an undeclared command takes a heavy one, a declared task a light one (fastlane.Request).
func slotOf(j wire.Job) wire.Slot {
	if j.AdHoc {
		return wire.SlotHeavy
	}
	return wire.SlotLight
}
