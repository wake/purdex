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

	defaultDelegateTimeout = 30 * time.Second // Service.Delegate: admission + sandbox + spawn
	defaultEngineOpTimeout = 10 * time.Second // Store.Get, AcquireLease, Archive
	// Service.Interrupt: Nexen confirms or reports ErrInterruptUnconfirmed
	// within its own interruptTimeout (15 s); this budget sits above it so
	// the engine's verdict — not a bare deadline — is what the caller sees.
	defaultEngineInterruptTimeout = 20 * time.Second
	defaultLeaseCleanupTimeout    = 5 * time.Second // ReleaseLease
)

// detachedContext derives a context for an engine call from the request's:
// detached from its cancellation (a client that disconnects mid-sequence
// must not cancel an admission, an interrupt, or a lease release — the
// half state is worse than finishing for a caller who is no longer
// listening) but bounded by d, so an engine that never answers cannot
// hold the per-session lock forever. Request values are kept.
func detachedContext(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), d)
}

// handoffProfile is the sandbox profile a handoff runs under unless the
// body names another one; it must be usable under the host policy.
const handoffProfile = "handoff"

// handoffSessionLabel is the execution label that binds a handed-off
// execution to its session code (spec §4.4 step 5). Together with the
// origin (handoffOrigin) it is what a take-back checks before it touches
// the execution (§4.4 take-back step 1b).
const handoffSessionLabel = "handoff_session"

// handoffOrigin is the execution origin a handoff writes: this daemon and
// the session the execution came from. One definition, so the take-back
// check cannot drift from what the handoff wrote.
func handoffOrigin(hostID, code string) string {
	return "purdex://host/" + hostID + "/session/" + code
}

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
	if m.delegateTimeout == 0 {
		m.delegateTimeout = defaultDelegateTimeout
	}
	if m.engineOpTimeout == 0 {
		m.engineOpTimeout = defaultEngineOpTimeout
	}
	if m.engineInterruptTimeout == 0 {
		m.engineInterruptTimeout = defaultEngineInterruptTimeout
	}
	if m.leaseCleanupTimeout == 0 {
		m.leaseCleanupTimeout = defaultLeaseCleanupTimeout
	}
}

// handoffRequest is the body of POST /api/sessions/{code}/nex-handoff.
// RollbackCommand is the host's cc resume template with the session id left
// as `{id}`; the daemon substitutes the id it read. KeepSession (exec-to-
// terminal spec §4.3) says whether the tmux session stays once the
// execution is running; absent means true, so an old SPA keeps today's
// behaviour.
type handoffRequest struct {
	ExpectedTmuxInstance string `json:"expected_tmux_instance"`
	Profile              string `json:"profile,omitempty"`
	RollbackCommand      string `json:"rollback_command,omitempty"`
	KeepSession          *bool  `json:"keep_session,omitempty"`
}

// keepSession is the request's KeepSession with the default applied.
func (r handoffRequest) keepSession() bool {
	return r.KeepSession == nil || *r.KeepSession
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
		Origin:          handoffOrigin(m.opts.Config.HostID, code),
		Labels:          map[string]string{"source": "purdex", handoffSessionLabel: code},
		ResumeSessionID: owner.SessionID,
	}
	// Detached from r.Context(): CC is already gone, and a client that
	// disconnects mid-delegate must not cancel the admission and leave the
	// pane idle with nothing to show for it. Bounded: a deadline is an
	// infra error and takes the rollback path below.
	ctx, cancel := detachedContext(r.Context(), m.delegateTimeout)
	result, err := m.sys.service.Delegate(ctx, req)
	cancel()
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

	// keep_session:false (spec §4.3): once the engine has confirmed the
	// execution running, the tmux session has nothing left in it — CC
	// exited before the delegate, the shell is idle — so it is killed and
	// the SPA records no origin session (the execution is later taken to a
	// NEW terminal). A delegate that answered anything but running (queued:
	// somebody else is launching the first turn; failed) keeps the session
	// — "confirmed running" is the condition, and the SPA's `from` stays
	// as today. A kill failure is logged and reported as kept: the session
	// is still there, so the SPA keeps its `from`.
	kept := true
	if !body.keepSession() && result.State == store.StateRunning {
		if err := m.tmux.KillSession(sess.Name); err != nil {
			m.logf("nex: handoff %s: kill-session %s (keep_session=false): %v", code, sess.Name, err)
		} else {
			kept = false
		}
	}

	m.logf("nex: handoff %s → execution %s (%s, profile=%s, session_kept=%v)", code, result.ID, result.State, result.EffectiveProfile, kept)
	writeJSON(w, http.StatusOK, map[string]any{
		"execution_id":      result.ID,
		"state":             string(result.State),
		"effective_profile": result.EffectiveProfile,
		"session_id":        owner.SessionID,
		"cwd":               owner.Cwd,
		"session_kept":      kept,
	})
}
