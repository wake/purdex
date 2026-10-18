package loop

import (
	"path/filepath"
	"time"

	"github.com/wake/purdex/internal/wwd/worker/quarantine"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

// Marker is the part of the quarantine store the guard writes with (*quarantine.Store).
type Marker interface {
	Begin(quarantine.Entry) error
	Discard(owner string) error
	Settle(owner string)
}

// entryOf is the quarantine entry of a job: its owner in the lock table, its job directory, its keys and, on a host that runs commands as
// systemd units, the unit whose absence proves the command gone.
func entryOf(e runner.Ended, sandboxed bool, now time.Time) quarantine.Entry {
	ent := quarantine.Entry{
		Owner: e.Source + "/" + e.Job.ID, JobID: e.Job.ID, Origin: e.Job.Origin, ClaimID: e.ClaimID,
		Dir: filepath.Dir(e.Root), Keys: e.Job.Locks, Since: now,
	}
	if sandboxed {
		ent.Unit = "wwd-" + e.Job.ID
	}
	return ent
}

type guard struct {
	m    Marker
	now  func() time.Time
	logf func(string, ...any)
}

// NewGuard is the runner's Guard over the quarantine store: before a sandboxed command starts, a marker says it is going (so that a
// daemon that dies meanwhile leaves its keys and workspace to the next one); when the command has ended and its unit is proven gone, the
// marker goes; when it is not, it stays and the run is quarantined. It returns nil (no guard) on a host without sandbox units: there is no
// unit whose absence could ever clear such a marker, and a marker nothing clears would hold the keys for good.
func NewGuard(m Marker, sandboxed bool, now func() time.Time, logf func(string, ...any)) runner.Guard {
	if m == nil || !sandboxed {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return guard{m: m, now: now, logf: logf}
}

func (g guard) Begin(e runner.Ended) error { return g.m.Begin(entryOf(e, true, g.now())) }

func (g guard) End(e runner.Ended, quarantined bool) {
	if quarantined {
		// The marker stays (it has been on disk since Begin). The run is over, so the entry stops being live now, in memory, where nothing
		// can fail: a Sweep clears it once the unit is gone, whatever happens to the delivery of this ending (a lease that ended, a worker
		// that is stopping).
		g.m.Settle(e.Source + "/" + e.Job.ID)
		return
	}
	if err := g.m.Discard(e.Source + "/" + e.Job.ID); err != nil && g.logf != nil {
		g.logf("worker: the marker of %s/%s could not be dropped (it is cleared at the next sweep once the unit is gone): %v", e.Source, e.Job.ID, err)
	}
}
