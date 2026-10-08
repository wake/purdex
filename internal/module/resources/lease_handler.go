package resourcesmod

// The lease routes (plan Task 1.4):
//
//	POST   /api/resources/leases              create (idempotent on client_id)
//	GET    /api/resources/leases/{id}?wait=N  the bounded long-poll; renews the lease
//	DELETE /api/resources/leases/{id}         release (held) or cancel (waiting)
//	DELETE /api/resources/leases?client_id=   the same, by the creator's client id
//
// Every client of these routes fails open (spec D-5): a 4xx or 5xx here means
// "run the command anyway", so the handlers prefer a clear error to a clever
// guess.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// bodyCap bounds a POST body; a request is a few hundred bytes.
const bodyCap = 64 << 10

// maxFieldLen bounds the free-text fields of a request.
const maxFieldLen = 256

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, resources.APIError{Error: code, Detail: detail})
}

// storageErr answers a database failure: the detail stays in the daemon log.
func (m *Module) storageErr(w http.ResponseWriter, what string, err error) {
	m.logf("[resources] %s: %v", what, err)
	writeErr(w, http.StatusInternalServerError, resources.ErrStorage, "resources.db failed; see the daemon log")
}

// leaseSpec is a validated request.
type leaseSpec struct {
	kind   string
	weight int
	waitS  int
	scope  string
	client string
}

// validate checks the request against the settings (kind weights, default
// deadline); on failure it writes the 400 and returns false.
func validate(w http.ResponseWriter, req resources.LeaseRequest, st resources.Settings) (leaseSpec, bool) {
	bad := func(detail string) (leaseSpec, bool) {
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, detail)
		return leaseSpec{}, false
	}
	client := strings.ToLower(req.ClientID)
	if !uuidV4.MatchString(client) {
		return bad("client_id must be a UUID v4")
	}
	spec := leaseSpec{client: client, kind: req.Kind}
	switch {
	case req.Kind != "" && req.Weight != 0:
		return bad("give exactly one of kind and weight")
	case req.Kind == "" && req.Weight == 0:
		return bad("give exactly one of kind and weight")
	case req.Kind != "":
		weight, ok := st.Weight(req.Kind)
		if !ok {
			writeErr(w, http.StatusBadRequest, resources.ErrUnknownKind, "unknown kind "+strconv.Quote(req.Kind))
			return leaseSpec{}, false
		}
		spec.weight = weight
	case req.Weight < 1 || req.Weight > resources.MaxExplicitWeight:
		return bad("weight must be between 1 and " + strconv.Itoa(resources.MaxExplicitWeight))
	default:
		spec.weight = req.Weight
	}
	if req.WaitS < 0 || req.WaitS > resources.MaxWaitS {
		return bad("wait_s must be between 0 and " + strconv.Itoa(resources.MaxWaitS))
	}
	spec.waitS = req.WaitS
	if spec.waitS == 0 {
		spec.waitS = int(st.Deadline() / time.Second)
	}
	if req.HolderPID <= 0 {
		return bad("holder_pid must be a positive process id")
	}
	switch req.Scope {
	case "":
		spec.scope = resources.ScopeProcess
	case resources.ScopeProcess, resources.ScopeSessionNew:
		spec.scope = req.Scope
	default:
		return bad("scope must be process or session-new")
	}
	if spec.scope == resources.ScopeSessionNew && req.SessionID == "" {
		return bad("scope session-new needs session_id")
	}
	for _, f := range []string{req.SessionID, req.HolderStart, req.ToolUseID, req.Kind} {
		if len(f) > maxFieldLen {
			return bad("a field is longer than " + strconv.Itoa(maxFieldLen) + " bytes")
		}
	}
	return spec, true
}

