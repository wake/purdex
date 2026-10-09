package teammod

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	var (
		spent bool
		held  *team.Approval // the member_relay row this create opened (pool spent out), for the announce after the commit
	)
	op, _, err = m.store.CreateMemberRelayOp(op, m.memberRelayGate(req.OriginInbox, mr, &spent, &held))
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
	m.logf("[team] relay op %s opened: member %s (%s) of team %s (%s)", op.ID, mr.Ref, mr.SessionID, t.ID, op.State)
	if spent {
		m.announceSpend(t.LeadSessionID) // the lead's pool is spent: publish the chain's new numbers
	}
	if held != nil { // the pool is spent out: a person decides (RQ-2 §4.1), listed as held at once
		m.holdForQuota(held.ID)
		m.broadcast("opened", held)
	} else {
		m.sendMemberControlAsync(op)
	}
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

// memberRelayGate is CreateMemberRelayOp's gate (RQ-2 §4.1), run in the create's transaction under createMu. Unattended
// off, or the quota rule off: the op stays `requested` and nothing is spent (a member's relay needs no approval, U13).
// Both on: one unit of the lead's pool is spent in this transaction (spent is set); at 0 the op becomes
// awaiting_approval with RequestID = the member_relay row's id, and that row (held) is inserted open in the same
// transaction. Any failure rolls everything back.
func (m *Module) memberRelayGate(inbox string, mr memberRow, spent *bool, held **team.Approval) MemberRelayGate {
	return func(tx *sql.Tx, op *team.RelayOp) (bool, error) {
		if !m.unattendedOn() || !m.quotaRuleOn() {
			return false, nil
		}
		err := spendPoolIn(tx, op.TeamID, op.CreatedAt)
		if err == nil {
			if m.afterPoolSpend != nil {
				if err := m.afterPoolSpend(); err != nil { // test seam: a failure right after the spend
					return false, err
				}
			}
			*spent = true
			return false, nil
		}
		if !errors.Is(err, ErrQuotaExhausted) {
			return false, err
		}
		lead, ok, err := m.origins.ResolveOrigin(inbox)
		if err != nil || !ok {
			return false, fmt.Errorf("the lead's origin: ok=%v err=%v", ok, err)
		}
		p := team.MemberRelayPayload{OpID: op.ID, TeamID: op.TeamID, LeadRef: lead.Ref, LeadTitle: lead.Title,
			MemberSessionID: op.SessionID, MemberRef: mr.Ref, MemberTitle: mr.Title}
		if m.usage != nil {
			if u, ok := m.usage.ContextUsage(op.SessionID); ok {
				p.UsedPercentage = u.UsedPercentage
			}
		}
		payload, err := json.Marshal(p)
		if err != nil {
			return false, err
		}
		now := op.CreatedAt
		row := team.Approval{ID: m.newID(), Kind: team.KindMemberRelay, HostID: op.HostID, Origin: lead, Payload: payload, State: team.StateOpen,
			CreatedAt: now, DeadlineAt: now + team.SelfRelayDeadlineS*1000, LeaseUntil: now + team.SelfRelayDeadlineS*1000} // nobody renews it
		if _, err := insertRowIn(tx, row, requestHash(team.KindMemberRelay, lead.SessionID, team.SelfRelayDeadlineS, payload), ""); err != nil {
			return false, err
		}
		if m.afterMemberRowInsert != nil {
			if err := m.afterMemberRowInsert(); err != nil { // test seam: a failure right after the row insert
				return false, err
			}
		}
		op.State, op.RequestID = team.RelayAwaitingApproval, row.ID
		*held = &row
		return true, nil
	}
}
