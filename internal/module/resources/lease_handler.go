package resourcesmod

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/resources"
)

// The lease routes (spec D-3/D-4, plan Task 1.4, P1-D2). A route that changes
// a row does it under stateMu (the one serialization boundary), wakes the
// pollers, and then runs the admission pass: a release frees capacity, a new
// request may fit at once. A long poll never holds stateMu while it waits.

const leaseBodyCap = 64 << 10

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, resources.APIError{Error: code, Detail: detail})
}

// validClientID is a lower-case UUID v4: what the mod and the CLI generate.
func validClientID(id string) bool {
	return ipeers.IsUUID(id) && id[14] == '4'
}

// handleLeaseCreate is POST /api/resources/leases. Modes off and measure
// grant at once and record nothing; advise and lease record a row, and the
// pass decides it. A replay of a client id answers with the same row.
func (m *Module) handleLeaseCreate(w http.ResponseWriter, r *http.Request) {
	var req resources.LeaseRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, leaseBodyCap+1))
	if err != nil || len(body) > leaseBodyCap {
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "body unreadable or over 64 KiB")
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "invalid JSON: "+err.Error())
		return
	}
	set := m.settings()
	weight, code, detail := m.requestWeight(req, set)
	switch {
	case code != "":
		writeErr(w, http.StatusBadRequest, code, detail)
		return
	case !validClientID(req.ClientID):
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "client_id must be a lower-case UUID v4")
		return
	case req.WaitS < 0 || req.WaitS > resources.MaxWaitS:
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "wait_s must be between 0 and "+strconv.Itoa(resources.MaxWaitS))
		return
	case req.Scope != "" && req.Scope != resources.ScopeProcess && req.Scope != resources.ScopeSessionNew:
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "scope must be process or session-new")
		return
	case req.HolderPID <= 0:
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "holder_pid is required")
		return
	case req.Scope == resources.ScopeSessionNew && req.SessionID == "":
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "scope session-new needs session_id")
		return
	}

	// Modes off and measure: nothing is recorded and the request is granted.
	if set.Mode != resources.ModeLease && set.Mode != resources.ModeAdvise {
		writeJSON(w, http.StatusOK, resources.LeaseResponse{State: resources.StateNone, Granted: true, Host: m.leaseHost(), Mode: set.Mode})
		return
	}
	if m.store == nil {
		writeErr(w, http.StatusServiceUnavailable, resources.ErrNotReady, "resources.db is not open")
		return
	}

	// A replay returns the row it made, whatever has become of it.
	if row, ok, err := m.store.GetByClientID(req.ClientID); err != nil {
		writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "read lease: "+err.Error())
		return
	} else if ok {
		if row.State == resources.StateWaiting {
			m.admissionPass(r.Context(), row.ID)
			row, _, _ = m.store.Get(row.ID)
		}
		writeJSON(w, http.StatusOK, m.leaseResponse(row, set, false))
		return
	}

	now := m.now()
	wait := time.Duration(req.WaitS) * time.Second
	if req.WaitS == 0 {
		wait = set.Deadline()
	}
	row := leaseRow{
		ID: uuid.NewString(), ClientID: req.ClientID, State: resources.StateWaiting, Kind: req.Kind, Weight: weight,
		SessionID: req.SessionID, HolderPID: req.HolderPID, HolderStart: req.HolderStart, Scope: req.Scope,
		ToolUseID: req.ToolUseID, CreatedAt: now.UnixMilli(), DeadlineAt: now.Add(wait).UnixMilli(),
		LeaseUntil: now.Add(resources.LeaseS * time.Second).UnixMilli(),
	}
	if row.Scope == "" {
		row.Scope = resources.ScopeProcess
	}
	fallback := false
	if row.Scope == resources.ScopeSessionNew {
		// The agent process of the session is what a session-new lease
		// measures under; a session the registry does not know falls back to
		// the holder pid itself (and says so).
		if pid, start, ok := m.sessionRoot(r, req.SessionID); ok {
			row.HolderPID, row.HolderStart = pid, start
		} else {
			row.Scope, fallback = resources.ScopeProcess, true
		}
	}

	m.stateMu.Lock()
	got, created, err := m.store.Create(row)
	m.stateMu.Unlock()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "create lease: "+err.Error())
		return
	}
	if got.State == resources.StateWaiting {
		m.admissionPass(r.Context(), got.ID)
		got, _, _ = m.store.Get(got.ID)
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, m.leaseResponse(got, set, fallback))
}

