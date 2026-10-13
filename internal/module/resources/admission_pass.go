package resourcesmod

import (
	"context"
	"encoding/json"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// The admission pass (lease v2 spec D-3′): it decides which waiting request is
// granted now and writes the grant. It has no HTTP in it; the routes and the
// loops call it.
//
// Who calls it: the sampler after every good tick; the sweeper after every
// tick while a request waits (so a deadline is honoured within a second with
// no sample and no change); and, later, every route that creates, releases or
// cancels a lease.
//
// Locking. stateMu is held from the read of the rows to the last grant, so no
// transition lands between the rows a decision was made on and the writes
// that follow. What it does under the lock is what the rule allows: two reads
// that use the `state` index (held rows and waiting rows, a handful each), the
// pure AdmitV2, and one single-row UPDATE per grant. The process table is never
// read under it: a session-new lease's baseline is worked out before the lock
// from the sampler's last process list and written by the grant's own UPDATE.

// passResult counts what a pass did, for tests.
type passResult struct {
	granted int
}

// admissionPass runs one pass. fresh is the id of the request the pass was
// started for ("" for a tick): its grant is recorded as `immediate`.
func (m *Module) admissionPass(ctx context.Context, fresh string) passResult {
	var res passResult
	if m.store == nil || m.skipPass {
		return res
	}
	set := m.settings()
	if set.Mode != resources.ModeLease && set.Mode != resources.ModeAdvise {
		if m.shadowLen.Load() > 0 {
			m.dropShadow()
		}
		return res
	}
	if set.Mode != resources.ModeAdvise && m.shadowLen.Load() > 0 {
		m.dropShadow() // switched away from advise: the counterfactual is over
	}
	// A heavy lease waits for the disk to be looked at (and the Go build cache trimmed when it runs low) before the locked
	// part of the pass: the walk is never done under stateMu (diskguard.go).
	m.diskPreflight(ctx, fresh != "")
	// One more round when a grant could not be made as planned: its CAS lost to
	// another writer, or a session-new waiter that arrived after the baselines
	// were worked out. Two rounds at most; the next trigger finishes the rest.
	for round := 0; round < 2; round++ {
		baselines := m.baselinesForWaiting(ctx)
		if m.beforeLockHook != nil {
			m.beforeLockHook()
		}
		n, again := m.passOnce(set, fresh, baselines)
		res.granted += n
		if !again {
			break
		}
	}
	return res
}

// passOnce is the locked part of a pass; again asks for another round.
//
// Mode lease: AdmitV2 decides (lease v2 spec D-3′): strictly the head of the
// queue, at most one grant, the gap after the last grant, the zone and trend
// table. Every waiter's reason is recorded for wait_reasons. Mode advise grants
// every waiter at once, as before; its counterfactual is the shadow queue (P1c).
func (m *Module) passOnce(set resources.Settings, fresh string, baselines map[string]string) (granted int, again bool) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	held, waiting, ok := m.readPassRows()
	// In advise the shadow queue has work even when nothing waits for real.
	if !ok || (len(waiting) == 0 && set.Mode != resources.ModeAdvise) {
		return 0, false
	}
	if m.passHook != nil && len(waiting) > 0 {
		m.passHook()
	}
	now := m.now()
	// A row that ended some way no path reported must not keep a reasons entry.
	all := make(map[string]bool, len(waiting))
	for _, w := range waiting {
		all[w.ID] = true
	}
	m.reasons.retain(all)
	// A waiter nobody polls any more (its lease ran out) is the sweeper's to
	// end as abandoned; granting it would hold capacity for a client that left.
	live := waiting[:0:0]
	for _, w := range waiting {
		if w.LeaseUntil > now.UnixMilli() {
			live = append(live, w)
		}
	}
	waiting = live

	// Two holds on a waiter that are not about the host: a session-new waiter
	// that arrived after the baselines were worked out has none yet (it is not
	// granted without one, and another round follows), and while a trim runs the
	// disk-guarded kinds wait for it (diskguard.go). The waiter is cut, which
	// stops the walk when it is the head.
	cut := make(map[string]bool)
	for _, w := range waiting {
		if w.Scope == resources.ScopeSessionNew && !m.baselineReady(w, baselines) {
			cut[w.ID], again = true, true
		}
		if m.diskHolding() && diskGuardedKinds[w.Kind] {
			cut[w.ID] = true
		}
	}

	zone, trend := m.admitZoneTrend()
	running := len(held) > 0
	lastGrant := m.lastGrantTime(held)
	var toGrant []leaseRow
	var gapLeft time.Duration
	var shadowGranted map[string]bool
	if set.Mode == resources.ModeAdvise {
		shadowGranted = m.shadowStep(set, held, waiting, zone, trend, now)
		// Advise grants everything at once, up to a cut waiter (FIFO is kept).
		for _, w := range waiting {
			if cut[w.ID] {
				break
			}
			toGrant = append(toGrant, w)
		}
	} else {
		vw := make([]resources.V2Waiter, len(waiting))
		byID := make(map[string]leaseRow, len(waiting))
		for i, w := range waiting {
			vw[i] = resources.V2Waiter{ID: w.ID, CreatedAt: time.UnixMilli(w.CreatedAt), Cut: cut[w.ID]}
			byID[w.ID] = w
		}
		var grantID string
		var reasons map[string]resources.V2Reason
		grantID, gapLeft, reasons = resources.AdmitV2(zone, trend, running, lastGrant, vw, now, set)
		for id, r := range reasons {
			m.reasons.observe(id, r, now)
		}
		if grantID != "" {
			toGrant = append(toGrant, byID[grantID])
		}
	}

	leases := make([]resources.Lease, len(held))
	for i, r := range held {
		leases[i] = resources.Lease{ID: r.ID, Weight: r.Weight, GrantedAt: time.UnixMilli(r.GrantedAt), Measured: r.EWMA, Samples: r.Samples}
	}
	host := m.admitHost()
	use := m.leaseUseSnapshot()
	for _, w := range toGrant {
		rec := resources.RecordOf(host, leases, use, w.Weight, now, set)
		path := resources.PathWaited
		switch {
		case zone == resources.ZoneUnknown:
			path = resources.PathUnknownZone
		case w.ID == fresh:
			path = resources.PathImmediate
		}
		d := resources.Decision{
			Path: path, WaitedMS: max(0, now.UnixMilli()-w.CreatedAt),
			Load1: host.Load1, NCPU: host.NCPU, Mem: host.Mem, Measured: host.Measured, Full: host.Full,
			Weight: w.Weight, SumCharge: rec.SumCharge, Unleased: rec.Unleased, WouldWaitR2: rec.WouldWaitR2,
		}
		v2 := resources.V2Decision(zone, trend, running, gapLeft)
		// Advise: would v2 have made this request wait? (not granted by the
		// shadow in the pass of its own request)
		wouldWait := set.Mode == resources.ModeAdvise && !shadowGranted[w.ID]
		won, err := m.store.GrantDecided(w.ID, now.UnixMilli(), wouldWait, d, v2, m.reasons.peek(w.ID, now), baselines[w.ID])
		if err != nil {
			m.logf("[resources] admission: grant %s: %v", w.ID, err)
			return granted, false
		}
		if !won {
			// Someone else moved the row after the read: the plan is stale.
			return granted, true
		}
		m.reasons.drop(w.ID)
		m.lastGrantMS = max(m.lastGrantMS, now.UnixMilli()) // only forward (a grant needs now >= lastGrant + gap, so this is a guard)
		m.lastGrantAtMS.Store(m.lastGrantMS)
		leases = append(leases, resources.Lease{ID: w.ID, Weight: w.Weight, GrantedAt: now})
		running = true
		granted++
		m.recordGrantWarning(w.ID, w.Kind)
		m.logf("[resources] lease %s (%s, weight %d) granted: %s, waited %d ms", w.ID, w.Kind, w.Weight, d.Path, d.WaitedMS)
	}
	if granted > 0 {
		m.wake()
	}
	return granted, again
}

