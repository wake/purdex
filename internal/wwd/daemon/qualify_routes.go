package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/wake/purdex/internal/wwd/qualify"
	"github.com/wake/purdex/internal/wwd/wire"
)

// Qualifier is the qualification coordinator as the routes use it (*qualify.Coordinator).
type Qualifier interface {
	Request(ctx context.Context, repoTask string) (qualify.Started, error)
	Status() (inFlight []qualify.Started, recent []qualify.Record)
	// Qualification is what the host holds of each task and what happens next (GET /v1/qualification).
	Qualification() (wire.HostQualification, error)
}

// qualificationRoutes registers GET /v1/qualification on both surfaces like GET /v1/exceptions (WWD-7b2-0a): on the socket ?host=<alias>
// forwards to that host, on TCP the host answers for itself (a peer's Purdex daemon reads it so). Read only, and nothing in it is secret (job
// ids, labels, fingerprints). Without a coordinator the route does not exist, which a reader takes for a wwd that predates it.
func (d *Daemon) qualificationRoutes(mux *http.ServeMux, peer bool) {
	if d.opts.Qualifier == nil {
		return
	}
	mux.HandleFunc("GET /v1/qualification", d.viaHost(peer, d.handleQualification))
}

func (d *Daemon) handleQualification(w http.ResponseWriter, r *http.Request) {
	v, err := d.opts.Qualifier.Qualification()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	v.Host = d.opts.Alias
	writeJSON(w, http.StatusOK, v)
}

// qualifyRoutes registers POST /v1/qualify and GET /v1/qualify on the local socket only (spec §7): a person asks this host to start a pair for
// a task, or looks at the pairs. A pair is queued into this host's own queue and decides about this host's own labels, so there is no route
// for another host to call and ?host= is refused, not forwarded. Without a coordinator the routes do not exist.
func (d *Daemon) qualifyRoutes(mux *http.ServeMux) {
	if d.opts.Qualifier == nil {
		return
	}
	mux.HandleFunc("POST /v1/qualify", d.localOnly(d.handleQualify))
	mux.HandleFunc("GET /v1/qualify", d.localOnly(d.handleQualifyStatus))
}

func (d *Daemon) handleQualify(w http.ResponseWriter, r *http.Request) {
	var req wire.QualifyRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `body must be {"task": "<repo>/<task>"}`})
		return
	}
	if err := req.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s, err := d.opts.Qualifier.Request(r.Context(), req.Task)
	if err != nil {
		writeJSON(w, qualifyStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, wire.QualifyStarted{Label: s.Label, Candidate: s.Candidate, Reference: s.Reference})
}

// qualifyStatus is the status a refused request answers: 404 for a task this host has no qualify for, 409 for a task it may not be asked about
// now (the reasons qualify.Check gives, in its words), 500 for the rest (the snapshot or the queue failed).
func qualifyStatus(err error) int {
	var unknown *qualify.UnknownTaskError
	var missing *qualify.MissingError
	switch {
	case errors.As(err, &unknown):
		return http.StatusNotFound
	case errors.As(err, &missing),
		errors.Is(err, qualify.ErrNoQualify), errors.Is(err, qualify.ErrNoDefinition), errors.Is(err, qualify.ErrReference),
		errors.Is(err, qualify.ErrWorkerOff), errors.Is(err, qualify.ErrInFlight), errors.Is(err, qualify.ErrAlreadyHas):
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func (d *Daemon) handleQualifyStatus(w http.ResponseWriter, r *http.Request) {
	inFlight, recent := d.opts.Qualifier.Status()
	out := wire.QualifyStatus{InFlight: []wire.QualifyStarted{}, Recent: []wire.QualifyRecord{}}
	for _, p := range inFlight {
		out.InFlight = append(out.InFlight, wire.QualifyStarted{Label: p.Label, Candidate: p.Candidate, Reference: p.Reference})
	}
	for _, rec := range recent {
		out.Recent = append(out.Recent, wire.QualifyRecord{
			Label: rec.Label, Outcome: string(rec.Outcome), Why: rec.Why, At: rec.At, Candidate: rec.Candidate, Reference: rec.Reference,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
