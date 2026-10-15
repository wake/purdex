package resourcesmod

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wake/purdex/internal/hostmem"
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
	// The setting is re-read every tick (nothing to subscribe to). Mode off
	// skips the reads but keeps the ticker, so switching back on needs no
	// restart.
	set := m.settings()
	m.mode = set.Mode
	m.lastSettings.Store(&set) // for GET /api/resources, which reads no settings itself
	// The load watcher is independent of the lease mode: with the mode off the tick still samples while the watch setting is on,
	// and publishes "off" all the same (nothing else of the module's work runs).
	wset, watchOn := m.watchSettings()
	if m.mode == resources.ModeOff && !watchOn {
		return m.goOff(ctx)
	}
	sctx, cancel := context.WithTimeout(ctx, sampleBudget)
	defer cancel()

	// The roots are read before the sampler (whose ps fork is the process
	// read) and again after it; see sessions.
	before, beforeErr := m.readRoots(sctx)
	sampledAt := m.now() // before the reads: the cache below never claims a reading is newer than it is
	raw, procs, err := m.sampler.Sample(sctx)
	if ctx.Err() != nil {
		return true // stopping: whatever the sampler said, it is not a failure
	}
	if errors.Is(err, resources.ErrUnsupported) {
		if m.mode == resources.ModeOff {
			m.watchNote("sampling", "sampling is not supported on this platform") // the watcher cannot run; "off" stays what is published
			return m.goOff(ctx)
		}
		m.publish(m.unavailable(resources.ReasonUnsupportedPlatform))
		m.logf("[resources] sampling is not supported on this platform; /api/resources reports unavailable")
		return true
	}
	if err == nil && !raw.Usable() {
		err = errUnusableReading
	}
	if err != nil {
		if m.mode == resources.ModeOff {
			m.watchNote("sampling", err.Error()) // no lease or publish work to fail with the mode off; the pass is skipped
			return m.goOff(ctx)
		}
		m.failed(err)
		return false
	}
	m.watchNote("sampling", "")
	m.cachePressure(raw, sampledAt)

	sessions, agreed := m.sessions(sctx, raw, procs, before, beforeErr)
	if watchOn {
		m.watchPass(ctx, raw, procs, agreed, wset)
	}
	if m.mode == resources.ModeOff {
		return m.goOff(ctx)
	}
	if m.degraded {
		m.logf("[resources] sampling recovered after %d failed tick(s)", m.fails)
	}
	m.fails, m.degraded = 0, false
	at := m.now()
	host := resources.ComputeHost(raw)
	host.Full = m.fullLatch.Update(host)
	m.notePageRate(&host, raw, at)
	m.noteZone(&host, at, set)
	m.publish(&resources.Snapshot{
		SampledAt: at,
		Available: true,
		Capacity:  resources.Capacity,
		Host:      host,
		Sessions:  sessions,
		Mode:      m.mode,
		SampleMS:  time.Since(began).Milliseconds(),
	})
	m.lastProcs.Store(&procs)
	// After the publish, on this goroutine and never on a request or hook
	// path: it reads the process table once more when a lease is held.
	held, _ := m.measureLeases(ctx, procs, raw)
	m.noteMinute(host, m.now())
	m.admissionPass(ctx, "")
	if m.store != nil && m.hasLeaseActivity(held) {
		m.requestEvent() // while something is held or waits, the state is worth a push per window
	}
	return false
}

