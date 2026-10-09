package teammod

import (
	"errors"

	"github.com/wake/purdex/internal/team"
)

// Module side of the member_relay approval (RQ-2a; spec 2026-10-09-rq2-member-relay-approval-spec.md §4.3, §4.2, §7).

// approveMemberRelay is approve() for a member_relay row: CloseMemberRelayApproved moves the row and its op in one
// transaction; the winner is announced as every close is (announceClosed → afterApproved). A re-check that refused
// closed the row cancelled instead: that close is announced too, and returned as *adoptRefusedError (the shape the
// click and the auto approve already handle for adopt: "closed cancelled by its own re-check, not retried").
func (m *Module) approveMemberRelay(a team.Approval, c Close) (after team.Approval, won bool, err error) {
	after, won, refused, err := m.store.CloseMemberRelayApproved(a.ID, c)
	if err != nil {
		return team.Approval{}, false, err
	}
	if won && c.SpentOut != nil && *c.SpentOut {
		m.markSpent(a.ID) // before the announce: afterApproved publishes the new numbers
	}
	if won {
		m.announceClosed(after, nil)
	}
	if refused != "" && won {
		return after, true, &adoptRefusedError{Code: refused}
	}
	return after, won, nil
}

// afterMemberRelayApproved is what follows an approved member_relay row, from the one winner point: the pool's new
// numbers if this approve spent, the control message to the member, the roster. The op is already `requested`
// (CloseMemberRelayApproved); afterClose does nothing for this kind, and never reports claimed.
func (m *Module) afterMemberRelayApproved(a team.Approval) {
	op, ok, err := m.store.RelayOpByRequest(a.ID)
	if err != nil || !ok {
		m.logf("[team] member relay approval %s: its op is missing (ok=%v err=%v)", a.ID, ok, err)
		return
	}
	if m.takeSpent(a.ID) {
		if t, found, err := m.store.TeamByID(op.TeamID); err == nil && found {
			m.announceSpend(t.LeadSessionID) // the team's CURRENT lead's chain
		}
	}
	m.sendMemberControlAsync(op)
	m.rosterChanged()
}

// memberRelayTeamLive is the sweeper's liveness test for a member_relay row: its op's team has not ended. A row with no
// op (impossible by the invariant) counts as not live, so the sweeper clears it.
func (m *Module) memberRelayTeamLive(a team.Approval) (bool, error) {
	op, ok, err := m.store.RelayOpByRequest(a.ID)
	if err != nil || !ok {
		return false, err
	}
	t, found, err := m.store.TeamByID(op.TeamID)
	if err != nil || !found {
		return false, err
	}
	return t.EndedAt == 0, nil
}

// reconcileMemberRelays is the boot's part of RQ-2 §7, under createMu: (1) an op and its row that disagree are repaired
// toward the row; (2) an open row whose team or member is gone is cancelled (the §4.3 re-check); (3) a `requested`
// member op is sent its control message again when the team's current lead is live (idempotent — the claim is a
// compare-and-set), else it is left to the claim timeout (P6-4b).
func (m *Module) reconcileMemberRelays(ops []team.RelayOp) {
	now := m.now()
	rows, err := m.store.ListOpen()
	if err != nil {
		m.logf("[team] boot: list open approvals: %v", err)
		return
	}
	for _, row := range rows {
		if row.Kind != team.KindMemberRelay {
			continue
		}
		op, ok, err := m.store.RelayOpByRequest(row.ID)
		if err != nil {
			m.logf("[team] boot: member relay row %s: %v", row.ID, err)
			continue
		}
		if !ok || op.State.Terminal() || op.State != team.RelayAwaitingApproval { // an open row with no awaiting op: invariant broken
			m.logf("[team] boot: member relay row %s is open but its op is %q; closing the row", row.ID, op.State)
			if _, _, err := m.closeAs(row.ID, Close{State: team.StateAbandoned, DecidedAt: now}); err != nil {
				m.logf("[team] boot: close member relay row %s: %v", row.ID, err)
			}
			continue
		}
		if after, won, reason, err := m.store.CancelMemberRelayIfGone(row.ID, now); err != nil {
			m.logf("[team] boot: re-check member relay row %s: %v", row.ID, err)
		} else if won {
			m.announceClosed(after, nil)
			m.logf("[team] boot: member relay row %s cancelled (%s)", row.ID, reason)
		}
	}
	for _, op := range ops {
		if op.Kind != team.RelayKindMember {
			continue
		}
		switch op.State {
		case team.RelayAwaitingApproval: // its row closed while the op did not follow: the op takes the row's verdict
			row, ok, err := m.store.Get(op.RequestID)
			if err != nil || !ok || row.State == team.StateOpen {
				continue
			}
			rep := opReportForClosedRow(row, now)
			if row.State == team.StateApproved {
				rep.State = team.RelayRequested
			}
			if _, _, err := m.store.ReportRelay(op.ID, rep); err != nil && !errors.Is(err, ErrNoSuchRelayOp) {
				m.logf("[team] boot: member relay op %s: %v", op.ID, err)
			}
		case team.RelayRequested:
			m.sendMemberControlAsync(op)
		}
	}
}
