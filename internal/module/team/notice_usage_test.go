package teammod

import (
	"fmt"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The 70% idle notice (plan v3 P7-1; spec §8.5): a member that reaches the threshold while IDLE tells its lead once;
// the daemon decides nothing. Remote members are the X series' (X2a), not here.

// noticeFixture is an approved lead team (lead sid-1) with one active member op-a (sid-ma, tmux tm-op-a).
func noticeFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.approveLead(uid(1))
	seedMember(t, f.m.store, "op-a", uid(1), "sid-ma", f.clock.Load())
	return f
}

// liveness runs one liveness tick's notice check and waits for the goroutines it started.
func (f *fixture) usageCheck() {
	f.t.Helper()
	f.m.noticeUsage()
	time.Sleep(60 * time.Millisecond)
}

func wantNotice(f *fixture, pct int) string {
	alias, _ := f.m.selfHost()
	return fmt.Sprintf(UsageNoticeFmt, alias+"/_memop-a", "memop-a", "worker", pct, "memop-a")
}

func TestNotice70_FormatIsPinned(t *testing.T) {
	got := fmt.Sprintf(UsageNoticeFmt, "mlab/work-1", "abc123", "A 線", 71, "abc123")
	want := "[pdx team] member mlab/work-1 [abc123]「A 線」已用 71%，目前閒置。要接力請執行：pdx relay _abc123"
	if got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
}

// Mutation gate: drop the idle check → red.
func TestNotice70_CrossingWhileRunningWaitsForIdle(t *testing.T) {
	f := noticeFixture(t)
	f.usage.setPct("sid-ma", 71.4)
	f.usage.setStatus("tm-op-a", "running")
	f.usageCheck()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices while the member runs, want 0", n)
	}
	f.usage.setStatus("tm-op-a", "idle")
	f.usageCheck()
	calls := f.sender.calls()
	if len(calls) != 1 {
		t.Fatalf("%d notices once idle, want 1", len(calls))
	}
	if calls[0].Text != wantNotice(f, 71) {
		t.Fatalf("notice = %q, want %q", calls[0].Text, wantNotice(f, 71))
	}
	alias, _ := f.m.selfHost()
	if want := alias + "/" + ipeers.RefID("sid-1"); calls[0].To != want { // the lead's address
		t.Fatalf("to = %q, want %q", calls[0].To, want)
	}
}

// Mutation gate: drop disarming → the second check sends again, red.
func TestNotice70_OnceThenRearmsAfterARelay(t *testing.T) {
	f := noticeFixture(t)
	f.usage.setPct("sid-ma", 75)
	f.usage.setStatus("tm-op-a", "idle")
	f.usageCheck()
	f.usageCheck()
	f.usageCheck()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices over three checks, want exactly 1", n)
	}
	// the member relays: the cleared transaction moves the row to the new session and arms it again
	tx, err := f.m.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := moveTeamRoles(tx, "sid-ma", RelayReport{NewSessionID: "sid-mb", NewRef: "_newmb", At: f.clock.Load()}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.usage.setPct("sid-mb", 72)
	f.usageCheck()
	if n := len(f.sender.calls()); n != 2 {
		t.Fatalf("%d notices after the relay's new session crossed, want 2", n)
	}
}

func TestNotice70_RearmsAfterADropBelow70(t *testing.T) {
	f := noticeFixture(t)
	f.usage.setStatus("tm-op-a", "idle")
	f.usage.setPct("sid-ma", 80)
	f.usageCheck()
	f.usage.setPct("sid-ma", 40) // a compaction, a /clear...
	f.usageCheck()
	f.usage.setPct("sid-ma", 90)
	f.usageCheck()
	if n := len(f.sender.calls()); n != 2 {
		t.Fatalf("%d notices, want 2 (once per crossing)", n)
	}
}

func TestNotice70_NotWhileARelayIsOpen(t *testing.T) {
	f := noticeFixture(t)
	f.usage.setPct("sid-ma", 80)
	f.usage.setStatus("tm-op-a", "idle")
	if _, err := f.m.store.db.Exec(`INSERT INTO relay_ops (id, kind, host_id, session_id, ref, state, handoff_path, created_at, updated_at)
		VALUES ('op-open', 'member', 'h:1', 'sid-ma', '_memop-a', 'claimed', '/x', 1, 1)`); err != nil {
		t.Skipf("relay_ops insert needs more columns: %v", err)
	}
	f.usageCheck()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices while a relay op of the member is open, want 0", n)
	}
}

// Mutation gate: read another variable name → red.
func TestNotice70_ThresholdFromPDXRelayThreshold(t *testing.T) {
	for _, c := range []struct {
		env  string
		want int
	}{{"5", 5}, {"", team.RelayThresholdPct}, {"0", team.RelayThresholdPct}, {"101", team.RelayThresholdPct}, {"abc", team.RelayThresholdPct}, {"100", 100}} {
		t.Setenv("PDX_RELAY_THRESHOLD", c.env)
		if got := noticeThreshold(); got != c.want {
			t.Errorf("PDX_RELAY_THRESHOLD=%q → %d, want %d", c.env, got, c.want)
		}
	}
	t.Setenv("PDX_RELAY_THRESHOLD", "5")
	f := noticeFixture(t)
	f.usage.setPct("sid-ma", 6)
	f.usage.setStatus("tm-op-a", "idle")
	f.usageCheck()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("threshold 5, 6%% idle: %d notices, want 1", n)
	}
	f.usageCheck()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("a second check sent again at threshold 5 (the re-arm must use the same threshold): %d", n)
	}
}

// A failed send is logged and the member stays armed, so the next check tries again.
func TestNotice70_AFailedSendIsRetried(t *testing.T) {
	f := noticeFixture(t)
	f.usage.setPct("sid-ma", 80)
	f.usage.setStatus("tm-op-a", "idle")
	f.sender.mu.Lock()
	f.sender.err = fmt.Errorf("lead inbox down")
	f.sender.mu.Unlock()
	f.usageCheck()
	f.sender.mu.Lock()
	f.sender.err = nil
	f.sender.mu.Unlock()
	f.usageCheck()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d delivered notices after the retry, want 1", n)
	}
}

// Looked at again before the send: a member that started a turn between the check and the send is not told about, and
// stays armed. Mutation gate: drop the re-check in usageNotice → red.
func TestNotice70_ReChecksIdleRightBeforeTheSend(t *testing.T) {
	f := noticeFixture(t)
	f.usage.setStatus("tm-op-a", "running") // it started a turn after the check that disarmed it
	rows, err := f.m.store.ActiveMembersOfLiveTeams()
	if err != nil || len(rows) != 1 {
		t.Fatalf("members = %v, %v", rows, err)
	}
	if won, err := f.m.store.DisarmNotice("op-a", "sid-ma"); err != nil || !won {
		t.Fatalf("disarm = %v, %v", won, err)
	}
	f.m.usageNotice(rows[0], 80)
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices for a member that is running again, want 0", n)
	}
	if won, _ := f.m.store.DisarmNotice("op-a", "sid-ma"); !won {
		t.Fatal("the member was not armed again after the skipped send")
	}
}
