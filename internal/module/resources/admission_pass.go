package resourcesmod

import (
	"context"
	"encoding/json"
	"strings"
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
		if m.shadowActive() {
			m.dropShadow()
		}
		return res
	}
	if set.Mode != resources.ModeAdvise && m.shadowActive() {
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
	// The advise shadow's verdicts are written here, with the lock released.
	if m.shadowUnwritten.Load() > 0 {
		m.flushShadow()
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
	held, waiting, positions, ok := m.readPassRows()
	// The mode just left lease and the sweeper has not drained yet: the positions
	// end here, before this pass could grant their rows as plain ones.
	// The mode was read before the lock; it is read again under it, so a switch back
	// to lease in between does not get its queue drained by this pass.
	if ok && set.Mode == resources.ModeAdvise && len(positions) > 0 {
		if cur, read := m.settingsRead(); read && cur.Mode == resources.ModeAdvise && m.drainPositions(m.now()) > 0 {
			held, waiting, positions, ok = m.readPassRows()
		}
	}
	// In advise the shadow queue has work even when nothing waits for real.
	if !ok || (len(waiting) == 0 && set.Mode != resources.ModeAdvise) {
		return 0, false
	}
	if m.passHook != nil && len(waiting) > 0 {
		m.passHook()
	}
	now := m.now()
	nowMS := now.UnixMilli()
	// A row that ended some way no path reported must not keep a reasons entry.
	all := make(map[string]bool, len(waiting)+len(positions))
	for _, w := range waiting {
		all[w.ID] = true
	}
	for _, p := range positions {
		all[posKey(p.ID)] = true
	}
	m.reasons.retain(all)
	// A waiter nobody polls any more (its lease ran out) is the sweeper's to
	// end as abandoned; granting it would hold capacity for a client that left.
	// A queue-mode row of a position is nobody's to poll (R1): it is live.
	live := waiting[:0:0]
	for _, w := range waiting {
		if w.LeaseUntil > nowMS || isQueueRow(w) {
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
	// Reserved counts as running (amendment A §2.5): a position that is offered,
	// running or idle holds the host for its session even between two commands.
	running := len(held) > 0
	posByID := make(map[string]*positionRow, len(positions))
	rowsByPos := map[string][]leaseRow{}
	for i := range positions {
		posByID[positions[i].ID] = &positions[i]
		if positions[i].State != resources.PosQueued {
			running = true
		}
	}
	for _, w := range waiting {
		if w.PositionID != "" {
			rowsByPos[w.PositionID] = append(rowsByPos[w.PositionID], w)
		}
	}
	lastGrant := m.lastGrantTime(held)
	gapOf := func() time.Duration {
		if lastGrant.IsZero() {
			return 0
		}
		return max(lastGrant.Add(set.Gap()).Sub(now), 0)
	}
	gapLeft := gapOf()

	leases := make([]resources.Lease, len(held))
	for i, r := range held {
		leases[i] = resources.Lease{ID: r.ID, Weight: r.Weight, GrantedAt: time.UnixMilli(r.GrantedAt), Measured: r.EWMA, Samples: r.Samples}
	}
	host := m.admitHost()
	load := resources.Load{Load1: host.Load1, NCPU: host.NCPU} // the sample the zone was judged on
	use := m.leaseUseSnapshot()

	// grant writes one grant: a plain row (posID ""), or the start of a command
	// of a position (posID, from: the state it was in).
	type outcome int
	const (
		grantWon outcome = iota
		grantLost
		grantFailed
	)
	grant := func(w leaseRow, path, posID, from string) outcome {
		rec := resources.RecordOf(host, leases, use, w.Weight, now, set)
		d := resources.Decision{
			Path: path, WaitedMS: max(0, nowMS-w.CreatedAt),
			Load1: host.Load1, NCPU: host.NCPU, Mem: host.Mem, Measured: host.Measured, Full: host.Full,
			Weight: w.Weight, SumCharge: rec.SumCharge, Unleased: rec.Unleased, WouldWaitR2: rec.WouldWaitR2,
		}
		v2 := resources.V2Decision(zone, trend, running, gapLeft)
		// Advise: would v2 have made this request wait? (not granted by the
		// shadow in the pass of its own request)
		wouldWait := set.Mode == resources.ModeAdvise && !m.shadow.atOnce[w.ID]
		var won bool
		var err error
		if posID == "" {
			won, err = m.store.GrantDecided(w.ID, nowMS, wouldWait, d, v2, m.reasons.peek(w.ID, now), baselines[w.ID])
		} else {
			won, err = m.store.GrantInPosition(posID, from, w.ID, nowMS, wouldWait, d, v2, m.reasons.peek(w.ID, now), baselines[w.ID])
		}
		if err != nil {
			m.logf("[resources] admission: grant %s: %v", w.ID, err)
			return grantFailed
		}
		if !won {
			return grantLost // someone else moved the row after the read: the plan is stale
		}
		m.reasons.drop(w.ID)
		delete(m.shadow.atOnce, w.ID)
		if path != resources.PathOffer { // the offer already started the gap
			m.lastGrantMS = max(m.lastGrantMS, nowMS) // only forward (a grant needs now >= lastGrant + gap, so this is a guard)
			m.lastGrantAtMS.Store(m.lastGrantMS)
			lastGrant = now
		}
		leases = append(leases, resources.Lease{ID: w.ID, Weight: w.Weight, GrantedAt: now})
		running = true
		granted++
		m.recordGrantWarning(w.ID, w.Kind)
		m.logf("[resources] lease %s (%s, weight %d) granted: %s, waited %d ms", w.ID, w.Kind, w.Weight, d.Path, d.WaitedMS)
		return grantWon
	}
	// finish ends the pass the way the original did: a lost compare-and-set asks
	// for another round, a failed write stops.
	finish := func(o outcome) (int, bool, bool) {
		switch o {
		case grantLost:
			return granted, true, true
		case grantFailed:
			return granted, false, true
		}
		return 0, false, false
	}

	if set.Mode == resources.ModeAdvise {
		gapLeft = m.shadowStep(set, held, waiting, zone, trend, load, now)
		// Advise grants everything at once, up to a cut waiter (FIFO is kept).
		for _, w := range waiting {
			if w.PositionID != "" {
				continue // a position's row is never granted as a plain one (the drain ends it)
			}
			if cut[w.ID] {
				break
			}
			path := resources.PathWaited
			switch {
			case zone == resources.ZoneUnknown:
				path = resources.PathUnknownZone
			case w.ID == fresh:
				path = resources.PathImmediate
			}
			if g, a, stop := finish(grant(w, path, "", "")); stop {
				return g, a
			}
		}
	} else {
		// startedNow are the positions whose command started in this pass: the row
		// that started it is not a second command.
		startedNow := map[string]bool{}
		// Reservation grants first: an offered or idle position starts its
		// session's command at once, without a table or gap check. The offer was
		// the decision (A §3.1).
		for _, p := range positions {
			if p.State != resources.PosOffered && p.State != resources.PosIdle {
				continue
			}
			w, found := grantableAt(rowsByPos[p.ID], fresh, cut)
			if !found {
				continue
			}
			path := resources.PathOffer
			if p.State == resources.PosIdle {
				path = resources.PathContinued
			}
			if g, a, stop := finish(grant(w, path, p.ID, p.State)); stop {
				return g, a
			}
			posByID[p.ID].State = resources.PosRunning
			startedNow[p.ID] = true
		}
		// A second command of a running position (coordinator Q6): the table
		// decides, whether or not someone is queued; when someone waits, the
		// position's own limit applies too. It uses the pass's one table
		// admission (at most one grant per pass).
		tableUsed := false
		for _, p := range positions {
			if posByID[p.ID].State != resources.PosRunning || startedNow[p.ID] {
				continue
			}
			w, found := freshAt(rowsByPos[p.ID], fresh, cut)
			if !found {
				continue
			}
			if m.someoneWaitsBesides(positions, waiting, p.ID) && (p.Warned || p.CommandsStarted >= resources.PositionMaxCommands) {
				continue
			}
			vw := []resources.V2Waiter{{ID: w.ID, CreatedAt: time.UnixMilli(w.CreatedAt)}}
			if id, _, _ := resources.AdmitV2(zone, trend, true, load, lastGrant, vw, now, set); id == "" {
				continue
			}
			if g, a, stop := finish(grant(w, resources.PathImmediate, p.ID, resources.PosRunning)); stop {
				return g, a
			}
			tableUsed = true
		}
		// The queue: queued positions and wait-mode rows, strictly by created_at.
		entries := make([]resources.V2Waiter, 0, len(waiting))
		// A position whose offer passed (not delivered in time) waits for its
		// session to be reachable, and for the next waiter to have had its turn.
		decidedAt := max(m.lastGrantMS, m.lastOfferMS)
		othersWaiting := false
		for _, p := range positions {
			if p.State == resources.PosQueued && p.PassedAt == 0 && len(rowsByPos[p.ID]) > 0 {
				othersWaiting = true
			}
		}
		for _, w := range waiting {
			if w.PositionID == "" {
				othersWaiting = true
			}
		}
		for _, p := range positions {
			if p.State != resources.PosQueued || len(rowsByPos[p.ID]) == 0 {
				continue
			}
			if p.PassedAt > 0 && !(m.canDeliver(p.SessionID) && (decidedAt > p.PassedAt || !othersWaiting)) {
				continue
			}
			c := false
			for _, w := range rowsByPos[p.ID] {
				c = c || cut[w.ID]
			}
			entries = append(entries, resources.V2Waiter{ID: posKey(p.ID), CreatedAt: time.UnixMilli(p.CreatedAt), Cut: c})
		}
		byID := map[string]leaseRow{}
		for _, w := range waiting {
			if w.PositionID != "" {
				continue
			}
			entries = append(entries, resources.V2Waiter{ID: w.ID, CreatedAt: time.UnixMilli(w.CreatedAt), Cut: cut[w.ID]})
			byID[w.ID] = w
		}
		var grantID string
		var reasons map[string]resources.V2Reason
		grantID, gapLeft, reasons = resources.AdmitV2(zone, trend, running, load, lastGrant, entries, now, set)
		for id, r := range reasons {
			m.reasons.observe(id, r, now)
		}
		if tableUsed {
			grantID = "" // a second command took this pass's admission; the head waits for the next
		}
		switch {
		case grantID == "":
		case isPosKey(grantID):
			p := posByID[strings.TrimPrefix(grantID, "pos:")]
			v2 := resources.V2Decision(zone, trend, running, gapLeft)
			won, err := m.store.OfferPosition(p.ID, nowMS, v2.Zone, v2.Trend, m.reasons.peek(grantID, now))
			if err != nil {
				m.logf("[resources] admission: offer %s: %v", p.ID, err)
				return granted, false
			}
			if !won {
				return granted, true
			}
			m.reasons.drop(grantID)
			m.lastOfferMS = max(m.lastOfferMS, nowMS)
			lastGrant = now
			p.State = resources.PosOffered
			m.logf("[resources] position %s offered", p.ID)
			m.wake()
			// The free host: the offer is made in the pass of the request that
			// asked, so it runs at once (V13).
			if w, found := freshAt(rowsByPos[p.ID], fresh, cut); found {
				path := resources.PathImmediate
				if zone == resources.ZoneUnknown {
					path = resources.PathUnknownZone
				}
				if g, a, stop := finish(grant(w, path, p.ID, resources.PosOffered)); stop {
					return g, a
				}
				p.State = resources.PosRunning
			}
		default:
			w := byID[grantID]
			path := resources.PathWaited
			switch {
			case zone == resources.ZoneUnknown:
				path = resources.PathUnknownZone
			case w.ID == fresh:
				path = resources.PathImmediate
			}
			if g, a, stop := finish(grant(w, path, "", "")); stop {
				return g, a
			}
		}
		// Naming (R7): no row waits at an offered or idle position unasked.
		for _, p := range positions {
			kind := ""
			switch posByID[p.ID].State {
			case resources.PosOffered:
				kind = "offer"
			case resources.PosIdle:
				kind = "idle"
			default:
				continue
			}
			if _, err := m.store.NameRows(p.ID, nowMS, kind); err != nil {
				m.logf("[resources] admission: name rows of %s: %v", p.ID, err)
			}
		}
	}
	if granted > 0 {
		m.wake()
	}
	return granted, again
}

// posKey is the reasons / queue key of a position.
func posKey(id string) string { return "pos:" + id }

func isPosKey(k string) bool { return strings.HasPrefix(k, "pos:") }

// isQueueRow says whether the row is a queue-mode request of a position.
func isQueueRow(w leaseRow) bool { return w.Mode == resources.CallQueue && w.PositionID != "" }

// grantableAt is the oldest waiting row of a position that the reservation may
// start now: the pass's own request (of either mode), or an ask row that was
// confirmed. A cut row is not started.
func grantableAt(rows []leaseRow, fresh string, cut map[string]bool) (leaseRow, bool) {
	for _, w := range rows {
		if cut[w.ID] {
			continue
		}
		if w.ID == fresh || (w.Mode == resources.CallAsk && w.ConfirmedAt > 0) {
			return w, true
		}
	}
	return leaseRow{}, false
}

// freshAt is the pass's own request, if it is a row of the position and not cut.
func freshAt(rows []leaseRow, fresh string, cut map[string]bool) (leaseRow, bool) {
	for _, w := range rows {
		if w.ID == fresh && !cut[w.ID] {
			return w, true
		}
	}
	return leaseRow{}, false
}

// someoneWaitsBesides says whether anyone other than the position waits: another
// queued position, or a wait-mode row (Q5).
func (m *Module) someoneWaitsBesides(positions []positionRow, waiting []leaseRow, positionID string) bool {
	for _, p := range positions {
		if p.ID != positionID && p.State == resources.PosQueued {
			return true
		}
	}
	for _, w := range waiting {
		if w.PositionID == "" {
			return true
		}
	}
	return false
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
	last := max(m.lastGrantMS, m.lastOfferMS)
	for _, r := range held {
		if r.DecPath == resources.PathOffer {
			continue
		}
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

// readPassRows reads the held rows, the waiting rows and the live positions;
// ok is false when a read failed (logged).
func (m *Module) readPassRows() (held, waiting []leaseRow, positions []positionRow, ok bool) {
	var err error
	if held, err = m.store.Active(); err != nil {
		m.logf("[resources] admission: list held leases: %v", err)
		return nil, nil, nil, false
	}
	if waiting, err = m.store.Waiting(); err != nil {
		m.logf("[resources] admission: list waiting leases: %v", err)
		return nil, nil, nil, false
	}
	if positions, err = m.store.LivePositions(); err != nil {
		m.logf("[resources] admission: list positions: %v", err)
		return nil, nil, nil, false
	}
	return held, waiting, positions, true
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

// mainTurnReader is what the agent module answers about a session's main turn.
type mainTurnReader interface {
	MainTurnRunning(sessionID string) (running, ok bool)
}

// canDeliver says whether the session can take a notice now: its main turn is not running. The agent module is looked up
// at use time, before stateMu is taken (the pass reads it first); a session it knows nothing about, or no agent module,
// reads as deliverable, and the outbox then finds out by trying.
func (m *Module) canDeliver(sessionID string) bool {
	if m.deliverable != nil {
		return m.deliverable(sessionID)
	}
	if m.core == nil || m.core.Registry == nil {
		return true
	}
	svc, ok := m.core.Registry.Get("agent.module")
	if !ok {
		return true
	}
	r, ok := svc.(mainTurnReader)
	if !ok {
		return true
	}
	running, known := r.MainTurnRunning(sessionID)
	return !known || !running
}