// admitZoneTrend is the zone and trend of the latest sample; an unavailable or
// missing sample reads as unknown, which admission fails open on (D-5).
func (m *Module) admitZoneTrend() (resources.Zone, resources.Trend) {
	if s := m.latest.Load(); s != nil && s.Available {
		return resources.ParseZone(s.Host.Zone), resources.ParseTrend(s.Host.Trend)
	}
	return resources.ZoneUnknown, resources.TrendUnknown
}

// lastGrantTime is the newest granted_at of any row, held or ended: the larger
// of the one kept in memory (set at every grant, read from the table at boot)
// and the held rows' own. Zero when nothing was ever granted. stateMu is held.
func (m *Module) lastGrantTime(held []leaseRow) time.Time {
	last := m.lastGrantMS
	for _, r := range held {
		last = max(last, r.GrantedAt)
	}
	if last == 0 {
		return time.Time{}
	}
	return time.UnixMilli(last)
}

// baselineReady says whether a session-new waiter can be granted: its
// baseline was worked out. An entry may be "" (unknown: the process table
// could not be read; spec D-5 fails open, so the lease is granted and charged
// its whole tree), "[]" (a session with nothing under it) or a list.
func (m *Module) baselineReady(w leaseRow, baselines map[string]string) bool {
	_, ok := baselines[w.ID]
	return ok
}

