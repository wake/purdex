package nex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/sandbox"
	"lab.protype.tw/wake/nexen/store"

	"github.com/wake/purdex/internal/module/session"
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
	// Service.Terminate loops stall -> interrupt -> write up to 3 times.
	defaultEngineTerminateTimeout = 3*defaultEngineInterruptTimeout + defaultEngineOpTimeout
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

// hasPermissionChannel reports whether the built-in profile called name
// routes the CLI's permission prompts to the host (Nexen's handoff_ask).
// Unknown names answer false.
func hasPermissionChannel(name string) bool {
	p, ok := sandbox.Lookup(name)
	return ok && p.PermissionChannel
}

// handoffProfileAllowed reports whether a handoff may run under profile on a
// host with this policy (permission channel plan Task 2). The profile itself
// must be usable as is: Nexen would otherwise narrow it silently or, for a
// permission-channel profile, reject it after the pane's Claude Code has
// already exited. A profile without the channel also needs the host's
// handoff opt-in (handoff usable), as every handoff did before; a channel
// profile needs only itself, so a host whose max_profile is handoff_ask
// (below handoff) offers the asking mode and refuses plain handoff.
func handoffProfileAllowed(policy sandbox.Policy, profile string) bool {
	usable := sandbox.UsableProfiles(policy)
	if !slices.Contains(usable, profile) {
		return false
	}
	return hasPermissionChannel(profile) || slices.Contains(usable, handoffProfile)
}

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
	if m.engineTerminateTimeout == 0 {
		m.engineTerminateTimeout = defaultEngineTerminateTimeout
	}
	if m.leaseCleanupTimeout == 0 {
		m.leaseCleanupTimeout = defaultLeaseCleanupTimeout
	}
	if m.ownerVisiblePoll == 0 {
		m.ownerVisiblePoll = defaultOwnerVisiblePoll
	}
	if m.ownerVisibleTimeout == 0 {
		m.ownerVisibleTimeout = defaultOwnerVisibleTimeout
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
	// PermissionTimeoutS is kept raw: decoding into an int would turn a
	// string, a fraction, an exponent, a bool or an over-int64 number into
	// malformed_body; validatePermissionTimeout answers each of them as
	// invalid_permission_timeout instead.
	PermissionTimeoutS json.RawMessage `json:"permission_timeout_s,omitempty"`
}

// keepSession is the request's KeepSession with the default applied.
func (r handoffRequest) keepSession() bool {
	return r.KeepSession == nil || *r.KeepSession
}

// permissionTimeoutField is the wire name of the auto-deny timeout, reported
// in an invalid_permission_timeout's field.
const permissionTimeoutField = "permission_timeout_s"

