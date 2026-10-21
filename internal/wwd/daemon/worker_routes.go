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
	writeJSON(w, http.StatusOK, d.opts.State.Worker().View())
}

func (d *Daemon) handlePutWorker(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(w, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	put, err := wire.Decode[wire.WorkerPut](body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if _, err := d.opts.State.SetWorkerIf(put.WorkerSettings, put.ExpectEtag); err != nil {
		var mm *EtagMismatchError
		if errors.As(err, &mm) {
			writeJSON(w, http.StatusConflict, wire.WorkerEtagMismatch{Reason: wire.WorkerEtagMismatchReason, Etag: mm.Current.Etag(), Worker: mm.Current.View()})
			return
		}
		stateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d.opts.State.Worker().View())
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
	// The hold fields (spec §13.4, WWD-8 8b-3) are kept in wwd.db since schema 7 and every host runs a wwd that reads them, so they are
	// taken like the rest; wire.Decode has checked them (Exception.Validate).
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
