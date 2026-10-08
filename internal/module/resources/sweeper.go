package resourcesmod

import (
	"context"
	"time"

	iagent "github.com/wake/purdex/internal/agent"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/resources"
)

// sweepInterval is the sweeper's tick, as team's sweeper (plan Task 1.5).
const sweepInterval = time.Second

// pruneEvery is how often the sweeper deletes ended rows past the retention.
const pruneEvery = time.Hour

// procView is the part of the process table the sweeper judges a holder by;
// *iagent.ProcessSnapshot satisfies it.
type procView interface {
	Alive(pid int) bool
	Start(pid int) (time.Time, error)
}

// runSweeper ticks until ctx ends. It is started after the boot reconcile and
// the sampler (spec D-4), so the rows it judges are the reconciled ones.
func (m *Module) runSweeper(ctx context.Context) {
	defer m.wg.Done()
	// The boot reconcile has just pruned.
	m.lastPrune = m.now()
	t := time.NewTicker(m.sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.sweepOnce(ctx)
			m.passIfWaiting(ctx)
		}
	}
}

// sweepOnce is one sweeper tick (spec D-4): it ends the waiting rows nobody
// polls any more, the held rows whose holder is gone and those held past
// max_hold, and prunes old ended rows once an hour. Everything runs under
// stateMu. Modes off and measure write no rows, so the rows they find are left
// for a later switch back to lease; only the retention prune still runs.
//
// Each end is a compare-and-set, so a row someone else ended or renewed since
// it was read is simply skipped: a poll that renewed the lease of a waiter
// the sweeper had read as expired wins (CloseIfExpired).
func (m *Module) sweepOnce(ctx context.Context) {
	if m.store == nil {
		return
	}
	set := m.settings()
	leasing := set.Mode != resources.ModeOff && set.Mode != resources.ModeMeasure

	// The process table is read before stateMu is taken (codex R1 + attack):
	// a read that stalls must not hold the lock every lease operation needs.
	// It is read only when a lease is held, only once the first snapshot has
	// been published (plan Task 1.5: holders are not judged before the first
	// sample), and the rows are read again under the lock. viewAt is when the
	// read began: a holder granted after it may not be in that table yet, so
	// sweepLeases judges only the rows granted strictly before it (a grant in the same millisecond cannot be ordered against the read).
	var view procView
	var viewAt time.Time
	if leasing && m.latest.Load() != nil && m.anyHeld() {
		viewAt = m.now()
		view = m.holderView(ctx)
	}

	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	now := m.now()
	if leasing {
		m.sweepLeases(now, set, view, viewAt)
	}
	if now.Sub(m.lastPrune) >= pruneEvery {
		m.lastPrune = now
		if _, err := m.store.Prune(now.Add(-retention).UnixMilli()); err != nil {
			m.logf("[resources] sweeper prune: %v", err)
		}
		if _, err := m.store.PruneMinutes(now.Add(-retention).UnixMilli()); err != nil {
			m.logf("[resources] sweeper prune minutes: %v", err)
		}
	}
}

// anyHeld says, without the lock, whether a lease is held; a hint that only
// decides whether the process table is worth reading. A failed read says
// nothing is held: the sweeper then judges no holder this tick.
func (m *Module) anyHeld() bool {
	held, err := m.store.Active()
	return err == nil && len(held) > 0
}

// sweepLeases ends what is overdue. stateMu is held. view is the process
// table read at viewAt, or nil when there is none (unknown reads as alive).
func (m *Module) sweepLeases(now time.Time, set resources.Settings, view procView, viewAt time.Time) {
	nowMS := now.UnixMilli()
	waiting, err := m.store.Waiting()
	if err != nil {
		m.logf("[resources] sweeper: list waiting leases: %v", err)
		return
	}
	for _, r := range waiting {
		if r.LeaseUntil > nowMS {
			continue
		}
		if m.sweepHook != nil {
			m.sweepHook(r)
		}
		won, err := m.store.CloseIfExpired(r.ID, nowMS)
		m.ended(r, resources.EndAbandoned, won, err)
	}

	held, err := m.store.Active()
	if err != nil {
		m.logf("[resources] sweeper: list held leases: %v", err)
		return
	}
	maxHold := set.MaxHold()
	viewAtMS := viewAt.UnixMilli()
	for _, r := range held {
		reason := ""
		switch {
		case view != nil && r.GrantedAt < viewAtMS && holderGone(view, r):
			reason = resources.EndHolderGone
		case now.Sub(time.UnixMilli(r.GrantedAt)) >= maxHold:
			reason = resources.EndExpired
		case vanished(r, now, set):
			reason = resources.EndVanished
		}
		if reason == "" {
			continue
		}
		if m.sweepHook != nil {
			m.sweepHook(r)
		}
		won, err := m.store.End(r.ID, reason, nowMS)
		m.ended(r, reason, won, err)
	}
}