// goOff is the tick of a module whose mode is off: no state of a run of failures, the open minute ended, "off" published.
func (m *Module) goOff(ctx context.Context) (stop bool) {
	m.fails, m.degraded = 0, false
	m.endMinute()
	m.publish(m.unavailable(resources.ReasonOff))
	return ctx.Err() != nil
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
//
// The second result is the roots both reads agree on (what the load watcher attributes its trees with); nil when there are none.
func (m *Module) sessions(ctx context.Context, raw resources.HostRaw, procs []resources.Proc, before []resources.Root, beforeErr error) ([]resources.SessionUse, []resources.Root) {
	if m.roots == nil {
		return []resources.SessionUse{}, nil
	}
	if beforeErr != nil {
		m.noteRoots(beforeErr.Error())
		return []resources.SessionUse{}, nil
	}
	after, err := m.readRoots(ctx)
	if err != nil {
		m.noteRoots(err.Error())
		return []resources.SessionUse{}, nil
	}
	m.noteRoots("")
	agreed := sameRoots(before, after)
	return resources.Attribute(procs, agreed, raw.NCPU, raw.MemBytes), agreed
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
	}
	if prev := m.latest.Load(); prev != nil {
		s = *prev // the last good figures, sessions and sampled_at
	}
	s.Available = false
	s.Reason = reason
	s.Mode = m.mode
	// A host that cannot be read has no zone; the latch starts again from
	// normal when readings come back.
	m.zoneLatch.Update(0, 0, 0, false)
	if m.lastZone != resources.ZoneUnknown || m.zoneSince.IsZero() {
		m.lastZone, m.zoneSince = resources.ZoneUnknown, m.now()
	}
	s.Host.Zone, s.Host.Trend, s.Host.ZoneSince = resources.ZoneUnknown.String(), resources.TrendUnknown.String(), m.zoneSince.UnixMilli()
	return &s
}

// pageRingKeep is how long a paging counter is kept: the rate looks back at most gate's rateWindow (10 s); a little more
// leaves the sample the window starts at.
const pageRingKeep = 12 * time.Second

// notePageRate works the page rate out of the counters the sampler read (pageouts + swapouts, vm_stat), with the arithmetic
// of the gate's admission (gate.RateOver), and takes the limit the gate holds work at from its config. A reading without
// the counters leaves the rate 0 and the ring as it was. Sampler goroutine only.
func (m *Module) notePageRate(host *resources.HostUse, raw resources.HostRaw, at time.Time) {
	m.rateLimit = m.gateCfg().Admission.PageRateLimit()
	m.haveRate = raw.PageCounters
	if !raw.PageCounters {
		return
	}
	cur := hostmem.PageSample{At: at, Total: raw.Pageouts + raw.Swapouts}
	host.PageRate = hostmem.RateOver(m.pageRing, cur)
	keep := m.pageRing[:0]
	for _, s := range m.pageRing {
		if at.Sub(s.At) <= pageRingKeep && !s.At.After(at) {
			keep = append(keep, s)
		}
	}
	m.pageRing = append(keep, cur)
}

// noteZone adds the reading to the ring and fills the host's zone, trend and
// zone start. Sampler goroutine only.
func (m *Module) noteZone(host *resources.HostUse, at time.Time, set resources.Settings) {
	m.ringMu.Lock()
	m.ring.Add(resources.TrendSample{At: at, Mem: host.Mem, CPU: host.CPU, Pressure: host.Pressure})
	window := m.ring.Since(at.Add(-set.TrendWindow()))
	m.ringMu.Unlock()
	// A host that gave neither the pressure level nor the paging counters has no zone to judge (unknown, as for a sampler that
	// fails), not a normal one; with one of the two the zone is judged by it.
	zone := m.zoneLatch.Update(host.Pressure, host.PageRate, m.rateLimit, host.Pressure > 0 || m.haveRate)
	if zone != m.lastZone || m.zoneSince.IsZero() {
		m.lastZone, m.zoneSince = zone, at
	}
	host.Zone, host.Trend = zone.String(), resources.TrendOf(window, at, set).String()
	host.ZoneSince = m.zoneSince.UnixMilli()
}

// ringCopy is the ring's samples, oldest first, as a copy.
func (m *Module) ringCopy() []resources.TrendSample {
	m.ringMu.Lock()
	defer m.ringMu.Unlock()
	return m.ring.All()
}

func (m *Module) publish(s *resources.Snapshot) { m.latest.Store(s) }
