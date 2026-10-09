package teammod

import (
	"context"
	"errors"
	"fmt"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// Reconciling a relay op from the agent frames (plan v3 P6-4a; spec §9.3; closes #1735). A relay op past `claimed`
// (claimed | writing | written) depends on a process that can die, or that can /clear without the mod saying so
// (a daemon restart in between, a mod that crashed). The pane's verified frame tells the truth: the same process
// carrying another session id is a /clear that happened (write the lineage), no live session at all is a member that
// is gone (fail the op).

// opBinding is the process and pane an op is bound to: its own columns (P6-2a), else — an old self op — its approval
// row's origin.
func (m *Module) opBinding(op team.RelayOp) (pid int, pane, procStart string) {
	if op.PID != 0 {
		procStart = op.ProcStart
		if procStart == "" && op.RequestID != "" { // an op from between P6-2a and P6-2b-1: the approval row has it
			if row, ok, err := m.store.Get(op.RequestID); err == nil && ok && row.Origin.PID == op.PID {
				procStart = row.Origin.ProcStart
			}
		}
		return op.PID, op.PaneID, procStart
	}
	if op.RequestID == "" {
		return 0, "", ""
	}
	row, ok, err := m.store.Get(op.RequestID)
	if err != nil || !ok {
		return 0, "", ""
	}
	return row.Origin.PID, paneOf(row.Origin.Tmux), row.Origin.ProcStart
}

// pastClaimed says whether op is in one of the states reconcileFromFrames judges.
func pastClaimed(op team.RelayOp) bool {
	return op.State == team.RelayClaimed || op.State == team.RelayWriting || op.State == team.RelayWritten
}

// reconcileFromFrames compares op, past claimed, with the live verified frames of its pane and returns the op as it is
// after. Another session of the SAME process on the pane (pid and, when the op has one, start time) → cleared into it,
// with every follow-up of a report. No verified frame on the pane and the op's session no longer live →
// failed{member_gone}. The same session alive, a pane it cannot name, or an unreadable frame list leave the op as it
// is. Another process's session is never written.
func (m *Module) reconcileFromFrames(ctx context.Context, op team.RelayOp) (team.RelayOp, error) {
	if !pastClaimed(op) || m.frames == nil {
		return op, nil
	}
	pid, pane, start := m.opBinding(op)
	if pane == "" || pid == 0 {
		return op, nil // nothing to compare with: left to the stall timeout
	}
	frames, err := m.frames.LiveSessions(ctx, "cc")
	if err != nil {
		return op, fmt.Errorf("reconcile op %s: frames: %w", op.ID, err)
	}
	unverified := false
	for _, f := range frames {
		if f.PaneID == pane && !f.Verified {
			unverified = true // alive, identity unreadable: not proof that the member is gone
		}
		if f.PaneID != pane || !f.Verified || f.SessionID == "" {
			continue
		}
		if f.SessionID == op.SessionID {
			return op, nil // the same session is alive
		}
		o, ok, err := m.origins.ResolveOriginBySession(f.SessionID)
		if err != nil {
			return op, fmt.Errorf("reconcile op %s: registry: %w", op.ID, err)
		}
		if !ok || o.PID != pid || start == "" || o.ProcStart != start {
			continue // another process's session, or no start time to prove it is ours: never written
		}
		after, _, err := m.applyReconcile(op, RelayReport{State: team.RelayCleared, NewSessionID: f.SessionID, NewRef: ipeers.RefID(f.SessionID), At: m.now()})
		return after, err
	}
	if unverified || m.origins.LiveSession(op.SessionID) {
		return op, nil
	}
	if m.now() < m.bootAt+team.BootGraceS*1000 {
		// Right after a restart a session may not be listed yet (spec §9.2): "no frame, not live" proves nothing. The
		// failure is irreversible, so it waits for the grace; the first liveness tick after it runs this again.
		return op, nil
	}
	after, _, err := m.applyReconcile(op, RelayReport{State: team.RelayFailed, Reason: team.RelayReasonMemberGone, At: m.now()})
	return after, err
}

// applyReconcile is a report the daemon makes itself: the same store path and the same follow-ups as the mod's report
// (afterReport, the handover notice, the lead's outcome notice), on Applied only.
func (m *Module) applyReconcile(op team.RelayOp, rep RelayReport) (team.RelayOp, bool, error) {
	after, res, err := m.store.ReportRelay(op.ID, rep)
	if errors.Is(err, ErrBadRelayReport) {
		m.logf("[team] reconcile op %s: %v", op.ID, err)
		return op, false, nil
	}
	if err != nil {
		return op, false, err
	}
	if res != ReportApplied {
		return after, false, nil
	}
	m.logf("[team] relay op %s → %s%s (a report the daemon made itself)", op.ID, after.State, reasonSuffix(after))
	m.afterReport(after)
	m.handoverNoticeAsync(after)
	m.outcomeNoticeAsync(after)
	return after, true, nil
}

// reconcileOpsFromFrames is the boot's part: every op past claimed.
func (m *Module) reconcileOpsFromFrames(ops []team.RelayOp) {
	for _, op := range ops {
		if !pastClaimed(op) {
			continue
		}
		if _, err := m.reconcileFromFrames(m.stopCtx, op); err != nil {
			m.logf("[team] boot: %v", err)
		}
	}
}

// The lead's notices about a member's relay (spec §8.2 step 8: "the lead is told about every failure"), fixed texts.
const (
	RelayDoneNoticeFmt      = "[pdx team] member 接力完成：%s → %s"
	RelayFailedNoticeFmt    = "[pdx team] member 接力失敗：%s（%s）"
	RelayCancelledNoticeFmt = "[pdx team] member 接力已取消：%s（%s）"
)

// outcomeNoticeAsync tells the team's lead that a MEMBER op ended (done, failed, cancelled), from its own goroutine.
// Callers pass an op they just applied (ReportApplied) — a re-send (Noop) never calls it, so there is one notice per
// transition. Best effort: a failure is logged.
func (m *Module) outcomeNoticeAsync(op team.RelayOp) {
	if m.sender == nil || op.Kind != team.RelayKindMember || op.TeamID == "" {
		return
	}
	switch op.State {
	case team.RelayDone, team.RelayFailed, team.RelayCancelled:
	default:
		return
	}
	m.goTracked(func() { m.outcomeNotice(op) })
}

func (m *Module) outcomeNotice(op team.RelayOp) {
	t, ok, err := m.store.TeamByID(op.TeamID)
	if err != nil || !ok || t.EndedAt != 0 {
		return // no live team to tell
	}
	var text string
	switch op.State {
	case team.RelayDone:
		text = fmt.Sprintf(RelayDoneNoticeFmt, op.Ref, op.NewRef)
	case team.RelayFailed:
		text = fmt.Sprintf(RelayFailedNoticeFmt, op.Ref, op.Reason)
	default:
		text = fmt.Sprintf(RelayCancelledNoticeFmt, op.Ref, op.Reason)
	}
	// Sent to the lead's current address, from the member's own inbox when it has one (its new session once cleared),
	// else from the lead's own.
	from := op.SessionID
	if op.NewSessionID != "" {
		from = op.NewSessionID
	}
	inbox, ok, err := m.origins.InboxOf(from)
	if err != nil || !ok {
		inbox, ok, err = m.origins.InboxOf(t.LeadSessionID)
	}
	if err != nil || !ok {
		m.logf("[team] outcome notice (op %s): no live inbox to send from (%v)", op.ID, err)
		return
	}
	alias, _ := m.selfHost()
	to := alias + "/" + ipeers.RefID(t.LeadSessionID)
	if o, live, err := m.origins.ResolveOriginBySession(t.LeadSessionID); err == nil && live && o.Address != "" {
		to = o.Address
	}
	ctx, cancel := context.WithTimeout(m.stopCtx, noticeSendTimeout)
	defer cancel()
	if _, err := m.sender.Send(ctx, ipeers.SendRequest{To: to, Text: text, OriginInbox: inbox}); err != nil {
		m.logf("[team] outcome notice (op %s) to %s: %v", op.ID, to, err)
	}
}

// reconcileAfterBootGrace runs the boot's frame reconciliation once more, at the first liveness tick after BootGraceS:
// the boot's own run could not fail an op for a session the registry had not listed yet.
func (m *Module) reconcileAfterBootGrace() {
	if m.bootReconciled || m.now() < m.bootAt+team.BootGraceS*1000 {
		return
	}
	list := m.store.ListActiveRelayOps
	if m.listActiveOps != nil {
		list = m.listActiveOps
	}
	ops, err := list()
	if err != nil {
		m.logf("[team] after the boot grace: list relay ops (tried again at the next liveness tick): %v", err)
		return // bootReconciled stays false: the deferred verdicts must not be lost
	}
	m.reconcileOpsFromFrames(ops)
	m.bootReconciled = true
}
