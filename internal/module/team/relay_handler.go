package teammod

import (
	"net/http"
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
	// IdleIntercept is the level of idle_intercept the mod said it has heard (RT-4a); 0 for a mod that has not, or an older one.
	IdleIntercept int
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
	role, err := m.store.SessionRole(sessionID)
	switch {
	case err != nil:
		return "", err
	case role == sessionRoleLead:
		return roleLead, nil
	case role.isMember(): // a remote member too: self relay off, status member:true (cross-host team spec §5.3)
		return roleMember, nil
	}
	return roleNone, nil
}

// memberAutoLogEvery is how often the same unreadable-switch error is logged again.
const memberAutoLogEvery = 10 * time.Minute

// memberAutoOn says whether the automatic relay help for members is on for this host (relay.member_auto, spec
// 2026-10-24 member-relay-switch). It reads the switch on every call. A switch that cannot be read counts as off (fail
// closed, like the self switches): nothing is intercepted or told, compaction simply runs. No reader at all is on.
func (m *Module) memberAutoOn() bool {
	if m.switches == nil {
		return true
	}
	sw, err := m.switches.RelaySwitches()
	if err == nil {
		return sw.MemberAuto
	}
	msg := err.Error()
	now := time.Now()
	m.memberAutoLog.mu.Lock()
	quiet := msg == m.memberAutoLog.msg && now.Sub(m.memberAutoLog.at) < memberAutoLogEvery
	if !quiet {
		m.memberAutoLog.msg, m.memberAutoLog.at = msg, now
	}
	m.memberAutoLog.mu.Unlock()
	if !quiet {
		m.logf("[team] member_auto unreadable, treated as off: %v", err)
	}
	return false
}

// selfRelayState is a session's role and effective self-relay state (spec
// §8.7), from one role read: a member is off with no self switch (U13; its automatic help has relay.member_auto, see memberAutoOn);
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
	h := helloInfo{ModVersion: req.ModVersion, Agent: req.Agent, At: m.now(), IdleIntercept: req.IdleIntercept}
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
	resp := team.RelayHelloResponse{
		OK: true, Role: role, SelfRelay: state,
		Threshold: team.RelayThresholdPct, MinGrowth: team.RelayMinGrowth,
		IdleMinutes: team.RelayIdleMinutes, IdleThreshold: team.RelayIdleThresholdPct,
	}
	if m.memberAutoOn() {
		resp.IdleIntercept = team.IdleInterceptMembers
	}
	if h, ok, err := m.store.FreshHalfOf(req.SessionID); err != nil {
		m.logf("[team] relay hello %s: fresh half: %v", req.SessionID, err)
	} else if ok {
		resp.FreshHalf = &h
	}
	// A mod that reloaded mid-relay (pdx setup, #2441) holds no state for its op: hand it the op it must pick up. A
	// failed read only costs the recovery (the stall rule still fails the op), never the hello.
	if op, ok, err := m.store.OpenRelayOpBySession(req.SessionID); err != nil {
		m.logf("[team] relay hello %s: open op: %v", req.SessionID, err)
	} else if ok && (op.State == team.RelayClaimed || op.State == team.RelayWriting || op.State == team.RelayWritten) {
		resp.ActiveRelay = &team.RelayClaimResponse{Op: op}
		if op.Kind == team.RelayKindMember {
			resp.ActiveRelay.Lead = m.leadOf(op)
		}
	} else if ok && op.State == team.RelayAwaitingApproval {
		// A final card nobody answered stays open for good (RT-2a1): a mod that reloaded holds no request for it and adopts it here.
		if row, found, err := m.store.Get(op.RequestID); err != nil {
			m.logf("[team] relay hello %s: card %s: %v", req.SessionID, op.RequestID, err)
		} else if found && row.State == team.StateOpen && row.UnansweredAt > 0 {
			resp.Waiting = &team.RelayWaiting{RequestID: row.ID, Op: op, UnansweredAt: row.UnansweredAt}
		}
	}
	m.writeJSON(w, http.StatusOK, resp)
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

// handleRelayWait is GET /api/relay/wait/{id}?wait=N: the self_relay
// approval row's long-poll, with the same lease renewal as a lead request
// (spec §8.7 (b) "The lease is renewed by the mod's wait"). It is
// handleGet under another path: the route is registered with {id} so
// PathValue agrees.
func (m *Module) handleRelayWait(w http.ResponseWriter, r *http.Request) { m.handleGet(w, r) }
