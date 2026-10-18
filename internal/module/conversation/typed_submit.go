package conversation

import (
	"net/http"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/tmux"
)

// POST /api/sessions/{code}/submit (K-1a-2, spec docs/specs/2026-10-11-keystroke-submit-spec.md §3): put the message into
// Claude Code's composer and press Enter, so Claude Code's own queue owns it. With the Purdex mod: the mod fills the composer
// (promptq fill), the daemon checks it is still fresh and presses Enter through the generation / pane-mode guard. Without a
// mod, after 10 s of no owner: blind typing, marked mode "keys".
//
// Order: validate the body, answer a known id from the ledgers (a replay is correct even if the session vanished), resolve
// the session and owner, take the pane lock then the session lock, re-resolve everything under the locks, then send.

type sessionSubmitRequest struct {
	Text                 string `json:"text"`
	ClientMsgID          string `json:"client_msg_id"`
	ExpectedTmuxInstance string `json:"expected_tmux_instance"`
}

// agentOwner is the resolved pane owner (an alias keeps the typed files short).
type agentOwner = agent.PaneOwner

func (m *Module) handleSessionSubmit(w http.ResponseWriter, r *http.Request) {
	td := m.typedDeps()
	if td == nil {
		fail(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	var in sessionSubmitRequest
	if status, code := decodeBodyErr(w, r, &in); code != "" {
		fail(w, status, code)
		return
	}
	switch {
	case !clientMsgIDRe.MatchString(in.ClientMsgID):
		fail(w, http.StatusBadRequest, "bad_client_msg_id")
		return
	case !tmux.ValidInstance(in.ExpectedTmuxInstance):
		fail(w, http.StatusBadRequest, "bad_expected_tmux_instance")
		return
	}
	text, code := sanitizePrompt(in.Text) // the one sanitiser; a leading "/" or "!" is refused in both modes
	if code != "" {
		fail(w, http.StatusBadRequest, code)
		return
	}
	text = padTrailingBackslash(text)
	if len(text) > maxPromptBytes { // the padding can push an exactly-at-limit text over: the limit applies to what goes out
		fail(w, http.StatusBadRequest, "too_long")
		return
	}
	tune := m.tuning()
	if m.answerKnown(w, td, in.ClientMsgID, tune) { // replay first: before the session or the owner are looked at
		return
	}
	scode := r.PathValue("code")
	info, err := td.sessions.GetSession(scode)
	switch {
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal")
		return
	case info == nil:
		fail(w, http.StatusNotFound, "session_not_found")
		return
	case info.TmuxInstance != in.ExpectedTmuxInstance:
		refuse(w, http.StatusConflict, "generation")
		return
	}
	owner, ok := m.resolveClaudeOwner(w, r, td, scode)
	if !ok {
		return
	}
	// From here the message is followed (stage received); the handler's answer decides its last stage. A replay answered under
	// the locks (a concurrent request recorded the id meanwhile) writes nothing.
	tok := m.stages.begin(owner.SessionID, in.ClientMsgID, text, tune.now())
	sw := &stageWriter{ResponseWriter: w}
	w = sw
	replayed := false
	defer func() {
		if !replayed {
			m.recordAnswer(owner.SessionID, in.ClientMsgID, tok, sw, tune.now())
		}
	}()
	release, ok := m.lockPaneAndSession(r.Context(), owner.TmuxPaneID, owner.SessionID, tune.lockWait)
	if !ok {
		refuse(w, http.StatusTooManyRequests, "busy")
		return
	}
	defer release()

	// Under the locks: the ledgers again (a concurrent request may have recorded the id), the owner again (it may have moved
	// while we waited) and the pane's identity.
	if m.answerKnown(w, td, in.ClientMsgID, tune) {
		replayed = true
		return
	}
	again, ok := m.resolveClaudeOwner(w, r, td, scode)
	if !ok {
		return
	}
	if again.SessionID != owner.SessionID || again.TmuxPaneID != owner.TmuxPaneID {
		refuse(w, http.StatusConflict, "owner_changed")
		return
	}
	// This request is the one that sends: it owns the id's stages from here (an earlier request of the same id that gave up
	// waiting for the locks, or is still waiting, can no longer overwrite them).
	tok = m.stages.claim(owner.SessionID, in.ClientMsgID, text, tune.now())
	if !m.paneIsUnique(w, r, td, again) {
		return
	}
	light := m.lightOf(again)
	if light == "waiting" {
		refuse(w, http.StatusConflict, "waiting")
		return
	}
	c := &typedCtx{stageTok: tok, td: td, id: in.ClientMsgID, text: text, expected: in.ExpectedTmuxInstance, tmuxID: info.TmuxID, owner: again}
	if td.q.HasOwner(again.SessionID) {
		m.fillSubmit(w, r, c, tune)
		return
	}
	m.keysSubmit(w, c, light, tune)
}

// resolveClaudeOwner finds the Claude Code frame that owns the tmux session; it answers the refusal itself when there is none.
func (m *Module) resolveClaudeOwner(w http.ResponseWriter, r *http.Request, td *typedDeps, code string) (agent.PaneOwner, bool) {
	owner, found, err := td.owners.ResolveSessionOwner(r.Context(), code)
	switch {
	case err != nil:
		fail(w, http.StatusServiceUnavailable, "owner_lookup_failed")
		return owner, false
	case !found || owner.AgentType != "cc" || owner.SessionID == "" || owner.TmuxPaneID == "":
		refuse(w, http.StatusConflict, "not_claude")
		return owner, false
	}
	return owner, true
}

// paneIsUnique is the pane-identity check. The Enter must go to the pane whose Claude Code process runs the mod that did the
// fill, but the mod's owner stream carries no frame id or pid to compare (modevents.StreamInfo has only the stream, sid and
// cwd), so the check is: exactly one live Claude Code pane has this session id, and it is the resolved owner's (409
// ambiguous_pane / pane_mismatch otherwise). Used in both modes.
func (m *Module) paneIsUnique(w http.ResponseWriter, r *http.Request, td *typedDeps, owner agent.PaneOwner) bool {
	panes, err := td.panes.ConfirmedOwners(r.Context(), owner.SessionID)
	switch {
	case err != nil && len(panes) == 0:
		fail(w, http.StatusServiceUnavailable, "owner_lookup_failed")
		return false
	case len(panes) > 1:
		refuse(w, http.StatusConflict, "ambiguous_pane")
		return false
	case len(panes) == 0 || panes[0].TmuxPaneID != owner.TmuxPaneID:
		refuse(w, http.StatusConflict, "pane_mismatch")
		return false
	}
	return true
}

// lightOf is the frame's current light: the cheap read when there is one, else what the owner lookup saw.
func (m *Module) lightOf(o agent.PaneOwner) string {
	if m.light != nil {
		if st, ok := m.light.LightStatus(o.SessionID, o.FrameID); ok {
			return st
		}
	}
	return o.Status
}

// freshLight is the light read right before a key goes out. ok is false when the cheap source exists but cannot answer (or
// answers unknown): the caller fails closed and types nothing. Without a cheap source the owner lookup's status is all there is.
func (m *Module) freshLight(o agent.PaneOwner) (string, bool) {
	if m.light == nil {
		return o.Status, o.Status != "" && o.Status != "unknown"
	}
	st, ok := m.light.LightStatus(o.SessionID, o.FrameID)
	if !ok || st == "" || st == "unknown" {
		return "", false
	}
	return st, true
}
