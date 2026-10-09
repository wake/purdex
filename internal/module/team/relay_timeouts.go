package teammod

import (
	"context"

	"github.com/wake/purdex/internal/team"
)

// Relay timeouts (plan v3 P6-4, branch A; the clock rule is §7 "P6-4b"). All of them end an op with a failure, which
// is irreversible, so none is judged inside the boot grace (BootGraceS), as the frame reconciliation is not.
//
//   - A member op still `requested` and never `seen` fails member_unresponsive RelayClaimTimeoutS (60 s) after it ENTERED
//     requested — its updated_at, which nothing else touches while it is requested and unseen. Not created_at: an op
//     approved after nine minutes (RQ-2) must not fail the moment it enters requested.
//   - After `seen` the mod is busy with a running turn and will claim when it ends: member_unresponsive
//     RelayStallTimeoutS (15 min) after seen_at.
//   - A stall: an op past claimed with no state change for RelayStallTimeoutS is looked at in the pane's frame first
//     (a /clear nobody reported, a member that is gone); if it is still not terminal it fails: member_unresponsive for a
//     member op, handoff_incomplete for a self op (coordinator decision 4).

// sweepRelayTimeouts is the sweeper's liveness-tick step.
func (m *Module) sweepRelayTimeouts() {
	now := m.now()
	if now < m.bootAt+team.BootGraceS*1000 {
		return
	}
	ops, err := m.store.ListActiveRelayOps()
	if err != nil {
		m.logf("[team] relay timeouts: list: %v", err)
		return
	}
	for _, op := range ops {
		if m.stopping() {
			return
		}
		m.judgeRelayTimeout(op, now)
	}
}

func (m *Module) judgeRelayTimeout(op team.RelayOp, now int64) {
	claimMs, stallMs := int64(team.RelayClaimTimeoutS)*1000, int64(team.RelayStallTimeoutS)*1000
	switch {
	case op.Kind == team.RelayKindMember && op.State == team.RelayRequested:
		due := op.SeenAt == 0 && now >= op.UpdatedAt+claimMs || op.SeenAt != 0 && now >= op.SeenAt+stallMs
		if due {
			m.failOnTimeout(op, team.RelayReasonMemberUnresponsive)
		}
	case pastClaimed(op) || op.State == team.RelayCleared:
		if now < op.UpdatedAt+stallMs {
			return
		}
		cur := op
		if pastClaimed(op) { // the frame may say it cleared or died; a cleared op has no pane to ask about
			var err error
			if cur, err = m.reconcileFromFrames(context.Background(), op); err != nil {
				m.logf("[team] stall of op %s: %v", op.ID, err)
				return // unknown is not a verdict
			}
		}
		if cur.State.Terminal() || cur.State != op.State {
			return // reconciled: the frame decided
		}
		reason := team.RelayReasonHandoffIncomplete
		if op.Kind == team.RelayKindMember {
			reason = team.RelayReasonMemberUnresponsive
		}
		m.failOnTimeout(op, reason)
	}
}

// failOnTimeout fails op with reason through the same path as a report (CAS on the state; the follow-ups and the lead's
// notice on Applied only).
func (m *Module) failOnTimeout(op team.RelayOp, reason string) {
	if _, err := m.applyReconcile(op, RelayReport{State: team.RelayFailed, Reason: reason, At: m.now()}); err != nil {
		m.logf("[team] relay op %s timeout: %v", op.ID, err)
		return
	}
	m.logf("[team] relay op %s timed out (%s)", op.ID, reason)
}
