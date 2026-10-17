package daemon

import (
	"net/http"

	"github.com/wake/purdex/internal/wwd/wire"
)

func (d *Daemon) factsRoutes(mux *http.ServeMux) {
	if d.opts.Sampler == nil {
		return
	}
	mux.HandleFunc("GET /v1/facts", d.handleFacts)
	mux.HandleFunc("GET /v1/facts/history", d.handleFactsHistory)
}

func (d *Daemon) handleFacts(w http.ResponseWriter, r *http.Request) {
	f, ok := d.opts.Sampler.Latest()
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no sample yet"})
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (d *Daemon) handleFactsHistory(w http.ResponseWriter, r *http.Request) {
	q, err := wire.ParseFactsHistoryQuery(r.URL.Query())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	recs := d.opts.Sampler.History(q.Since, q.Step)
	if recs == nil {
		recs = []wire.Facts{}
	}
	writeJSON(w, http.StatusOK, wire.FactsHistoryResponse{Step: q.Step, Records: recs})
}