// requestWeight resolves exactly one of kind and weight; code is "" on
// success.
func (m *Module) requestWeight(req resources.LeaseRequest, set resources.Settings) (weight int, code, detail string) {
	switch {
	case req.Kind != "" && req.Weight != 0:
		return 0, resources.ErrBadRequest, "give kind or weight, not both"
	case req.Kind == "" && req.Weight == 0:
		return 0, resources.ErrBadRequest, "kind or weight is required"
	case req.Kind != "":
		wgt, ok := set.Weight(req.Kind)
		if !ok {
			return 0, resources.ErrUnknownKind, "unknown kind " + strconv.Quote(req.Kind)
		}
		return wgt, "", ""
	case req.Weight < 1 || req.Weight > resources.MaxExplicitWeight:
		return 0, resources.ErrBadRequest, "weight must be between 1 and " + strconv.Itoa(resources.MaxExplicitWeight)
	}
	return req.Weight, "", ""
}

// sessionRoot finds the session's agent process in the registry: its pid and
// start text. The process table is read without a fork.
func (m *Module) sessionRoot(r *http.Request, sessionID string) (pid int, start string, ok bool) {
	if m.roots == nil {
		return 0, "", false
	}
	snap, err := m.procSnapshot(r.Context())
	if err != nil {
		return 0, "", false
	}
	roots, err := m.roots.ProcessRoots(snap)
	if err != nil {
		return 0, "", false
	}
	for _, root := range roots {
		if root.SessionID == sessionID {
			return root.PID, root.ProcStart, true
		}
	}
	return 0, "", false
}

// handleLeaseGet is GET /api/resources/leases/{id}?wait=N: a poll that
// returns at once when the row is held or ended, and otherwise waits for a
// state change, up to N seconds (at most MaxPollS). Every poll of a waiting
// row renews its lease.
func (m *Module) handleLeaseGet(w http.ResponseWriter, r *http.Request) {
	if m.store == nil {
		writeErr(w, http.StatusNotFound, resources.ErrNoLease, "no such lease")
		return
	}
	id := r.PathValue("id")
	wait := 0
	if q := r.URL.Query().Get("wait"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "wait must be a number of seconds")
			return
		}
		wait = min(n, resources.MaxPollS)
	}
	timer := time.NewTimer(time.Duration(wait) * time.Second)
	defer timer.Stop()
	for {
		// The row is read and the generation captured under one lock hold: a
		// transition after this read closes the channel taken here, so it
		// cannot slip between the read and the wait.
		m.stateMu.Lock()
		row, ok, err := m.store.Get(id)
		gen := m.genChan()
		if err == nil && ok && row.State == resources.StateWaiting {
			if rerr := m.store.RenewLease(id, m.now().Add(resources.LeaseS*time.Second).UnixMilli()); rerr != nil {
				m.logf("[resources] renew lease %s: %v", id, rerr)
			}
		}
		m.stateMu.Unlock()
		switch {
		case err != nil:
			writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "read lease: "+err.Error())
			return
		case !ok:
			writeErr(w, http.StatusNotFound, resources.ErrNoLease, "no such lease")
			return
		case row.State != resources.StateWaiting:
			writeJSON(w, http.StatusOK, m.leaseResponse(row, m.settings(), false))
			return
		}
		if m.pollHook != nil {
			m.pollHook()
		}
		select {
		case <-gen:
		case <-timer.C:
			writeJSON(w, http.StatusOK, m.leaseResponse(row, m.settings(), false))
			return
		case <-r.Context().Done():
			return
		case <-m.runCtx.Done():
			writeJSON(w, http.StatusOK, m.leaseResponse(row, m.settings(), false))
			return
		}
	}
}

