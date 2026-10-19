package daemon

import (
	"errors"
	"io"
	"net/http"

	"github.com/wake/purdex/internal/wwd/wire"
)

// maxRequestBody bounds the JSON a settings route reads.
const maxRequestBody = 64 << 10

// workerRoutes registers the worker and exception routes (spec §9) on both
// surfaces. On the socket, ?host=<alias> naming another host forwards the call to
// it; on TCP the host parameter never reaches here (rejectHostParam).
func (d *Daemon) workerRoutes(mux *http.ServeMux, peer bool) {
	if d.opts.State == nil {
		return
	}
	mux.HandleFunc("GET /v1/worker", d.viaHost(peer, d.handleGetWorker))
	mux.HandleFunc("PUT /v1/worker", d.viaHost(peer, d.handlePutWorker))
	mux.HandleFunc("GET /v1/exceptions", d.viaHost(peer, d.handleGetExceptions))
	mux.HandleFunc("POST /v1/exceptions", d.viaHost(peer, d.handlePostException))
	mux.HandleFunc("DELETE /v1/exceptions/{name}", d.viaHost(peer, d.handleDeleteException))
}

// viaHost wraps a handler: on the socket a request about another host of the pool
// is forwarded (its body read once, here), anything else is answered locally.
func (d *Daemon) viaHost(peer bool, local http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if peer {
			local(w, r)
			return
		}
		target, isLocal, status := d.forwardTarget(r)
		switch {
		case status != 0:
			writeJSON(w, status, map[string]string{"error": "unknown host", "host": r.URL.Query().Get("host")})
		case !isLocal:
			body, err := readBody(w, r)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			d.forward(w, r, target, body)
		default:
			local(w, r)
		}
	}
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
	if err != nil {
		return nil, errors.New("request body too large or unreadable")
	}
	return b, nil
}

func (d *Daemon) handleGetWorker(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.opts.State.Worker())
}

func (d *Daemon) handlePutWorker(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(w, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ws, err := wire.Decode[wire.WorkerSettings](body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if _, err := d.opts.State.SetWorker(ws); err != nil {
		stateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d.opts.State.Worker())
}

// handleGetExceptions answers the exceptions in force with their holds (read-only; a host without this route answers 404 to a reader).
func (d *Daemon) handleGetExceptions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.opts.State.Exceptions())
}

func (d *Daemon) handlePostException(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(w, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	e, err := wire.Decode[wire.Exception](body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Phase 1 of the contract (spec §13.1): readers know the hold fields, no writer fills them yet. Taking them here would publish
	// them on SSE to a proto 1 reader and lose them at the next restart (wwd.db does not keep them). 8b-3 lifts this.
	if e.Allow != nil || len(e.Flags) != 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "exception: allow and flags are not accepted yet (WWD-8 8b-3)"})
		return
	}
	if _, err := d.opts.State.AddException(e); err != nil {
		stateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (d *Daemon) handleDeleteException(w http.ResponseWriter, r *http.Request) {
	removed, err := d.opts.State.RemoveException(r.PathValue("name"))
	if err != nil {
		stateError(w, err)
		return
	}
	if !removed {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such exception"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// stateError maps a HostState error: a refused request is 400, anything else 500.
func stateError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrInvalid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}
