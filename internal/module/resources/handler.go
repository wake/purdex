package resourcesmod

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// handleGet serves the latest snapshot, optionally narrowed to one session.
// It never samples (hot-path rule): before the first tick ends it answers
// available = false, reason = warming_up.
func (m *Module) handleGet(w http.ResponseWriter, r *http.Request) {
	// The snapshot is the last tick's, whole: mode, available and reason come
	// from one tick, so they cannot contradict each other. A changed setting
	// shows from the next tick (at most one interval later).
	snap := m.current()
	m.addLeases(&snap)
	if sid := r.URL.Query().Get("session"); sid != "" {
		snap = onlySession(snap, sid)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(snap)
}

// recentWindow is how far back the snapshot's recent list looks.
const recentWindow = time.Hour

// addLeases fills the snapshot copy with the held leases, the queue (with
// positions) and the last ended leases, from resources.db. Three reads, not
// one statement (store.Listing), so the lists are one view of the rows. A read failure leaves the lists
// out (logged once per run of failures): the host figures stand on their own.
//
// There is no per-lease measurement yet (P1-2b): a held lease is charged its
// full weight and its use reads 0.
func (m *Module) addLeases(snap *resources.Snapshot) {
	if m.store == nil {
		return
	}
	nowT := m.now()
	active, waiting, ended, err := m.store.Listing(resources.RecentLimit, nowT.Add(-recentWindow).UnixMilli())
	m.noteList(err)
	if err != nil {
		return
	}
	nowMs := nowT.UnixMilli()
	for _, a := range active {
		snap.Leases = append(snap.Leases, resources.LeaseView{
			ID: a.ID, Kind: a.Kind, Weight: a.Weight, Charge: float64(a.Weight),
			SessionID: a.SessionID, AgeS: max(0, (nowMs-a.GrantedAt)/1000), Overrun: a.Overrun,
		})
	}
	for i, w := range waiting {
		snap.Waiters = append(snap.Waiters, resources.WaiterView{
			ID: w.ID, Kind: w.Kind, Weight: w.Weight, SessionID: w.SessionID, Position: i + 1,
			WaitedS: max(0, (nowMs-w.CreatedAt)/1000), DeadlineInS: max(0, (w.DeadlineAt-nowMs)/1000),
		})
	}
	for _, e := range ended {
		snap.Recent = append(snap.Recent, resources.RecentView{
			ID: e.ID, Kind: e.Kind, Weight: e.Weight, SessionID: e.SessionID, EndReason: e.EndReason,
			Overrun: e.Overrun, WouldWait: e.WouldWait, WaitedMS: e.WaitedMS,
			EndedAt: time.UnixMilli(e.EndedAt).UTC(),
		})
	}
}

// noteList logs a failed lease listing when it starts, and its recovery.
func (m *Module) noteList(err error) {
	m.noteMu.Lock()
	failing := err != nil
	changed := failing != m.listFailing
	m.listFailing = failing
	m.noteMu.Unlock()
	switch {
	case !changed:
	case failing:
		m.logf("[resources] list leases for /api/resources: %v", err)
	default:
		m.logf("[resources] listing leases works again")
	}
}

// current is the last published snapshot, or the warming-up one.
func (m *Module) current() resources.Snapshot {
	if s := m.latest.Load(); s != nil {
		return *s
	}
	return resources.Snapshot{
		SampledAt: m.now(),
		Reason:    resources.ReasonWarmingUp,
		Capacity:  resources.Capacity,
		Sessions:  []resources.SessionUse{},
		Mode:      resources.ModeMeasure,
	}
}

// onlySession returns a copy of s whose sessions are just sid's (none for an
// unknown id). The published snapshot is shared and stays untouched.
func onlySession(s resources.Snapshot, sid string) resources.Snapshot {
	kept := []resources.SessionUse{}
	for _, u := range s.Sessions {
		if u.SessionID == sid {
			kept = append(kept, u)
		}
	}
	s.Sessions = kept
	return s
}
