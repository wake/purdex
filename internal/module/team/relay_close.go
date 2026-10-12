package teammod

import (
	"encoding/json"

	"github.com/wake/purdex/internal/team"
)

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
	sr, err := m.store.SessionRole(a.Origin.SessionID)
	if err != nil {
		return rep, err
	}
	if sr.isMember() {
		// A person's manual relay of a member, local or remote, goes on (MR-1, MR-2): the ROW's payload says so, as at the approve.
		var sp team.SelfRelayPayload
		manual := json.Unmarshal(a.Payload, &sp) == nil && sp.Manual
		// A member whose kill started after the card opened is cancelled too: the cleared moves active rows only.
		killing, err := m.store.IsKillingMember(a.Origin.SessionID)
		if err != nil {
			return rep, err
		}
		if !manual || killing {
			rep.State, rep.Reason = team.RelayCancelled, team.ErrMemberRelayIsLeads
		}
	}
	return rep, nil
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
