package daemon

import (
	"net/http"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// The periods the observation routes answer for when the caller names none: the observations list a day, the undeclared list a week.
// Records are kept 14 days (store.ObservationRetention), so a caller can ask for up to that.
const (
	observationsDefaultSince = 24 * time.Hour
	undeclaredDefaultSince   = 7 * 24 * time.Hour
)

func (d *Daemon) now() time.Time {
	if d.opts.Now != nil {
		return d.opts.Now()
	}
	return time.Now()
}

// observeRoutes registers the observation reads (spec 8.4) on both surfaces, from the process's one Observer. On the socket
// ?host=<alias> forwards the call to that host like the worker routes; on TCP the host parameter never reaches here.
func (d *Daemon) observeRoutes(mux *http.ServeMux, peer bool) {
	if d.opts.Observer == nil {
		return
	}
	mux.HandleFunc("GET /v1/observations", d.viaHost(peer, d.handleObservations))
	mux.HandleFunc("GET /v1/observations/undeclared", d.viaHost(peer, d.handleUndeclared))
}

// observeSince is the start of the period the request asks for: ?since=<RFC 3339>, else the default back from now.
func (d *Daemon) observeSince(r *http.Request, def time.Duration) (time.Time, error) {
	if s := r.URL.Query().Get("since"); s != "" {
		return time.Parse(time.RFC3339, s)
	}
	return d.now().Add(-def), nil
}

func (d *Daemon) handleObservations(w http.ResponseWriter, r *http.Request) {
	since, err := d.observeSince(r, observationsDefaultSince)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "since: " + err.Error()})
		return
	}
	obs, err := d.opts.Observer.Observations(since)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, wire.ObservationsResponse{Host: d.opts.Alias, Since: since, Observations: obs})
}

func (d *Daemon) handleUndeclared(w http.ResponseWriter, r *http.Request) {
	since, err := d.observeSince(r, undeclaredDefaultSince)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "since: " + err.Error()})
		return
	}
	groups, err := d.opts.Observer.Undeclared(since)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, wire.UndeclaredResponse{Host: d.opts.Alias, Since: since, Groups: groups})
}
