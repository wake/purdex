package resourcesmod

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wake/purdex/internal/resources"
)

var errUnusableReading = errors.New("host reading is not usable (no cpu count or memory size, or a page count that overflows)")

// run is the sampler goroutine: a tick at once, then one per interval. It
// returns when ctx ends, or at once when the platform cannot be sampled (no
// retry loop).
func (m *Module) run(ctx context.Context) {
	defer m.wg.Done()
	if m.tick(ctx) {
		return
	}
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if m.tick(ctx) {
				return
			}
		}
	}
}

// tick takes one sample and publishes the result. It reports true when the
// loop should end: the platform is unsupported, or ctx was cancelled.
func (m *Module) tick(ctx context.Context) (stop bool) {
	began := time.Now()
	sctx, cancel := context.WithTimeout(ctx, sampleBudget)
	defer cancel()

	// The roots are read before the sampler (whose ps fork is the process
	// read) and again after it; see sessions.
	before, beforeErr := m.readRoots(sctx)
	raw, procs, err := m.sampler.Sample(sctx)
	if ctx.Err() != nil {
		return true // stopping: whatever the sampler said, it is not a failure
	}
	if errors.Is(err, resources.ErrUnsupported) {
		m.publish(m.unavailable(resources.ReasonUnsupportedPlatform))
		m.logf("[resources] sampling is not supported on this platform; /api/resources reports unavailable")
		return true
	}
	if err == nil && !raw.Usable() {
		err = errUnusableReading
	}
	if err != nil {
		m.failed(err)
		return false
	}

	sessions := m.sessions(sctx, raw, procs, before, beforeErr)
	if m.degraded {
		m.logf("[resources] sampling recovered after %d failed tick(s)", m.fails)
	}
	m.fails, m.degraded = 0, false
	host := resources.ComputeHost(raw)
	host.Full = m.fullLatch.Update(host)
	m.publish(&resources.Snapshot{
		SampledAt: m.now(),
		Available: true,
		Capacity:  resources.Capacity,
		Host:      host,
		Sessions:  sessions,
		Mode:      resources.ModeMeasure,
		SampleMS:  time.Since(began).Milliseconds(),
	})
	return false
}

// readRoots takes one process snapshot (start times, no argv, no fork) and
// lists the sessions whose processes it vouches for. The error text names
// which step failed.
func (m *Module) readRoots(ctx context.Context) ([]resources.Root, error) {
	if m.roots == nil {
		return nil, nil
	}
	snap, err := m.procSnapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("process table: %w", err)
	}
	roots, err := m.roots.ProcessRoots(snap)
	if err != nil {
		return nil, fmt.Errorf("session roots: %w", err)
	}
	return roots, nil
}

// sessions attributes the process list to the registry's sessions. The
// process list is the sampler's ps, taken between two reads of the roots, and
// ps carries no start times, so a root is only trusted when both reads
// vouch for the same process (same session, pid and start): a pid that was
// reused while ps ran would otherwise be charged with the old process's
// numbers (codex R1 + attack on P0-2). A session whose root changed in
// between is simply left out of this tick.
//
// Any trouble reading the roots leaves the list empty, never fails the tick:
// the host figures stand on their own. A standing problem is logged once.
func (m *Module) sessions(ctx context.Context, raw resources.HostRaw, procs []resources.Proc, before []resources.Root, beforeErr error) []resources.SessionUse {
	if m.roots == nil {
		return []resources.SessionUse{}
	}
	if beforeErr != nil {
		m.noteRoots(beforeErr.Error())
		return []resources.SessionUse{}
	}
	after, err := m.readRoots(ctx)
	if err != nil {
		m.noteRoots(err.Error())
		return []resources.SessionUse{}
	}
	m.noteRoots("")
	return resources.Attribute(procs, sameRoots(before, after), raw.NCPU, raw.MemBytes)
}

// sameRoots keeps the roots both lists agree on, in the order of after.
func sameRoots(before, after []resources.Root) []resources.Root {
	type identity struct {
		session, start string
		pid            int
	}
	seen := make(map[identity]bool, len(before))
	for _, r := range before {
		seen[identity{r.SessionID, r.ProcStart, r.PID}] = true
	}
	out := make([]resources.Root, 0, len(after))
	for _, r := range after {
		if seen[identity{r.SessionID, r.ProcStart, r.PID}] {
			out = append(out, r)
		}
	}
	return out
}

// noteRoots logs a roots problem when it changes, and the return to normal.
func (m *Module) noteRoots(problem string) {
	if problem == m.rootsNote {
		return
	}
	if problem == "" {
		m.logf("[resources] session attribution recovered")
	} else {
		m.logf("[resources] no sessions attributed: %s", problem)
	}
	m.rootsNote = problem
}

// failed records one failed tick. The first failure of a run is logged, the
// recovery is logged by the tick that ends it, and only the third in a row
// changes what /api/resources says: the last good reading goes stale and is
// flagged sample_failed.
func (m *Module) failed(err error) {
	m.fails++
	if !m.degraded {
		m.degraded = true
		m.logf("[resources] sampling failing: %v", err)
	}
	if m.fails < failuresBeforeUnavailable {
		return
	}
	m.publish(m.unavailable(resources.ReasonSampleFailed))
}

// unavailable is the snapshot for a host that cannot be read: the last good
// reading (if any, even one already flagged) with Available = false and the reason, so the figures stay
// visible but flagged, and sampled_at still names when they were taken.
func (m *Module) unavailable(reason string) *resources.Snapshot {
	s := resources.Snapshot{
		SampledAt: m.now(),
		Capacity:  resources.Capacity,
		Sessions:  []resources.SessionUse{},
		Mode:      resources.ModeMeasure,
	}
	if prev := m.latest.Load(); prev != nil {
		s = *prev // the last good figures, sessions and sampled_at
	}
	s.Available = false
	s.Reason = reason
	return &s
}

func (m *Module) publish(s *resources.Snapshot) { m.latest.Store(s) }