// readPassRows reads the held and the waiting rows; ok is false when either
// read failed (logged).
func (m *Module) readPassRows() (held, waiting []leaseRow, ok bool) {
	var err error
	if held, err = m.store.Active(); err != nil {
		m.logf("[resources] admission: list held leases: %v", err)
		return nil, nil, false
	}
	if waiting, err = m.store.Waiting(); err != nil {
		m.logf("[resources] admission: list waiting leases: %v", err)
		return nil, nil, false
	}
	return held, waiting, true
}

// admitHost is the host figure the pass decides on: the latest sample, or an
// all-zero reading when there is none or it is unavailable (not full, nothing
// measured: leases then queue against each other and deadlines still pass).
func (m *Module) admitHost() resources.HostUse {
	if s := m.latest.Load(); s != nil && s.Available {
		return s.Host
	}
	return resources.HostUse{}
}

// baselinesForWaiting works out, outside the lock, the baseline of every
// session-new waiter (all of them, not a prediction of the ones to be granted:
// a prediction that differs from the locked plan would cut the queue at a
// waiter that has none). The process table is read only when there is one.
func (m *Module) baselinesForWaiting(ctx context.Context) map[string]string {
	waiting, err := m.store.Waiting()
	if err != nil {
		m.logf("[resources] admission: list waiting leases: %v", err)
		return nil
	}
	var want []leaseRow
	for _, w := range waiting {
		if w.Scope == resources.ScopeSessionNew {
			want = append(want, w)
		}
	}
	if len(want) == 0 {
		return map[string]string{}
	}
	return m.captureBaselines(ctx, want)
}

// captureBaselines is the baseline of each row: the processes under its agent
// pid at the sampler's last reading, each with its start time (read without a
// fork from the process table). An entry whose start cannot be read is left
// out, so that it is charged rather than trusted. The list is up to one
// sampling interval old: a child started in that last interval is not in it
// and is charged to the lease (a known, conservative error, spec D-8).
func (m *Module) captureBaselines(ctx context.Context, rows []leaseRow) map[string]string {
	out := make(map[string]string, len(rows))
	// One load: the list tested for "there is a sample" is the list used.
	last := m.lastProcs.Load()
	view, err := m.readView(ctx)
	if err != nil || last == nil {
		// Unknown, not empty: "" is stored as NULL and measured as the whole
		// tree (spec D-5: fail open, on the high side).
		if !m.baselineFailing.Swap(true) {
			m.logf("[resources] admission: no baseline for session-new leases (process table or sample unavailable): %v", err)
		}
		for _, r := range rows {
			out[r.ID] = ""
		}
		return out
	}
	m.baselineFailing.Store(false)
	procs := *last
	children := make(map[int][]int, len(procs))
	for _, p := range procs {
		children[p.PPID] = append(children[p.PPID], p.PID)
	}
	for _, r := range rows {
		entries := []resources.BaselineEntry{}
		seen := map[int]bool{r.HolderPID: true}
		queue := []int{r.HolderPID}
		for len(queue) > 0 {
			pid := queue[0]
			queue = queue[1:]
			for _, c := range children[pid] {
				if seen[c] {
					continue
				}
				seen[c] = true
				queue = append(queue, c)
				if st, err := view.Start(c); err == nil && !st.IsZero() {
					entries = append(entries, resources.BaselineEntry{PID: c, StartMS: st.UnixMilli()})
				}
			}
		}
		b, _ := json.Marshal(entries)
		out[r.ID] = string(b)
	}
	return out
}
