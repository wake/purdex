package daemon

import (
	"net/http"

	"github.com/wake/purdex/internal/wwd/wire"
)

// AlertStore reads the host's active alerts (store.Store).
type AlertStore interface {
	ActiveAlerts() ([]wire.Alert, error)
}

// alertsRoutes registers GET /v1/alerts on both surfaces (spec §13.3): the host's active alerts. On the socket ?host=<alias>
// forwards it to that host like the worker routes; on TCP the host parameter never reaches here. Without a store the route is not
// registered and answers 404, which readers take as "this host has no alerts yet" (§13.1), as they do for a host that predates it.
// This build only reads: nothing writes the table until the alert rules (8a-1, 8a-2), so the list is empty.
func (d *Daemon) alertsRoutes(mux *http.ServeMux, peer bool) {
	if d.opts.Alerts == nil {
		return
	}
	local := func(w http.ResponseWriter, r *http.Request) { d.handleAlerts(w, r) }
	if !peer {
		// a read of this host's own list on the socket is the local Purdex daemon's deliverer (spec §13.3): it is the partner's heartbeat, but
		// only a read that was answered counts: a store that fails every time is a deliverer that reads nothing, and must not look alive
		local = func(w http.ResponseWriter, r *http.Request) {
			if d.handleAlerts(w, r) {
				d.partnerAt.Store(d.clock().UnixNano())
			}
		}
	}
	mux.HandleFunc("GET /v1/alerts", d.viaHost(peer, local))
}

// handleAlerts answers with the active alerts and says whether it could.
func (d *Daemon) handleAlerts(w http.ResponseWriter, r *http.Request) bool {
	alerts, err := d.opts.Alerts.ActiveAlerts()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return false
	}
	if alerts == nil {
		alerts = []wire.Alert{}
	}
	writeJSON(w, http.StatusOK, wire.AlertList{Host: d.opts.Alias, Alerts: alerts})
	return true
}
