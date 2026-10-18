package daemon

import (
	"context"
	"log"
	"sync/atomic"
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
	// dropped counts the rows a full writer queue turned away; Run reports it (the callback itself does no logging).
	dropped atomic.Int64
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
		r.dropped.Add(1)
	}
}

// Run writes the queued rows until ctx ends, then the ones still waiting.
func (r *UsageRecorder) Run(ctx context.Context) {
	for {
		select {
		case u := <-r.out:
			r.save(u)
			r.reportDropped()
		case <-ctx.Done():
			for {
				select {
				case u := <-r.out:
					r.save(u)
				default:
					r.reportDropped()
					return
				}
			}
		}
	}
}

func (r *UsageRecorder) reportDropped() {
	if n := r.dropped.Swap(0); n > 0 {
		r.logf("wwd: %d usage rows dropped: the writer was behind", n)
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
	if j.Result == nil {
		return wire.JobUsage{}, false
	}
	// A result can lack a time (a lease that ran out, a release): then the samples say when the job was there. With neither a time
	// nor a sample there is no evidence the job ran, and no row.
	started, ended, guessed := time.Time{}, time.Time{}, false
	switch {
	case j.Result.StartedAt != nil:
		started = *j.Result.StartedAt
	case seen:
		started, guessed = sm.FirstSeen, true
	default:
		return wire.JobUsage{}, false
	}
	switch {
	case j.Result.FinishedAt != nil:
		ended = *j.Result.FinishedAt
	case seen:
		ended = sm.LastSeen
	default:
		return wire.JobUsage{}, false
	}
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
	u.Partial = guessed || started.Before(r.tr.StartedAt()) // a start made up from the samples is not the job's own
	return u, true
}

// slotOf is the slot the job was charged, which is a rule of the host's admission today and not a property of the job (wire.Job has no
// slot, nor does a manifest): a queue job the worker runs takes a heavy slot (worker/loop: it claims only while running < Slots.Heavy
// and the headroom has a heavy slot; lease, 10-18); a fast-lane job takes the slot its request named, a heavy one for an undeclared
// command (adhoc) and a light one for a declared task (fastlane.Request.GateClass). When the admission rule changes, this changes
// with it (a loop.SlotOf, if the worker side makes one).
func slotOf(j wire.Job) wire.Slot {
	if j.Local && !j.AdHoc {
		return wire.SlotLight
	}
	return wire.SlotHeavy
}
