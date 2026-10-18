package poolmod

import (
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/wwd/wire"
)

// ErrWWDRejected: wwd refused the change itself (a 4xx of its own: the settings did not validate, no such exception, an
// unknown host). The status is wwd's and the detail is what it said.
const ErrWWDRejected = "wwd_rejected"

// maxControlBody caps a change's body: a worker setting or an exception is a few hundred bytes.
const maxControlBody = 64 << 10

// validAlias is the one rule for a host alias (wire.ValidAlias), the same one wwd's config applies to its peers: whatever
// the config accepts, these routes can name. client.Mutate escapes the host and the exception name, so neither can add a
// parameter or a path.
func validAlias(s string) bool { return wire.ValidAlias(s) }

// exceptionName is an exception name as one path segment: the alias rule, and not a dot segment.
func exceptionName(s string) bool { return wire.ValidAlias(s) && s != "." && s != ".." }

func (m *Module) registerControl(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/pool/hosts/{alias}/worker", m.handlePutWorker)
	mux.HandleFunc("POST /api/pool/hosts/{alias}/exceptions", m.handlePostException)
	mux.HandleFunc("DELETE /api/pool/hosts/{alias}/exceptions/{name}", m.handleDeleteException)
}

// adminOnly refuses anyone but the admin token: a device, a one-time ticket and a host principal are not it. (A device is
// also refused before this by the device allow-list, which is default deny; this is the handler's own check.)
func adminOnly(w http.ResponseWriter, r *http.Request) bool {
	if _, isDevice := devices.PrincipalFrom(r.Context()); isDevice || !devices.IsAdmin(r.Context()) {
		writeErr(w, http.StatusForbidden, "forbidden", "changing a host's worker is for the admin token")
		return false
	}
	if p, ok := middleware.PrincipalFrom(r.Context()); ok && p.Kind != middleware.PrincipalAdmin {
		writeErr(w, http.StatusForbidden, "forbidden", "changing a host's worker is for the admin token")
		return false
	}
	return true
}

// readControlBody reads the body, at most maxControlBody.
func readControlBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxControlBody))
	if err != nil {
		writeErr(w, http.StatusBadRequest, ErrBadRequest, "the body is too large or unreadable")
		return nil, false
	}
	return b, true
}

func (m *Module) handlePutWorker(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	alias := r.PathValue("alias")
	if !validAlias(alias) {
		writeErr(w, http.StatusBadRequest, ErrBadRequest, "the host alias is empty, too long or holds a control character")
		return
	}
	body, ok := readControlBody(w, r)
	if !ok {
		return
	}
	if _, err := wire.Decode[wire.WorkerSettings](body); err != nil {
		writeErr(w, http.StatusBadRequest, ErrBadRequest, "worker settings: "+err.Error())
		return
	}
	m.change(w, r, http.MethodPut, "/v1/worker", alias, body)
}

func (m *Module) handlePostException(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	alias := r.PathValue("alias")
	if !validAlias(alias) {
		writeErr(w, http.StatusBadRequest, ErrBadRequest, "the host alias is empty, too long or holds a control character")
		return
	}
	body, ok := readControlBody(w, r)
	if !ok {
		return
	}
	if _, err := wire.Decode[wire.Exception](body); err != nil {
		writeErr(w, http.StatusBadRequest, ErrBadRequest, "exception: "+err.Error())
		return
	}
	m.change(w, r, http.MethodPost, "/v1/exceptions", alias, body)
}

func (m *Module) handleDeleteException(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	alias, name := r.PathValue("alias"), r.PathValue("name")
	if !validAlias(alias) {
		writeErr(w, http.StatusBadRequest, ErrBadRequest, "the host alias is empty, too long or holds a control character")
		return
	}
	if !exceptionName(name) {
		writeErr(w, http.StatusBadRequest, ErrBadRequest, "the exception name is not a plain name")
		return
	}
	m.change(w, r, http.MethodDelete, "/v1/exceptions/"+url.PathEscape(name), alias, nil)
}

// change asks the local wwd to apply a change to host (it forwards when host is another one) and relays its answer. A
// change is never cached and never shares a flight with another: each is its own call, but it takes one of the in-flight
// slots like a read does.
func (m *Module) change(w http.ResponseWriter, r *http.Request, method, path, host string, body []byte) {
	select {
	case m.inflight <- struct{}{}:
		defer func() { <-m.inflight }()
	default:
		writeErr(w, http.StatusTooManyRequests, ErrBusy, "too many pool calls in flight; retry")
		return
	}
	status, answer, err := m.client().Mutate(r.Context(), method, path, host, body)
	if err != nil {
		e := wwdError(err)
		writeErr(w, e.status, e.code, e.detail)
		return
	}
	switch {
	case status >= 200 && status < 300:
		if len(answer) > 0 {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		_, _ = w.Write(answer)
	case status >= 400 && status < 500:
		writeErr(w, status, ErrWWDRejected, clip(string(answer)))
	default:
		// wwd's 502 is the peer that could not be reached (its {host, reason}); anything else is wwd failing
		writeErr(w, http.StatusBadGateway, ErrWWDError, "wwd answered "+http.StatusText(status)+": "+clip(string(answer)))
	}
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
