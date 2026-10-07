package teammod

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/wake/purdex/internal/team"
)

// 分流 routes (spec §6.6 steps 1–7; wire: internal/team/wire_ask.go).
//
//	POST /api/ask/begin        the mod opens a hook row for the native dialog it is showing
//	GET  /api/ask/wait/{id}    the mod's bounded long-poll (renews the lease, like /api/team/approvals/{id})
//	POST /api/ask/report/{id}  the native dialog settled: answered_local (terminal first) or dismissed
//
// A remote client answers through the ordinary decide route (handleDecide →
// decideHook). Every close goes through closeAs, so a hook row competes in
// the same CAS as a lead request and broadcasts exactly one closed — plus
// the one deliberate second closed of a terminal_override.

// terminalClient is decided_by for a close the terminal made.
func terminalClient() *team.Client {
	return &team.Client{Kind: team.ClientKindTerminal, Label: team.ClientKindTerminal}
}

// hookPayloadFor validates the begin payload for kind and returns it
// normalised with tool_use_id and terminal_only set by the daemon. An error
// string names what is wrong (a 400).
func hookPayloadFor(kind team.Kind, toolUseID string, raw json.RawMessage, terminalOnly bool) ([]byte, string) {
	switch kind {
	case team.KindHookAsk:
		var p team.HookAskPayload
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, "payload is not a hook_ask payload: " + err.Error()
			}
		}
		var qs []json.RawMessage
		if err := json.Unmarshal(p.Questions, &qs); err != nil || len(qs) == 0 {
			return nil, "payload.questions must be a non-empty array"
		}
		p.ToolUseID, p.TerminalOnly = toolUseID, terminalOnly
		b, err := json.Marshal(p)
		if err != nil {
			return nil, "encode payload: " + err.Error()
		}
		return b, ""
	case team.KindHookPermission:
		var p team.HookPermissionPayload
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, "payload is not a hook_permission payload: " + err.Error()
			}
		}
		if strings.TrimSpace(p.ToolName) == "" {
			return nil, "payload.tool_name is required"
		}
		if len(p.ToolInput) == 0 {
			p.ToolInput = json.RawMessage(`{}`)
		}
		p.ToolUseID, p.TerminalOnly = toolUseID, terminalOnly
		b, err := json.Marshal(p)
		if err != nil {
			return nil, "encode payload: " + err.Error()
		}
		return b, ""
	default:
		return nil, "kind must be hook_ask or hook_permission"
	}
}

// newHookRow is a fresh open hook row for origin, not yet stored, and its
// request hash. A terminal_only row has no lease (nobody polls it); a
// mod-raised one keeps the usual lease, renewed by each /api/ask/wait.
// Neither has a deadline (NoExpiryAt): the row lives as long as the native
// dialog.
func (m *Module) newHookRow(origin team.Origin, kind team.Kind, payload []byte, terminalOnly bool) (team.Approval, string) {
	now := m.now()
	lease := now + team.LeaseS*1000
	if terminalOnly {
		lease = team.NoExpiryAt
	}
	a := team.Approval{
		ID: uuid.NewString(), Kind: kind, HostID: m.hostID(), Origin: origin, Payload: payload, State: team.StateOpen,
		CreatedAt: now, DeadlineAt: team.NoExpiryAt, LeaseUntil: lease,
	}
	return a, requestHash(kind, origin.SessionID, 0, payload)
}

// openHookRow inserts and announces a hook row for origin. Callers hold
// createMu.
func (m *Module) openHookRow(origin team.Origin, kind team.Kind, payload []byte, terminalOnly bool) (team.Approval, error) {
	a, hash := m.newHookRow(origin, kind, payload, terminalOnly)
	stored, _, inserted, err := m.store.Create(a, hash)
	if err != nil {
		return team.Approval{}, err
	}
	if !inserted {
		return team.Approval{}, errors.New("fresh uuid already present")
	}
	m.announceOpened(stored, terminalOnly)
	return stored, nil
}

