package teammod

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/wake/purdex/internal/team"
)

// handleRelayBegin is POST /api/relay/begin (spec §8.1, §8.7 (b)): the mod
// asks to self-relay. Role, switch and pause are checked first (409s of
// spec §14), then — under createMu, like a lead request — one op in
// awaiting_approval and one self_relay approval row open together, and
// the request id is answered. Only self: a member relay is the lead's
// POST /api/team/relays (P6).
func (m *Module) handleRelayBegin(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.RelayBeginRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if !req.Self {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "only self relays begin here; a member relay is POST /api/team/relays", nil)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" || req.UsedPercentage < 0 || req.UsedPercentage > 100 || req.Window < 0 {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "session_id is required; used_percentage must be 0–100 and window non-negative", nil)
		return
	}
	// An idle ask (idle relay spec §2) names its minutes (1–59); the threshold ask has neither. A /relay is a person's
	// word, never an idle ask.
	switch req.Reason {
	case "":
		if req.IdleMinutes != 0 {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "idle_minutes is only for reason idle", nil)
			return
		}
	case team.RelayBeginReasonIdle:
		if req.IdleMinutes < 1 || req.IdleMinutes > 59 || req.Manual {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "an idle begin needs idle_minutes 1–59 and cannot be manual", nil)
			return
		}
	default:
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "unknown reason", nil)
		return
	}
	if req.ReaskID != "" {
		if u, err := uuid.Parse(req.ReaskID); err != nil || u.String() != strings.ToLower(req.ReaskID) {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "reask_id must be a UUID", nil)
			return
		}
	}
	if req.RequestID != "" {
		if u, err := uuid.Parse(req.RequestID); err != nil || u.Version() != 4 || u.Variant() != uuid.RFC4122 {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "request_id must be a UUID v4", nil)
			return
		}
		if m.replayOfBegin(w, req) {
			return
		}
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
	role, state, _, err := m.selfRelayState(req.SessionID)
	if err != nil {
		m.logf("[team] relay begin %s: %v", req.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "role or switches unreadable; see the daemon log", nil)
		return
	}
	if role == roleMember && req.Manual {
		// A person's /relay in a member's session (MR-1, U-M1): the member refusals are lifted. It is judged like a
		// session with no role: the ordinary switch, and the pause (which a member cannot lift) is ignored.
		if !m.manualMemberAllowed(w, req.SessionID) {
			return
		}
		sw, err := m.switches.RelaySwitches()
		if err != nil {
			m.logf("[team] relay begin %s: %v", req.SessionID, err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "role or switches unreadable; see the daemon log", nil)
			return
		}
		role, state = roleNone, "on"
		if !sw.SelfSolo {
			state = "off"
		}
	}
	person := req.Manual && req.PersonToken != "" // a person's own /relay (RT-1a): the token is checked under createMu
	switch {
	case person:
		// the switches, /relay off and the pause do not apply to what the person typed
	case role == roleMember:
		m.writeErr(w, http.StatusConflict, team.ErrMemberRelayIsLeads, "member 的接力由 lead 安排", nil)
		return
	case state == "off":
		m.writeErr(w, http.StatusConflict, team.ErrSelfRelayOff, "self relay is off on this host (host config relay)", nil)
		return
	case state == "paused":
		m.writeErr(w, http.StatusConflict, team.ErrSelfRelayPaused, "this session paused self relay (/relay on lifts it)", nil)
		return
	}
	// model_id / effort are the daemon's to fill (spec U18 (b), M21): the
	// session's last statusline reading, kept by the agent module. The mod
	// sends neither; a session without a reading shows neither.
	sp := team.SelfRelayPayload{UsedPercentage: req.UsedPercentage, Window: req.Window, Manual: req.Manual, Reason: req.Reason, IdleMinutes: req.IdleMinutes, Person: person}
	if m.usage != nil {
		if u, ok := m.usage.ContextUsage(req.SessionID); ok {
			sp.ModelID, sp.Effort = u.ModelID, u.Effort
		}
	}
	payload, err := json.Marshal(sp)
	if err != nil {
		m.writeErr(w, http.StatusInternalServerError, errStorage, "encode payload: "+err.Error(), nil)
		return
	}

	m.createMu.Lock()
	defer m.createMu.Unlock()
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	if m.replayOfBegin(w, req) { // the same request may have won the lock while this one waited
		return
	}
	if person && !m.consumePerson(req.SessionID, team.PersonCmdRelay, req.PersonToken) {
		m.writeErr(w, http.StatusConflict, team.ErrPersonTokenInvalid, "the person token is unknown, spent, expired or not for this session's /relay", nil)
		return
	}
	// At most one open self-relay per session (spec §8.7 (c)): the op is
	// the authority; the approval row follows it. An op still in
	// awaiting_approval whose row is no longer open (or never got written)
	// is re-derived from the row first — the op and the row are two
	// writes, and a crash or a failed second write between them must not
	// hold the session's begins at 409 until the next daemon boot.
	if open, found, err := m.store.OpenRelayOpBySession(req.SessionID); err != nil {
		m.logf("[team] relay begin %s: %v", req.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	} else if found {
		if cur, still, err := m.reconcileAwaitingOp(open); err != nil {
			m.logf("[team] relay begin %s: reconcile op %s: %v", req.SessionID, open.ID, err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
			return
		} else if still {
			if person && cur.State == team.RelayAwaitingApproval {
				m.personApproveOpen(w, req.SessionID, cur) // the card is the person's to approve, and they just did
				return
			}
			m.writeJSON(w, http.StatusConflict, team.APIError{Error: team.ErrRelayOpen, Detail: "this session already has a relay in progress", Op: &cur})
			return
		}
	}
	if m.afterOpenCheck != nil {
		m.afterOpenCheck(req.SessionID)
	}
	// U13 again, under createMu just before the op is created (P4-3 review
	// H2): a session that became a member since the check above opens nothing.
	if role, err := m.relayRole(req.SessionID); err != nil {
		m.logf("[team] relay begin %s: %v", req.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "role unreadable; see the daemon log", nil)
		return
	} else if role == roleMember {
		// A manual begin passes (MR-1), unless the member since became a remote one (MR-2).
		if !req.Manual {
			m.writeErr(w, http.StatusConflict, team.ErrMemberRelayIsLeads, "member 的接力由 lead 安排", nil)
			return
		}
		if !m.manualMemberAllowed(w, req.SessionID) {
			return
		}
	}
	if err := os.MkdirAll(m.relayDir, 0o700); err != nil {
		m.logf("[team] relay begin %s: mkdir %s: %v", req.SessionID, m.relayDir, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "cannot create the relay directory; see the daemon log", nil)
		return
	}
	now := m.now()
	// A valid release token (spec §3.4, UP-3b) makes the begin a card for a person: this session's, unspent, under 24 h. Read under
	// createMu, where the reconciliation's prune also runs; the card's transaction spends it.
	reask := false
	if req.ReaskID != "" {
		var err error
		if reask, err = m.store.ReleaseTokenValid(req.ReaskID, req.SessionID, now-team.PauseTokenTTLMs); err != nil {
			m.logf("[team] relay begin %s: %v", req.SessionID, err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
			return
		}
	}
	if person {
		reask = false // the person's relay is the answer; a release token is not spent on it
	}
	sp.Reask = reask
	if reask {
		sp.ReaskID = req.ReaskID
	}
	opID, reqID := m.newID(), req.RequestID
	if reqID == "" {
		reqID = m.newID()
	}
	pct := req.UsedPercentage
	op := team.RelayOp{
		ID: opID, Kind: team.RelayKindSelf, HostID: m.hostID(), SessionID: origin.SessionID, Ref: origin.Ref,
		RequestID: reqID, State: team.RelayAwaitingApproval, HandoffPath: filepath.Join(m.relayDir, opID+".md"),
		UsedPercentage: &pct, CreatedAt: now, UpdatedAt: now, PID: origin.PID, PaneID: paneOf(origin.Tmux), ProcStart: origin.ProcStart,
	}
	sp.OpID = opID
	payload, _ = json.Marshal(sp) // encoded above already; now with its op id
	row := team.Approval{
		ID: reqID, Kind: team.KindSelfRelay, HostID: m.hostID(), Origin: origin, Payload: payload, State: team.StateOpen,
		CreatedAt: now, DeadlineAt: now + team.SelfRelayDeadlineS*1000, LeaseUntil: now + team.LeaseS*1000,
	}
	hash := requestHash(team.KindSelfRelay, origin.SessionID, team.SelfRelayDeadlineS, payload)
	if reask {
		// A re-ask after a pause: never paused, never approved for the person, whatever the switch and the quota say.
		stored, err := m.store.CreateSelfRelayCard(op, row, hash, req.ReaskID, now-team.PauseTokenTTLMs)
		if err != nil {
			if errors.Is(err, ErrRelayOpOpen) && m.writeRelayOpen(w, req.SessionID) {
				return
			}
			m.logf("[team] relay begin %s: reask card: %v", req.SessionID, err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
			return
		}
		m.logf("[team] relay op %s opened: self, origin=%s (%s) request=%s, a re-ask after a pause: a card for a person", opID, origin.Ref, origin.SessionID, reqID)
		m.broadcast("opened", &stored)
		m.writeJSON(w, http.StatusCreated, team.RelayBeginResponse{Op: op, RequestID: reqID})
		return
	}
	if person {
		m.beginPerson(w, op, row, hash)
		return
	}
	exhausted := false // the chain has no quota left: the request opens for a person below and is listed as held at once
	if m.unattendedOn() {
		var done bool
		if done, exhausted = m.beginApproved(w, op, row, hash); done {
			return // approved at begin, or refused
		}
	}
	if err := m.store.CreateRelayOp(op); err != nil {
		// The table's one-open-op index caught a creator the check above
		// did not see (PR P5a-1a codex R1): the same 409, not a 500.
		if errors.Is(err, ErrRelayOpOpen) && m.writeRelayOpen(w, req.SessionID) {
			return
		}
		m.logf("[team] relay begin %s: %v", req.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	stored, _, inserted, err := m.store.Create(row, hash)
	if err != nil || !inserted {
		// The op is already there: close it so the session is not stuck
		// behind an op whose approval never opened.
		if _, _, rerr := m.store.ReportRelay(opID, RelayReport{State: team.RelayCancelled, Reason: team.RelayReasonAbandoned, At: now}); rerr != nil {
			m.logf("[team] relay begin %s: cancel orphan op %s: %v", req.SessionID, opID, rerr)
		}
		m.logf("[team] relay begin %s: approval row: inserted=%v err=%v", req.SessionID, inserted, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if exhausted {
		m.holdForQuota(stored.ID) // only now that the row exists; the sweeps keep it quiet and approve it once the quota is raised
	}
	m.logf("[team] relay op %s opened: self, origin=%s (%s) used=%.0f%% request=%s", opID, origin.Ref, origin.SessionID, req.UsedPercentage, reqID)
	m.broadcast("opened", &stored)
	m.writeJSON(w, http.StatusCreated, team.RelayBeginResponse{Op: op, RequestID: reqID})
}

// replayOfBegin answers a begin whose request_id already opened an op (the CLI's retry after a lost response, PR #1726 A-1) with that
// op, in whatever state it is now, never a second one; true when it answered (a replay or an error). It runs before createMu and again
// under it: two identical begins that arrive together must both get the op the first one opened (codex, UP-3b).
func (m *Module) replayOfBegin(w http.ResponseWriter, req team.RelayBeginRequest) bool {
	if req.RequestID == "" {
		return false
	}
	op, ok, err := m.store.RelayOpByRequest(req.RequestID)
	if err != nil {
		m.logf("[team] relay begin %s: replay lookup %s: %v", req.SessionID, req.RequestID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return true
	}
	if !ok {
		return false
	}
	// Same id ⇒ same request: the session AND the payload must match (Store.Create's idempotency rule, "same hash is a retry, a
	// different hash is a conflicting reuse"); anything else is 409.
	if op.SessionID != req.SessionID {
		m.writeErr(w, http.StatusConflict, team.ErrBadRequest, "request_id belongs to another session's relay", nil)
		return true
	}
	if !sameBeginPayload(m, op, req) {
		m.writeErr(w, http.StatusConflict, team.ErrBadRequest, "request_id reused with a different used_percentage, window, manual, reason or idle_minutes", nil)
		return true
	}
	m.writeJSON(w, http.StatusCreated, team.RelayBeginResponse{Op: op, RequestID: op.RequestID})
	return true
}

// writeRelayOpen answers 409 relay_open with the session's open op; false
// (nothing written) when there is none to show.
func (m *Module) writeRelayOpen(w http.ResponseWriter, sessionID string) bool {
	open, found, err := m.store.OpenRelayOpBySession(sessionID)
	if err != nil || !found {
		return false
	}
	m.writeJSON(w, http.StatusConflict, team.APIError{Error: team.ErrRelayOpen, Detail: "this session already has a relay in progress", Op: &open})
	return true
}

// manualMemberAllowed is false (and the answer written) when a member's manual begin cannot go on: the member is
// being removed, or the role cannot be read. A member of a team led on another host passes like a local one (MR-2).
func (m *Module) manualMemberAllowed(w http.ResponseWriter, sessionID string) bool {
	if killing, err := m.store.IsKillingMember(sessionID); err != nil {
		m.logf("[team] relay begin %s: %v", sessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "role unreadable; see the daemon log", nil)
		return false
	} else if killing {
		// moveTeamRoles moves active rows only: a relay now would leave the seat behind.
		m.writeErr(w, http.StatusConflict, team.ErrRelayUnsupported, "this member is being removed; it cannot be relayed by hand", nil)
		return false
	}
	return true
}

// sameBeginPayload reports whether a replayed begin carries the payload the
// op was opened with: the op's used_percentage and the approval row's
// window and manual flag (SelfRelayPayload). A row that cannot be read or
// decoded is "not the same" — a replay is proven, never assumed.
func sameBeginPayload(m *Module, op team.RelayOp, req team.RelayBeginRequest) bool {
	if op.UsedPercentage == nil || *op.UsedPercentage != req.UsedPercentage {
		return false
	}
	row, ok, err := m.store.Get(op.RequestID)
	if err != nil || !ok {
		return false
	}
	var sp team.SelfRelayPayload
	if err := json.Unmarshal(row.Payload, &sp); err != nil {
		return false
	}
	if sp.Reask && req.ReaskID != sp.ReaskID {
		return false // a re-ask replayed with another token, or none, is not the same request
	}
	if req.ReaskID != "" && !sp.Reask {
		// An ordinary begin replayed with a release token that is valid now is another request, not a retry: answering it with the
		// ordinary op would leave the token unspent and the person's card unopened (codex R1).
		if valid, err := m.store.ReleaseTokenValid(req.ReaskID, op.SessionID, m.now()-team.PauseTokenTTLMs); err != nil || valid {
			return false
		}
	}
	return sp.Window == req.Window && sp.Manual == req.Manual && sp.Reason == req.Reason && sp.IdleMinutes == req.IdleMinutes &&
		sp.Person == (req.Manual && req.PersonToken != "")
}

// paneOf is the "%N" pane id at the end of an Origin.Tmux ("<session>:@<win>.%<pane>"), "" when there is none.
func paneOf(tmux string) string {
	if i := strings.LastIndex(tmux, ".%"); i >= 0 {
		return tmux[i+1:]
	}
	return ""
}
