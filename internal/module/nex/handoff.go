package nex

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/sandbox"
	"lab.protype.tw/wake/nexen/store"

	"github.com/wake/purdex/internal/tmux"
)

// Handoff timing (spec §4.4). Fields on Module so tests can shrink them;
// applyHandoffDefaults fills the zero values in Init.
const (
	defaultHandoffResolveTimeout   = 5 * time.Second        // identity lookup
	defaultHandoffInterruptTimeout = 10 * time.Second       // CCOperator.Interrupt
	defaultHandoffExitTimeout      = 10 * time.Second       // CCOperator.Exit
	defaultRollbackWait            = 15 * time.Second       // wait for CC after a rollback resume
	defaultRollbackPoll            = 250 * time.Millisecond // liveness poll interval during that wait
)

// handoffProfile is the sandbox profile a handoff runs under unless the
// body names another one; it must be usable under the host policy.
const handoffProfile = "handoff"

func (m *Module) applyHandoffDefaults() {
	if m.handoffResolveTimeout == 0 {
		m.handoffResolveTimeout = defaultHandoffResolveTimeout
	}
	if m.handoffInterruptTimeout == 0 {
		m.handoffInterruptTimeout = defaultHandoffInterruptTimeout
	}
	if m.handoffExitTimeout == 0 {
		m.handoffExitTimeout = defaultHandoffExitTimeout
	}
	if m.rollbackWait == 0 {
		m.rollbackWait = defaultRollbackWait
	}
	if m.rollbackPoll == 0 {
		m.rollbackPoll = defaultRollbackPoll
	}
}

// handoffRequest is the body of POST /api/sessions/{code}/nex-handoff.
// RollbackCommand is the host's cc resume template with the session id left
// as `{id}`; the daemon substitutes the id it read.
type handoffRequest struct {
	ExpectedTmuxInstance string `json:"expected_tmux_instance"`
	Profile              string `json:"profile,omitempty"`
	RollbackCommand      string `json:"rollback_command,omitempty"`
}

// writeJSON writes v as the response body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeHandoffError answers with the structured shape the nex module already
// uses for nex_unavailable — {"error", "code"} — plus any extra fields the
// step contributes (step, after_exit, rolled_back, session_id, …).
func writeHandoffError(w http.ResponseWriter, status int, code, msg string, extra map[string]any) {
	body := map[string]any{"error": msg, "code": code}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, status, body)
}

// handleNexHandoff hands an interactive Claude Code session in a tmux pane
// to the embedded engine (spec §4.4 "Daemon: nex-handoff"). The whole
// sequence runs under the per-session lock; every check that can fail
// without side effects runs before the first key is sent, and the
// generation is sampled three times — before identity, after the liveness
// check (the last moment before a key goes out), and after exit.
func (m *Module) handleNexHandoff(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	// Preconditions: cheap, no lock.
	if m.sys.service == nil || m.opts.Config == nil {
		msg := "nex engine unavailable"
		if m.initErr != nil {
			msg = m.initErr.Error()
		}
		writeHandoffError(w, http.StatusServiceUnavailable, "nex_unavailable", msg, nil)
		return
	}
	if !slices.Contains(sandbox.UsableProfiles(m.opts.Config.Sandbox), handoffProfile) {
		writeHandoffError(w, http.StatusConflict, "handoff_unsupported",
			"host sandbox policy does not allow the handoff profile", nil)
		return
	}

	var body handoffRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeHandoffError(w, http.StatusBadRequest, "malformed_body", "invalid request body: "+err.Error(), nil)
		return
	}
	if !tmux.ValidInstance(body.ExpectedTmuxInstance) {
		writeHandoffError(w, http.StatusBadRequest, "invalid_instance", "invalid expected_tmux_instance", nil)
		return
	}
	// The principal is a pure function of the request; resolve it before
	// anything irreversible so a failure here cannot strand an exited CC.
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

	// Identity, before exit (F12): the session id is what the engine resumes
	// and what a rollback resumes; without it there is nothing to hand off.
	owner, ok := m.resolveHandoffOwner(code)
	if !ok {
		writeHandoffError(w, http.StatusConflict, "no_identity", "no Claude Code session identity for this pane", nil)
		return
	}
	target := paneTarget(sess)
	if !m.prober.IsAliveFor("cc", target) {
		writeHandoffError(w, http.StatusConflict, "no_cc", "no Claude Code running in the pane", nil)
		return
	}
	// Last sample before any key is sent.
	if m.sessions.TmuxInstance() != expected {
		writeHandoffError(w, http.StatusConflict, "tmux_instance_mismatch", "session belongs to another tmux generation", nil)
		return
	}

	if step, err := m.stopCC(target); err != nil {
		writeHandoffError(w, http.StatusGatewayTimeout, "cc_exit_timeout", step+" Claude Code: "+err.Error(),
			map[string]any{"step": step})
		return
	}

	// A generation change after exit is a different tmux server: the pane we
	// exited no longer exists to receive a resume, so no rollback and no
	// delegate — the session id goes back for a manual resume.
	if m.sessions.TmuxInstance() != expected {
		writeHandoffError(w, http.StatusConflict, "tmux_instance_mismatch", "tmux server restarted after Claude Code exited",
			map[string]any{"after_exit": true, "rolled_back": false, "session_id": owner.SessionID})
		return
	}

	profile := body.Profile
	if profile == "" {
		profile = handoffProfile
	}
	req := execution.Request{
		PrincipalID:     principal,
		Provider:        "claude",
		Brief:           "(handed off from tmux session " + sess.Name + ")",
		SandboxProfile:  profile,
		Mounts:          []execution.Mount{{Path: owner.Cwd, Role: "cwd", Writable: true}},
		Origin:          "purdex://host/" + m.opts.Config.HostID + "/session/" + code,
		Labels:          map[string]string{"source": "purdex", "handoff_session": code},
		ResumeSessionID: owner.SessionID,
	}
	// Not r.Context(): CC is already gone, and a client that disconnects
	// mid-delegate must not cancel the admission and leave the pane idle
	// with nothing to show for it.
	result, err := m.sys.service.Delegate(context.Background(), req)
	if err != nil || result.State == store.StateRejected {
		reason := result.RejectReason
		extra := map[string]any{"session_id": owner.SessionID}
		if err != nil {
			reason = err.Error()
			extra["infra_error"] = true
		}
		extra["reject_reason"] = reason
		extra["rolled_back"] = m.rollbackHandoff(sess, expected, body.RollbackCommand, owner.SessionID, target)
		m.logf("nex: handoff %s rejected (%s); rolled_back=%v", code, reason, extra["rolled_back"])
		writeHandoffError(w, http.StatusConflict, "delegate_rejected", "engine rejected the handoff: "+reason, extra)
		return
	}

	m.logf("nex: handoff %s → execution %s (%s, profile=%s)", code, result.ID, result.State, result.EffectiveProfile)
	writeJSON(w, http.StatusOK, map[string]any{
		"execution_id":      result.ID,
		"state":             string(result.State),
		"effective_profile": result.EffectiveProfile,
		"session_id":        owner.SessionID,
		"cwd":               owner.Cwd,
	})
}
