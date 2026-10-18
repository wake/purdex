package daemon

import (
	"net/http"
	"time"

	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

// The periods the observation routes answer for when the caller names none: the observations list a day, the undeclared list a week.
// Records are kept 14 days (store.ObservationRetention), so a caller can ask for up to that.
const (
	observationsDefaultSince = 24 * time.Hour
	undeclaredDefaultSince   = 7 * 24 * time.Hour
)

// One answer carries at most this much, so a busy host cannot make an answer the pool's forwarding refuses (8 MiB) or one that
// costs the daemon the memory of a whole retention. The newest observations and the biggest groups are kept; the response says
// when something was left out.
const (
	maxObservationsAnswer = 1000
	maxUndeclaredGroups   = 500
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

// observeSince is the start of the period the request asks for: ?since=<RFC 3339>, else the default back from now. It never
// reaches back farther than the retention: nothing older is kept.
func (d *Daemon) observeSince(r *http.Request, def time.Duration) (time.Time, error) {
	since := d.now().Add(-def)
	if s := r.URL.Query().Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, err
		}
		since = t
	}
	return maxTime(since, d.now().Add(-store.ObservationRetention)), nil
}

func maxTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return b
	}
	return a
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
	// A page back in time: what began at or before until. The timestamp alone is enough of a cursor because a tree's start is the
	// sample that first saw it and the tracker keeps at most ObserveConfig.MaxLive (256) trees at once, so at most 256 records share
	// one start, always fewer than a page (maxObservationsAnswer): every page moves back.
	if s := r.URL.Query().Get("until"); s != "" {
		until, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "until: " + err.Error()})
			return
		}
		kept := obs[:0:0]
		for _, o := range obs {
			if !o.StartedAt.After(until) {
				kept = append(kept, o)
			}
		}
		obs = kept
	}
	resp := wire.ObservationsResponse{Host: d.opts.Alias, Since: since}
	if len(obs) > maxObservationsAnswer {
		obs = obs[len(obs)-maxObservationsAnswer:] // oldest first: the newest are at the end
		oldest := obs[0].StartedAt
		resp.Truncated, resp.NextUntil = true, &oldest
	}
	resp.Observations = obs
	writeJSON(w, http.StatusOK, resp)
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
	truncated := len(groups) > maxUndeclaredGroups
	if truncated {
		groups = groups[:maxUndeclaredGroups] // biggest total CPU first
	}
	writeJSON(w, http.StatusOK, wire.UndeclaredResponse{Host: d.opts.Alias, Since: since, Groups: groups, Truncated: truncated})
}
