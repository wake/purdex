package teammod

// `pdx kill <ref> --handoff` (RT-3c, relay/team rules v2 T38): the member writes its handoff first, and is closed once the file is
// written. The lead's request opens a member op of mode handoff (requested) and the member's mod is told by the control message as for
// a relay; the mod writes the file and reports `written`, which ends the op (cancelled / half) AND claims the member for the kill in the
// same transaction (reportRelayIn). The signal follows the commit (finishHandoffKill). A crash between the two leaves a killing row the
// sweeper gives back to active - it never signals on the lead's behalf - and the lead is told to kill again.

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/wake/purdex/internal/team"
)

// killWithHandoff answers POST /api/team/kill with handoff:true: 201 and the op. A remote member, or one whose mod is older than
// protocol 8, is relay_unsupported; the member must be an active one of the caller's team.
func (m *Module) killWithHandoff(w http.ResponseWriter, req team.KillRequest, t team.Team, mr memberRow) {
	if m.isRemoteRow(mr) {
		m.writeErr(w, http.StatusConflict, team.ErrRelayUnsupported, "member "+mr.Ref+" runs on another host: a handoff kill is local only", nil)
		return
	}
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	if mr.State != team.MemberActive {
		m.writeErr(w, http.StatusConflict, team.ErrNotYourMember, req.Target+" is no active member of team "+t.ID, nil)
		return
	}
	if prev, open, err := m.store.OpenRelayOpByNewSession(mr.SessionID); err != nil {
		m.logf("[team] kill --handoff %s: %v", mr.Ref, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	} else if open {
		m.writeJSON(w, http.StatusConflict, team.APIError{Error: team.ErrRelayOpen, Detail: "this member's previous relay (to this session) is still settling", Op: &prev})
		return
	}
	m.createMemberRelayFor(w, team.RelayCreateRequest{ID: uuid.NewString(), OriginInbox: req.OriginInbox, Target: req.Target}, t, mr, true)
}

// finishHandoffKill is the part after the commit of a handoff op's `written`: the member row is already killing (claimed in the
// transaction); the signal goes as `pdx kill`'s does and the lead hears once. A row that is not killing under this op's claim
// (the member left, another kill took it) is nobody's to finish.
func (m *Module) finishHandoffKill(op team.RelayOp) {
	if _, running := m.handoffKills.LoadOrStore(op.ID, struct{}{}); running {
		return
	}
	defer m.handoffKills.Delete(op.ID)
	t, ok, err := m.store.TeamByID(op.TeamID)
	if err != nil || !ok {
		return
	}
	if _, mine := m.store.handoffClaims.LoadAndDelete(op.ID); mine {
		rows, err := m.store.LocalKillingMembers()
		if err != nil {
			m.logf("[team] handoff kill %s: %v", op.ID, err)
			return
		}
		for _, mr := range rows {
			if mr.SessionID != op.SessionID || mr.TeamID != op.TeamID {
				continue
			}
			address, title := m.memberNoticeName(mr)
			m.rosterChanged() // the member shows as killing from the claim on
			if why := m.signalClaimedMember(mr); why != "" {
				m.logf("[team] handoff kill %s: member %s not closed: %s", op.ID, mr.Ref, why)
				if ok, err := m.store.GiveBackMemberKilling(mr.SpawnOp, mr.SessionID, m.now()); err != nil || !ok {
					m.logf("[team] handoff kill %s: could not give the claim back (%v, %v); the sweeper decides", op.ID, ok, err)
				}
				m.rosterChanged()
				m.noticeToLead(mr, t, fmt.Sprintf(team.HandoffNotClosedFmt, address, title, op.HandoffPath), "handoff kill not closed")
				return
			}
			m.noticeToLead(mr, t, fmt.Sprintf(team.HandoffClosedFmt, address, title, op.HandoffPath), "handoff kill")
			return
		}
		return
	}
	// No claim of this op's (the kill claim missed, or this is a repeated report): the file is written but nothing closes the member. A
	// member another kill is closing (killing), that is closed, gone or released is that path's; one still ACTIVE - of any host - is told
	// to the lead, who asks again (RT-3c codex).
	rows, err := m.store.MembersOf(op.TeamID)
	if err != nil {
		return
	}
	for _, mr := range rows {
		if mr.SessionID == op.SessionID && mr.State == team.MemberActive {
			address, title := m.memberNoticeName(mr)
			m.noticeToLead(mr, t, fmt.Sprintf(team.HandoffNotClosedFmt, address, title, op.HandoffPath), "handoff kill not closed")
			return
		}
	}
}

// signalClaimedMember ends a member that is already claimed killing and marks the row, as killAndMark does after its claim; it returns
// why it could not ("" = closed).
func (m *Module) signalClaimedMember(mr memberRow) string {
	mark, want := m.store.MarkMemberKilled, team.MemberKilled
	if mr.Origin == team.MemberOriginAdopted {
		ended, _, _, why := m.killAdopted(mr)
		if why != "" {
			return why
		}
		if ended { // it had already ended: nothing was killed by this call
			mark, want = m.store.MarkMemberGone, team.MemberGone
		}
	} else {
		if m.tmux == nil {
			return "daemon has no tmux"
		}
		if _, _, why := m.killMember(mr); why != "" {
			return why
		}
	}
	ok, err := mark(mr.SpawnOp, mr.SessionID, m.now())
	if err != nil {
		return err.Error()
	}
	if ok {
		m.logf("[team] member %s (%s) %s after its handoff", mr.Ref, mr.SessionID, want)
		m.rosterChanged()
	}
	return ""
}

// handoffOfKilling is the handoff op whose `written` claimed this killing row (the row's updated_at is the op's), if any: a kill of the
// ordinary kind has no such op.
func (m *Module) handoffOfKilling(mr memberRow) (team.RelayOp, bool) {
	h, found, err := m.store.FreshHalfOf(mr.SessionID)
	if err != nil || !found {
		return team.RelayOp{}, false
	}
	op, ok, err := m.store.GetRelayOp(h.OpID)
	if err != nil || !ok || op.Mode != team.RelayModeHandoff || op.State != team.RelayCancelled || op.SessionID != mr.SessionID || op.UpdatedAt != mr.UpdatedAt {
		return team.RelayOp{}, false
	}
	return op, true
}

// tellHandoffNotClosed tells the lead that the handoff file is written but its member was not closed (the kill never completed and the
// member is active again): it asks again with `pdx kill`, and a `--handoff` one reuses the file. Goes by noticeToLead, so a member the
// lead cannot be spoken as is told through the self-notice sender.
func (m *Module) tellHandoffNotClosed(mr memberRow, op team.RelayOp) {
	t, ok, err := m.store.TeamByID(op.TeamID)
	if err != nil || !ok || t.EndedAt != 0 {
		return
	}
	address, title := m.memberNoticeName(mr)
	m.goTracked(func() {
		m.noticeToLead(mr, t, fmt.Sprintf(team.HandoffNotClosedFmt, address, title, op.HandoffPath), "handoff kill not closed")
	})
}
