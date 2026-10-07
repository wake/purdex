package teammod

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// Terminal-only degradation (spec §6.6 table row 3, U19 point 4): a
// session WITHOUT the Purdex mod (--safe-mode, started before the install,
// Codex) still shows its AskUserQuestion and permission prompts to remote
// clients as read-only cards, through the settings hooks:
//
//   - PreToolUse/AskUserQuestion, PermissionRequest (forwarded ungated by
//     `pdx hook`): open a hook row flagged terminal_only when the session's
//     mod has not said hello and someone remote is connected; the answer to
//     the hook is {} either way, so the native dialog shows at once.
//   - PostToolUse, PostToolUseFailure, Stop, UserPromptSubmit, SessionEnd
//     (forwarded only while the session's HookAsksDir flag exists): close
//     the session's terminal_only rows — PostToolUse/AskUserQuestion as
//     answered_local with the answers CC put in tool_response, a matching
//     PostToolUse(Failure) of a permission row's tool as dismissed, Stop /
//     UserPromptSubmit / SessionEnd everything of the session as dismissed.
//
// The flag file <data_dir>/hookasks/<agent>/<session_id> exists while the
// session has at least one open terminal_only row; the daemon writes and
// removes it here, so `pdx hook` pays a stat, not a round trip, on the
// frequent events. Nothing here ever produces a decision.

// HookAsksDir is the flag dir, next to team.HookLocksDir.
const HookAsksDir = "hookasks"

// answerEmptyDecision is every "no decision" answer of /api/hooks/decide:
// the event is observed for the terminal-only degradation first (it never
// fails the request), then {} is written. The deny path never comes here.
func (m *Module) answerEmptyDecision(w http.ResponseWriter, req team.HookDecideRequest) {
	m.observeHookEvent(req)
	m.writeJSON(w, http.StatusOK, team.HookDecideResponse{})
}

// observeHookEvent never fails the request and never produces a decision.
func (m *Module) observeHookEvent(req team.HookDecideRequest) {
	if req.SessionID == "" {
		return
	}
	switch req.Event {
	case team.HookEventPreToolUse:
		if req.ToolName == "AskUserQuestion" {
			m.openTerminalOnly(req, team.KindHookAsk)
		}
	case team.HookEventPermissionRequest:
		m.openTerminalOnly(req, team.KindHookPermission)
	case "PostToolUse", "PostToolUseFailure":
		m.closeTerminalOnlyForTool(req)
	case "Stop", "UserPromptSubmit", "SessionEnd":
		m.closeTerminalOnlyAll(req.Agent, req.SessionID)
	}
}

// terminalOnlyPayload builds the row payload from the hook's fields.
func terminalOnlyPayload(req team.HookDecideRequest, kind team.Kind) ([]byte, string) {
	if kind == team.KindHookAsk {
		var in struct {
			Questions json.RawMessage `json:"questions"`
		}
		_ = json.Unmarshal(req.ToolInput, &in)
		p, _ := json.Marshal(team.HookAskPayload{Questions: in.Questions})
		return hookPayloadFor(kind, req.ToolUseID, p, true)
	}
	var raw struct {
		Suggestions json.RawMessage `json:"permission_suggestions"`
	}
	_ = json.Unmarshal(req.Raw, &raw)
	p, _ := json.Marshal(team.HookPermissionPayload{ToolName: req.ToolName, ToolInput: req.ToolInput, Suggestions: raw.Suggestions})
	return hookPayloadFor(kind, req.ToolUseID, p, true)
}

func (m *Module) openTerminalOnly(req team.HookDecideRequest, kind team.Kind) {
	if m.modPresent(req.SessionID) || !m.responders.Any() || m.stopping() {
		return
	}
	payload, bad := terminalOnlyPayload(req, kind)
	if bad != "" {
		m.logf("[team] hook observe %s/%s: %s", req.Event, req.ToolName, bad)
		return
	}
	origin, ok, err := m.origins.ResolveOriginBySession(req.SessionID)
	if err != nil || !ok {
		return
	}
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if m.stopping() {
		return
	}
	if req.ToolUseID != "" {
		if _, found, err := m.store.OpenByToolUse(origin.SessionID, req.ToolUseID); err != nil || found {
			return // the mod got here first (or the DB failed): nothing to add
		}
	} else if kind == team.KindHookPermission && m.permissionRowOpen(origin.SessionID, req.ToolName, req.ToolInput) {
		// PermissionRequest carries no tool_use_id: the key is session +
		// tool_name + sha256(tool_input). A re-fired prompt opens nothing more.
		return
	}
	if _, err := m.openHookRow(origin, kind, payload, true); err != nil {
		m.logf("[team] hook observe: open terminal_only: %v", err)
		return
	}
	m.setAskFlag(req.Agent, req.SessionID, true)
}

