package teammod

import (
	"context"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// Relay timeouts (plan v3 P6-4, branch A; the clock rule is §7 "P6-4b"). All of them end an op with a failure, which
// is irreversible, so none is judged inside the boot grace (BootGraceS), as the frame reconciliation is not.
//
//   - A member op still `requested` and never `seen` fails member_unresponsive RelayClaimTimeoutS (60 s) after it ENTERED
//     requested — its updated_at, which nothing else touches while it is requested and unseen. Not created_at: an op
//     approved after nine minutes (RQ-2) must not fail the moment it enters requested.
//   - After `seen` the mod is busy with a running turn and will claim when it ends. #2439: while the agent module says the
//     member is in a turn (running, or waiting on a prompt) the op waits up to RelayBusyCapS (60 min) after seen_at; at
//     RelayStallTimeoutS (15 min) the lead is told once; at the cap it fails member_busy_timeout (member_blocked when the
//     agent is waiting on a prompt). A member that is idle (its turn ended) and still unclaimed RelayIdleGraceS (2 min)
//     after it was first seen idle fails member_unresponsive: the mod did not claim. With no agent status at all, the old
//     rule stands: member_unresponsive RelayStallTimeoutS after seen_at.
//   - Never seen: member_unseen (member_blocked when the agent says it is waiting on a prompt), so the lead can tell a
//     mod that never answered from one that was busy.
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
	m.pruneBusyMarks(ops)
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
		m.judgeMemberRequested(op, now, claimMs, stallMs)
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

// judgeMemberRequested is the rule for a member op in requested (see the list at the top of the file).
func (m *Module) judgeMemberRequested(op team.RelayOp, now, claimMs, stallMs int64) {
	mr, status, known := m.memberAgentStatus(op)
	if op.SeenAt == 0 {
		if now >= op.UpdatedAt+claimMs {
			reason := team.RelayReasonMemberUnseen
			if known && status == agentWaiting {
				reason = team.RelayReasonMemberBlocked
			}
			m.failOnTimeout(op, reason)
		}
		return
	}
	switch {
	case !known:
		if now >= op.SeenAt+stallMs {
			m.failOnTimeout(op, team.RelayReasonMemberUnresponsive)
		}
	case status == agentRunning || status == agentWaiting:
		m.forgetIdle(op.ID)
		if now >= op.SeenAt+int64(team.RelayBusyCapS)*1000 {
			reason := team.RelayReasonMemberBusyTimeout
			if status == agentWaiting {
				reason = team.RelayReasonMemberBlocked
			}
			m.failOnTimeout(op, reason)
		} else if now >= op.SeenAt+stallMs {
			m.busyNoticeOnce(op, mr)
		}
	default: // idle (or error, clear): the turn ended and the mod has not claimed
		if since := m.idleSinceOf(op.ID, now); now >= since+int64(team.RelayIdleGraceS)*1000 {
			m.failOnTimeout(op, team.RelayReasonMemberUnresponsive)
		}
	}
}

// memberAgentStatus reads the member's agent status as noticeUsage does; known is false when there is no reading or the
// session is no active member of a live team (then the old rule applies).
func (m *Module) memberAgentStatus(op team.RelayOp) (mr memberRow, status string, known bool) {
	if m.status == nil {
		return memberRow{}, "", false
	}
	mr, _, ok, err := m.store.ActiveMemberInLiveTeam(op.SessionID)
	if err != nil || !ok {
		return memberRow{}, "", false
	}
	status, known = m.status.AgentStatus(mr.TmuxSession)
	return mr, status, known
}

// idleSinceOf is when this daemon first saw the op's member idle (now on the first look). In memory: after a restart the
// count starts again, which only gives the mod another two minutes.
func (m *Module) idleSinceOf(opID string, now int64) int64 {
	m.busyMu.Lock()
	defer m.busyMu.Unlock()
	if m.idleSince == nil {
		m.idleSince = map[string]int64{}
	}
	if t, ok := m.idleSince[opID]; ok {
		return t
	}
	m.idleSince[opID] = now
	return now
}

func (m *Module) forgetIdle(opID string) {
	m.busyMu.Lock()
	delete(m.idleSince, opID)
	m.busyMu.Unlock()
}

// busyNoticeOnce tells the lead, once per op, that the member's turn is long and the relay waits for its end. A notice that
// did not go is tried again at the next sweep. Once per daemon run: the mark is in memory, so a restart inside the wait can
// repeat it once.
func (m *Module) busyNoticeOnce(op team.RelayOp, mr memberRow) {
	if m.sender == nil {
		return
	}
	m.busyMu.Lock()
	if m.busyNoticed == nil {
		m.busyNoticed = map[string]struct{}{}
	}
	if _, done := m.busyNoticed[op.ID]; done {
		m.busyMu.Unlock()
		return
	}
	m.busyNoticed[op.ID] = struct{}{}
	m.busyMu.Unlock()
	undo := func() {
		m.busyMu.Lock()
		delete(m.busyNoticed, op.ID)
		m.busyMu.Unlock()
	}
	if !m.goTracked(func() {
		t, ok, err := m.store.TeamByID(op.TeamID)
		if err != nil || !ok || t.EndedAt != 0 {
			return // no live team to tell
		}
		address, _ := m.memberNoticeName(mr)
		if !m.noticeToLead(mr, t, fmt.Sprintf(team.RelayBusyNoticeFmt, address, strings.TrimPrefix(mr.Ref, "_")), "busy notice") {
			undo()
		}
	}) {
		undo()
	}
}

// pruneBusyMarks drops the marks of ops that are no longer active.
func (m *Module) pruneBusyMarks(active []team.RelayOp) {
	live := make(map[string]struct{}, len(active))
	for _, op := range active {
		live[op.ID] = struct{}{}
	}
	m.busyMu.Lock()
	defer m.busyMu.Unlock()
	for id := range m.busyNoticed {
		if _, ok := live[id]; !ok {
			delete(m.busyNoticed, id)
		}
	}
	for id := range m.idleSince {
		if _, ok := live[id]; !ok {
			delete(m.idleSince, id)
		}
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
