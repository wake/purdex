// Package yield is the worker's side of the owner rule on air26 (WWD spec §3.1, §5 "Owner reclaim"): while the owner is using the machine
// the worker takes no new job and gives back the ones it runs. Whether the owner is active is decided by the facts (internal/wwd/facts,
// one shared rule); this package only reads that answer, it does not detect anything.
package yield

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Decision is what the worker does with one reading of facts.owner.
type Decision struct {
	MayClaim bool   // the worker may take new jobs
	Reclaim  bool   // the jobs that run are to be given back now
	Reason   string // the owner's reason while yielding ("hid", "build", "simulator")
}

// Yielder follows the owner readings. Safe for concurrent use.
type Yielder struct {
	now func() time.Time

	mu       sync.Mutex
	yielding bool
	since    time.Time
	reason   string
}

// New is a Yielder; now is the clock.
func New(now func() time.Time) *Yielder { return &Yielder{now: now} }

// Observe takes the latest facts.owner (nil: this host has no owner rule, or the reading is missing) and says what to do. changed is true
// for the reading that starts a yield and for the one that ends it, so that the worker can log and count the two. Reclaim is a level:
// it stays true while yielding, so a job claimed in the instant before a reading is given back at the next one. A missing reading while
// yielding does not end the yield: only a reading that says the owner is away does.
func (y *Yielder) Observe(o *wire.OwnerState) (d Decision, changed bool) {
	y.mu.Lock()
	defer y.mu.Unlock()
	switch {
	case o == nil:
		// no news: whatever was decided stands
	case o.Active:
		if !y.yielding {
			y.yielding, y.since, changed = true, y.now(), true
		}
		y.reason = o.Reason
	default:
		if y.yielding {
			y.yielding, y.reason, changed = false, "", true
		}
	}
	if y.yielding {
		return Decision{MayClaim: false, Reclaim: true, Reason: y.reason}, changed
	}
	return Decision{MayClaim: true}, changed
}

// YieldedSince says since when the worker has been yielding, and whether it is.
func (y *Yielder) YieldedSince() (time.Time, bool) {
	y.mu.Lock()
	defer y.mu.Unlock()
	return y.since, y.yielding
}

// Job is a job the worker is running, as ReclaimAll needs it.
type Job interface {
	ID() string
	// Stop ends the job's processes (the whole group, then the marker sweep) and returns when none is left.
	Stop(ctx context.Context) error
}

// Releaser gives a job back to its origin: release with reason "reclaim", so the job is queued again and no attempt is counted.
type Releaser interface {
	Release(ctx context.Context, jobID string) error
}

// releaseTimeout bounds one release: a call to an origin that does not answer must not hold the reclaim for ever. (A variable so that a
// test can lower it.)
var releaseTimeout = 15 * time.Second

// ReclaimAll stops the jobs, all at the same time (the owner is waiting), and gives each one back after its processes are gone. A job
// that cannot be stopped is still given back: its lease would run out otherwise, and the owner has to be left alone. Every error is
// returned, and one job's failure never keeps another from being given back.
func ReclaimAll(ctx context.Context, jobs []Job, rel Releaser) error {
	errs := make([]error, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j Job) {
			defer wg.Done()
			stopErr := j.Stop(ctx)
			// The release does not share the stop's context: the stop may have ended because that context did, and a release made with
			// it would abort at once and leave the job to come back only when its lease runs out. It gets its own, with a bound.
			rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
			relErr := rel.Release(rctx, j.ID())
			cancel()
			errs[i] = errors.Join(stopErr, relErr)
		}(i, j)
	}
	wg.Wait()
	return errors.Join(errs...)
}