// takeOverTerminalOnly replaces the open terminal_only row old with a fresh
// answerable row for the mod's begin. The close and the insert are one
// transaction (Store.ReplaceTerminalOnly): an error leaves old open and
// announces nothing. Only after the commit come old's closed and wake-up
// (when this call closed it — something else may have first) and then the
// new row's opened, so a card sees the read-only card go before the
// answerable one arrives. Callers hold createMu.
func (m *Module) takeOverTerminalOnly(old team.Approval, origin team.Origin, kind team.Kind, payload []byte) (team.Approval, error) {
	a, hash := m.newHookRow(origin, kind, payload, false)
	closed, won, stored, err := m.store.ReplaceTerminalOnly(old.ID, Close{State: team.StateDismissed, DecidedAt: m.now()}, a, hash)
	if err != nil {
		return team.Approval{}, err
	}
	if won {
		m.logf("[team] approval %s dismissed: the mod's begin takes it over as %s", old.ID, stored.ID)
		m.announceClosed(closed, nil)
	}
	m.announceOpened(stored, false)
	return stored, nil
}

func (m *Module) announceOpened(a team.Approval, terminalOnly bool) {
	m.logf("[team] approval %s opened: kind=%s origin=%s (%s) terminal_only=%v", a.ID, a.Kind, a.Origin.Ref, a.Origin.SessionID, terminalOnly)
	m.broadcast("opened", &a)
}

