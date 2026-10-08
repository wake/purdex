package resourcesmod

import (
	"encoding/json"
	"net/http"

	"github.com/wake/purdex/internal/resources"
)

// handleGet serves the latest snapshot, optionally narrowed to one session.
// It never samples (hot-path rule): before the first tick ends it answers
// available = false, reason = warming_up.
func (m *Module) handleGet(w http.ResponseWriter, r *http.Request) {
	snap := m.current()
	if sid := r.URL.Query().Get("session"); sid != "" {
		snap = onlySession(snap, sid)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(snap)
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
