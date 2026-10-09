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
	case op.State == team.RelayCleared:
		if now < op.UpdatedAt+stallMs {
			return
		}
		// The mod reports done when the SEED turn ends, and the seed sends the new session on with the original work, so
		// its first turn often runs past 15 minutes. If the new session is alive the relay itself succeeded: report done
		// (amends coordinator decision 4, 1f 2026-10-09). Only a new session that is not there fails the op.
		if m.newSessionLive(op) {
			m.settleOnTimeout(op, RelayReport{State: team.RelayDone, At: now})
			return
		}
		reason := team.RelayReasonHandoffIncomplete
		if op.Kind == team.RelayKindMember {
			reason = team.RelayReasonMemberUnresponsive
		}
		m.failOnTimeout(op, reason)
	case pastClaimed(op):
		if now < op.UpdatedAt+stallMs {
			return
		}
		cur, err := m.reconcileFromFrames(context.Background(), op) // the frame may say it cleared or died
		if err != nil {
			m.logf("[team] stall of op %s: %v", op.ID, err)
			return // unknown is not a verdict
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

// failOnTimeout fails op with reason through the same path as a report (CAS on the state, the updated_at and the
// seen_at of the snapshot; the follow-ups and the lead's notice on Applied only).
func (m *Module) failOnTimeout(op team.RelayOp, reason string) {
	m.settleOnTimeout(op, RelayReport{State: team.RelayFailed, Reason: reason, At: m.now()})
}

// newSessionLive says whether a cleared op's new session is there: a verified frame on the op's pane, or the registry.
func (m *Module) newSessionLive(op team.RelayOp) bool {
	if op.NewSessionID == "" {
		return false
	}
	if _, pane, _ := m.opBinding(op); pane != "" && m.frames != nil {
		if frames, err := m.frames.LiveSessions(context.Background(), "cc"); err == nil {
			for _, f := range frames {
				if f.PaneID == pane && f.Verified && f.SessionID == op.NewSessionID {
					return true
				}
			}
		}
	}
	return m.origins.LiveSession(op.NewSessionID)
}

// settleOnTimeout applies rep (failed, or done for a stalled cleared) to op, conditional on the snapshot the judgement
// used: a seen, a claim or a report that landed since wins.
func (m *Module) settleOnTimeout(op team.RelayOp, rep RelayReport) {
	reason := rep.Reason
	rep.Expect = &RelayExpect{State: op.State, UpdatedAt: op.UpdatedAt, SeenAt: op.SeenAt}
	_, applied, err := m.applyReconcile(op, rep)
	switch {
	case err != nil:
		m.logf("[team] relay op %s timeout: %v", op.ID, err)
	case applied:
		m.logf("[team] relay op %s timed out → %s (%s)", op.ID, rep.State, reason)
	default:
		m.logf("[team] relay op %s: a timeout judged from a stale read was skipped (progress landed first)", op.ID)
	}
}