// ended records the outcome of one close: a win wakes the pollers; a lost
// race is not an error.
func (m *Module) ended(r leaseRow, reason string, won bool, err error) {
	switch {
	case err != nil:
		m.logf("[resources] sweeper: end lease %s (%s): %v", r.ID, reason, err)
	case won:
		m.logf("[resources] lease %s (%s, pid %d) ended: %s", r.ID, r.Kind, r.HolderPID, reason)
		m.wake()
	}
}

// readView takes the process table through the sweepView seam, or the
// module's own snapshot reader. It touches no lock and no module state, so the
// sweeper, the sampler's lease measure and a request can all call it.
func (m *Module) readView(ctx context.Context) (procView, error) {
	take := m.sweepView
	if take == nil {
		take = func(ctx context.Context) (procView, error) {
			snap, err := m.procSnapshot(ctx)
			if err != nil {
				return nil, err
			}
			return snap, nil
		}
	}
	sctx, cancel := context.WithTimeout(ctx, sampleBudget)
	defer cancel()
	return take(sctx)
}

// holderView takes the process table the holders are judged against, or nil
// when it cannot be read: unknown reads as alive. A standing failure is logged
// once.
func (m *Module) holderView(ctx context.Context) procView {
	view, err := m.readView(ctx)
	if err != nil {
		if !m.viewFailing {
			m.viewFailing = true
			m.logf("[resources] sweeper: process table unreadable, holders count as alive: %v", err)
		}
		return nil
	}
	if m.viewFailing {
		m.viewFailing = false
		m.logf("[resources] sweeper: process table readable again")
	}
	return view
}

// holderGone says whether the lease's holder process is provably gone: its pid
// is not in the table, or its start time differs from the one the row recorded
// (the pid was reused). It is the rule of OriginResolver.LeadPresence, at
// second precision; anything that cannot be told (no pid, a start text that is
// empty or does not parse, a start time the table cannot give) is alive.
func holderGone(view procView, r leaseRow) bool {
	want, err := ipeers.ParseProcStart(r.HolderStart)
	if r.HolderPID <= 0 || err != nil {
		return false
	}
	if !view.Alive(r.HolderPID) {
		return true
	}
	got, err := view.Start(r.HolderPID)
	if err != nil || got.IsZero() {
		return false
	}
	return !got.Truncate(time.Second).Equal(want.Truncate(time.Second))
}

var _ procView = (*iagent.ProcessSnapshot)(nil)

// passIfWaiting runs the admission pass after a sweeper tick when a request is
// waiting: the sweep may just have freed capacity, and a deadline passes with
// no sample and no change (spec R6, plan review #3). With nobody waiting it is
// one indexed count.
func (m *Module) passIfWaiting(ctx context.Context) {
	if m.store == nil {
		return
	}
	if n, err := m.store.CountWaiting(ctx); err != nil || n == 0 {
		return
	}
	m.admissionPass(ctx, "")
}

// emptySamplesToVanish is how many samples in a row a session-new lease's
// tracked tree must have been empty before the lease counts as vanished.
const emptySamplesToVanish = 2

// vanished is the rule that catches a mod that crashed between acquire and
// release (spec D-4, plan Task 1.5): a session-new lease past its warmup whose
// tracked tree was empty for two samples in a row. It can end only a lease
// with nothing running under it; while a process of the command exists the
// count is back at 0. A process-scope lease is judged by its holder alone.
func vanished(r leaseRow, now time.Time, set resources.Settings) bool {
	return r.Scope == resources.ScopeSessionNew && r.EmptySamples >= emptySamplesToVanish &&
		now.Sub(time.UnixMilli(r.GrantedAt)) >= set.Warmup()
}