// handleLeaseDelete is DELETE /api/resources/leases/{id}: released for a held
// lease, cancelled for a waiting one; an ended one is answered as it is.
func (m *Module) handleLeaseDelete(w http.ResponseWriter, r *http.Request) {
	if m.store == nil {
		writeErr(w, http.StatusNotFound, resources.ErrNoLease, "no such lease")
		return
	}
	m.endLease(w, r, func() (leaseRow, bool, error) { return m.store.Get(r.PathValue("id")) }, true)
}

// handleLeaseDeleteByClient is DELETE /api/resources/leases?client_id=: the
// same by the creator's client id, so that a client that lost the answer can
// still release what it may have been granted. An id the daemon never saw is
// not an error.
func (m *Module) handleLeaseDeleteByClient(w http.ResponseWriter, r *http.Request) {
	cid := r.URL.Query().Get("client_id")
	if !validClientID(cid) {
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "client_id must be a lower-case UUID v4")
		return
	}
	if m.store == nil {
		writeJSON(w, http.StatusOK, resources.LeaseResponse{State: resources.StateNone, Mode: m.settings().Mode})
		return
	}
	m.endLease(w, r, func() (leaseRow, bool, error) { return m.store.GetByClientID(cid) }, false)
}

// endLease ends the row find returns and answers with it. notFound404 says
// whether a missing row is a 404 (by id) or the state none (by client id).
func (m *Module) endLease(w http.ResponseWriter, r *http.Request, find func() (leaseRow, bool, error), notFound404 bool) {
	m.stateMu.Lock()
	row, ok, err := find()
	if err == nil && ok && row.State != resources.StateEnded {
		reason := resources.EndReleased
		if row.State == resources.StateWaiting {
			reason = resources.EndCancelled
		}
		var won bool
		if won, err = m.store.End(row.ID, reason, m.now().UnixMilli()); err == nil {
			if won {
				m.logf("[resources] lease %s (%s) ended: %s", row.ID, row.Kind, reason)
				m.wake()
			}
			row, _, err = m.store.Get(row.ID)
		}
	}
	m.stateMu.Unlock()
	switch {
	case err != nil:
		writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "end lease: "+err.Error())
		return
	case !ok && notFound404:
		writeErr(w, http.StatusNotFound, resources.ErrNoLease, "no such lease")
		return
	case !ok:
		writeJSON(w, http.StatusOK, resources.LeaseResponse{State: resources.StateNone, Mode: m.settings().Mode})
		return
	}
	// The capacity just freed may let the next request in.
	m.admissionPass(r.Context(), "")
	writeJSON(w, http.StatusOK, m.leaseResponse(row, m.settings(), false))
}

// leaseHost is the host figure a lease answer carries.
func (m *Module) leaseHost() resources.LeaseHost {
	if s := m.latest.Load(); s != nil && s.Available {
		return resources.LeaseHost{Measured: s.Host.Measured, Full: s.Host.Full}
	}
	return resources.LeaseHost{}
}

// leaseResponse describes a row. In mode advise would_wait says whether lease
// mode would have held the request back.
func (m *Module) leaseResponse(row leaseRow, set resources.Settings, fallback bool) resources.LeaseResponse {
	resp := resources.LeaseResponse{
		ID: row.ID, State: row.State, Granted: row.GrantedAt != 0, Overrun: row.Overrun,
		WaitedMS: row.WaitedMS, Host: m.leaseHost(), Mode: set.Mode, ScopeFallback: fallback, EndReason: row.EndReason,
	}
	if set.Mode == resources.ModeAdvise {
		ww := row.WouldWait
		resp.WouldWait = &ww
	}
	if row.State == resources.StateWaiting {
		resp.WaitedMS = max(0, m.now().UnixMilli()-row.CreatedAt)
		if waiting, err := m.store.Waiting(); err == nil {
			for i, wr := range waiting {
				if wr.ID == row.ID {
					resp.Position = i + 1
					break
				}
			}
		}
	}
	return resp
}
