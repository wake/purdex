package teammod

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wake/purdex/internal/team"
)

// TitleMover moves a session title to a new session id (lead-team-relay
// spec §8.4); *store.PeerLabelStore in production, nil when the daemon
// runs without a meta store (tests): then no title moves and that is logged.
type TitleMover interface {
	Move(fromSessionID, toSessionID string, now time.Time) (bool, error)
}

// helloInfo is what a session's mod last said in hello (spec §8.3); P6's
// relay_unsupported check reads it, P8a-1a's modPresent() reads presence.
type helloInfo struct {
	ModVersion, Agent string
	At                int64
}

// modSeenCap bounds the modSeen map; sessions come and go and nothing else prunes it.
const modSeenCap = 512

// The roles of spec §8.7, as RelayHelloResponse.Role carries them.
const (
	roleNone   = "none"
	roleLead   = "lead"
	roleMember = "member"
)

// relayRole is the session's role (spec §8.7), read live on every hello,
// self and begin: lead of a live team, active member of one, else none
// (an ended team's lead or member, D4). A store error is an error, never
// none (plan v3 deviation 12): the caller answers 500 (fail closed).
func (m *Module) relayRole(sessionID string) (string, error) {
	if _, ok, err := m.store.LiveTeamByLead(sessionID); err != nil {
		return "", err
	} else if ok {
		return roleLead, nil
	}
	if _, _, ok, err := m.store.ActiveMemberInLiveTeam(sessionID); err != nil {
		return "", err
	} else if ok {
		return roleMember, nil
	}
	return roleNone, nil
}

// selfRelayState is a session's role and effective self-relay state (spec
// §8.7), from one role read: a member is off with no switch (U13);
// otherwise the host switch for the role, then the session's pause.
// hostSwitch is the switch that applied (false for a member, which has none).
func (m *Module) selfRelayState(sessionID string) (role, state string, hostSwitch bool, err error) {
	role, err = m.relayRole(sessionID)
	if err != nil {
		return "", "", false, err
	}
	if role == roleMember {
		return role, "off", false, nil
	}
	sw, err := m.switches.RelaySwitches()
	if err != nil {
		return role, "", false, err
	}
	hostSwitch = sw.SelfSolo
	if role == roleLead {
		hostSwitch = sw.SelfLead
	}
	if !hostSwitch {
		return role, "off", false, nil
	}
	paused, err := m.store.SelfRelayPaused(sessionID)
	if err != nil {
		return role, "", hostSwitch, err
	}
	if paused {
		return role, "paused", hostSwitch, nil
	}
	return role, "on", hostSwitch, nil
}

// handleRelayHello is POST /api/relay/hello (spec §8.3): the mod says this
// session can relay and learns its role, switch state and the thresholds.
func (m *Module) handleRelayHello(w http.ResponseWriter, r *http.Request) {
	var req team.RelayHelloRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "session_id is required", nil)
		return
	}
	role, state, _, err := m.selfRelayState(req.SessionID)
	if err != nil {
		m.logf("[team] relay hello %s: %v", req.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "role or switches unreadable; see the daemon log", nil)
		return
	}
	m.helloMu.Lock() // memory and the row are written in one order: the last hello to take the lock wins in both
	m.mu.Lock()
	h := helloInfo{ModVersion: req.ModVersion, Agent: req.Agent, At: m.now()}
	m.modSeen[req.SessionID] = h
	if len(m.modSeen) > modSeenCap { // the same victim the table picks: the oldest At, the greatest session id among ties
		victim, v := "", helloInfo{}
		for sid, e := range m.modSeen {
			if victim == "" || e.At < v.At || (e.At == v.At && sid > victim) {
				victim, v = sid, e
			}
		}
		delete(m.modSeen, victim)
	}
	m.mu.Unlock()
	if err := m.store.UpsertModHello(req.SessionID, h, modSeenCap); err != nil { // the memory answers; a lost row only costs a hello after a restart
		m.logf("[team] relay hello %s: %v", req.SessionID, err)
	}
	m.helloMu.Unlock()
	m.writeJSON(w, http.StatusOK, team.RelayHelloResponse{
		OK: true, Role: role, SelfRelay: state,
		Threshold: team.RelayThresholdPct, MinGrowth: team.RelayMinGrowth,
	})
}

