package teammod

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/team"
)

// claimedMemberOp: a member op (pid 42, pane %2) driven to claimed through the store.
func (f *fixture) claimedMemberOp() team.RelayOp {
	f.t.Helper()
	f.memberTeam("2")
	_, op, ae := f.createRelay(rid(400), "/tmp/10.sock", "_mem001")
	if op.ID == "" {
		f.t.Fatalf("create: %+v", ae)
	}
	if _, res, err := f.m.store.ReportRelay(op.ID, RelayReport{State: team.RelayClaimed, At: 5}); err != nil || res != ReportApplied {
		f.t.Fatalf("claim: %v %v", res, err)
	}
	return f.op(op.ID)
}

func (f *fixture) setFrames(fs ...agent.TerminalSession) { f.m.frames = &fakeFrames{list: fs} }

func frameOf(sid, pane string, verified bool) agent.TerminalSession {
	return agent.TerminalSession{FrameID: "fr-" + sid, PaneID: pane, AgentType: "cc", SessionID: sid, Verified: verified}
}

// #1735: the pane's verified frame carries another session of the SAME process: a /clear happened that nobody
// reported. The lineage is written and the op is cleared. Mutation gate: reconcile only awaiting_approval → red.
func TestReconcile_PastClaimedWithANewFrameSessionWritesTheLineage(t *testing.T) {
	f := newFixture(t)
	op := f.claimedMemberOp()
	f.origins.cleared = map[string]int{"sid-new": 42}
	f.setFrames(frameOf("sid-new", "%2", true))
	got, err := f.m.reconcileFromFrames(context.Background(), op)
	if err != nil || got.State != team.RelayCleared || got.NewSessionID != "sid-new" || got.NewRef == "" {
		t.Fatalf("after: %+v err=%v", got, err)
	}
	refs, _ := f.m.store.PreviousRefs()
	if len(refs) == 0 {
		t.Fatal("no lineage row")
	}
}

// Mutation gate: skip the PID check → another process's session is written (red).
func TestReconcile_NewFrameUnderAnotherPIDIsNotCleared(t *testing.T) {
	f := newFixture(t)
	op := f.claimedMemberOp()
	f.origins.cleared = map[string]int{"sid-other": 99}
	f.setFrames(frameOf("sid-other", "%2", true))
	got, err := f.m.reconcileFromFrames(context.Background(), op)
	if err != nil || got.State != team.RelayClaimed {
		t.Fatalf("another process's session moved the op: %+v err=%v", got, err)
	}
	if refs, _ := f.m.store.PreviousRefs(); len(refs) != 0 {
		t.Fatalf("lineage written for another process: %v", refs)
	}
}

// An unverified frame is not a witness either way.
func TestReconcile_AnUnverifiedFrameIsIgnored(t *testing.T) {
	f := newFixture(t)
	op := f.claimedMemberOp()
	f.origins.cleared = map[string]int{"sid-new": 42}
	f.setFrames(frameOf("sid-new", "%2", false))
	if got, _ := f.m.reconcileFromFrames(context.Background(), op); got.State != team.RelayClaimed {
		t.Fatalf("an unverified frame cleared the op: %+v", got)
	}
}

func TestReconcile_NoFrameAndSessionGoneFailsMemberGone(t *testing.T) {
	f := newFixture(t)
	op := f.claimedMemberOp()
	f.setFrames()
	f.origins.markDead("sid-m1")
	got, err := f.m.reconcileFromFrames(context.Background(), op)
	if err != nil || got.State != team.RelayFailed || got.Reason != team.RelayReasonMemberGone {
		t.Fatalf("after: %+v err=%v", got, err)
	}
}

