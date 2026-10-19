package jobsmod

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

// Error codes of the control routes besides the read ones.
const (
	ErrWWDRejected  = "wwd_rejected"   // wwd refused the change itself (a 4xx of its own: the job has ended, no such job); its status comes with it
	ErrHostNotLocal = "host_not_local" // 400: this change is only made on the host the job is queued on, and host names another
)

// maxControlBody caps a change's body: a position is a few bytes.
const maxControlBody = 4 << 10

// registerControl: what the App does to a job (wwd spec §10). Admin token only: these routes are not in the device allow-list
// (cmd/pdx/device_scope.go), and the handlers check it themselves too.
func (m *Module) registerControl(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/pool/jobs/{id}/cancel", m.handleCancel)
	mux.HandleFunc("POST /api/pool/jobs/{id}/position", m.handlePosition)
}

// adminOnly refuses anyone but the admin token: a device, a one-time ticket and a host principal are not it. (Same rule as the pool
// module's control routes.)
func adminOnly(w http.ResponseWriter, r *http.Request) bool {
	if _, isDevice := devices.PrincipalFrom(r.Context()); isDevice || !devices.IsAdmin(r.Context()) {
		writeErr(w, http.StatusForbidden, "forbidden", "changing a job is for the admin token")
		return false
	}
	if p, ok := middleware.PrincipalFrom(r.Context()); ok && p.Kind != middleware.PrincipalAdmin {
		writeErr(w, http.StatusForbidden, "forbidden", "changing a job is for the admin token")
		return false
	}
	return true
}

// writeChangeError answers a failed change: wwd's own 4xx keeps its status as wwd_rejected (the App reads "the job had ended" from a
// 409), everything else is what a failed read is.
func writeChangeError(w http.ResponseWriter, err error) {
	var se *client.StatusError
	if errors.As(err, &se) && se.Code >= 400 && se.Code < 500 {
		writeErr(w, se.Code, ErrWWDRejected, clip(se.Body))
		return
	}
	writeWWDError(w, err)
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

// handleCancel is POST /api/pool/jobs/{id}/cancel?host=: the local wwd cancels the job, or forwards the cancel to the peer; the answer
// is the job as it is then.
func (m *Module) handleCancel(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	h, ok := host(w, r)
	if !ok {
		return
	}
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	release, ok := m.slot(w)
	if !ok {
		return
	}
	defer release()
	job, err := m.client().CancelAt(r.Context(), h, id)
	if err != nil {
		writeChangeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// handlePosition is POST /api/pool/jobs/{id}/position {"position": front|back|hold|normal}: the manual place of a queued job. wwd sets
// it on the host the job is queued on only, so a host that is not this daemon's own wwd is refused here, before wwd is asked.
func (m *Module) handlePosition(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	h, ok := host(w, r)
	if !ok {
		return
	}
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxControlBody))
	if err != nil {
		writeErr(w, http.StatusBadRequest, ErrBadRequest, "the body is too large or unreadable")
		return
	}
	req, err := wire.Decode[wire.PositionRequest](body) // strict, and it validates
	if err != nil {
		writeErr(w, http.StatusBadRequest, ErrBadRequest, "position: "+err.Error())
		return
	}
	release, ok := m.slot(w)
	if !ok {
		return
	}
	defer release()
	cl := m.client()
	if h != "" {
		info, err := cl.Info(r.Context())
		if err != nil {
			writeChangeError(w, err)
			return
		}
		if info.Alias != h {
			writeErr(w, http.StatusBadRequest, ErrHostNotLocal, "a job's place is set on the host it is queued on, "+info.Alias+" here; "+h+" is another host")
			return
		}
	}
	job, err := cl.SetPosition(r.Context(), id, req.Position)
	if err != nil {
		writeChangeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}
