package teammod

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

const minute = int64(60_000)

// requestedMemberOp: a member op in requested whose updated_at is the entry into requested.
func (f *fixture) requestedMemberOp() team.RelayOp {
	f.t.Helper()
	f.memberTeam("2")
	_, op, ae := f.createRelay(rid(500), "/tmp/10.sock", "_mem001")
	if op.ID == "" {
		f.t.Fatalf("create: %+v", ae)
	}
	return f.op(op.ID)
}

func (f *fixture) sweepTimeouts() { f.m.sweepRelayTimeouts() }

// Unseen: 60 s from the ENTRY into requested. Mutation gate: count from created_at → the nine-minute op fails at once (red).
func TestSweep_UnseenRequested60sFails(t *testing.T) {
	f := newFixture(t)
	op := f.requestedMemberOp()
	f.clock.Add(59_000)
	f.sweepTimeouts()
	if f.op(op.ID).State != team.RelayRequested {
		t.Fatal("failed before 60 s")
	}
	f.clock.Add(1_000)
	f.sweepTimeouts()
	if got := f.op(op.ID); got.State != team.RelayFailed || got.Reason != team.RelayReasonMemberUnseen {
		t.Fatalf("after 60 s: %+v", got)
	}
	waitFor(t, func() bool { return len(f.leadNotices()) == 1 })
}

// An op created, then approved nine minutes later (RQ-2): it must not fail the moment it enters requested.
func TestSweep_AnOpApprovedNineMinutesLateGetsItsOwnSixtySeconds(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("2")
	f.setPool("sid-1", 0)
	f.unatt.set(true)
	f.qrule.set(true, nil)
	_, op, _ := f.createRelay(rid(501), "/tmp/10.sock", "_mem001")
	if op.State != team.RelayAwaitingApproval {
		t.Fatalf("op = %+v", op)
	}
	f.clock.Add(9 * minute)
	f.sweepTimeouts()
	if f.op(op.ID).State != team.RelayAwaitingApproval {
		t.Fatal("an awaiting op is the approval row's, not the timeout's")
	}
	if code, body := f.decide(op.RequestID, "approve"); code != 200 {
		t.Fatalf("approve: %d %s", code, body)
	}
	f.sweepTimeouts()
	if got := f.op(op.ID); got.State != team.RelayRequested {
		t.Fatalf("failed at the entry into requested: %+v", got)
	}
	f.clock.Add(61_000)
	f.sweepTimeouts()
	if got := f.op(op.ID); got.State != team.RelayFailed || got.Reason != team.RelayReasonMemberUnseen {
		t.Fatalf("60 s after the entry: %+v", got)
	}
}

func TestSweep_SeenRequestedWaitsUpTo15MinFromSeen(t *testing.T) {
	f := newFixture(t)
	op := f.requestedMemberOp()
	f.clock.Add(30_000)
	if code, _, ae := f.seen(op.ID, "sid-m1"); code != 200 {
		t.Fatalf("seen: %d %+v", code, ae)
	}
	f.clock.Add(14 * minute) // far past the 60 s: seen turned that timer off
	f.sweepTimeouts()
	if f.op(op.ID).State != team.RelayRequested {
		t.Fatal("a seen op failed before 15 min from seen")
	}
	f.clock.Add(minute)
	f.sweepTimeouts()
	if got := f.op(op.ID); got.State != team.RelayFailed || got.Reason != team.RelayReasonMemberUnresponsive {
		t.Fatalf("15 min after seen: %+v", got)
	}
}

// Past claimed with no progress for 15 min: the frame first; still alive and not terminal → member_unresponsive.
func TestSweep_StallAfterClaim15Min(t *testing.T) {
	f := newFixture(t)
	op := f.claimedMemberOp()
	f.setFrames(frameOf("sid-m1", "%2", true)) // the same session, alive
	f.clock.Add(15*minute - 1_000)
	f.sweepTimeouts()
	if f.op(op.ID).State != team.RelayClaimed {
		t.Fatal("failed before the stall timeout")
	}
	f.clock.Add(1_000)
	f.sweepTimeouts()
	if got := f.op(op.ID); got.State != team.RelayFailed || got.Reason != team.RelayReasonMemberUnresponsive {
		t.Fatalf("stalled: %+v", got)
	}
}

// A stall is first looked up in the frame: a /clear that nobody reported is cleared, not failed.
func TestSweep_StallConsultsTheFrameBeforeFailing(t *testing.T) {
	f := newFixture(t)
	op := f.claimedMemberOp()
	f.origins.cleared = map[string]int{"sid-new": 42}
	f.setFrames(frameOf("sid-new", "%2", true))
	f.clock.Add(15 * minute)
	f.sweepTimeouts()
	if got := f.op(op.ID); got.State != team.RelayCleared || got.NewSessionID != "sid-new" {
		t.Fatalf("stalled op with a new frame session: %+v", got)
	}
}

func TestSweep_SelfOpStalledFailsHandoffIncomplete(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1")
	f.decide(out.RequestID, "approve")
	f.setFrames(frameOf("sid-1", "%1", true))
	f.clock.Add(15 * minute)
	f.sweepTimeouts()
	if got := f.op(out.Op.ID); got.State != team.RelayFailed || got.Reason != team.RelayReasonHandoffIncomplete {
		t.Fatalf("self op: %+v", got)
	}
}

