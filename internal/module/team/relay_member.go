package teammod

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// POST /api/team/relays (plan v3 P6-2b-1; spec §8.2 step 1): the lead asks for one of its members' relay. The op is
// created `requested` in one transaction with the membership confirmation (CreateMemberRelayOp), then the member's
// mod is told by a control message through the in-process sender (PL-1d1; no virtual peer, ruling Q1). Claiming and
// the op's long-poll are P6-2b-2.

// controlSendTimeout bounds one control send.
const controlSendTimeout = 5 * time.Second

// noMemberModDetail is relay_unsupported's detail (plan v3 P6-2b).
func noMemberModDetail(ref string) string {
	return ref + " 沒有載入 Purdex mod（或版本不符），無法接力；請手動接力或重開這個 member"
}

// modProtocolAtLeast reports whether the session's hello carried a protocol version of at least min (decimal).
func (m *Module) modProtocolAtLeast(sessionID string, min int) bool {
	m.mu.Lock()
	h, ok := m.modSeen[sessionID]
	m.mu.Unlock()
	if !ok {
		return false
	}
	v, err := strconv.Atoi(h.ModVersion)
	return err == nil && v >= min
}

func (m *Module) handleRelayCreate(w http.ResponseWriter, r *http.Request) {
	var req team.RelayCreateRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if id, err := uuid.Parse(req.ID); err != nil || id.Version() != 4 {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "id must be a UUID v4", nil)
		return
	}
	t, ok := m.callerTeam(w, req.OriginInbox)
	if !ok {
		return
	}
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	fail := func(err error) {
		m.logf("[team] member relay %s: %v", req.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
	}
	mr, found, err := m.matchMember(t, req.Target)
	switch {
	case errors.Is(err, errRegistry):
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
		return
	case err != nil:
		fail(err)
		return
	}
	// Replay: the same id for the same member is the op it opened, whatever its state; for another, id_conflict. "Same
	// member" is the same row incarnation: a session's row is moved to its new session by a relay, so the session id alone
	// would also accept a row released and adopted again later — that row is younger than the op.
	if old, had, err := m.store.GetRelayOp(req.ID); err != nil {
		fail(err)
		return
	} else if had {
		if old.Kind == team.RelayKindMember && old.TeamID == t.ID && found && (mr.SessionID == old.SessionID || mr.SessionID == old.NewSessionID) && mr.CreatedAt <= old.CreatedAt {
			m.writeJSON(w, http.StatusOK, team.RelayCreateResponse{Op: old})
			return
		}
		m.writeErr(w, http.StatusConflict, team.ErrIDConflict, "this relay id belongs to another request", nil)
		return
	}
	if !found || mr.State != team.MemberActive {
		m.writeErr(w, http.StatusConflict, team.ErrNotYourMember, req.Target+" is no active member of team "+t.ID, nil)
		return
	}
	origin, live, err := m.origins.ResolveOriginBySession(mr.SessionID)
	if err != nil {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
		return
	}
	if !live {
		m.writeErr(w, http.StatusNotFound, team.ErrUnknownSession, "member "+mr.Ref+" is not a running session", nil)
		return
	}
	if !m.modProtocolAtLeast(mr.SessionID, team.MinMemberRelayModVersion) {
		m.writeErr(w, http.StatusConflict, team.ErrRelayUnsupported, noMemberModDetail(mr.Ref), nil)
		return
	}
	if m.writeRelayOpen(w, mr.SessionID) {
		return
	}
	if err := os.MkdirAll(m.relayDir, 0o700); err != nil {
		fail(err)
		return
	}
	start := mr.ProcStart
	if start == "" {
		start = origin.ProcStart
	}
	if start == "" { // no start time, no identity: the cleared binding would be pid-only
		m.writeErr(w, http.StatusNotFound, team.ErrUnknownSession, "member "+mr.Ref+": its process cannot be identified", nil)
		return
	}
	now := m.now()
	op := team.RelayOp{
		ID: req.ID, Kind: team.RelayKindMember, HostID: m.hostID(), SessionID: mr.SessionID, Ref: mr.Ref, TeamID: t.ID,
		State: team.RelayRequested, HandoffPath: filepath.Join(m.relayDir, req.ID+".md"),
		PID: mr.PID, PaneID: mr.PaneID, ProcStart: start, CreatedAt: now, UpdatedAt: now,
	}
	if m.beforeMemberRelayInsert != nil {
		m.beforeMemberRelayInsert(mr)
	}
	op, _, err = m.store.CreateMemberRelayOp(op, nil)
	switch {
	case errors.Is(err, ErrMemberNotActive):
		m.writeErr(w, http.StatusConflict, team.ErrNotYourMember, "member "+mr.Ref+" left the team while the relay was being opened", nil)
		return
	case errors.Is(err, ErrRelayOpOpen):
		if !m.writeRelayOpen(w, mr.SessionID) {
			fail(err)
		}
		return
	case err != nil:
		fail(err)
		return
	}
	m.logf("[team] relay op %s opened: member %s (%s) of team %s", op.ID, mr.Ref, mr.SessionID, t.ID)
	m.sendMemberControlAsync(op)
	m.writeJSON(w, http.StatusCreated, team.RelayCreateResponse{Op: op})
}

// sendMemberControlAsync sends the control message after the commit, off the request's path.
func (m *Module) sendMemberControlAsync(op team.RelayOp) {
	if m.sender == nil {
		return
	}
	m.goTracked(func() { m.sendMemberControl(op) })
}

// sendMemberControl tells the member's mod to claim the op: `[pdx-relay:control] op=<id>`, from the inbox of the
// team's CURRENT lead (a lead that relayed since the request is not the one the request came from) to the member's
// ref. It is its own step so a later path (RQ-2's approval, the boot's re-send) calls the same function; it is safe to
// repeat for an op id — the claim is a compare-and-set — and it sends nothing for an op that is no longer requested.
// A failure is logged: the claim timeout (P6-4) covers a message that never arrived.
func (m *Module) sendMemberControl(op team.RelayOp) {
	cur, ok, err := m.store.GetRelayOp(op.ID)
	if err != nil || !ok || cur.State != team.RelayRequested {
		if err != nil {
			m.logf("[team] control for op %s: %v", op.ID, err)
		}
		return
	}
	t, ok, err := m.store.TeamByID(cur.TeamID)
	if err != nil || !ok || t.EndedAt != 0 {
		m.logf("[team] control for op %s: team %s is not live (%v)", op.ID, cur.TeamID, err)
		return
	}
	inbox, ok, err := m.origins.InboxOf(t.LeadSessionID)
	if err != nil || !ok {
		m.logf("[team] control for op %s: the lead has no live inbox (%v)", op.ID, err)
		return
	}
	alias, _ := m.selfHost()
	ctx, cancel := context.WithTimeout(m.stopCtx, controlSendTimeout)
	defer cancel()
	if _, err := m.sender.Send(ctx, ipeers.SendRequest{To: alias + "/" + cur.Ref, Text: team.RelayControlPrefix + cur.ID, OriginInbox: inbox}); err != nil {
		m.logf("[team] control for op %s to %s: %v", op.ID, cur.Ref, err)
	}
}
