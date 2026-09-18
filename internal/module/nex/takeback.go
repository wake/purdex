package nex

// POST /api/sessions/{code}/nex-takeback (spec §4.4 "Daemon: nex-takeback"):
// the reverse of handoff.go — settle the execution, resume its Claude Code
// session in the tmux pane, and archive the execution. Shares the
// per-session lock, the error shape and waitForCC with the handoff.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/store"

	"github.com/wake/purdex/internal/tmux"
)

// takebackRequest is the body of POST /api/sessions/{code}/nex-takeback.
// ResumeCommand is the host's cc resume template with the session id left
// as `{id}`; LeaseID is the control lease the caller already holds on the
// execution, if any — with one, the daemon interrupts under it and never
// releases it.
type takebackRequest struct {
	ExpectedTmuxInstance string `json:"expected_tmux_instance"`
	ExecutionID          string `json:"execution_id"`
	ResumeCommand        string `json:"resume_command"`
	LeaseID              string `json:"lease_id,omitempty"`
}

// settled reports whether an execution state is one a take-back may resume
// from: the transcript has no writer, so the terminal can become one.
func settled(s store.State) bool {
	return s == store.StateIdle || s == store.StateFailed || s == store.StateTerminated
}

// boundToSession reports whether exec is the execution a handoff of
// session code on this host created: its handoff_session label names the
// code and its origin is handoffOrigin(hostID, code). Labels are the
// canonical JSON text the store keeps; a value that does not decode to an
// object is not bound (never "bound by default").
func boundToSession(exec store.Execution, hostID, code string) bool {
	var labels map[string]string
	if err := json.Unmarshal([]byte(exec.Labels), &labels); err != nil {
		return false
	}
	return labels[handoffSessionLabel] == code && exec.Origin == handoffOrigin(hostID, code)
}

