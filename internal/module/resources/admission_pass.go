package resourcesmod

import (
	"context"
	"encoding/json"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// The admission pass (spec D-2/D-3, plan Task 1.5, P1-D1): it decides which
// waiting requests are granted now and writes the grants. It has no HTTP in
// it; the routes (D-2) and the loops call it.
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
// pure Admit, and one single-row UPDATE per grant. The process table is never
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
		return res
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
func (m *Module) passOnce(set resources.Settings, fresh string, baselines map[string]string) (granted int, again bool) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	held, waiting, ok := m.readPassRows()
	if !ok || len(waiting) == 0 {
		return 0, false
	}
	if m.passHook != nil {
		m.passHook()
	}
	now := m.now()
	// A waiter nobody polls any more (its lease ran out) is the sweeper's to
	// end as abandoned; granting it would hold capacity for a client that left.
	live := waiting[:0:0]
	for _, w := range waiting {
		if w.LeaseUntil > now.UnixMilli() {
			live = append(live, w)
		}
	}
	waiting = live
	// A session-new waiter that arrived after the baselines were worked out has
	// none yet. It is not granted without one, and nobody behind it is
	// planned either (they would overtake it, which FIFO does not allow while
	// it may fit): the queue is cut there and another round follows.
	ready := waiting
	for i, w := range waiting {
		if w.Scope == resources.ScopeSessionNew && !m.baselineReady(w, baselines) {
			ready, again = waiting[:i], true
			break
		}
	}
	for _, pg := range m.plan(set, held, ready, fresh, now) {
		w := pg.row
		won, err := m.store.GrantDecided(w.ID, now.UnixMilli(), pg.wouldWait, pg.grant, baselines[w.ID])
		if err != nil {
			m.logf("[resources] admission: grant %s: %v", w.ID, err)
			return granted, false
		}
		if !won {
			// Someone else moved the row after the read: the plan is stale.
			return granted, true
		}
		granted++
		m.logf("[resources] lease %s (%s, weight %d) granted: %s, waited %d ms", w.ID, w.Kind, w.Weight,
			pg.grant.Path, pg.grant.WaitedMS)
	}
	if granted > 0 {
		m.wake()
	}
	return granted, again
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

// plannedGrant is one row the pass will grant.
type plannedGrant struct {
	row       leaseRow
	grant     resources.Grant
	wouldWait bool
}

// plan runs Admit on the rows. In mode lease its grants are the plan. In mode
// advise every waiter is granted at once, and wouldWait says whether mode
// lease would have kept it back (spec: advise computes and records, never
// holds).
func (m *Module) plan(set resources.Settings, held, waiting []leaseRow, fresh string, now time.Time) []plannedGrant {
	host := m.admitHost()
	leases := make([]resources.Lease, len(held))
	for i, r := range held {
		leases[i] = resources.Lease{ID: r.ID, Weight: r.Weight, GrantedAt: time.UnixMilli(r.GrantedAt), Measured: r.EWMA, Samples: r.Samples}
	}
	byID := make(map[string]leaseRow, len(waiting))
	waiters := make([]resources.Waiter, len(waiting))
	for i, w := range waiting {
		byID[w.ID] = w
		waiters[i] = resources.Waiter{ID: w.ID, Weight: w.Weight, EnqueuedAt: time.UnixMilli(w.CreatedAt),
			Deadline: time.UnixMilli(w.DeadlineAt), Fresh: w.ID == fresh}
	}
	use := m.leaseUseSnapshot()
	var out []plannedGrant
	if set.Mode != resources.ModeAdvise {
		for _, g := range resources.Admit(host, leases, use, waiters, now, set) {
			out = append(out, plannedGrant{row: byID[g.ID], grant: g})
		}
		return out
	}
	// Advise: what lease mode would have done is worked out on its own, from
	// the real deadlines, and says only whether each request would have been
	// held back. Every request is then granted at once, and the decision
	// recorded is the one the pass really makes: a second Admit in which every
	// deadline has passed, so each grant counts in the sum of the next, as it
	// does on the host. A request that lease mode would have queued therefore
	// does not take capacity from the counterfactual of a later one.
	lease := map[string]resources.Grant{}
	for _, g := range resources.Admit(host, leases, use, waiters, now, set) {
		lease[g.ID] = g
	}
	for i := range waiters {
		waiters[i].Deadline = now
	}
	for _, g := range resources.Admit(host, leases, use, waiters, now, set) {
		pg := plannedGrant{row: byID[g.ID], grant: g}
		l, fits := lease[g.ID]
		pg.wouldWait = !fits || l.Overrun
		pg.grant.Overrun = false
		if pg.grant.Path == resources.PathOverrun {
			pg.grant.Path = resources.PathWaited
			if g.ID == fresh {
				pg.grant.Path = resources.PathImmediate
			}
		}
		out = append(out, pg)
	}
	return out
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