// handleLeasePost is POST /api/resources/leases.
func (m *Module) handleLeasePost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, bodyCap+1))
	if err != nil || len(body) > bodyCap {
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "body unreadable or over 64 KiB")
		return
	}
	var req resources.LeaseRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "invalid JSON: "+err.Error())
		return
	}
	st := m.settings() // re-read on every POST: a weight or mode change applies to the next one
	spec, ok := validate(w, req, st)
	if !ok {
		return
	}

	// Modes off and measure (and a module that cannot store anything) grant
	// at once and record nothing.
	if m.store == nil || st.Mode == resources.ModeOff || st.Mode == resources.ModeMeasure {
		snap := m.current()
		mode := st.Mode
		if m.store == nil {
			mode = resources.ModeMeasure
		}
		writeJSON(w, http.StatusOK, resources.LeaseResponse{
			State: resources.StateNone, Granted: true, Mode: mode,
			Host: resources.LeaseHost{Measured: snap.Host.Measured, Full: snap.Host.Full},
		})
		return
	}

	holderPID, holderStart, scope, fallback := req.HolderPID, req.HolderStart, spec.scope, false
	if scope == resources.ScopeSessionNew {
		if pid, start, found := m.originOf(r.Context(), req.SessionID); found {
			holderPID, holderStart = pid, start
		} else {
			scope, fallback = resources.ScopeProcess, true
		}
	}

	nowMs := m.now().UnixMilli()
	row := leaseRow{
		ID: m.newID(), ClientID: spec.client, Kind: spec.kind, Weight: spec.weight,
		SessionID: req.SessionID, HolderPID: holderPID, HolderStart: holderStart,
		Scope: scope, ToolUseID: req.ToolUseID,
		CreatedAt:  nowMs,
		DeadlineAt: nowMs + int64(spec.waitS)*1000,
		LeaseUntil: nowMs + resources.LeaseS*1000,
	}

	m.stateMu.Lock()
	got, created, err := m.store.Create(row)
	if err != nil {
		m.stateMu.Unlock()
		m.storageErr(w, "create lease", err)
		return
	}
	if created {
		m.bumpLocked()
		m.passLocked(m.runCtx, st)
	} else if got.State == resources.StateWaiting {
		_ = m.store.RenewLease(got.ID, nowMs+resources.LeaseS*1000)
	}
	cur, _, err := m.store.Get(got.ID)
	pos := m.positionLocked(cur)
	m.stateMu.Unlock()
	if err != nil {
		m.storageErr(w, "read lease after create", err)
		return
	}
	resp := m.leaseResponse(cur, st, pos)
	resp.ScopeFallback = fallback && created
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, resp)
}

// originOf resolves a session's agent process from the registry for scope
// session-new; found is false for an unknown session or any trouble reading
// the registry (the caller then falls back to the process scope).
func (m *Module) originOf(ctx context.Context, sessionID string) (pid int, start string, found bool) {
	if m.roots == nil {
		return 0, "", false
	}
	ctx, cancel := context.WithTimeout(ctx, sampleBudget)
	defer cancel()
	roots, err := m.readRoots(ctx)
	if err != nil {
		m.logf("[resources] resolve session %s: %v", sessionID, err)
		return 0, "", false
	}
	for _, root := range roots {
		if root.SessionID == sessionID {
			return root.PID, root.ProcStart, true
		}
	}
	return 0, "", false
}

// positionLocked is the 1-based queue place of a waiting row, 0 otherwise.
func (m *Module) positionLocked(r leaseRow) int {
	if r.State != resources.StateWaiting {
		return 0
	}
	waiting, err := m.store.Waiting()
	if err != nil {
		return 0
	}
	for i, w := range waiting {
		if w.ID == r.ID {
			return i + 1
		}
	}
	return 0
}

// leaseResponse describes a row. WouldWait is reported in mode advise only.
func (m *Module) leaseResponse(r leaseRow, st resources.Settings, position int) resources.LeaseResponse {
	host := m.current().Host
	resp := resources.LeaseResponse{
		ID: r.ID, State: r.State, Granted: r.GrantedAt != 0, Overrun: r.Overrun,
		Position: position, WaitedMS: r.WaitedMS, EndReason: r.EndReason,
		Host: resources.LeaseHost{Measured: host.Measured, Full: host.Full},
		Mode: st.Mode,
	}
	if r.State == resources.StateWaiting {
		resp.WaitedMS = max(0, m.now().UnixMilli()-r.CreatedAt)
	}
	if st.Mode == resources.ModeAdvise {
		ww := r.WouldWait
		resp.WouldWait = &ww
	}
	return resp
}

// pollWait parses ?wait=N: whole seconds, clamped to the poll cap.
func pollWait(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, errors.New("wait must be a non-negative number of seconds")
	}
	return time.Duration(min(n, resources.MaxPollS)) * time.Second, nil
}