// toolInputHash is sha256 over the compacted tool_input JSON (byte-equal
// inputs hash equal whatever the whitespace); "" for an empty input.
func toolInputHash(in json.RawMessage) string {
	var buf bytes.Buffer
	if len(in) == 0 || json.Compact(&buf, in) != nil {
		return ""
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

// permissionRowOpen reports whether a terminal_only hook_permission row for
// the same (session, tool_name, tool_input hash) is already open.
func (m *Module) permissionRowOpen(sessionID, toolName string, in json.RawMessage) bool {
	rows, err := m.store.OpenTerminalOnlyBySession(sessionID)
	if err != nil {
		return true // do not pile rows on a failing DB
	}
	want := toolInputHash(in)
	for _, a := range rows {
		if a.Kind != team.KindHookPermission {
			continue
		}
		var p team.HookPermissionPayload
		if json.Unmarshal(a.Payload, &p) == nil && p.ToolName == toolName && toolInputHash(p.ToolInput) == want {
			return true
		}
	}
	return false
}

// answersOf reads PostToolUse's tool_response.answers for AskUserQuestion.
func answersOf(raw json.RawMessage) map[string]string {
	var r struct {
		ToolResponse struct {
			Answers map[string]string `json:"answers"`
		} `json:"tool_response"`
	}
	if json.Unmarshal(raw, &r) != nil || len(r.ToolResponse.Answers) == 0 {
		return nil
	}
	return r.ToolResponse.Answers
}

// The closes hold createMu from the read of the open rows through the flag
// refresh, as openTerminalOnly does from its check through the flag write:
// otherwise a refresh that read "no row" could remove the flag a concurrent
// open just wrote, and that row's closing events would never be forwarded.
func (m *Module) closeTerminalOnlyForTool(req team.HookDecideRequest) {
	m.createMu.Lock()
	defer m.createMu.Unlock()
	rows, err := m.store.OpenTerminalOnlyBySession(req.SessionID)
	if err != nil {
		m.logf("[team] hook observe: %v", err)
		return
	}
	now := m.now()
	for _, a := range rows {
		var c Close
		switch a.Kind {
		case team.KindHookAsk:
			var p team.HookAskPayload
			if json.Unmarshal(a.Payload, &p) != nil || p.ToolUseID != req.ToolUseID {
				continue
			}
			c = Close{State: team.StateDismissed, DecidedAt: now}
			if req.Event == "PostToolUse" {
				if answers := answersOf(req.Raw); answers != nil {
					c = Close{State: team.StateAnsweredLocal, DecidedAt: now, DecidedBy: terminalClient(), Hook: &team.HookDecision{Answers: answers}}
				}
			}
		case team.KindHookPermission:
			var p team.HookPermissionPayload
			if json.Unmarshal(a.Payload, &p) != nil || p.ToolName != req.ToolName {
				continue
			}
			c = Close{State: team.StateDismissed, DecidedAt: now}
		default:
			continue
		}
		if _, _, err := m.closeAs(a.ID, c); err != nil {
			m.logf("[team] hook observe: close %s: %v", a.ID, err)
		}
	}
	m.refreshAskFlag(req.Agent, req.SessionID)
}

func (m *Module) closeTerminalOnlyAll(agent, sessionID string) {
	m.createMu.Lock()
	defer m.createMu.Unlock()
	rows, err := m.store.OpenTerminalOnlyBySession(sessionID)
	if err != nil {
		m.logf("[team] hook observe: %v", err)
		return
	}
	now := m.now()
	for _, a := range rows {
		if _, _, err := m.closeAs(a.ID, Close{State: team.StateDismissed, DecidedAt: now}); err != nil {
			m.logf("[team] hook observe: close %s: %v", a.ID, err)
		}
	}
	m.refreshAskFlag(agent, sessionID)
}

// refreshAskFlag keeps the flag iff the session still has an open
// terminal_only row (a failed read keeps it: a stale flag is cheap).
// Callers hold createMu.
func (m *Module) refreshAskFlag(agent, sessionID string) {
	rows, err := m.store.OpenTerminalOnlyBySession(sessionID)
	if err != nil {
		return
	}
	if m.afterAskFlagQuery != nil {
		m.afterAskFlagQuery()
	}
	m.setAskFlag(agent, sessionID, len(rows) > 0)
}

// plainElem reports whether s is one ordinary path element. filepath.Base
// is not a check: it would alias "../victim" onto "victim".
func plainElem(s string) bool {
	return s != "" && s != "." && s != ".." && filepath.Base(s) == s && !strings.ContainsAny(s, `/\`)
}

// askFlagPath is <data_dir>/hookasks/<agent>/<session_id>, or "" when the
// data dir is unknown or agent / session_id is not a single plain path
// element — the same rule `pdx hook` applies when it reads the flag.
func (m *Module) askFlagPath(agent, sessionID string) string {
	if m.dataDir == "" || !plainElem(agent) || !plainElem(sessionID) {
		return ""
	}
	return filepath.Join(m.dataDir, HookAsksDir, agent, sessionID)
}

func (m *Module) setAskFlag(agent, sessionID string, on bool) {
	p := m.askFlagPath(agent, sessionID)
	if p == "" {
		return
	}
	if !on {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			m.logf("[team] hook observe: remove flag: %v", err)
		}
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		m.logf("[team] hook observe: flag dir: %v", err)
		return
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		m.logf("[team] hook observe: flag: %v", err)
	}
}
