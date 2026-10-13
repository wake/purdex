package resourcesmod

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/wake/purdex/internal/resources"
)

// The routes a session uses on its own position (amendment A §5): confirm a
// background command whose turn came, cancel a request or the whole position, and
// release the position early. Each acts on the caller's own session only: the
// caller is the agent behind holder_pid (the P2a ancestry walk), and another
// session's target is a 403. That guards against mistakes, as #2774 does; it is
// not a defence against a token holder.

// callerSession reads the body and resolves the caller's session. It answers the
// error itself and reports ok = false when there is no session to act for.
func (m *Module) callerSession(w http.ResponseWriter, r *http.Request) (string, bool) {
	if m.store == nil {
		writeErr(w, http.StatusServiceUnavailable, resources.ErrNotReady, "resources.db is not open")
		return "", false
	}
	var req resources.CallerRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, leaseBodyCap+1))
	if err != nil || len(body) > leaseBodyCap {
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "body unreadable or over 64 KiB")
		return "", false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || req.HolderPID <= 0 {
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "holder_pid is required")
		return "", false
	}
	session, _, _ := m.sessionOfProcess(r.Context(), req.HolderPID)
	if session == "" {
		writeErr(w, http.StatusForbidden, resources.ErrNoSession, "the caller is not under an agent of this host")
		return "", false
	}
	return session, true
}

// ownLease finds the row of the route and checks it is the caller's.
func (m *Module) ownLease(w http.ResponseWriter, r *http.Request, session string) (leaseRow, bool) {
	row, ok, err := m.store.Get(r.PathValue("id"))
	switch {
	case err != nil:
		writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "read lease: "+err.Error())
		return leaseRow{}, false
	case !ok:
		writeErr(w, http.StatusNotFound, resources.ErrNoLease, "no such lease")
		return leaseRow{}, false
	case row.SessionID != session:
		writeErr(w, http.StatusForbidden, resources.ErrNotYourSession, "this request is not of your session")
		return leaseRow{}, false
	}
	return row, true
}

// handleLeaseConfirm is POST /api/resources/leases/{id}/confirm: the session says
// yes to a background command whose turn came.
func (m *Module) handleLeaseConfirm(w http.ResponseWriter, r *http.Request) {
	session, ok := m.callerSession(w, r)
	if !ok {
		return
	}
	row, ok := m.ownLease(w, r, session)
	if !ok {
		return
	}
	set := m.settings()
	if row.State != resources.StateWaiting {
		writeJSON(w, http.StatusOK, m.leaseResponse(row, set, false)) // held or ended: nothing to confirm
		return
	}
	if row.Mode != resources.CallAsk {
		writeErr(w, http.StatusBadRequest, resources.ErrBadRequest, "only a background (ask) request is confirmed")
		return
	}
	m.stateMu.Lock()
	pos, found, err := m.store.PositionByID(row.PositionID)
	if err == nil && found && pos.State == resources.PosQueued {
		m.stateMu.Unlock()
		writeErr(w, http.StatusConflict, resources.ErrNotYourTurn, "your turn has not come yet")
		return
	}
	if err == nil {
		err = m.store.ConfirmRow(row.ID, m.now().UnixMilli())
	}
	m.stateMu.Unlock()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "confirm: "+err.Error())
		return
	}
	m.wake()
	m.admissionPass(r.Context(), row.ID) // the confirmed command starts at once if it may
	m.answerLease(w, r, row.ID, set)
}

// handleLeaseCancel is POST /api/resources/leases/{id}/cancel: the session
// withdraws a request that has not run.
func (m *Module) handleLeaseCancel(w http.ResponseWriter, r *http.Request) {
	session, ok := m.callerSession(w, r)
	if !ok {
		return
	}
	row, ok := m.ownLease(w, r, session)
	if !ok {
		return
	}
	set := m.settings()
	now := m.now()
	if m.routeHook != nil {
		m.routeHook()
	}
	m.stateMu.Lock()
	cur, found, err := m.store.Get(row.ID) // the row as it is under the lock: a pass may have started it
	switch {
	case err != nil || !found:
	case cur.State == resources.StateWaiting:
		if cur.PositionID != "" {
			_, err = m.store.EndRowMaybePosition(cur.ID, resources.EndCancelledBySession, now.UnixMilli(), m.reasons.peek(cur.ID, now))
		} else {
			_, err = m.store.EndWaiting(cur.ID, resources.EndCancelledBySession, now.UnixMilli(), m.reasons.peek(cur.ID, now))
		}
		if err == nil {
			m.reasons.drop(cur.ID)
			cur, _, err = m.store.Get(cur.ID)
		}
	}
	m.stateMu.Unlock()
	switch {
	case err != nil || !found:
		writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "cancel failed")
		return
	case cur.State == resources.StateHeld:
		writeErr(w, http.StatusConflict, resources.ErrRunning, "the command is running; it is released when it ends")
		return
	}
	m.wake()
	m.admissionPass(r.Context(), "") // a place may have opened
	writeJSON(w, http.StatusOK, m.leaseResponse(cur, set, false))
}