// handleNexTakeback resumes an execution's Claude Code session in its tmux
// pane and archives the execution. Every check that needs no execution
// access runs first — session, generation, "is CC already back?" — so a
// stale tab cannot interrupt a running execution it can no longer resume
// (spec §4.4 step 1). Only then is the row read, and a running execution
// interrupted under a lease.
func (m *Module) handleNexTakeback(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	if m.sys.service == nil || m.sys.store == nil || m.opts.Config == nil {
		msg := "nex engine unavailable"
		if m.initErr != nil {
			msg = m.initErr.Error()
		}
		writeHandoffError(w, http.StatusServiceUnavailable, "nex_unavailable", msg, nil)
		return
	}

	var body takebackRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeHandoffError(w, http.StatusBadRequest, "malformed_body", "invalid request body: "+err.Error(), nil)
		return
	}
	if !tmux.ValidInstance(body.ExpectedTmuxInstance) {
		writeHandoffError(w, http.StatusBadRequest, "invalid_instance", "invalid expected_tmux_instance", nil)
		return
	}
	if body.ExecutionID == "" {
		writeHandoffError(w, http.StatusBadRequest, "missing_execution_id", "execution_id is required", nil)
		return
	}
	if body.ResumeCommand == "" {
		writeHandoffError(w, http.StatusBadRequest, "missing_resume_command", "resume_command is required", nil)
		return
	}
	principal, err := m.principal(r)
	if err != nil {
		writeHandoffError(w, http.StatusInternalServerError, "principal_unresolved", err.Error(), nil)
		return
	}

	if !m.locks.TryLock(code) {
		writeHandoffError(w, http.StatusConflict, "handoff_in_progress", "a handoff is already in progress for this session", nil)
		return
	}
	defer m.locks.Unlock(code)

	// Preflight: nothing below touches the execution.
	sess, err := m.sessions.GetSession(code)
	if err != nil {
		writeHandoffError(w, http.StatusInternalServerError, "session_lookup_failed", err.Error(), nil)
		return
	}
	if sess == nil {
		writeHandoffError(w, http.StatusNotFound, "session_missing", "session not found", nil)
		return
	}
	expected := body.ExpectedTmuxInstance
	if m.sessions.TmuxInstance() != expected {
		writeHandoffError(w, http.StatusConflict, "tmux_instance_mismatch", "session belongs to another tmux generation", nil)
		return
	}
	target := paneTarget(sess)
	if m.prober.IsAliveFor("cc", target) {
		writeHandoffError(w, http.StatusConflict, "cc_already_running", "Claude Code is already running in the pane", nil)
		return
	}

	// Every engine call below runs under detachedContext(r.Context(), …),
	// as Delegate does in the handoff: from the first engine call on, a
	// client that disconnects must not cancel the sequence halfway — an
	// interrupted execution whose pane never received its resume, or an
	// acquired lease whose release was cancelled, is worse than finishing
	// for a caller who is no longer listening. Each call has its own
	// budget, so an engine that never answers ends in a 500 and the lock
	// is released; release and archive get fresh contexts of their own so
	// an expired interrupt budget cannot take them down with it.
	parent := r.Context()
	execID := body.ExecutionID

	exec, err := m.getExecution(parent, execID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeHandoffError(w, http.StatusNotFound, "execution_not_found", "execution not found", nil)
			return
		}
		writeHandoffError(w, http.StatusInternalServerError, "store_error", "reading execution: "+err.Error(), nil)
		return
	}

	// Step 1b: the execution must be the one a handoff of THIS session
	// created — label and origin both, as the handoff wrote them. Another
	// session's execution, one launched from the Headless section, or a row
	// from another host is refused before any lease, interrupt, send or
	// archive: the caller cannot resume it in this pane, so stopping it
	// would only strand it.
	if !boundToSession(exec, m.opts.Config.HostID, code) {
		writeHandoffError(w, http.StatusConflict, "execution_not_bound", "execution is not bound to this session",
			map[string]any{"execution_id": execID, "session_code": code})
		return
	}

	if exec.State == store.StateRunning {
		leaseID := body.LeaseID
		if leaseID == "" {
			lease, err := m.acquireLease(parent, execID, principal)
			if err != nil {
				if errors.Is(err, store.ErrLeaseHeld) {
					writeHandoffError(w, http.StatusConflict, "held_by", "execution lease is held by "+exec.LeasePrincipalID,
						map[string]any{"principal": exec.LeasePrincipalID})
					return
				}
				writeHandoffError(w, http.StatusInternalServerError, "lease_error", "acquiring lease: "+err.Error(), nil)
				return
			}
			leaseID = lease.ID
			// Released on every path out of here — unconfirmed, failed,
			// store error, success — so the daemon never sits on a lease
			// the SPA would then have to wait out. A caller-provided lease
			// stays the caller's. Fresh context: the interrupt's may have
			// expired, and that must not stop the release.
			defer func() {
				if err := m.releaseLease(parent, execID, leaseID, principal); err != nil {
					m.logf("nex: takeback %s: releasing lease %s on %s: %v", code, leaseID, execID, err)
				}
			}()
		}

		_, err = m.interruptExecution(parent, execution.InterruptRequest{ExecutionID: execID, LeaseID: leaseID, PrincipalID: principal})
		switch {
		case err == nil, errors.Is(err, execution.ErrNoLiveTurn):
			// Idle by the time the signal went out: nothing to stop.
		case errors.Is(err, execution.ErrInterruptUnconfirmed):
			writeHandoffError(w, http.StatusGatewayTimeout, "interrupt_unconfirmed", "interrupt not confirmed: "+err.Error(), nil)
			return
		default:
			writeHandoffError(w, http.StatusInternalServerError, "interrupt_failed", "interrupting execution: "+err.Error(), nil)
			return
		}

		if exec, err = m.getExecution(parent, execID); err != nil {
			writeHandoffError(w, http.StatusInternalServerError, "store_error", "re-reading execution: "+err.Error(), nil)
			return
		}
	}

	if !settled(exec.State) {
		writeHandoffError(w, http.StatusConflict, "execution_not_settled", "execution is "+string(exec.State)+", not settled",
			map[string]any{"state": string(exec.State)})
		return
	}

	sid := exec.SessionID
	if sid == "" {
		sid = exec.ResumeSessionID
	}
	if sid == "" {
		writeHandoffError(w, http.StatusConflict, "no_session_id", "execution has no Claude Code session id to resume", nil)
		return
	}

	// Last look before a key goes out, lock still held: the store read and
	// the interrupt above are a window in which the user can resume by
	// hand, and a resume command typed into a pane that already runs CC
	// lands in CC's prompt as text. The execution is left as it is (settled,
	// unarchived); the SPA learns which session is already up.
	if m.prober.IsAliveFor("cc", target) {
		writeHandoffError(w, http.StatusConflict, "cc_already_running", "Claude Code is already running in the pane",
			map[string]any{"session_id": sid})
		return
	}

	keys := strings.ReplaceAll(body.ResumeCommand, "{id}", sid) + "\n"
	sent, err := m.tmux.SendKeysIfInstanceTarget(sess.TmuxID, paneWindow, expected, keys)
	if err != nil {
		writeHandoffError(w, http.StatusInternalServerError, "send_failed", "sending resume command: "+err.Error(),
			map[string]any{"session_id": sid})
		return
	}
	if !sent {
		writeHandoffError(w, http.StatusConflict, "tmux_instance_mismatch", "tmux server restarted before the resume command was sent",
			map[string]any{"session_id": sid})
		return
	}
	if !m.waitForCC(target) {
		writeHandoffError(w, http.StatusGatewayTimeout, "cc_start_timeout", "Claude Code did not start in the pane",
			map[string]any{"session_id": sid})
		return
	}

	// The terminal is now the writer of that transcript; a second handoff
	// creates a new execution. Failure to archive is logged, not fatal.
	archived := true
	if err := m.archiveExecution(parent, execution.ArchiveRequest{ExecutionID: execID, PrincipalID: principal, Archived: true}); err != nil {
		m.logf("nex: takeback %s: archiving %s: %v", code, execID, err)
		archived = false
	}

	m.logf("nex: takeback %s ← execution %s (session %s, archived=%v)", code, execID, sid, archived)
	writeJSON(w, http.StatusOK, map[string]any{"session_id": sid, "archived": archived})
}

