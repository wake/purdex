package teammod

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// handleHookDecide is POST /api/hooks/decide (spec §6.6): the lock answer
// for a session whose flag file exists. P2c knows one lock, the open lead
// request of the session: PreToolUse is denied with the spec's reason,
// PermissionRequest gets {} (the PreToolUse deny already stopped the call).
// No lock ⇒ {} — and the session's flag file is removed, so a flag whose
// writer died (a SIGKILLed `pdx lead request`) costs exactly one answered
// {} and then disappears (spec §6.6 "a stale flag costs one answered {}").
// P6 adds the relay lock before that removal; P8a adds the terminal-only
// kinds.
func (m *Module) handleHookDecide(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.HookDecideRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if req.Agent != team.HookAgentCC && req.Agent != team.HookAgentCodex {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, `agent must be "cc" or "codex"`, nil)
		return
	}
	if strings.TrimSpace(req.Event) == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "event is required", nil)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "session_id is required", nil)
		return
	}
	if req.Event != team.HookEventPreToolUse && req.Event != team.HookEventPermissionRequest {
		// PostToolUse, PostToolUseFailure, Stop, UserPromptSubmit,
		// SessionEnd, …: no decision exists for them, and nothing is
		// removed. Always 200 {} — P8a-1d forwards these very events to
		// this route (its observeHookEvent runs above this line) and a 400
		// would stop the terminal-only degradation from ever closing.
		m.answerEmptyDecision(w, req)
		return
	}
	// The lookup and the removal are one critical section under createMu,
	// the lock create holds across its own OpenByOrigin and insert. Without
	// it a stale answer could delete a fresh flag: the old request closes,
	// this lookup finds none, the same session creates a new request (201)
	// and its CLI rewrites the flag, then the removal below deletes that new
	// flag and the hard lock is silently off. Under the lock the answer
	// either completes before the create (the removed flag is one the CLI
	// rewrites after its 201) or sees the new request (deny, no removal).
	open, found, err := m.openLeadAndRemoveStaleFlag(req)
	if err != nil {
		m.logf("[team] hook decide %s: %v", req.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !found {
		m.answerEmptyDecision(w, req)
		return
	}
	if req.Event != team.HookEventPreToolUse {
		m.answerEmptyDecision(w, req)
		return
	}
	m.logf("[team] hook deny: session %s tool %q while lead request %s is open", req.SessionID, req.ToolName, open.ID)
	m.writeJSON(w, http.StatusOK, team.HookDecideResponse{
		Decision: "deny",
		Reason:   fmt.Sprintf(team.LeadLockReasonFmt, open.ID),
		Lock:     team.HookLockLeadRequest,
		ID:       open.ID,
	})
}

// openLeadAndRemoveStaleFlag looks the session's open lead request up and,
// when there is none, removes its flag — both under createMu, so no create
// for the same origin can insert between the two (see handleHookDecide).
func (m *Module) openLeadAndRemoveStaleFlag(req team.HookDecideRequest) (team.Approval, bool, error) {
	m.createMu.Lock()
	defer m.createMu.Unlock()
	open, found, err := m.store.OpenByOrigin(req.SessionID, team.KindLead)
	if m.afterOpenByOrigin != nil {
		m.afterOpenByOrigin()
	}
	if err != nil || found {
		return open, found, err
	}
	m.removeHookLock(req.Agent, req.SessionID)
	return team.Approval{}, false, nil
}

// removeHookLock deletes the session's flag file; a missing file is fine.
func (m *Module) removeHookLock(agent, sessionID string) {
	p := team.HookLockPath(m.dataDir, agent, sessionID)
	if p == "" {
		return
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.logf("[team] remove hook lock %s: %v", p, err)
	}
}

// pruneHookLocks removes every <dataDir>/hooklocks/cc/<session_id> whose session the
// registry no longer lists (spec §6.6 "the team sweeper deletes flags whose
// session is gone"). Only the cc directory: LiveSession is a CC registry
// check and codex flags have no liveness oracle yet (nobody writes them in
// P2c; a stale one goes through handleHookDecide's removal instead). A
// missing directory is nothing to prune. Returns how many were removed.
//
// Two guards keep a valid flag alive. A registry that could not be read
// answers "live" (peers/origin_resolver.go), so a read error prunes
// nothing. And a flag whose session has an open lead request is never
// pruned, whatever the registry says: the open request is the lock, the
// flag is what makes the hook ask, and the registry can lag the session
// (boot grace, spec §9.2 — which is also why Start does not prune). That
// check and the removal run under createMu, like handleHookDecide's, so a
// create cannot open a request and rewrite the flag between the two.
func (m *Module) pruneHookLocks() int {
	if m.dataDir == "" {
		return 0
	}
	dir := filepath.Join(m.dataDir, team.HookLocksDir, team.HookAgentCC)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			m.logf("[team] prune hook locks: %v", err)
		}
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		sid := e.Name()
		if m.origins.LiveSession(sid) {
			continue
		}
		if m.pruneUnlessLeadOpen(filepath.Join(dir, sid), sid) {
			n++
		}
	}
	if n > 0 {
		m.logf("[team] pruned %d stale hook lock flag(s)", n)
	}
	return n
}

// pruneUnlessLeadOpen removes the flag at p unless its session has an open
// lead request (or the store could not say), under createMu. Reports
// whether the file was removed.
func (m *Module) pruneUnlessLeadOpen(p, sid string) bool {
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if _, found, err := m.store.OpenByOrigin(sid, team.KindLead); err != nil {
		m.logf("[team] prune hook lock %s: %v", sid, err)
		return false
	} else if found {
		return false
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.logf("[team] prune hook lock %s: %v", sid, err)
		return false
	}
	return true
}
