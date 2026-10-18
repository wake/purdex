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
		if !peer {
			target, local, status := d.forwardTarget(r)
			switch {
			case status != 0:
				writeJSON(w, status, map[string]string{"error": "unknown host", "host": r.URL.Query().Get("host")})
				return
			case !local:
				d.forwardGET(w, r, target)
				return
			}
		}
		recs := d.opts.Sampler.History(q.Since, q.Step)
		if recs == nil {
			recs = []wire.Facts{}
		}
		writeJSON(w, http.StatusOK, wire.FactsHistoryResponse{Step: q.Step, Records: recs})
	}
}
