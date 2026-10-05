package nex

// POST /api/nex/executions/{id}/take-to-terminal (exec-to-terminal spec
// §4.1; conversation entity spec §4.3, D5, D6): bring a claude execution's
// conversation to a terminal — a fresh tmux session the daemon creates in
// the execution's cwd — by resuming its Claude Code session there, and
// only after that resume succeeded, exit the worker it was (terminate +
// archive, exitWorker). The engine steps are the take-back's
// (settleForResume, resumeInWindow in takeback.go); what differs is the
// session — created here, and killed again (by id, under the generation it
// was created in) if the resume fails.
//
// Resume first, exit after. What used to be the "archive first" fence is
// now two things: the transfer holds the control lease from before the
// settle until after the exit, so nobody can send into the worker while
// the terminal resumes; and the exec:<id> and sid:<S> locks plus the owner
// check stop a second resume of S. A failed resume exits nothing — there
// is no un-archive rollback. An exited execution (archived or terminated)
// and a rejected one are accepted too (D6: "rebuild as terminal"); the
// owner check, not "archived", is what refuses a second click once S is in
// a terminal. Only an execution that was live is exited.

import (
	"encoding/json"
	"errors"
	"net/http"

	"lab.protype.tw/wake/nexen/store"

	"github.com/wake/purdex/internal/module/session"
)

// takeToTerminalRequest is the body. SessionName is the tmux session to
// create (the SPA names it `{slug}-{N}` as the launcher does and retries
// on session_exists); ResumeCommand and LeaseID are the take-back's.
type takeToTerminalRequest struct {
	SessionName   string `json:"session_name"`
	ResumeCommand string `json:"resume_command"`
	LeaseID       string `json:"lease_id,omitempty"`
}

// takeToTerminalLockKey is the HandoffLocks key for an execution. Session
// codes are base36 without a colon, so the prefix cannot name one.
func takeToTerminalLockKey(execID string) string { return "exec:" + execID }

