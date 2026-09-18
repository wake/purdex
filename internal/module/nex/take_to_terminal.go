package nex

// POST /api/nex/executions/{id}/take-to-terminal (exec-to-terminal spec
// §4.1): take any claude execution — one with no origin session — to a
// fresh tmux session the daemon creates in the execution's cwd, resume its
// Claude Code session there, and archive the execution. The engine steps
// are the take-back's (settleForResume, resumeInWindow in takeback.go);
// what differs is the session — created here, and killed again (by id,
// under the generation it was created in) if the archive or the resume
// fails — and the order: the execution is archived before the resume is
// typed, and un-archived if the resume fails, so a failed call never
// leaves a second writer next to an unarchived execution, and a
// succeeding one has no window in which the SPA could resume the
// execution a second time.

import (
	"encoding/json"
	"errors"
	"net/http"

	"lab.protype.tw/wake/nexen/execution"
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

// handleTakeToTerminal runs spec §4.1 steps 1–8 in order: lock, body,
// row, row preflights (provider, state), the preflights that must not cost
// an interrupt (name free, cwd usable), settle, create, archive, resume.
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

	// Step 1: one take-to-terminal per execution at a time.
	lockKey := takeToTerminalLockKey(execID)
	if !m.locks.TryLock(lockKey) {
		writeHandoffError(w, http.StatusConflict, "takeback_in_progress", "a take-back is already in progress for this execution", nil)
		return
	}
	defer m.locks.Unlock(lockKey)

	// Step 2: body.
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

	// Step 3: the row, once (settleForResume re-reads only after an
	// interrupt). Detached, bounded contexts as in the take-back.
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
	// Archived: the terminal (or whoever archived it) owns that transcript
	// now. A retried 200, or a second click from a pane whose swap failed,
	// must not start a second `claude --resume` on the same session file.
	if exec.ArchivedAt != 0 {
		writeHandoffError(w, http.StatusConflict, "execution_archived", "execution is archived; its session is already resumed elsewhere",
			map[string]any{"session_id": firstNonEmpty(exec.SessionID, exec.ResumeSessionID)})
		return
	}
	// queued / rejected: nothing to interrupt and nothing to resume yet —
	// refused before any lease (settleForResume would say the same, but
	// after the preflights below, and the SPA never shows the button here).
	if exec.State == store.StateQueued || exec.State == store.StateRejected {
		writeHandoffError(w, http.StatusConflict, "execution_not_settled", "execution is "+string(exec.State)+", not settled",
			map[string]any{"state": string(exec.State)})
		return
	}

	// Step 4: preflights that must not cost an interrupt. A running turn
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

	// Step 5: settle — lease-if-running → interrupt → re-read → settled →
	// session id. A lease acquired here is released after the archive.
	exec, sid, release, herr := m.settleForResume(parent, exec, body.LeaseID, principal)
	if herr != nil {
		herr.write(w)
		return
	}
	defer release()

	// Step 6: the session. Ours from here on.
	info, err := m.sessions.CreateSession(name, exec.Cwd)
	if err != nil {
		var ce *session.CreateError
		switch {
		case errors.Is(err, session.ErrSessionExists):
			// Lost the race with step 4: somebody else's session, untouched.
			writeHandoffError(w, http.StatusConflict, "session_exists", "session already exists: "+name,
				map[string]any{"session_name": name})
		case errors.Is(err, session.ErrInvalidCwd):
			// Vanished between step 4 and now.
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

	// Step 7: archive BEFORE the resume (codex F2). Once the keys go out
	// the terminal may be writing the transcript, and an execution still
	// unarchived in that window is one the SPA can resume a second time.
	// A failure here kills the session just created — ours, nothing ran in
	// it — and leaves the execution settled and unarchived for a retry.
	if err := m.archiveExecution(parent, execution.ArchiveRequest{ExecutionID: execID, PrincipalID: principal, Archived: true}); err != nil {
		m.logf("nex: take-to-terminal %s: archiving: %v", execID, err)
		killed := m.killCreatedSession(execID, info, "archive_failed")
		writeHandoffError(w, http.StatusInternalServerError, "archive_failed", "archiving execution: "+err.Error(),
			map[string]any{"session_id": sid, "session_name": name, "session_killed": killed})
		return
	}

	// Step 8: resume in window 0 of the session just created, guarded by
	// the generation it was created under. On failure the session is
	// killed again — by id, under that same generation, so a server that
	// restarted in between (tmux_instance_mismatch, or a restart the send
	// never got to see) declines the kill and a stranger who reused the
	// name is left alone (I3) — and the execution is un-archived so it can
	// be retried (a failure there is logged; the detail says which state
	// the row is in). The detail carries the session id for a manual resume.
	if herr := m.resumeInWindow(info, info.TmuxInstance, body.ResumeCommand, sid); herr != nil {
		killed := m.killCreatedSession(execID, info, herr.code)
		unarchived := true
		if err := m.archiveExecution(parent, execution.ArchiveRequest{ExecutionID: execID, PrincipalID: principal, Archived: false}); err != nil {
			m.logf("nex: take-to-terminal %s: unarchiving after %s: %v", execID, herr.code, err)
			unarchived = false
		}
		if herr.detail == nil {
			herr.detail = map[string]any{}
		}
		herr.detail["session_name"] = name
		herr.detail["session_killed"] = killed
		herr.detail["unarchived"] = unarchived
		herr.write(w)
		return
	}

	m.logf("nex: take-to-terminal %s → session %s/%s (session %s, archived)", execID, info.Code, info.Name, sid)
	writeJSON(w, http.StatusOK, map[string]any{"session": info, "session_id": sid, "archived": true})
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