// A cleared op is left alone until its 15 minutes; then (its new session being live here) it is done.
func TestSweep_AClearedOpIsLeftAloneUntilItsStallTimeout(t *testing.T) {
	f := newFixture(t)
	op := f.claimedMemberOp()
	f.origins.cleared = map[string]int{"sid-new": 42}
	f.report(op.ID, team.RelayReportRequest{State: team.RelayWriting})
	f.report(op.ID, team.RelayReportRequest{State: team.RelayWritten})
	f.report(op.ID, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-new"})
	f.clock.Add(14 * minute)
	f.sweepTimeouts()
	if f.op(op.ID).State != team.RelayCleared {
		t.Fatal("a cleared op failed early")
	}
	f.clock.Add(minute)
	f.sweepTimeouts()
	if got := f.op(op.ID); got.State != team.RelayDone {
		t.Fatalf("stalled cleared: %+v", got)
	}
}

// Irreversible, so nothing is judged inside the boot grace. Mutation gate: drop the grace → red.
func TestSweep_NothingIsJudgedInsideTheBootGrace(t *testing.T) {
	f := newFixture(t)
	op := f.requestedMemberOp()
	f.clock.Add(10 * minute)
	f.m.bootAt = f.clock.Load() - 1_000
	f.sweepTimeouts()
	if f.op(op.ID).State != team.RelayRequested {
		t.Fatal("timed out inside the boot grace")
	}
	f.clock.Add(team.BootGraceS * 1000)
	f.sweepTimeouts()
	if f.op(op.ID).State != team.RelayFailed {
		t.Fatal("not judged after the grace")
	}
}

// The tick runs it on the liveness cadence.
func TestSweep_TheLivenessTickRunsTheTimeouts(t *testing.T) {
	f := newFixture(t)
	op := f.requestedMemberOp()
	f.clock.Add(2 * minute)
	f.m.tickN = livenessEvery - 1
	f.m.tick()
	if f.op(op.ID).State != team.RelayFailed {
		t.Fatal("the liveness tick did not run the relay timeouts")
	}
}

// The judgement is made from a snapshot; a seen or a claim that lands before the write wins. Mutation gate: drop the
// expectation from failOnTimeout → the stale judgement fails the op (red).
func TestSweep_ProgressThatLandsAfterTheSnapshotWins(t *testing.T) {
	f := newFixture(t)
	op := f.requestedMemberOp()
	f.clock.Add(2 * minute)
	stale := f.op(op.ID)
	if code, _, _ := f.seen(op.ID, "sid-m1"); code != 200 { // the mod answers after the sweeper's read
		t.Fatal("seen")
	}
	getLogs := f.logs()
	f.m.judgeRelayTimeout(stale, f.clock.Load())
	if f.op(op.ID).State != team.RelayRequested {
		t.Fatalf("a stale judgement failed an op that was seen meanwhile: %+v", f.op(op.ID))
	}
	for _, l := range getLogs() {
		if strings.Contains(l, "timed out") {
			t.Fatalf("a skipped timeout was logged as applied: %q", l)
		}
	}
	// and a claim
	f2 := newFixture(t)
	op2 := f2.requestedMemberOp()
	f2.clock.Add(2 * minute)
	stale2 := f2.op(op2.ID)
	f2.claim(op2.ID, "sid-m1")
	f2.m.judgeRelayTimeout(stale2, f2.clock.Load())
	if f2.op(op2.ID).State != team.RelayClaimed {
		t.Fatalf("a stale judgement failed a claimed op: %+v", f2.op(op2.ID))
	}
}

// A stalled cleared op: the seed turn runs on after a successful relay, so a live new session means done; a new session
// that is not there fails. Mutation gate: cleared always failed → the live case is red.
func TestSweep_StalledClearedWithALiveNewSessionIsDone(t *testing.T) {
	f := newFixture(t)
	op := f.claimedMemberOp()
	f.origins.cleared = map[string]int{"sid-new": 42}
	for _, s := range []team.RelayReportRequest{{State: team.RelayWriting}, {State: team.RelayWritten}, {State: team.RelayCleared, NewSessionID: "sid-new"}} {
		f.report(op.ID, s)
	}
	f.setFrames(frameOf("sid-new", "%2", true))
	f.clock.Add(15 * minute)
	f.sweepTimeouts()
	if got := f.op(op.ID); got.State != team.RelayDone {
		t.Fatalf("stalled cleared, new session live: %+v", got)
	}
	waitFor(t, func() bool { return len(f.leadNotices()) == 1 })
	if n := f.leadNotices(); !strings.Contains(n[0], "接力完成") {
		t.Fatalf("the lead's notice = %v", n)
	}
}

func TestSweep_StalledClearedWithoutItsNewSessionFails(t *testing.T) {
	f := newFixture(t)
	op := f.claimedMemberOp()
	f.origins.cleared = map[string]int{"sid-new": 42}
	for _, s := range []team.RelayReportRequest{{State: team.RelayWriting}, {State: team.RelayWritten}, {State: team.RelayCleared, NewSessionID: "sid-new"}} {
		f.report(op.ID, s)
	}
	f.setFrames()
	f.origins.markDead("sid-new")
	f.clock.Add(15 * minute)
	f.sweepTimeouts()
	if got := f.op(op.ID); got.State != team.RelayFailed || got.Reason != team.RelayReasonMemberUnresponsive {
		t.Fatalf("stalled cleared, new session gone: %+v", got)
	}
	waitFor(t, func() bool { return len(f.leadNotices()) == 1 })
	if n := f.leadNotices(); !strings.Contains(n[0], "接力失敗") {
		t.Fatalf("the lead's notice = %v", n)
	}
}
