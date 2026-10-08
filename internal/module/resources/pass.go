package resourcesmod

import (
	"context"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// verdict is what a pass decided for one waiter it grants.
type verdict struct {
	overrun   bool // granted at its deadline though it did not fit (spec R6)
	wouldWait bool // mode advise: granted at once, mode lease would have queued it
}

// pass runs one admission pass: it decides which waiters start now and grants
// them. The settings are read before the lock (one SQLite read); the rows, the
// decision and every grant happen under stateMu, so no transition can land
// between the snapshot Admit decided on and the grants it issues.
//
// P1-2a runs it after a POST, a DELETE (release or cancel); P1-2b adds the
// sampler tick and the 1 s sweeper tick, which call this same function.
func (m *Module) pass(ctx context.Context) {
	if m.store == nil {
		return
	}
	st := m.settings()
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	m.passLocked(ctx, st)
}

// passLocked is pass with stateMu held and the settings read. A grant whose
// compare-and-set fails (an external writer, or a bug) aborts the pass and
// runs it once more from fresh rows; losing twice is logged and left for the
// next trigger.
func (m *Module) passLocked(ctx context.Context, st resources.Settings) {
	if m.store == nil {
		return
	}
	for attempt := 0; attempt < 2; attempt++ {
		lost, err := m.passOnce(ctx, st)
		if err != nil {
			m.logf("[resources] admission pass: %v", err)
			return
		}
		if !lost {
			return
		}
	}
	m.logf("[resources] admission pass: a grant lost its compare-and-set twice; waiting for the next trigger")
}

// passOnce reads the rows, decides and grants. lost reports that a Grant
// returned false.
func (m *Module) passOnce(ctx context.Context, st resources.Settings) (lost bool, err error) {
	if ctx.Err() != nil {
		return false, nil
	}
	active, err := m.store.Active()
	if err != nil {
		return false, err
	}
	waiting, err := m.store.Waiting()
	if err != nil {
		return false, err
	}
	if len(waiting) == 0 {
		return false, nil
	}
	nowT := m.now()
	nowMs := nowT.UnixMilli()
	decided := m.decide(st, active, waiting, nowT)

	granted := false
	defer func() {
		if granted {
			m.bumpLocked()
		}
	}()
	for _, w := range waiting {
		v, ok := decided[w.ID]
		if !ok {
			continue
		}
		won, err := m.store.Grant(w.ID, nowMs, v.overrun, v.wouldWait)
		if err != nil {
			return false, err
		}
		if !won {
			return true, nil
		}
		granted = true
		row, ok, err := m.store.Get(w.ID)
		if err == nil && ok {
			m.onGranted(row)
		}
	}
	return false, nil
}

// decide maps the waiters to grant to their verdicts, by the mode:
//
//   - lease: the Admitter's grants and overruns, the rest keeps waiting;
//   - advise: every waiter is granted; the ones the Admitter would have kept
//     waiting carry wouldWait;
//   - measure and off (a mode change with waiters left over): every waiter is
//     granted, none as an overrun.
func (m *Module) decide(st resources.Settings, active, waiting []leaseRow, now time.Time) map[string]verdict {
	out := make(map[string]verdict, len(waiting))
	if st.Mode != resources.ModeLease && st.Mode != resources.ModeAdvise {
		for _, w := range waiting {
			out[w.ID] = verdict{}
		}
		return out
	}

	snap := m.current()
	leases := make([]resources.Lease, 0, len(active))
	for _, a := range active {
		leases = append(leases, resources.Lease{
			ID: a.ID, Weight: a.Weight, GrantedAt: time.UnixMilli(a.GrantedAt),
			Measured: a.EWMA, Samples: a.Samples,
		})
	}
	waiters := make([]resources.Waiter, 0, len(waiting))
	for _, w := range waiting {
		waiters = append(waiters, resources.Waiter{
			ID: w.ID, Weight: w.Weight, EnqueuedAt: time.UnixMilli(w.CreatedAt), Deadline: time.UnixMilli(w.DeadlineAt),
		})
	}
	grant, overrun := m.admit.Admit(AdmitInput{
		Host:      snap.Host,
		Available: snap.Available,
		Leases:    leases,
		LeaseUse:  map[string]float64{}, // per-lease measurement is P1-2b
		Waiters:   waiters,
		Now:       now,
		Settings:  st,
	})

	queued := make(map[string]bool, len(waiting))
	for _, w := range waiting {
		queued[w.ID] = true
	}
	for _, id := range grant {
		if queued[id] {
			out[id] = verdict{}
		}
	}
	for _, id := range overrun {
		if queued[id] {
			out[id] = verdict{overrun: true}
		}
	}
	if st.Mode == resources.ModeAdvise {
		for _, w := range waiting {
			if _, ok := out[w.ID]; !ok {
				out[w.ID] = verdict{wouldWait: true}
			}
		}
	}
	return out
}

// bumpLocked wakes every long-poll that captured the current generation and
// installs the next one. Call it, with stateMu held, after any transition of
// a row: create, grant, end of any reason, boot reconcile.
func (m *Module) bumpLocked() {
	close(m.gen)
	m.gen = make(chan struct{})
}

// endLocked ends a row with the reason and wakes the polls; reports whether
// this call ended it. stateMu must be held.
func (m *Module) endLocked(id, reason string) (bool, error) {
	won, err := m.store.End(id, reason, m.now().UnixMilli())
	if won {
		m.bumpLocked()
	}
	return won, err
}

// closeExpiredLocked is the sweeper's abandoned-waiter close (P1-2b calls
// it): CloseIfExpired plus the wake-up. stateMu must be held.
func (m *Module) closeExpiredLocked(id string) (bool, error) {
	won, err := m.store.CloseIfExpired(id, m.now().UnixMilli())
	if won {
		m.bumpLocked()
	}
	return won, err
}
