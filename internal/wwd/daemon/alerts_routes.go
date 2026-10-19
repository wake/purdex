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
	local := d.handleAlerts
	if !peer {
		// a read of this host's own list on the socket is the local Purdex daemon's deliverer (spec §13.3): it is the partner's heartbeat
		local = func(w http.ResponseWriter, r *http.Request) {
			d.partnerAt.Store(d.clock().UnixNano())
			d.handleAlerts(w, r)
		}
	}
	mux.HandleFunc("GET /v1/alerts", d.viaHost(peer, local))
}

func (d *Daemon) handleAlerts(w http.ResponseWriter, r *http.Request) {
	alerts, err := d.opts.Alerts.ActiveAlerts()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if alerts == nil {
		alerts = []wire.Alert{}
	}
	writeJSON(w, http.StatusOK, wire.AlertList{Host: d.opts.Alias, Alerts: alerts})
}