// handleTakeToTerminal runs, in order:
//
//  1. engine, then principal;
//  2. the exec:<id> lock (409 takeback_in_progress);
//  3. body;
//  4. the row, then the provider check;
//  5. the session id S (409 no_session_id), then queued → 409
//     execution_not_settled;
//  6. the sid:<S> lock (409 transfer_in_progress);
//  7. the owner check: nothing but this execution, and only while it is
//     live, may own S (409 session_owned / 503 owner_check_failed);
//  8. the preflights that must not cost an interrupt (name free, cwd usable);
//  9. a live running / idle row: takeControl (held for the whole transfer,
//     released on return), the row re-read under that control (gone → the
//     exited path), settleForResume on it, then renewControl — a full TTL
//     for the fence;
//  10. create the session;
//  11. resume — on failure the session is killed and nothing is exited;
//  12. a row that was live: exitWorker under the transfer's control;
//  13. 200 {session, session_id, archived, exited, exit_error?}.
func (m *Module) handleTakeToTerminal(w http.ResponseWriter, r *http.Request) {
	execID := r.PathValue("id")

	if m.sys.service == nil || m.sys.store == nil || m.opts.Config == nil {
		msg := "nex engine unavailable"
		if m.initErr != nil {
			msg = m.initErr.Error()
		}
		writeHandoffError(w, http.StatusServiceUnavailable, "nex_unavailable", msg, nil)
		return
	}
	principal, err := m.principal(r)
	if err != nil {
		writeHandoffError(w, http.StatusInternalServerError, "principal_unresolved", err.Error(), nil)
		return
	}

	// Step 2: one take-to-terminal per execution at a time.
	lockKey := takeToTerminalLockKey(execID)
	if !m.locks.TryLock(lockKey) {
		writeHandoffError(w, http.StatusConflict, "takeback_in_progress", "a take-back is already in progress for this execution", nil)
		return
	}
	defer m.locks.Unlock(lockKey)

	// Step 3: body.
	var body takeToTerminalRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeHandoffError(w, http.StatusBadRequest, "malformed_body", "invalid request body: "+err.Error(), nil)
		return
	}
	if body.SessionName == "" {
		writeHandoffError(w, http.StatusBadRequest, "missing_session_name", "session_name is required", nil)
		return
	}
	if !session.ValidSessionName(body.SessionName) {
		writeHandoffError(w, http.StatusBadRequest, "invalid_session_name", "invalid session name: must match ^[a-zA-Z0-9_-]+$", nil)
		return
	}
	if body.ResumeCommand == "" {
		writeHandoffError(w, http.StatusBadRequest, "missing_resume_command", "resume_command is required", nil)
		return
	}
	name := body.SessionName

	// Step 4: the row (re-read once control is held, step 9). Detached, bounded contexts as in the take-back.
	parent := r.Context()
	exec, err := m.getExecution(parent, execID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeHandoffError(w, http.StatusNotFound, "execution_not_found", "execution not found", nil)
			return
		}
		writeHandoffError(w, http.StatusInternalServerError, "store_error", "reading execution: "+err.Error(), nil)
		return
	}
	if exec.Provider != "claude" {
		writeHandoffError(w, http.StatusConflict, "provider_unsupported", "only claude executions can be taken to a terminal",
			map[string]any{"provider": exec.Provider})
		return
	}

	// Step 5: the conversation, S. Without it there is nothing to resume
	// and nothing to lock. Queued: nothing to resume yet — refused before
	// any lock or lease (the SPA never shows the button there).
	sid := firstNonEmpty(exec.SessionID, exec.ResumeSessionID)
	if sid == "" {
		writeHandoffError(w, http.StatusConflict, "no_session_id", "execution has no Claude Code session id to resume", nil)
		return
	}
	if exec.State == store.StateQueued {
		writeHandoffError(w, http.StatusConflict, "execution_not_settled", "execution is queued, not settled",
			map[string]any{"state": string(store.StateQueued)})
		return
	}

	// Step 6 (D2): the conversation's lock. Every Purdex transfer of S
	// holds it, and the manual-resume handler skips S while it is held.
	sidKey := sidLockKey(sid)
	if !m.locks.TryLock(sidKey) {
		writeHandoffError(w, http.StatusConflict, "transfer_in_progress", "this conversation is being moved already",
			map[string]any{"session_id": sid})
		return
	}
	defer m.locks.Unlock(sidKey)

	// Step 7 (§4.3, D6): S may move only if nothing but this execution owns
	// it. An exited execution owns nothing; a second click after a success
	// finds S in the terminal it just reached and stops here.
	wasLive := isLiveExecution(exec)
	allowExec := ""
	if wasLive {
		allowExec = exec.ID
	}
	if herr := m.checkOwners(parent, sid, allowExec, ""); herr != nil {
		herr.write(w)
		return
	}

	// Step 8: preflights that must not cost an interrupt. A running turn
	// is left running when the name is taken or the cwd is gone.
	if m.sessions.SessionExists(name) {
		writeHandoffError(w, http.StatusConflict, "session_exists", "session already exists: "+name,
			map[string]any{"session_name": name})
		return
	}
	if exec.Cwd == "" {
		writeHandoffError(w, http.StatusConflict, "cwd_missing", "execution has no cwd to open a terminal in",
			map[string]any{"cwd": ""})
		return
	}
	if err := m.sessions.ValidateCwd(exec.Cwd); err != nil {
		writeHandoffError(w, http.StatusConflict, "cwd_missing", "execution cwd is not usable: "+err.Error(),
			map[string]any{"cwd": exec.Cwd})
		return
	}

	// Step 9 (D5): control for the whole transfer — the caller's lease,
	// else one acquired here, else a pdx holder's borrowed (D4); a non-pdx
	// holder answers held_by before anything is created. Holding it is the
	// fence that replaced "archive first": nobody can send into the worker
	// between the resume and the exit. Rows that take no sends (exited,
	// failed, rejected) need none.
	ctl := control{release: noRelease}
	if wasLive && (exec.State == store.StateRunning || exec.State == store.StateIdle) {
		var herr *handoffError
		if ctl, herr = m.takeControl(parent, execID, body.LeaseID, principal); herr != nil {
			herr.write(w)
			return
		}
		defer func() { ctl.release() }() // ctl may be replaced by renewControl below
		// The row read at step 4 predates the control: a turn may have
		// started since, and the lease fences only sends made from now on.
		// Settle what the row says under control, not what it said before.
		fresh, gerr := m.getExecution(parent, execID)
		if gerr != nil {
			writeHandoffError(w, http.StatusInternalServerError, "store_error", "re-reading execution: "+gerr.Error(), nil)
			return
		}
		exec = fresh
		if !isLiveExecution(exec) {
			// Exited meanwhile: the exited path — no settle, no renew, no
			// exit afterwards. The deferred release still frees the lease.
			wasLive = false
		} else {
			if exec, _, herr = m.settleForResume(parent, exec, ctl); herr != nil {
				herr.write(w)
				return
			}
			// A full TTL from here: the fence must outlive create + resume, even
			// when the lease was borrowed with seconds left. A lease lost since
			// is re-taken (renewControl), so the exit runs under a live one.
			if ctl, herr = m.renewControl(parent, execID, ctl, principal); herr != nil {
				herr.write(w)
				return
			}
		}
	}

	// Step 10: the session. Ours from here on.
	info, err := m.sessions.CreateSession(name, exec.Cwd)
	if err != nil {
		var ce *session.CreateError
		switch {
		case errors.Is(err, session.ErrSessionExists):
			// Lost the race with step 8: somebody else's session, untouched.
			writeHandoffError(w, http.StatusConflict, "session_exists", "session already exists: "+name,
				map[string]any{"session_name": name})
		case errors.Is(err, session.ErrInvalidCwd):
			// Vanished between step 8 and now.
			writeHandoffError(w, http.StatusConflict, "cwd_missing", "execution cwd is not usable: "+err.Error(),
				map[string]any{"cwd": exec.Cwd})
		default:
			alive := errors.As(err, &ce) && ce.SessionAlive()
			m.logf("nex: take-to-terminal %s: creating session %s: %v (session_alive=%v)", execID, name, err, alive)
			writeHandoffError(w, http.StatusInternalServerError, "session_create_failed", "creating session: "+err.Error(),
				map[string]any{"session_name": name, "session_alive": alive})
		}
		return
	}

	// Step 10b: look at the owners again, right before the keys. The lease,
	// interrupt, renew and create above are a window in which an external
	// terminal can resume S; the sid lock only coordinates Purdex transfers.
	// This narrows that window but cannot close it: a manual resume is
	// reconciled by the Q1 handler (P1a-4). The session just created goes.
	if herr := m.checkOwners(parent, sid, allowExec, ""); herr != nil {
		killed := m.killCreatedSession(execID, info, herr.code)
		if herr.detail == nil {
			herr.detail = map[string]any{}
		}
		herr.detail["session_name"] = name
		herr.detail["session_killed"] = killed
		herr.detail["exited"] = !wasLive
		herr.write(w)
		return
	}

	// Step 11: resume in window 0 of the session just created, guarded by
	// the generation it was created under. On failure the session is
	// killed again — by id, under that same generation, so a server that
	// restarted in between (tmux_instance_mismatch, or a restart the send
	// never got to see) declines the kill and a stranger who reused the
	// name is left alone (I3) — and nothing is exited: the worker is as it
	// was (settled), for a retry. The detail carries the session id for a
	// manual resume.
	if herr := m.resumeInWindow(info, info.TmuxInstance, body.ResumeCommand, sid); herr != nil {
		killed := m.killCreatedSession(execID, info, herr.code)
		if herr.detail == nil {
			herr.detail = map[string]any{}
		}
		herr.detail["session_name"] = name
		herr.detail["session_killed"] = killed
		herr.detail["exited"] = !wasLive
		herr.write(w)
		return
	}

	// Step 12 (§4.3): the terminal owns S now; the worker it was exits,
	// under the transfer's control. The resume succeeded, so the answer is
	// 200 either way: the SPA must swap the pane to the terminal that now
	// runs S. A worker that could not exit (terminate and archive both
	// failed) is reported as exited:false with exit_error, for the SPA to
	// ask the user to exit it by hand.
	resp := map[string]any{"session": info, "session_id": sid, "archived": exec.ArchivedAt != 0, "exited": !wasLive}
	if wasLive {
		out, herr := m.exitWorker(parent, exec, ctlPtr(ctl), principal)
		if herr != nil {
			m.logf("nex: take-to-terminal %s: exiting the worker after the resume: %s (%s)", execID, herr.code, herr.msg)
			resp["exit_error"] = herr.code
		}
		resp["exited"], resp["archived"] = out.Exited(), out.Archived || exec.ArchivedAt != 0
	}

	// Step 13.
	m.logf("nex: take-to-terminal %s → session %s/%s (session %s, exited=%v)", execID, info.Code, info.Name, sid, resp["exited"])
	writeJSON(w, http.StatusOK, resp)
}

// killCreatedSession kills the session this call created — by id, and
// only under the generation it was created in (KillSessionIfInstance) —
// after the step named by `after` failed. Reports whether it was killed.
// A refusal (the server restarted: the session died with the old one, and
// its id or name may now be somebody else's) and a failure are both
// logged and reported as not killed.
func (m *Module) killCreatedSession(execID string, info *session.SessionInfo, after string) bool {
	killed, err := m.tmux.KillSessionIfInstance(info.TmuxID, info.TmuxInstance)
	switch {
	case err != nil:
		m.logf("nex: take-to-terminal %s: kill-session %s (%s) after %s: %v", execID, info.Name, info.TmuxID, after, err)
	case !killed:
		m.logf("nex: take-to-terminal %s: tmux generation moved since %s was created; not killing %s after %s", execID, info.Name, info.TmuxID, after)
	}
	return killed
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