// answerLease writes the row as it is now.
func (m *Module) answerLease(w http.ResponseWriter, r *http.Request, id string, set resources.Settings) {
	row, ok, err := m.store.Get(id)
	if err != nil || !ok {
		writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "read lease failed")
		return
	}
	writeJSON(w, http.StatusOK, m.leaseResponse(row, set, false))
}

// positionAnswer describes a position after one of the routes.
func positionAnswer(p positionRow) resources.PositionResponse {
	return resources.PositionResponse{ID: p.ID, State: p.State, EndReason: p.EndReason, DonePending: p.DoneRequested && p.State == resources.PosRunning}
}

// handlePositionCancel is POST /api/resources/positions/{id}/cancel: the session
// gives its place up. A running position is not cancelled (Q10): its command ends
// by itself and `done` releases the position.
func (m *Module) handlePositionCancel(w http.ResponseWriter, r *http.Request) {
	session, ok := m.callerSession(w, r)
	if !ok {
		return
	}
	if m.routeHook != nil {
		m.routeHook()
	}
	m.stateMu.Lock()
	pos, found, err := m.store.PositionByID(r.PathValue("id"))
	var ended bool
	if err == nil && found && pos.SessionID == session && (pos.State == resources.PosQueued || pos.State == resources.PosOffered || pos.State == resources.PosIdle) {
		ended, err = m.endPositionLocked(pos, resources.EndCancelledBySession)
		if err == nil {
			pos, _, err = m.store.PositionByID(pos.ID)
		}
	}
	m.stateMu.Unlock()
	switch {
	case err != nil:
		writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "cancel position: "+err.Error())
		return
	case !found:
		writeErr(w, http.StatusNotFound, resources.ErrNoPosition, "no such position")
		return
	case pos.SessionID != session:
		writeErr(w, http.StatusForbidden, resources.ErrNotYourSession, "this position is not of your session")
		return
	case pos.State == resources.PosRunning:
		writeErr(w, http.StatusConflict, resources.ErrRunning, "a command is running; use done to release the position after it")
		return
	}
	if ended {
		m.wake()
		m.admissionPass(r.Context(), "") // the next in line may be offered
	}
	writeJSON(w, http.StatusOK, positionAnswer(pos))
}

// handlePositionDone is POST /api/resources/positions/done: the session lets its
// own position go early. Idle, offered or queued ends at once; running ends when
// its last command does, without a window.
func (m *Module) handlePositionDone(w http.ResponseWriter, r *http.Request) {
	session, ok := m.callerSession(w, r)
	if !ok {
		return
	}
	if m.routeHook != nil {
		m.routeHook()
	}
	m.stateMu.Lock()
	pos, found, err := m.store.LivePositionOf(session)
	var ended bool
	if err == nil && found {
		if pos.State == resources.PosRunning {
			_, err = m.store.MarkDone(pos.ID)
		} else {
			ended, err = m.endPositionLocked(pos, resources.EndDone)
		}
		if err == nil {
			pos, _, err = m.store.PositionByID(pos.ID)
		}
	}
	m.stateMu.Unlock()
	switch {
	case err != nil:
		writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "done: "+err.Error())
		return
	case !found:
		writeJSON(w, http.StatusOK, resources.PositionResponse{State: resources.StateNone})
		return
	}
	if ended {
		m.wake()
		m.admissionPass(r.Context(), "") // the next in line may be offered
	}
	writeJSON(w, http.StatusOK, positionAnswer(pos))
}

// endPositionLocked ends a position that is queued, offered or idle together with
// its waiting rows. stateMu is held; a position a pass started in the meantime is
// not ended (the compare-and-set loses) and ended is false.
func (m *Module) endPositionLocked(pos positionRow, reason string) (ended bool, err error) {
	ended, err = m.store.EndPositionWithRows(pos.ID, m.now().UnixMilli(), endPosition{Reason: reason,
		From: []string{resources.PosQueued, resources.PosOffered, resources.PosIdle}})
	if err == nil && ended {
		m.flushWindowCost(pos.ID)
		m.writePendingCosts()
	}
	return ended, err
}
