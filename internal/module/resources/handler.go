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
	snap := m.current()
	st := m.settings()
	snap.Mode = st.Mode
	if m.store == nil {
		snap.Mode = resources.ModeMeasure
	} else if err := m.addLeases(&snap, st); err != nil {
		m.logf("[resources] list leases for /api/resources: %v", err)
	}
	if sid := r.URL.Query().Get("session"); sid != "" {
		snap = onlySession(snap, sid)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(snap)
}

// addLeases fills the snapshot copy with the held leases, the queue and the
// last ended leases, read under stateMu so the three lists belong together.
// A read failure leaves them out: the host figures stand on their own.
func (m *Module) addLeases(snap *resources.Snapshot, st resources.Settings) error {
	m.stateMu.Lock()
	active, err := m.store.Active()
	var waiting, ended []leaseRow
	if err == nil {
		waiting, err = m.store.Waiting()
	}
	nowT := m.now()
	if err == nil {
		ended, err = m.store.Recent(resources.RecentLimit, nowT.Add(-recentWindow).UnixMilli())
	}
	m.stateMu.Unlock()
	if err != nil {
		return err
	}
	nowMs := nowT.UnixMilli()
	for _, a := range active {
		charge := resources.Charge(resources.Lease{
			ID: a.ID, Weight: a.Weight, GrantedAt: time.UnixMilli(a.GrantedAt), Measured: a.EWMA, Samples: a.Samples,
		}, nowT, st)
		snap.Leases = append(snap.Leases, resources.LeaseView{
			ID: a.ID, Kind: a.Kind, Weight: a.Weight, Charge: charge,
			SessionID: a.SessionID, AgeS: max(0, (nowMs-a.GrantedAt)/1000), Overrun: a.Overrun,
		})
	}
	for i, w := range waiting {
		snap.Waiters = append(snap.Waiters, resources.WaiterView{
			ID: w.ID, Kind: w.Kind, Weight: w.Weight, SessionID: w.SessionID, Position: i + 1,
			WaitedS: max(0, (nowMs-w.CreatedAt)/1000), DeadlineInS: (w.DeadlineAt - nowMs) / 1000,
		})
	}
	for _, e := range ended {
		snap.Recent = append(snap.Recent, resources.RecentView{
			ID: e.ID, Kind: e.Kind, Weight: e.Weight, SessionID: e.SessionID, EndReason: e.EndReason,
			Overrun: e.Overrun, WouldWait: e.WouldWait, WaitedMS: e.WaitedMS,
			EndedAt: time.UnixMilli(e.EndedAt).UTC(),
		})
	}
	return nil
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

// recentWindow is how far back the snapshot's recent list looks.
const recentWindow = time.Hour
