package resourcesmod

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
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
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields() // a misspelt field must not read as "left out"
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if _, err := dec.Token(); err != io.EOF { // nothing may follow the object
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "invalid JSON: unexpected data after the object")
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
	case req.Mode != "" && req.Mode != resources.CallQueue && req.Mode != resources.CallAsk && req.Mode != resources.CallWait && req.Mode != resources.CallAuto:
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "mode must be queue, ask, wait or auto")
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

	// A process-scope request that names no session gets the one whose agent is
	// among the holder's ancestors (`pdx lease run` sends none). Resolved before
	// any lookup, so that a replay and a re-attach see the same session.
	// req keeps what the client sent: the replay check compares a session only if
	// the client gave one, so a lookup that finds nothing one time and a session
	// the next (or the other way round) is still the same request.
	session := req.SessionID
	var agentPID int
	var agentStart string
	if req.Scope != resources.ScopeSessionNew && session == "" {
		var failed bool
		session, agentPID, agentStart, failed = m.sessionOfProcessChecked(r.Context(), req.HolderPID)
		if failed && req.Mode == resources.CallAuto && set.Mode == resources.ModeLease {
			// Auto cannot tell an interactive session from none when the look itself
			// failed, and guessing wait would hold the session. The CLI gives up after
			// its grace and lets the command run: the pool is advice. A retry of a
			// request that already made its row is its replay, whatever the probe says.
			if row, ok, err := m.store.GetByClientID(req.ClientID); err == nil && ok {
				m.replay(w, r, req, row, set)
				return
			}
			writeErr(w, http.StatusServiceUnavailable, resources.ErrNotReady, "could not tell which session the caller is in")
			return
		}
	}

	// An auto request becomes queue for a caller under an interactive session
	// (a notice can reach it) and wait for any other; the row keeps the result.
	mode := req.Mode
	if mode == resources.CallAuto {
		mode = resources.CallWait
		if set.Mode == resources.ModeLease && session != "" && m.sessionInteractive(session) {
			mode = resources.CallQueue
		}
	}

	// A replay returns the row it made, whatever has become of it, but only to
	// a request that is the one it was made for.
	if row, ok, err := m.store.GetByClientID(req.ClientID); err != nil {
		writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "read lease: "+err.Error())
		return
	} else if ok {
		m.replay(w, r, req, row, set)
		return
	}
	if req.Scope != resources.ScopeSessionNew {
		if code, detail := validHolderStart(req.HolderStart); code != "" {
			writeErr(w, http.StatusBadRequest, code, detail)
			return
		}
		// A run below an outer run of the same session is the same job (P2f): held, no row. A replay above has
		// already won, and the request is checked like any other first. A request that names its own session has
		// no agent pid here and is never nested (the mod's foreground lease, plan N3).
		outer, ok, failed := m.nestedUnder(r.Context(), req.HolderPID, agentPID, session)
		if failed {
			writeErr(w, http.StatusServiceUnavailable, resources.ErrNotReady, "could not tell whether the caller runs below a lease")
			return
		}
		if ok {
			m.store.countNested(outer.ID)
			writeJSON(w, http.StatusOK, resources.LeaseResponse{State: resources.StateHeld, Granted: true,
				Host: m.leaseHost(), Mode: set.Mode, Nested: outer.ID})
			return
		}
	}

	now := m.now()
	wait := time.Duration(req.WaitS) * time.Second
	if req.WaitS == 0 {
		wait = set.Deadline()
	}
	row := leaseRow{
		ID: uuid.NewString(), ClientID: req.ClientID, State: resources.StateWaiting, Kind: req.Kind, Weight: weight,
		SessionID: session, HolderPID: req.HolderPID, HolderStart: req.HolderStart, Scope: req.Scope,
		ToolUseID: req.ToolUseID, Parkable: req.Parkable, CreatedAt: now.UnixMilli(), DeadlineAt: now.Add(wait).UnixMilli(),
		LeaseUntil: now.Add(resources.LeaseS * time.Second).UnixMilli(),
		Mode:       mode,
		Command:    resources.CutCommand(req.Command, resources.MaxCommandBytes),
		Cwd:        resources.CutCommand(req.Cwd, resources.MaxCwdBytes),
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
			agentPID, agentStart = pid, start
		} else {
			row.Scope, fallback = resources.ScopeProcess, true
			if code, detail := validHolderStart(req.HolderStart); code != "" {
				writeErr(w, http.StatusBadRequest, code, detail+" (the session is unknown, so the holder process is what is tracked)")
				return
			}
		}
	}

	// Lease mode, a caller that asked for queue or ask, and a session: the
	// request belongs to that session's position (amendment A §2.3).
	if set.Mode == resources.ModeLease && session != "" && (mode == resources.CallQueue || mode == resources.CallAsk) {
		m.createPositioned(w, r, req, row, agentPID, agentStart, set, fallback)
		return
	}

	m.stateMu.Lock()
	got, created, err := m.store.Create(row)
	m.stateMu.Unlock()
	if err == nil && created {
		m.requestEvent() // a new waiter shows in the queue
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "create lease: "+err.Error())
		return
	}
	if !created {
		// Another POST of the same client id won the insert between the
		// lookup and the create: it is a replay after all.
		m.replay(w, r, req, got, set)
		return
	}
	if got.State == resources.StateWaiting {
		m.admissionPass(r.Context(), got.ID)
		var ok bool
		if got, ok, err = m.store.Get(got.ID); err != nil || !ok {
			writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "read lease after admission failed")
			return
		}
	}
	writeJSON(w, http.StatusCreated, m.leaseResponse(got, set, fallback))
}

