package daemon

import (
	"errors"
	"net/http"

	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

// EarnedStore reads the host's earned and quarantined task labels (store.Store).
type EarnedStore interface {
	EarnedLabels() ([]wire.EarnedLabel, error)
}

// earnedRoutes registers GET /v1/earned-labels on the local socket only (spec §7): what `pdx wwd labels` lists. A peer has no use for
// another host's quarantine list (it sees the advertised labels), so the TCP surface has no such route; ?host= is not forwarded.
// Without a store the route is not registered.
func (d *Daemon) earnedRoutes(mux *http.ServeMux) {
	if d.opts.Earned == nil {
		return
	}
	mux.HandleFunc("GET /v1/earned-labels", d.localOnly(d.handleEarned))
}

func (d *Daemon) handleEarned(w http.ResponseWriter, r *http.Request) {
	rows, err := d.opts.Earned.EarnedLabels()
	var skipped *store.SkippedRowsError
	if err != nil && !errors.As(err, &skipped) { // damaged rows are left out, the rest is still the answer
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []wire.EarnedLabel{}
	}
	writeJSON(w, http.StatusOK, wire.EarnedLabelList{Host: d.opts.Alias, Labels: rows})
}