// handleRelaySelf is POST /api/relay/self (spec §8.7): the per-session
// pause. "on" lifts the session's own pause only — never a host switch
// that is off; "status" reads. A member is told its relay is the lead's.
func (m *Module) handleRelaySelf(w http.ResponseWriter, r *http.Request) {
	var req team.RelaySelfRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "session_id is required", nil)
		return
	}
	switch req.Action {
	case "on", "off":
		role, err := m.relayRole(req.SessionID)
		if err != nil {
			m.logf("[team] relay self %s: %v", req.SessionID, err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "role unreadable; see the daemon log", nil)
			return
		}
		if role == roleMember {
			m.writeErr(w, http.StatusConflict, team.ErrMemberRelayIsLeads, "member 的接力由 lead 安排", nil)
			return
		}
		if err := m.store.SetSelfRelayPaused(req.SessionID, req.Action == "off", m.now()); err != nil {
			m.logf("[team] relay self %s: %v", req.SessionID, err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
			return
		}
	case "status":
	default:
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, `action must be "off", "on" or "status"`, nil)
		return
	}
	role, state, hostSwitch, err := m.selfRelayState(req.SessionID)
	if err != nil {
		m.logf("[team] relay self %s: %v", req.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "role or switches unreadable; see the daemon log", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, team.RelaySelfResponse{SelfRelay: state, HostSwitch: hostSwitch, Member: role == roleMember})
}

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
	if req.RequestID != "" {
		if u, err := uuid.Parse(req.RequestID); err != nil || u.Version() != 4 || u.Variant() != uuid.RFC4122 {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "request_id must be a UUID v4", nil)
			return
		}
		// A replay (the CLI's retry after a lost response, PR #1726 A-1):
		// the request already opened its op — answer with that op, in
		// whatever state it is now, never a second one.
		if op, ok, err := m.store.RelayOpByRequest(req.RequestID); err != nil {
			m.logf("[team] relay begin %s: replay lookup %s: %v", req.SessionID, req.RequestID, err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
			return
		} else if ok {
			// Same id ⇒ same request: the session AND the payload must match
			// (Store.Create's idempotency rule, "same hash is a retry, a
			// different hash is a conflicting reuse"); anything else is 409.
			if op.SessionID != req.SessionID {
				m.writeErr(w, http.StatusConflict, team.ErrBadRequest, "request_id belongs to another session's relay", nil)
				return
			}
			if !sameBeginPayload(m, op, req) {
				m.writeErr(w, http.StatusConflict, team.ErrBadRequest, "request_id reused with a different used_percentage or window", nil)
				return
			}
			m.writeJSON(w, http.StatusCreated, team.RelayBeginResponse{Op: op, RequestID: op.RequestID})
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
	switch {
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
	sp := team.SelfRelayPayload{UsedPercentage: req.UsedPercentage, Window: req.Window}
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
		m.writeErr(w, http.StatusConflict, team.ErrMemberRelayIsLeads, "member 的接力由 lead 安排", nil)
		return
	}
	if err := os.MkdirAll(m.relayDir, 0o700); err != nil {
		m.logf("[team] relay begin %s: mkdir %s: %v", req.SessionID, m.relayDir, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "cannot create the relay directory; see the daemon log", nil)
		return
	}
	now := m.now()
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

// handleRelayWait is GET /api/relay/wait/{id}?wait=N: the self_relay
// approval row's long-poll, with the same lease renewal as a lead request
// (spec §8.7 (b) "The lease is renewed by the mod's wait"). It is
// handleGet under another path: the route is registered with {id} so
// PathValue agrees.
func (m *Module) handleRelayWait(w http.ResponseWriter, r *http.Request) { m.handleGet(w, r) }

func reasonSuffix(op team.RelayOp) string {
	if op.Reason == "" {
		return ""
	}
	return " (" + op.Reason + ")"
}

// afterClose follows every approval close that won (closeWith): a
// self_relay row's close moves its op (spec §8.7 (b)): approved →
// claimed; denied → cancelled{denied}; timeout → cancelled{timeout};
// cancelled or abandoned → cancelled{abandoned}. A lead row has no op.
// The two writes are not one transaction: a failure here is logged, and
// the op is re-derived from its row by reconcileAwaitingOp at the next
// begin of that session and by the boot reconciliation (P5a-2b).
func (m *Module) afterClose(a team.Approval, rep *RelayReport) {
	if a.Kind != team.KindSelfRelay {
		return
	}
	op, ok, err := m.store.RelayOpByRequest(a.ID)
	if err != nil || !ok {
		m.logf("[team] approval %s closed but its relay op is missing: ok=%v err=%v", a.ID, ok, err)
		return
	}
	var report RelayReport
	if rep != nil {
		report = *rep // a terminal report drove this close: its state and reason stand
	} else if report, err = m.closedRowReport(a, m.now()); err != nil {
		m.logf("[team] approval %s %s: relay op %s left for reconciliation: %v", a.ID, a.State, op.ID, err)
		return
	}
	after, res, err := m.store.ReportRelay(op.ID, report)
	if err != nil {
		m.logf("[team] approval %s %s: relay op %s: %v", a.ID, a.State, op.ID, err)
		return
	}
	switch res {
	case ReportApplied:
		m.logf("[team] relay op %s → %s%s (approval %s %s)", op.ID, after.State, reasonSuffix(after), a.ID, a.State)
	case ReportBadTransition:
		m.logf("[team] approval %s %s: relay op %s is %s, which does not lead to the row's state; left as is", a.ID, a.State, op.ID, after.State)
	}
}

// opReportForClosedRow is the op transition a closed self_relay row
// implies (spec §8.7 (b)); afterClose and reconcileAwaitingOp share it so
// the mapping lives once.
func opReportForClosedRow(a team.Approval, at int64) RelayReport {
	rep := RelayReport{At: at}
	switch a.State {
	case team.StateApproved:
		rep.State = team.RelayClaimed
	case team.StateDenied:
		rep.State, rep.Reason = team.RelayCancelled, team.RelayReasonDenied
	case team.StateTimeout:
		rep.State, rep.Reason = team.RelayCancelled, team.RelayReasonTimeout
	default: // cancelled, abandoned
		rep.State, rep.Reason = team.RelayCancelled, team.RelayReasonAbandoned
	}
	return rep
}

// closedRowReport is opReportForClosedRow with U13 (P4-3 review H2): an
// approved row of a session that is now a member cancels its op
// {member_relay_is_leads} instead of claiming it. A role read error is an error.
func (m *Module) closedRowReport(a team.Approval, at int64) (RelayReport, error) {
	rep := opReportForClosedRow(a, at)
	if rep.State != team.RelayClaimed {
		return rep, nil
	}
	role, err := m.relayRole(a.Origin.SessionID)
	if role == roleMember {
		rep.State, rep.Reason = team.RelayCancelled, team.ErrMemberRelayIsLeads
	}
	return rep, err
}

// reconcileAwaitingOp re-derives an awaiting_approval op from its approval
// row: the row is open → the op is genuinely open (still = true); the row
// is closed → the op takes the transition the close implied (afterClose
// missed it, or failed); the row does not exist → the op is an orphan of a
// begin that crashed between its two writes and is cancelled{abandoned}.
// Any other op state is open by definition. It returns the op as it is
// after the step (the 409 carries THAT, not the stale read) and whether
// it is still open. Called under createMu.
func (m *Module) reconcileAwaitingOp(op team.RelayOp) (cur team.RelayOp, still bool, err error) {
	if op.State != team.RelayAwaitingApproval {
		return op, true, nil
	}
	row, ok, err := m.store.Get(op.RequestID)
	if err != nil {
		return op, true, err
	}
	rep := RelayReport{State: team.RelayCancelled, Reason: team.RelayReasonAbandoned, At: m.now()}
	switch {
	case ok && row.State == team.StateOpen:
		return op, true, nil
	case ok:
		if rep, err = m.closedRowReport(row, m.now()); err != nil {
			return op, true, err
		}
	}
	after, res, err := m.store.ReportRelay(op.ID, rep)
	if err != nil {
		return op, true, err
	}
	if res == ReportApplied {
		m.logf("[team] relay op %s → %s%s (reconciled at begin: approval %s %s)", op.ID, after.State, reasonSuffix(after), op.RequestID, rowStateOrMissing(row, ok))
	}
	return after, !after.State.Terminal(), nil
}

func rowStateOrMissing(row team.Approval, ok bool) string {
	if !ok {
		return "missing"
	}
	return string(row.State)
}

// sameBeginPayload reports whether a replayed begin carries the payload the
// op was opened with: the op's used_percentage and the approval row's
// window (SelfRelayPayload). A row that cannot be read or decoded is "not
// the same" — a replay is proven, never assumed.
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
	return sp.Window == req.Window
}

// paneOf is the "%N" pane id at the end of an Origin.Tmux ("<session>:@<win>.%<pane>"), "" when there is none.
func paneOf(tmux string) string {
	if i := strings.LastIndex(tmux, ".%"); i >= 0 {
		return tmux[i+1:]
	}
	return ""
}
