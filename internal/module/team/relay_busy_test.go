package teammod

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// #2439: a member relay waits out a long turn (up to RelayBusyCapS after seen), tells the lead once at 15 minutes, fails an
// idle-and-unclaimed op after 2 minutes, and says WHY a member never acknowledged (member_unseen / member_blocked).

const memberTmux = "tm-op-m1" // the fixture member's tmux session (seeded row)

func (f *fixture) seenMemberOp(agentStatus string) team.RelayOp {
	f.t.Helper()
	op := f.requestedMemberOp()
	if agentStatus != "" {
		f.usage.setStatus(memberTmux, agentStatus)
	}
	if code, _, ae := f.seen(op.ID, "sid-m1"); code != 200 {
		f.t.Fatalf("seen: %d %+v", code, ae)
	}
	return f.op(op.ID)
}

func (f *fixture) busyNotices() []string {
	var out []string
	for _, c := range f.sender.calls() {
		if strings.Contains(c.Text, "這一輪已跑 15 分鐘") {
			out = append(out, c.Text)
		}
	}
	return out
}

func TestBusyNotice_FormatIsPinned(t *testing.T) {
	got := fmt.Sprintf(team.RelayBusyNoticeFmt, "mlab/work-1", "abc123")
	want := "[pdx team] member mlab/work-1 [abc123] 這一輪已跑 15 分鐘，接力會在它的回合結束時進行（最多再等 45 分鐘）"
	if got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
}

// A running member past 15 minutes: the op stays open, the lead is told ONCE, and the mod can still claim at its turn end.
// Mutation gates: fail at 15 min as before → red; no dedupe → two notices (red).
func TestBusy_ARunningMemberPast15MinKeepsTheOpAndTellsTheLeadOnce(t *testing.T) {
	f := newFixture(t)
	op := f.seenMemberOp("running")
	f.clock.Add(14 * minute)
	f.sweepTimeouts()
	if n := len(f.busyNotices()); n != 0 {
		t.Fatalf("%d notices before 15 min", n)
	}
	f.clock.Add(minute)
	f.sweepTimeouts()
	waitFor(t, func() bool { return len(f.busyNotices()) == 1 })
	if got := f.op(op.ID); got.State != team.RelayRequested {
		t.Fatalf("at 15 min: %+v", got)
	}
	for range 3 {
		f.clock.Add(minute)
		f.sweepTimeouts()
	}
	time.Sleep(80 * time.Millisecond)
	if n := len(f.busyNotices()); n != 1 {
		t.Fatalf("%d notices after more sweeps, want exactly 1", n)
	}
	if want := fmt.Sprintf(team.RelayBusyNoticeFmt, "self/w-one", "mem001"); f.busyNotices()[0] != want {
		t.Fatalf("notice = %q, want %q", f.busyNotices()[0], want)
	}
	// the turn ends at minute 20: the mod claims
	f.clock.Add(minute)
	if code, _, ae := f.claim(op.ID, "sid-m1"); code != 200 {
		t.Fatalf("claim at 20 min: %d %+v", code, ae)
	}
}

// A transient store failure while the notice is being built must not use up its single chance (codex R1): the mark is given
// back and the next sweep sends it. A team that is really gone keeps the mark (nothing to tell).
func TestBusy_ATransientLookupFailureDoesNotSuppressTheNotice(t *testing.T) {
	f := newFixture(t)
	op := f.seenMemberOp("running")
	mr, _, _, _ := f.m.store.ActiveMemberInLiveTeam("sid-m1")
	if _, err := f.m.store.db.Exec(`ALTER TABLE teams RENAME TO teams_away`); err != nil {
		t.Fatal(err)
	}
	f.m.busyNoticeOnce(op, mr)
	time.Sleep(100 * time.Millisecond)
	if _, err := f.m.store.db.Exec(`ALTER TABLE teams_away RENAME TO teams`); err != nil {
		t.Fatal(err)
	}
	if n := len(f.busyNotices()); n != 0 {
		t.Fatalf("a notice went out while the lookup failed (%d)", n)
	}
	f.m.busyNoticeOnce(op, mr) // the next sweep
	waitFor(t, func() bool { return len(f.busyNotices()) == 1 })
}

