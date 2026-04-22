package arbmode

import (
	"encoding/json"
	"net/http"
)

// Snapshotter is the subset of Manager the Handler needs. Interface used for
// test mocking without requiring a full Manager in unit tests.
type Snapshotter interface {
	Snapshot() Snapshot
}

// Handler serves GET /api/agent/arbitrator/mode — returns the current mode
// snapshot as JSON. Read-only; non-GET methods return 405.
type Handler struct {
	mgr Snapshotter
}

// NewHandler creates a Handler backed by the given Snapshotter.
func NewHandler(mgr Snapshotter) *Handler {
	return &Handler{mgr: mgr}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snap := h.mgr.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Current   ArbMode `json:"current"`
		Pending   ArbMode `json:"pending"`
		EnvLocked bool    `json:"env_locked"`
	}{
		Current:   snap.Current,
		Pending:   snap.Pending,
		EnvLocked: snap.EnvLocked,
	})
}
