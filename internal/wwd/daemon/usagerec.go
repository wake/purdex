package daemon

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/ident"
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
	// workerRuns: the worker's loop reports its runs here (ForWorker), so a queue job's final event is not a second source of its row.
	workerRuns atomic.Bool
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

// ranHere says this recorder writes the job's row from its final event: a fast-lane job (its process is the caller's, so wwd has only the
// samples), and, until the worker's loop reports its runs (ForWorker), a queue job whose result names this host. Once it does, a job the
// worker ran is written from the run itself (RecordRun), where the exact figures are, and its final event is not used: the first row,
// the one that stays, is never the less exact one.
func (r *UsageRecorder) ranHere(j wire.Job) bool {
	if j.Local {
		return true
	}
	return !r.workerRuns.Load() && j.Result != nil && j.Result.Host == r.host
}

// ForWorker is for the worker loop's Config.Usage: the loop reports each run through RecordRun, and from now on a queue job is written
// from its run, not from its final event. It is called when the loop is wired, before the loop runs: it is not a switch to flip while
// jobs are running (an event already on its way could still write its row first).
func (r *UsageRecorder) ForWorker() *UsageRecorder {
	r.workerRuns.Store(true)
	return r
}

// row is the usage row of a job's event; ok is false when there is nothing to record (not final, ran elsewhere, never ran).
func (r *UsageRecorder) row(j wire.Job) (wire.JobUsage, bool) {
	if !j.State.Terminal() || !r.ranHere(j) {
		return wire.JobUsage{}, false
	}
	sm, seen := r.tr.TakeMark(ident.OriginTag(r.host), j.ID) // takes it out even for a job that is not recorded, so nothing is kept for it
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
		JobID: j.ID, Host: r.host, Repo: j.Repo.Name, Task: j.Task, SlotCharged: wire.JobSlot(j), Class: j.Class, Source: wire.UsageQueue,
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