// validHolderStart requires the start time the sweeper judges the holder by: a
// start text that does not parse would make the holder count as alive until
// max_hold.
func validHolderStart(s string) (code, detail string) {
	if s == "" {
		return resources.ErrBadRequest, "holder_start is required"
	}
	if t, err := ipeers.ParseProcStart(s); err != nil || t.IsZero() {
		return resources.ErrBadRequest, "holder_start must be a process start time in the registry's UTC form"
	}
	return "", ""
}

// replay answers a POST whose client id already has a row: the row, when the
// request is the one it was made for (kind or weight, session, tool use, and
// the holder unless the row is a session-new one), else 409 client_id_reused.
func (m *Module) replay(w http.ResponseWriter, r *http.Request, req resources.LeaseRequest, row leaseRow, set resources.Settings) {
	// A process row keeps the session found when it was made (or none); req.SessionID
	// is only what the client itself sent, so a replay is the same request whatever
	// the lookup found this time or the time before.
	sessionOK := row.SessionID == req.SessionID || (req.SessionID == "" && row.Scope == resources.ScopeProcess)
	same := sessionOK && row.ToolUseID == req.ToolUseID && modeReplays(req.Mode, row.Mode)
	if req.Kind != "" {
		same = same && row.Kind == req.Kind
	} else {
		same = same && row.Kind == "" && row.Weight == req.Weight
	}
	// A process request replays a process row only. A session-new request
	// replays a session-new row (the resolved agent process is not compared)
	// or, when the session was unknown, the process row that tracks its
	// holder (which is compared).
	switch {
	case req.Scope == resources.ScopeSessionNew && row.Scope == resources.ScopeSessionNew:
	case req.Scope == resources.ScopeSessionNew || req.Scope == "" || req.Scope == resources.ScopeProcess:
		same = same && row.Scope == resources.ScopeProcess && row.HolderPID == req.HolderPID && row.HolderStart == req.HolderStart
	}
	if !same {
		writeErr(w, http.StatusConflict, resources.ErrClientIDReused, "this client_id was used for a different request")
		return
	}
	if row.State == resources.StateWaiting {
		m.admissionPass(r.Context(), row.ID)
		var ok bool
		var err error
		if row, ok, err = m.store.Get(row.ID); err != nil || !ok {
			writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "read lease after admission failed")
			return
		}
	}
	// A session-new request whose row tracks a plain process is the fallback.
	fallback := req.Scope == resources.ScopeSessionNew && row.Scope == resources.ScopeProcess
	writeJSON(w, http.StatusOK, m.leaseResponse(row, set, fallback))
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
	ctx, cancel := context.WithTimeout(r.Context(), sampleBudget)
	defer cancel()
	snap, err := m.procSnapshot(ctx)
	if err != nil {
		return 0, "", false
	}
	roots, err := m.roots.ProcessRoots(snap)
	if err != nil {
		return 0, "", false
	}
	for _, root := range roots {
		if root.SessionID == sessionID {
			return root.PID, root.ProcStart, true // the registry's own text: UTC
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
	// The client that polls names itself. If the row has been taken over by a
	// newer call (re-attach), the answer is "replaced" and nothing is renewed.
	// An older client sends none and is treated as before.
	cid := r.URL.Query().Get("client_id")
	if cid != "" && !validClientID(cid) {
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "client_id must be a lower-case UUID v4")
		return
	}
	timer := time.NewTimer(time.Duration(wait) * time.Second)
	defer timer.Stop()
	final := false // the wait is over: answer with the row as it is now
	for {
		// The row is read and the generation captured under one lock hold: a
		// transition after this read closes the channel taken here, so it
		// cannot slip between the read and the wait.
		m.stateMu.Lock()
		row, ok, err := m.store.Get(id)
		gen := m.genChan()
		replaced := err == nil && ok && cid != "" && row.ClientID != cid
		if err == nil && ok && !replaced && row.State == resources.StateWaiting {
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
		case replaced:
			writeJSON(w, http.StatusOK, resources.LeaseResponse{ID: row.ID, State: resources.StateReplaced, Mode: m.settings().Mode, Host: m.leaseHost()})
			return
		case row.State != resources.StateWaiting || final:
			writeJSON(w, http.StatusOK, m.leaseResponse(row, m.settings(), false))
			return
		}
		if m.pollHook != nil {
			m.pollHook()
		}
		select {
		case <-gen:
		case <-timer.C:
			final = true // a grant that landed with the timer is not missed: read again
		case <-r.Context().Done():
			return
		case <-m.runCtx.Done():
			final = true
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
	finalNotice := ""
	m.stateMu.Lock()
	row, ok, err := find()
	if err == nil && ok && row.State != resources.StateEnded {
		reason := resources.EndReleased
		if row.State == resources.StateWaiting {
			reason = resources.EndCancelled
		}
		var won bool
		now := m.now()
		if row.State == resources.StateWaiting && row.PositionID != "" {
			won, err = m.store.EndRowMaybePosition(row.ID, reason, now.UnixMilli(), m.reasons.peek(row.ID, now))
		} else if row.State == resources.StateWaiting {
			won, err = m.store.EndWaiting(row.ID, reason, now.UnixMilli(), m.reasons.peek(row.ID, now))
		} else {
			won, finalNotice, err = m.endCommand(row, reason, now, r.URL.Query().Get("notice") == "1", "")
		}
		if err == nil {
			if won {
				m.reasons.drop(row.ID)
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
	resp := m.leaseResponse(row, m.settings(), false)
	resp.Notice = finalNotice
	if resp.Notice == "" && row.State == resources.StateEnded && r.URL.Query().Get("notice") == "1" {
		// A retry of a release whose answer was lost: the notice is on the row.
		resp.Notice = row.Notice
	}
	writeJSON(w, http.StatusOK, resp)
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
		Warning: m.warnFor(row),
	}
	if set.Mode == resources.ModeAdvise {
		ww := row.WouldWait
		resp.WouldWait = &ww
	}
	resp.PositionID = row.PositionID
	if row.State == resources.StateWaiting {
		resp.WaitedMS = max(0, m.now().UnixMilli()-row.CreatedAt)
		resp.Position = m.queuePlace(row)
		if isQueueRow(row) {
			// A queue-mode request that did not run is answered at once, never held.
			resp.State, resp.QueuedReason = resources.StateQueued, m.queuedReasonOf(row)
		}
	}
	return resp
}

// queuePlace is the 1-based place of a waiting row in the queue, counting queued
// positions and wait-mode rows together by created_at; 0 for a row that is not in
// the queue (it waits at its own position, which is not queued).
func (m *Module) queuePlace(row leaseRow) int {
	type entry struct {
		key       string
		createdAt int64
		row       bool // a wait-mode row; on the same millisecond a position goes first, as the pass orders them
	}
	var entries []entry
	positions, err := m.store.LivePositions()
	if err != nil {
		return 0
	}
	for _, p := range positions {
		if p.State == resources.PosQueued {
			entries = append(entries, entry{posKey(p.ID), p.CreatedAt, false})
		}
	}
	waiting, err := m.store.Waiting()
	if err != nil {
		return 0
	}
	for _, w := range waiting {
		if w.PositionID == "" {
			entries = append(entries, entry{w.ID, w.CreatedAt, true})
		}
	}
	slices.SortStableFunc(entries, func(a, b entry) int {
		if a.createdAt != b.createdAt {
			return cmp.Compare(a.createdAt, b.createdAt)
		}
		if a.row != b.row {
			return cmp.Compare(b2i(a.row), b2i(b.row))
		}
		return cmp.Compare(a.key, b.key)
	})
	key := row.ID
	if row.PositionID != "" {
		key = posKey(row.PositionID)
	}
	for i, e := range entries {
		if e.key == key {
			return i + 1
		}
	}
	return 0
}

// queuedReasonOf says why a waiting queue-mode row did not run: it waits behind
// the queue, or behind its own session's running command.
func (m *Module) queuedReasonOf(row leaseRow) string {
	if pos, ok, err := m.store.PositionByID(row.PositionID); err == nil && ok && pos.State == resources.PosRunning {
		return resources.QueuedOwnRunning
	}
	return resources.QueuedBehindQueue
}

// modeReplays says whether a request of mode req may replay a row made with mode
// row. An auto request replays the row it resolved to (queue or wait), never an
// ask row: auto cannot make one.
func modeReplays(req, row string) bool {
	if req == resources.CallAuto {
		c := callMode(row)
		return c == resources.CallQueue || c == resources.CallWait
	}
	return callMode(row) == callMode(req)
}

// callMode reads an empty mode as wait: rows from before amendment A and
// clients that name no mode are the same kind of request as an explicit wait.
func callMode(m string) string {
	if m == "" {
		return resources.CallWait
	}
	return m
}