// handleLeaseGet is GET /api/resources/leases/{id}?wait=N, the bounded
// long-poll. Each round takes stateMu, renews a waiting row's lease, reads
// the row and captures the current generation channel; only then does it
// release the lock and wait on that channel, the timer, the request or Stop.
// A transition between the read and the wait closes the captured channel, so
// it cannot be missed, and nothing is kept per row in memory: a restarted
// daemon answers the same poll from the persisted row.
func (m *Module) handleLeaseGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wait, err := pollWait(r.URL.Query().Get("wait"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, err.Error())
		return
	}
	if m.store == nil {
		writeErr(w, http.StatusNotFound, resources.ErrNoLease, "no such lease")
		return
	}
	st := m.settings()
	until := time.Now().Add(wait)
	for {
		m.stateMu.Lock()
		row, found, err := m.store.Get(id)
		if err == nil && found && row.State == resources.StateWaiting {
			err = m.store.RenewLease(id, m.now().UnixMilli()+resources.LeaseS*1000)
		}
		gen := m.gen
		pos := 0
		if err == nil && found {
			pos = m.positionLocked(row)
		}
		m.stateMu.Unlock()
		if m.afterRead != nil {
			m.afterRead(id)
		}
		if err != nil {
			m.storageErr(w, "poll lease "+id, err)
			return
		}
		if !found {
			writeErr(w, http.StatusNotFound, resources.ErrNoLease, "no such lease")
			return
		}
		remaining := time.Until(until)
		if row.State != resources.StateWaiting || remaining <= 0 {
			writeJSON(w, http.StatusOK, m.leaseResponse(row, st, pos))
			return
		}
		timer := time.NewTimer(remaining)
		select {
		case <-gen:
		case <-timer.C:
		case <-r.Context().Done():
			timer.Stop()
			return
		case <-m.runCtx.Done():
			// Stopping: answer the row as it is; the client re-polls and
			// meets the restart.
			timer.Stop()
			writeJSON(w, http.StatusOK, m.leaseResponse(row, st, pos))
			return
		}
		timer.Stop()
	}
}

// handleLeaseDelete is DELETE /api/resources/leases/{id}.
func (m *Module) handleLeaseDelete(w http.ResponseWriter, r *http.Request) {
	if m.store == nil {
		writeErr(w, http.StatusNotFound, resources.ErrNoLease, "no such lease")
		return
	}
	st := m.settings()
	m.stateMu.Lock()
	row, found, err := m.store.Get(r.PathValue("id"))
	if err == nil && !found {
		m.stateMu.Unlock()
		writeErr(w, http.StatusNotFound, resources.ErrNoLease, "no such lease")
		return
	}
	if err == nil {
		row, err = m.releaseLocked(row, st)
	}
	m.stateMu.Unlock()
	if err != nil {
		m.storageErr(w, "delete lease", err)
		return
	}
	writeJSON(w, http.StatusOK, m.leaseResponse(row, st, 0))
}

// handleLeaseDeleteByClient is DELETE /api/resources/leases?client_id=: the
// same by the creator's client id, so a client that lost the create or poll
// answer can still release what it may have been granted. An id the daemon
// never saw is not an error: there is nothing to release.
func (m *Module) handleLeaseDeleteByClient(w http.ResponseWriter, r *http.Request) {
	client := strings.ToLower(r.URL.Query().Get("client_id"))
	if !uuidV4.MatchString(client) {
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "client_id must be a UUID v4")
		return
	}
	st := m.settings()
	if m.store == nil {
		writeJSON(w, http.StatusOK, resources.LeaseResponse{State: resources.StateNone, Mode: st.Mode})
		return
	}
	m.stateMu.Lock()
	row, found, err := m.store.GetByClientID(client)
	if err == nil && found {
		row, err = m.releaseLocked(row, st)
	}
	m.stateMu.Unlock()
	if err != nil {
		m.storageErr(w, "delete lease by client id", err)
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, resources.LeaseResponse{State: resources.StateNone, Mode: st.Mode})
		return
	}
	writeJSON(w, http.StatusOK, m.leaseResponse(row, st, 0))
}

// releaseLocked ends a held row as released or a waiting row as cancelled,
// then runs a pass (the freed capacity or the shorter queue may let someone
// start). An ended row is returned as it is: DELETE is idempotent. stateMu
// must be held. It returns the row as it is afterwards.
func (m *Module) releaseLocked(row leaseRow, st resources.Settings) (leaseRow, error) {
	reason := resources.EndReleased
	switch row.State {
	case resources.StateEnded:
		return row, nil
	case resources.StateWaiting:
		reason = resources.EndCancelled
	}
	won, err := m.endLocked(row.ID, reason)
	if err != nil {
		return row, err
	}
	if won {
		m.passLocked(m.runCtx, st)
	}
	cur, found, err := m.store.Get(row.ID)
	if err != nil || !found {
		return row, err
	}
	return cur, nil
}