// Past the hard cap the op fails member_busy_timeout, and the lead is told by the usual failure notice.
func TestBusy_ARunningMemberPastTheCapFailsMemberBusyTimeout(t *testing.T) {
	f := newFixture(t)
	op := f.seenMemberOp("running")
	f.clock.Add(60*minute - 1_000)
	f.sweepTimeouts()
	if f.op(op.ID).State != team.RelayRequested {
		t.Fatal("failed before the cap")
	}
	f.clock.Add(1_000)
	f.sweepTimeouts()
	if got := f.op(op.ID); got.State != team.RelayFailed || got.Reason != team.RelayReasonMemberBusyTimeout {
		t.Fatalf("at the cap: %+v", got)
	}
	waitFor(t, func() bool { return len(f.leadNotices()) == 1 })
}

// Idle (its turn ended) and still unclaimed for 2 minutes: the mod did not claim. The 2 minutes count from when the member
// was seen idle, and a running turn in between starts them again.
func TestBusy_IdleAndUnclaimedFor2MinFailsMemberUnresponsive(t *testing.T) {
	f := newFixture(t)
	op := f.seenMemberOp("running")
	f.clock.Add(30 * minute) // a long turn first
	f.sweepTimeouts()
	if f.op(op.ID).State != team.RelayRequested {
		t.Fatal("failed while running")
	}
	f.usage.setStatus(memberTmux, "idle")
	f.sweepTimeouts() // first seen idle
	f.clock.Add(2*minute - 1_000)
	f.sweepTimeouts()
	if f.op(op.ID).State != team.RelayRequested {
		t.Fatal("failed before 2 min of idle")
	}
	f.usage.setStatus(memberTmux, "running") // a new turn: the count restarts
	f.sweepTimeouts()
	f.usage.setStatus(memberTmux, "idle")
	f.sweepTimeouts()
	f.clock.Add(2*minute - 1_000)
	f.sweepTimeouts()
	if f.op(op.ID).State != team.RelayRequested {
		t.Fatal("the idle count did not restart after a running turn")
	}
	f.clock.Add(1_000)
	f.sweepTimeouts()
	if got := f.op(op.ID); got.State != team.RelayFailed || got.Reason != team.RelayReasonMemberUnresponsive {
		t.Fatalf("idle for 2 min: %+v", got)
	}
}

// No reading of the agent status (the agent module knows nothing of the session): today's rule, 15 min from seen.
func TestBusy_NoStatusKeepsTheOld15MinuteRule(t *testing.T) {
	f := newFixture(t)
	op := f.seenMemberOp("")
	f.clock.Add(15*minute - 1_000)
	f.sweepTimeouts()
	if f.op(op.ID).State != team.RelayRequested {
		t.Fatal("failed early")
	}
	f.clock.Add(1_000)
	f.sweepTimeouts()
	if got := f.op(op.ID); got.State != team.RelayFailed || got.Reason != team.RelayReasonMemberUnresponsive {
		t.Fatalf("15 min, no status: %+v", got)
	}
}

// Never seen: member_unseen, or member_blocked when the agent says it is waiting on a prompt.
func TestUnseen_FailsMemberUnseenOrMemberBlocked(t *testing.T) {
	for status, want := range map[string]string{"": team.RelayReasonMemberUnseen, "idle": team.RelayReasonMemberUnseen,
		"running": team.RelayReasonMemberUnseen, "waiting": team.RelayReasonMemberBlocked} {
		f := newFixture(t)
		op := f.requestedMemberOp()
		if status != "" {
			f.usage.setStatus(memberTmux, status)
		}
		f.clock.Add(59_000)
		f.sweepTimeouts()
		if f.op(op.ID).State != team.RelayRequested {
			t.Fatalf("%q: failed before 60 s", status)
		}
		f.clock.Add(1_000)
		f.sweepTimeouts()
		if got := f.op(op.ID); got.State != team.RelayFailed || got.Reason != want {
			t.Errorf("status %q: %+v, want %s", status, got, want)
		}
	}
}

// Seen, waiting on a prompt past the cap: it is blocked, not merely busy.
func TestBusy_AWaitingMemberPastTheCapIsBlocked(t *testing.T) {
	f := newFixture(t)
	op := f.seenMemberOp("waiting")
	f.clock.Add(60 * minute)
	f.sweepTimeouts()
	if got := f.op(op.ID); got.State != team.RelayFailed || got.Reason != team.RelayReasonMemberBlocked {
		t.Fatalf("waiting at the cap: %+v", got)
	}
}