// handleAskBegin is POST /api/ask/begin (spec §6.6 step 1).
func (m *Module) handleAskBegin(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.AskBeginRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SessionID) == "" || strings.TrimSpace(req.ToolUseID) == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "session_id and tool_use_id are required", nil)
		return
	}
	payload, bad := hookPayloadFor(req.Kind, req.ToolUseID, req.Payload, false)
	if bad != "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, bad, nil)
		return
	}
	// No remote responder ⇒ no row at all: the native dialog runs alone and
	// the mod pays nothing more (step 1). Checked before the origin lookup,
	// which reads the registry.
	if !m.responders.Any() {
		m.writeErr(w, http.StatusConflict, team.ErrNoResponders, "no client is connected to this host", nil)
		return
	}
	origin, ok, err := m.origins.ResolveOriginBySession(req.SessionID)
	if err != nil {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
		return
	}
	if !ok {
		m.writeErr(w, http.StatusNotFound, team.ErrUnknownSession, "session_id is not a live Claude Code session on this host", nil)
		return
	}
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	open, found, err := m.store.OpenByToolUse(origin.SessionID, req.ToolUseID)
	if err != nil {
		m.logf("[team] ask begin: %v", err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if found && !isTerminalOnly(open) {
		m.writeErr(w, http.StatusConflict, team.ErrAskOpen, "this tool use already has an open request", &open)
		return
	}
	if m.afterOpenByToolUse != nil {
		m.afterOpenByToolUse()
	}
	var stored team.Approval
	if found {
		// The settings hook got here first (the mod's hello was missed):
		// the read-only card gives way to an answerable one, atomically.
		stored, err = m.takeOverTerminalOnly(open, origin, req.Kind, payload)
	} else {
		stored, err = m.openHookRow(origin, req.Kind, payload, false)
	}
	if err != nil {
		m.logf("[team] ask begin: %v", err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.writeJSON(w, http.StatusCreated, team.AskBeginResponse{ID: stored.ID})
}

// isTerminalOnly reads the payload flag both hook payloads carry.
func isTerminalOnly(a team.Approval) bool {
	var p struct {
		TerminalOnly bool `json:"terminal_only"`
	}
	return json.Unmarshal(a.Payload, &p) == nil && p.TerminalOnly
}

// pollRow is the long-poll of GET /api/team/approvals/{id} and of
// GET /api/ask/wait/{id}: register the waiter, renew the lease, read, and
// while open and wait > 0 wait for the close, the timer, the client or Stop,
// then read again. ok=false means an error response was written.
func (m *Module) pollRow(w http.ResponseWriter, r *http.Request, id string) (team.Approval, bool) {
	wait, err := pollWait(r.URL.Query().Get("wait"))
	if err != nil {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, err.Error(), nil)
		return team.Approval{}, false
	}
	ch := m.addWaiter(id)
	defer m.removeWaiter(id, ch)
	if err := m.store.RenewLease(id, m.now()+team.LeaseS*1000); err != nil {
		m.logf("[team] get %s: %v", id, err)
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "storage error; retry", nil)
		return team.Approval{}, false
	}
	a, ok, err := m.store.Get(id)
	if err != nil {
		m.logf("[team] get %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return team.Approval{}, false
	}
	if !ok {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such approval request", nil)
		return team.Approval{}, false
	}
	if m.afterRead != nil {
		m.afterRead(id)
	}
	if a.State == team.StateOpen && wait > 0 {
		timer := time.NewTimer(time.Duration(wait) * time.Second)
		defer timer.Stop()
		select {
		case <-ch:
		case <-timer.C:
		case <-r.Context().Done():
		case <-m.stopCtx.Done():
		}
		if a, ok, err = m.store.Get(id); err != nil || !ok {
			m.logf("[team] get %s after wait: ok=%v err=%v", id, ok, err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
			return team.Approval{}, false
		}
	}
	return a, true
}

// askWaitOf maps a hook row to the wait body (spec §6.6 step 2). A remote
// answer is answered_remote with its hook: approved for either kind, and
// denied for a hook_permission (hook.behavior "deny"; a hook_ask cannot be
// denied — decideHook refuses it).
func askWaitOf(a team.Approval) team.AskWaitResponse {
	switch {
	case a.State == team.StateOpen:
		return team.AskWaitResponse{State: team.AskStillOpen}
	case a.State == team.StateApproved,
		a.State == team.StateDenied && a.Kind == team.KindHookPermission:
		return team.AskWaitResponse{State: team.AskAnsweredRemote, Hook: a.Hook}
	default:
		return team.AskWaitResponse{State: team.AskClosed, Reason: string(a.State)}
	}
}

// handleAskWait is GET /api/ask/wait/{id}?wait=25.
func (m *Module) handleAskWait(w http.ResponseWriter, r *http.Request) {
	a, ok := m.pollRow(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if !team.IsHookKind(a.Kind) {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "not a hook request", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, askWaitOf(a))
}

// handleAskReport is POST /api/ask/report/{id} (spec §6.6 steps 3, 5, 6).
// answered_local closes an open row through the CAS; when a remote decide
// already won (approved or denied), the terminal's answer still stands: the
// row becomes terminal_override and a second closed is broadcast. The
// terminal's answer is required and checked by kind first (localHookFor):
// without it the report is 400 and touches nothing. dismissed closes an
// open row (any hook it carries is ignored); against a closed one it is a
// no-op. Both answer the row as it now is, so a repeat is idempotent.
func (m *Module) handleAskReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req team.AskReportRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if req.State != team.StateAnsweredLocal && req.State != team.StateDismissed {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, `state must be "answered_local" or "dismissed"`, nil)
		return
	}
	a, ok, err := m.store.Get(id)
	if err != nil {
		m.logf("[team] ask report %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !ok {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such approval request", nil)
		return
	}
	if !team.IsHookKind(a.Kind) {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "not a hook request", nil)
		return
	}
	now := m.now()
	c := Close{State: req.State, DecidedAt: now}
	if req.State == team.StateAnsweredLocal {
		// Validated before any CAS: a hookless answer must neither close an
		// open row nor override a remote answer (P8a-1b review).
		hook, bad := localHookFor(a.Kind, req.Hook)
		if bad != "" {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, bad, nil)
			return
		}
		c.DecidedBy = terminalClient()
		c.Hook = hook
	}
	after, won, err := m.closeAs(id, c)
	if err != nil {
		m.logf("[team] ask report %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if won {
		m.logf("[team] approval %s %s by the terminal (origin %s)", id, after.State, after.Origin.Ref)
		m.writeJSON(w, http.StatusOK, after)
		return
	}
	if req.State == team.StateAnsweredLocal && (after.State == team.StateApproved || after.State == team.StateDenied) {
		// Step 5: the remote decide won the CAS — an approve, or a
		// hook_permission deny — but the terminal had already shown its
		// answer. Record the override and tell every card.
		over, won, err := m.store.OverrideIfDecided(id, now, c.Hook)
		if err != nil {
			m.logf("[team] ask report %s: %v", id, err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
			return
		}
		if won {
			m.logf("[team] approval %s terminal_override: the terminal's answer replaces %q's (origin %s)", id, labelOf(after.DecidedBy), after.Origin.Ref)
			m.broadcast("closed", &over)
		}
		m.writeJSON(w, http.StatusOK, over)
		return
	}
	m.writeJSON(w, http.StatusOK, after)
}

// localHookFor checks the terminal's answer of an answered_local report for
// kind and returns it in the kind's shape (as decideHook shapes a remote
// one): non-empty answers for a hook_ask; behavior allow (with
// updated_input) or deny (with message) for a hook_permission. A non-empty
// string names what is wrong (a 400).
func localHookFor(kind team.Kind, h *team.HookDecision) (*team.HookDecision, string) {
	if h == nil {
		h = &team.HookDecision{}
	}
	switch kind {
	case team.KindHookAsk:
		if len(h.Answers) == 0 {
			return nil, "answered_local of a hook_ask needs hook.answers"
		}
		return &team.HookDecision{Answers: h.Answers}, ""
	case team.KindHookPermission:
		switch h.Behavior {
		case "allow":
			return &team.HookDecision{Behavior: "allow", UpdatedInput: h.UpdatedInput}, ""
		case "deny":
			return &team.HookDecision{Behavior: "deny", Message: h.Message}, ""
		}
		return nil, `answered_local of a hook_permission needs hook.behavior "allow" or "deny"`
	default:
		return nil, "not a hook request"
	}
}

func labelOf(c *team.Client) string {
	if c == nil {
		return ""
	}
	return c.Label
}

// decideHook is handleDecide's branch for a hook row (spec §6.6 steps 4–5):
// the remote client's answer rides in `hook`; a terminal_only card is
// read-only; hook_ask takes approve with answers only, hook_permission takes
// approve (allow) or deny. The close goes through the same closeAs.
func (m *Module) decideHook(w http.ResponseWriter, a team.Approval, req team.DecideRequest, state team.State, client team.Client) {
	if isTerminalOnly(a) {
		m.writeErr(w, http.StatusConflict, team.ErrTerminalOnly, "這題只能在終端機回答", &a)
		return
	}
	hook := req.Hook
	if hook == nil {
		hook = &team.HookDecision{}
	}
	switch a.Kind {
	case team.KindHookAsk:
		if state != team.StateApproved {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "a hook_ask is answered with decision approve and hook.answers", nil)
			return
		}
		if len(hook.Answers) == 0 {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "hook.answers is required", nil)
			return
		}
		hook = &team.HookDecision{Answers: hook.Answers}
	case team.KindHookPermission:
		if state == team.StateApproved {
			hook = &team.HookDecision{Behavior: "allow", UpdatedInput: hook.UpdatedInput}
		} else {
			hook = &team.HookDecision{Behavior: "deny", Message: hook.Message}
		}
	}
	after, won, err := m.closeAs(a.ID, Close{State: state, DecidedAt: m.now(), DecidedBy: &client, Hook: hook})
	if err != nil {
		m.logf("[team] decide %s: %v", a.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !won {
		m.writeErr(w, http.StatusConflict, team.ErrAlreadyDecided, "this request was closed first by someone else", &after)
		return
	}
	m.logf("[team] approval %s %s by %s %q from %s (origin %s)", a.ID, after.State, client.Kind, client.Label, client.Addr, after.Origin.Ref)
	m.writeJSON(w, http.StatusOK, after)
}
