package daemon

import (
	"net/http"

	"github.com/wake/purdex/internal/wwd/wire"
)

func (d *Daemon) factsRoutes(mux *http.ServeMux, peer bool) {
	if d.opts.Sampler == nil {
		return
	}
	mux.HandleFunc("GET /v1/facts", d.handleFacts)
	mux.HandleFunc("GET /v1/facts/history", d.handleFactsHistory(peer))
	mux.HandleFunc("GET /v1/facts/series", d.handleFactsSeries(peer))
}

func (d *Daemon) handleFacts(w http.ResponseWriter, r *http.Request) {
	f, ok := d.opts.Sampler.Latest()
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no sample yet"})
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// handleFactsHistory answers for this host; on the socket (peer false) a
// ?host=<alias> naming another host of the pool is forwarded to it.
func (d *Daemon) handleFactsHistory(peer bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q, err := wire.ParseFactsHistoryQuery(r.URL.Query())
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if d.forwardedOrRefused(w, r, peer) {
			return
		}
		recs := d.opts.Sampler.History(q.Since, q.Step)
		if recs == nil {
			recs = []wire.Facts{}
		}
		writeJSON(w, http.StatusOK, wire.FactsHistoryResponse{Step: q.Step, Records: recs})
	}
}

// forwardedOrRefused handles ?host= on the socket (peer false): another host of the pool is asked and its answer passed on, an
// unknown one is a 404. It reports whether the request is done; false means it is about this host.
func (d *Daemon) forwardedOrRefused(w http.ResponseWriter, r *http.Request, peer bool) bool {
	if peer {
		return false
	}
	target, local, status := d.forwardTarget(r)
	switch {
	case status != 0:
		writeJSON(w, status, map[string]string{"error": "unknown host", "host": r.URL.Query().Get("host")})
		return true
	case !local:
		d.forwardGET(w, r, target)
		return true
	}
	return false
}

// handleFactsSeries is GET /v1/facts/series?since=&step=&limit=: this host's history cut down to the points a chart draws, so that a
// day of 1 m points is 200 KB on the tailnet instead of 10 MB of records. It is forwarded like the history, and the host that has the
// data does the cutting. At most limit points (SeriesMaxPoints when absent); the latest are kept and "truncated" says so.
func (d *Daemon) handleFactsSeries(peer bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q, err := wire.ParseFactsSeriesQuery(r.URL.Query())
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if d.forwardedOrRefused(w, r, peer) {
			return
		}
		writeJSON(w, http.StatusOK, wire.SeriesOf(wire.FactsHistoryResponse{Step: q.Step, Records: d.opts.Sampler.History(q.Since, q.Step)}, q.Limit))
	}
}