func TestReconcile_SameSessionAliveIsLeftAlone(t *testing.T) {
	f := newFixture(t)
	op := f.claimedMemberOp()
	f.setFrames(frameOf("sid-m1", "%2", true))
	if got, _ := f.m.reconcileFromFrames(context.Background(), op); got.State != team.RelayClaimed {
		t.Fatalf("a live session was judged: %+v", got)
	}
	// no frame at all but the registry still lists the session: alive
	f.setFrames()
	if got, _ := f.m.reconcileFromFrames(context.Background(), op); got.State != team.RelayClaimed {
		t.Fatalf("a listed session with no frame was failed: %+v", got)
	}
	// requested and awaiting ops are not this function's
	op.State = team.RelayRequested
	if got, _ := f.m.reconcileFromFrames(context.Background(), op); got.State != team.RelayRequested {
		t.Fatal("a requested op was touched")
	}
}

// The boot runs it for every op past claimed (#1735 end to end at Start's entry point).
func TestReconcile_BootReconcilesAnOpStuckPastClaimed(t *testing.T) {
	f := newFixture(t)
	op := f.claimedMemberOp()
	f.origins.cleared = map[string]int{"sid-new": 42}
	f.setFrames(frameOf("sid-new", "%2", true))
	f.m.reconcileRelays()
	if got := f.op(op.ID); got.State != team.RelayCleared || got.NewSessionID != "sid-new" {
		t.Fatalf("after boot: %+v", got)
	}
}

func (f *fixture) leadNotices() []string {
	var out []string
	for _, c := range f.sender.calls() {
		if strings.HasPrefix(c.Text, "[pdx team] member 接力") {
			out = append(out, c.Text)
		}
	}
	return out
}

// One notice per transition, member ops only, on Applied only. Mutation gate: notify on Noop → the repeat adds one (red).
func TestNotice_DoneAndFailureOncePerTransition(t *testing.T) {
	f := newFixture(t)
	f.origins.cleared = map[string]int{"sid-new": 42}
	op := f.claimedMemberOp()
	for _, s := range []team.RelayReportRequest{{State: team.RelayWriting}, {State: team.RelayWritten}, {State: team.RelayCleared, NewSessionID: "sid-new"}} {
		if code, _, ae := f.report(op.ID, s); code != 200 {
			t.Fatalf("%s: %d %+v", s.State, code, ae)
		}
	}
	if code, _, _ := f.report(op.ID, team.RelayReportRequest{State: team.RelayDone}); code != 200 {
		t.Fatal("done")
	}
	waitFor(t, func() bool { return len(f.leadNotices()) == 1 })
	f.report(op.ID, team.RelayReportRequest{State: team.RelayDone}) // the idempotent re-send
	time.Sleep(150 * time.Millisecond)
	n := f.leadNotices()
	if len(n) != 1 || !strings.Contains(n[0], "接力完成") || !strings.Contains(n[0], op.Ref) {
		t.Fatalf("done notices = %v", n)
	}
	// a failure of a second op
	f2 := newFixture(t)
	op2 := f2.claimedMemberOp()
	f2.report(op2.ID, team.RelayReportRequest{State: team.RelayFailed, Error: team.RelayReasonHandoffIncomplete})
	f2.report(op2.ID, team.RelayReportRequest{State: team.RelayFailed, Error: team.RelayReasonHandoffIncomplete})
	waitFor(t, func() bool { return len(f2.leadNotices()) >= 1 })
	time.Sleep(150 * time.Millisecond)
	if n := f2.leadNotices(); len(n) != 1 || !strings.Contains(n[0], "接力失敗") || !strings.Contains(n[0], team.RelayReasonHandoffIncomplete) {
		t.Fatalf("failure notices = %v", n)
	}
}

// A self op's end is nobody's to tell the lead.
func TestNotice_SelfOpsAreNotAnnouncedToALead(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1")
	f.decide(out.RequestID, "approve")
	f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayFailed, Error: team.RelayReasonHandoffIncomplete})
	time.Sleep(150 * time.Millisecond)
	if n := f.leadNotices(); len(n) != 0 {
		t.Fatalf("a self op was announced: %v", n)
	}
}