// validatePermissionTimeout reads permission_timeout_s as sent (raw) for a
// handoff or rebuild that will run under profile (permission channel plan
// Task 3, Nexen's delegate rule). Absent and null mean none (0, which the
// delegate request then leaves unset). Anything else must be a JSON integer
// in 0..store.MaxPermissionTimeoutS; a string, a fraction, an exponent, a
// bool or a number beyond int64 is refused, never coerced, and so is a
// positive value next to a profile without the permission channel. A
// refusal is 400 invalid_permission_timeout with field; the caller answers
// it before any side effect.
func validatePermissionTimeout(profile string, raw json.RawMessage) (int, *handoffError) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return 0, nil
	}
	refuse := func(msg string) (int, *handoffError) {
		return 0, &handoffError{status: http.StatusBadRequest, code: "invalid_permission_timeout", msg: msg,
			detail: map[string]any{"field": permissionTimeoutField}}
	}
	n, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return refuse(permissionTimeoutField + " must be an integer number of seconds")
	}
	if n < 0 || n > store.MaxPermissionTimeoutS {
		return refuse(fmt.Sprintf("%s %d is outside 0..%d", permissionTimeoutField, n, store.MaxPermissionTimeoutS))
	}
	if n > 0 && !hasPermissionChannel(profile) {
		return refuse(fmt.Sprintf("%s needs a permission-channel profile, got %q", permissionTimeoutField, profile))
	}
	return int(n), nil
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

	var body handoffRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeHandoffError(w, http.StatusBadRequest, "malformed_body", "invalid request body: "+err.Error(), nil)
		return
	}
	// The policy check needs the body: it is about the profile this handoff
	// will run under, not about handoff alone.
	profile := body.Profile
	if profile == "" {
		profile = handoffProfile
	}
	if !handoffProfileAllowed(m.opts.Config.Sandbox, profile) {
		writeHandoffError(w, http.StatusConflict, "handoff_unsupported",
			"host sandbox policy does not allow a handoff under the "+profile+" profile",
			map[string]any{"profile": profile})
		return
	}
	permissionTimeout, herr := validatePermissionTimeout(profile, body.PermissionTimeoutS)
	if herr != nil {
		herr.write(w)
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

	sess, err := session.GetSessionWithin(r.Context(), m.sessions, code)
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
	// Conversation entity (§4.3, D2, D7): S moves only if this pane's CC is
	// its one owner. The sid lock also tells the manual-resume handler that
	// a rollback resume below is ours.
	sidKey := sidLockKey(owner.SessionID)
	if !m.locks.TryLock(sidKey) {
		writeHandoffError(w, http.StatusConflict, "transfer_in_progress", "this conversation is being moved already",
			map[string]any{"session_id": owner.SessionID})
		return
	}
	defer m.locks.Unlock(sidKey)
	if herr := m.checkOwners(r.Context(), owner.SessionID, "", owner.TmuxPaneID); herr != nil {
		herr.write(w)
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

	req := execution.Request{
		PrincipalID:     principal,
		Provider:        "claude",
		Brief:           "",
		StartIdle:       true, // an idle row, no turn: switching writes no fake exchange (spec §8)
		SandboxProfile:  profile,
		Mounts:          []execution.Mount{{Path: owner.Cwd, Role: "cwd", Writable: true}},
		Origin:          handoffOrigin(m.opts.Config.HostID, code),
		Labels:          map[string]string{"source": "purdex", handoffSessionLabel: code, purdexSessionLabel: owner.SessionID},
		ResumeSessionID: owner.SessionID,
		// 0 (absent) leaves it unset: a request that never asked waits forever.
		PermissionTimeoutS: permissionTimeout,
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
		if result.ID != "" {
			extra["execution_id"] = result.ID
		}
		rolled := m.rollbackHandoff(sess, expected, body.RollbackCommand, owner.SessionID, target)
		extra["rolled_back"] = rolled
		// D7: rolled back, the terminal owns S again, so the rejected row
		// exits (one state). Not rolled back, it stays as the start-failed
		// worker the SPA shows in the pane. Engine calls are detached
		// (detachedContext), so r.Context() cannot cut this short.
		if rolled && result.ID != "" {
			exited := false
			if rej, gerr := m.getExecution(r.Context(), result.ID); gerr == nil {
				out, herr := m.exitWorker(r.Context(), rej, nil, principal)
				exited = herr == nil && out.Exited()
				if herr != nil {
					// The row stays live (unarchived) for a later exit; the
					// answer says why (a lease race is a retryable 409).
					m.logf("nex: handoff %s: exiting rejected execution %s: %s (%s)", code, result.ID, herr.code, herr.msg)
					extra["exit_error"] = herr.code
				}
			} else {
				m.logf("nex: handoff %s: reading rejected execution %s: %v", code, result.ID, gerr)
			}
			extra["exited"] = exited
		}
		m.logf("nex: handoff %s rejected (%s); rolled_back=%v", code, reason, extra["rolled_back"])
		writeHandoffError(w, http.StatusConflict, "delegate_rejected", "engine rejected the handoff: "+reason, extra)
		return
	}

	// keep_session:false (spec §4.3): once the engine has confirmed the
	// execution running or idle (start_idle), the tmux session has nothing
	// left in it — CC exited before the delegate, the shell is idle — so it
	// is killed and the SPA records no origin session (the execution is
	// later taken to a NEW terminal). A delegate that answered anything
	// else (queued: somebody else is launching the first turn; failed)
	// keeps the session: "confirmed running or idle" is the condition, and
	// the SPA's `from` stays as today. The kill is by session id under the
	// generation this request verified (KillSessionIfInstance, codex F4): a
	// server that restarted during the delegate declines it, since the
	// session this request checked died with the old server and whatever
	// answers to its id or name now is somebody else's. A refusal and a
	// failure are both logged and reported as kept: as far as this request
	// knows a session is still there, so the SPA keeps its `from`.
	kept := true
	if !body.keepSession() && (result.State == store.StateRunning || result.State == store.StateIdle) {
		killed, err := m.tmux.KillSessionIfInstance(sess.TmuxID, expected)
		switch {
		case err != nil:
			m.logf("nex: handoff %s: kill-session %s (%s, keep_session=false): %v", code, sess.Name, sess.TmuxID, err)
		case !killed:
			m.logf("nex: handoff %s: tmux generation moved during the delegate; not killing %s (%s, keep_session=false)", code, sess.Name, sess.TmuxID)
		default:
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