// The engine calls of the take-back, each under its own detached, bounded
// context (see handleNexTakeback and detachedContext).

func (m *Module) getExecution(parent context.Context, id string) (store.Execution, error) {
	ctx, cancel := detachedContext(parent, m.engineOpTimeout)
	defer cancel()
	return m.sys.store.Get(ctx, id)
}

func (m *Module) acquireLease(parent context.Context, execID, principal string) (store.Lease, error) {
	ctx, cancel := detachedContext(parent, m.engineOpTimeout)
	defer cancel()
	return m.sys.service.AcquireLease(ctx, execID, principal)
}

func (m *Module) releaseLease(parent context.Context, execID, leaseID, principal string) error {
	ctx, cancel := detachedContext(parent, m.leaseCleanupTimeout)
	defer cancel()
	return m.sys.service.ReleaseLease(ctx, execID, leaseID, principal)
}

func (m *Module) interruptExecution(parent context.Context, req execution.InterruptRequest) (execution.InterruptResult, error) {
	ctx, cancel := detachedContext(parent, m.engineInterruptTimeout)
	defer cancel()
	return m.sys.service.Interrupt(ctx, req)
}

func (m *Module) archiveExecution(parent context.Context, req execution.ArchiveRequest) error {
	ctx, cancel := detachedContext(parent, m.engineOpTimeout)
	defer cancel()
	return m.sys.service.Archive(ctx, req)
}
