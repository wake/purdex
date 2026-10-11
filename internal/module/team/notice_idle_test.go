package teammod

import (
	"fmt"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// The member idle notice (idle relay spec §3.3, D3): a local active member idle for RelayIdleMinutes with its context at
// RelayIdleThresholdPct or more tells its lead ONCE per idle period; the lead decides (`pdx relay _<ref>`). The clock is
// the fixture's (m.now), never a real wait.

const idleMin = int64(team.RelayIdleMinutes) * 60_000

func idleNoticeText(f *fixture, minutes, pct int) string {
	alias, _ := f.m.selfHost()
	return fmt.Sprintf(UsageIdleNoticeFmt, alias+"/_memop-a", "memop-a", "worker", minutes, pct, "memop-a")
}

// idleSetup is a member idle at 55%, seen idle for the first time now (the clock starts here).
func idleSetup(t *testing.T) *fixture {
	t.Helper()
	f := noticeFixture(t)
	f.usage.setPct("sid-ma", 55)
	f.usage.setStatus("tm-op-a", "idle")
	f.idleCheck() // first sight of the idle: the clock starts
	return f
}

func (f *fixture) idleCheck() {
	f.t.Helper()
	f.m.noticeIdle()
	time.Sleep(60 * time.Millisecond)
}

func (f *fixture) advance(ms int64) { f.clock.Add(ms) }

func TestIdleNotice_FormatIsPinned(t *testing.T) {
	got := fmt.Sprintf(UsageIdleNoticeFmt, "mlab/work-1", "abc123", "A 線", 40, 55, "abc123")
	want := "[pdx team] member mlab/work-1 [abc123]「A 線」閒置 40 分鐘、已用 55%，快取快過期。要趁現在接力請執行：pdx relay _abc123（不接力的話，Claude Code 稍後會自行壓縮）"
	if got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
	if got, want := fmt.Sprintf(CompactedIdleNoticeFmt, "_abc123"), "[pdx team] _abc123 閒置太久，被 Claude Code 自動壓縮了（快取過期前的閒置壓縮，不是 context 滿）"; got != want {
		t.Fatalf("compacted notice = %q, want %q", got, want)
	}
}

// Mutation gate: drop the idle timing (tell at first sight) → the "just before the minutes" assertion is red.
func TestIdleNotice_AfterIdleMinutes(t *testing.T) {
	f := idleSetup(t)
	f.advance(idleMin - 1)
	f.idleCheck()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices one ms before %d minutes, want 0", n, team.RelayIdleMinutes)
	}
	f.advance(1)
	f.idleCheck()
	calls := f.sender.calls()
	if len(calls) != 1 {
		t.Fatalf("%d notices at %d minutes, want 1", len(calls), team.RelayIdleMinutes)
	}
	if calls[0].Text != idleNoticeText(f, team.RelayIdleMinutes, 55) {
		t.Fatalf("notice = %q, want %q", calls[0].Text, idleNoticeText(f, team.RelayIdleMinutes, 55))
	}
}

// The minutes in the text are the whole minutes it has been idle, not the configured number.
func TestIdleNotice_NamesTheMinutesItWasIdle(t *testing.T) {
	f := idleSetup(t)
	f.advance(idleMin + 2*60_000 + 59_000) // the tick that sees it can be late
	f.idleCheck()
	calls := f.sender.calls()
	if len(calls) != 1 || calls[0].Text != idleNoticeText(f, team.RelayIdleMinutes+2, 55) {
		t.Fatalf("calls = %+v, want one notice with %d minutes", calls, team.RelayIdleMinutes+2)
	}
}

// Mutation gate: drop the told mark → red.
func TestIdleNotice_OncePerIdlePeriod(t *testing.T) {
	f := idleSetup(t)
	f.advance(idleMin)
	f.idleCheck()
	f.advance(10 * 60_000)
	f.idleCheck()
	f.idleCheck()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices in one idle period, want 1", n)
	}
}

// Running again ends the period: the clock and the told mark both go; the next idle period tells again after its own 40.
func TestIdleNotice_ResetOnRunning(t *testing.T) {
	f := idleSetup(t)
	f.advance(idleMin)
	f.idleCheck()
	f.usage.setStatus("tm-op-a", "running")
	f.idleCheck()
	f.usage.setStatus("tm-op-a", "idle")
	f.idleCheck() // a new period starts here
	f.advance(idleMin - 1)
	f.idleCheck()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices before the new period's minutes, want still 1", n)
	}
	f.advance(1)
	f.idleCheck()
	if n := len(f.sender.calls()); n != 2 {
		t.Fatalf("%d notices after the new period's minutes, want 2", n)
	}
}

// A status that is not idle (waiting on a person, unknown) is not idle time.
func TestIdleNotice_WaitingOrUnknownIsNotIdle(t *testing.T) {
	f := idleSetup(t)
	f.advance(idleMin - 1000)
	f.usage.setStatus("tm-op-a", "waiting")
	f.idleCheck()
	f.usage.setStatus("tm-op-a", "idle")
	f.idleCheck()
	f.advance(2000)
	f.idleCheck()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices after a non-idle blip, want 0 (the clock restarted)", n)
	}
}

func TestIdleNotice_BelowThreshold(t *testing.T) {
	f := idleSetup(t)
	f.usage.setPct("sid-ma", float64(team.RelayIdleThresholdPct)-0.5)
	f.advance(2 * idleMin)
	f.idleCheck()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices under %d%%, want 0", n, team.RelayIdleThresholdPct)
	}
	f.usage.setPct("sid-ma", float64(team.RelayIdleThresholdPct))
	f.idleCheck()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices at exactly %d%%, want 1", n, team.RelayIdleThresholdPct)
	}
}

// Mutation gate: drop the relay-open / ask-open check → red.
func TestIdleNotice_SkipsRelayOpenOrAskOpen(t *testing.T) {
	f := idleSetup(t)
	f.advance(idleMin)
	if _, err := f.m.store.db.Exec(`INSERT INTO relay_ops (id, kind, host_id, session_id, ref, state, handoff_path, created_at, updated_at)
		VALUES ('op-open', 'member', 'h:1', 'sid-ma', '_memop-a', 'claimed', '/x', 1, 1)`); err != nil {
		t.Fatalf("relay_ops insert: %v", err)
	}
	f.idleCheck()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices with a relay op open, want 0", n)
	}
	if _, err := f.m.store.db.Exec(`DELETE FROM relay_ops WHERE id = 'op-open'`); err != nil {
		t.Fatal(err)
	}
	now := f.clock.Load()
	if _, _, err := f.m.store.CreateRelayAsk(RelayAsk{ID: rid(900), SessionID: "sid-ma", UsedPct: 72, Window: 1000000, CreatedAt: now, ExpiresAt: now + team.RelayAskHoldS*1000}); err != nil {
		t.Fatal(err)
	}
	f.idleCheck()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices with an ask open, want 0", n)
	}
}

// Right before the send: a member that started a turn between the check and the send is not told about, and the period
// is not marked told. Mutation gate: drop the re-check in idleNotice → red.
func TestIdleNotice_RecheckBeforeSend(t *testing.T) {
	f := idleSetup(t)
	f.advance(idleMin)
	rows, err := f.m.store.ActiveMembersOfLiveTeams()
	if err != nil || len(rows) != 1 {
		t.Fatalf("members = %v, %v", rows, err)
	}
	gen, won := f.m.claimIdleTold("sid-ma")
	if !won {
		t.Fatal("no period to claim")
	}
	f.usage.setStatus("tm-op-a", "running") // it started a turn after the check
	f.m.idleNotice(rows[0], 55, team.RelayIdleMinutes, gen)
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices for a member that is running, want 0", n)
	}
	f.usage.setStatus("tm-op-a", "idle")
	gen, won = f.m.claimIdleTold("sid-ma")
	if !won {
		t.Fatal("the skipped send did not give the period back")
	}
	f.m.idleNotice(rows[0], 55, team.RelayIdleMinutes, gen)
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices once idle again, want 1", n)
	}
}

// Codex attack: a goroutine of one idle period must not send for a later period, nor unmark it. The status flickers
// (idle → waiting → idle) while the notice of the first period is still on its way. Mutation gate: drop the generation → red.
func TestIdleNotice_AStaleGoroutineNeitherSendsNorUnmarksALaterPeriod(t *testing.T) {
	f := idleSetup(t)
	f.advance(idleMin)
	rows, _ := f.m.store.ActiveMembersOfLiveTeams()
	gen1, won := f.m.claimIdleTold("sid-ma")
	if !won {
		t.Fatal("no period to claim")
	}
	f.usage.setStatus("tm-op-a", "waiting")
	f.idleCheck() // the first period ends
	f.usage.setStatus("tm-op-a", "idle")
	f.idleCheck() // a second one starts
	gen2, won := f.m.claimIdleTold("sid-ma")
	if !won || gen2 == gen1 {
		t.Fatalf("second period: won=%v gen1=%d gen2=%d", won, gen1, gen2)
	}
	f.m.idleNotice(rows[0], 55, team.RelayIdleMinutes, gen1) // the goroutine of the first period, late
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices from a stale goroutine, want 0", n)
	}
	f.usage.setStatus("tm-op-a", "running") // …and it takes the release path
	f.m.idleNotice(rows[0], 55, team.RelayIdleMinutes, gen1)
	if _, won := f.m.claimIdleTold("sid-ma"); won {
		t.Fatal("the stale goroutine's release unmarked the second period")
	}
}

// Codex attack: the member was moved to a new session (its relay) or released after the check: the old row's notice is void.
// Mutation gate: drop the active-member re-read → red.
func TestIdleNotice_AMemberThatIsGoneOrMovedIsNotToldAbout(t *testing.T) {
	f := idleSetup(t)
	f.advance(idleMin)
	rows, _ := f.m.store.ActiveMembersOfLiveTeams()
	gen, _ := f.m.claimIdleTold("sid-ma")
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
	f.m.idleNotice(rows[0], 55, team.RelayIdleMinutes, gen)
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices for a session the member left, want 0", n)
	}
}

// Codex attack: a compaction ends the idle question for that stretch — an idle compaction that landed before the clock
// reached the minutes (a restart in between, a longer configured wait) must not be followed by "cache about to expire".
// Mutation gate: drop markIdleCompacted → red.
func TestIdleNotice_NotAfterTheSessionWasCompacted(t *testing.T) {
	f := idleSetup(t)
	f.advance(idleMin - 60_000)
	f.compacted("sid-ma", "idle")
	waitFor(t, func() bool { return len(f.sender.calls()) == 1 })
	f.advance(2 * 60_000)
	f.idleCheck()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices, want only the compaction's", n)
	}
	f.usage.setStatus("tm-op-a", "running") // the stretch ends…
	f.idleCheck()
	f.usage.setStatus("tm-op-a", "idle")
	f.idleCheck() // …and the next one is its own
	f.advance(idleMin)
	f.idleCheck()
	if n := len(f.sender.calls()); n != 2 {
		t.Fatalf("%d notices after the next stretch's minutes, want 2", n)
	}
}

// Advisory: a send that fails is not retried by the next ticks of the same idle period.
func TestIdleNotice_AFailedSendIsNotRetried(t *testing.T) {
	f := idleSetup(t)
	f.advance(idleMin)
	f.sender.setErr(fmt.Errorf("lead inbox down"))
	f.idleCheck()
	f.sender.setErr(nil)
	f.idleCheck()
	f.idleCheck()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d delivered notices, want 0 (no retry)", n)
	}
}

// D3: independent of the 70% notice, and a member whose mod asks for itself still gets it (its own ask is only at 70%).
func TestIdleNotice_IgnoresAsksForItself(t *testing.T) {
	f := idleSetup(t)
	f.m.modSeen["sid-ma"] = helloInfo{ModVersion: fmt.Sprint(team.MinMemberAskModVersion), Agent: "claude", At: f.clock.Load()}
	if !f.m.asksForItself("sid-ma") {
		t.Fatal("the fixture's mod does not ask for itself")
	}
	f.advance(idleMin)
	f.idleCheck()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices for a member whose mod asks for itself, want 1", n)
	}
}

// The 70% notice and the idle notice do not depend on each other's flags: a member at 80% idle for 40 minutes gets both.
func TestIdleNotice_IndependentOfThe70Notice(t *testing.T) {
	f := idleSetup(t)
	f.usage.setPct("sid-ma", 80)
	f.usageCheck() // the 70% notice
	f.advance(idleMin)
	f.idleCheck()
	if n := len(f.sender.calls()); n != 2 {
		t.Fatalf("%d notices, want 2 (the 70%% one and the idle one)", n)
	}
}

func TestIdleNotice_NoLeadNoTeamNoNotice(t *testing.T) {
	f := idleSetup(t)
	f.advance(idleMin)
	f.m.sender = nil
	f.idleCheck() // must not panic
}

// Mutation gate: read other variable names → red.
func TestIdleNotice_EnvOverrides(t *testing.T) {
	for _, c := range []struct {
		env  string
		want int
	}{{"2", 2}, {"", team.RelayIdleMinutes}, {"0", team.RelayIdleMinutes}, {"abc", team.RelayIdleMinutes}, {"50", 50}, {"51", team.RelayIdleMinutes}, {"1440", team.RelayIdleMinutes}} {
		t.Setenv("PDX_RELAY_IDLE_MINUTES", c.env)
		if got := noticeIdleMinutes(); got != c.want {
			t.Errorf("PDX_RELAY_IDLE_MINUTES=%q → %d, want %d", c.env, got, c.want)
		}
	}
	for _, c := range []struct {
		env  string
		want int
	}{{"5", 5}, {"", team.RelayIdleThresholdPct}, {"0", team.RelayIdleThresholdPct}, {"101", team.RelayIdleThresholdPct}, {"100", 100}} {
		t.Setenv("PDX_RELAY_IDLE_THRESHOLD", c.env)
		if got := noticeIdleThreshold(); got != c.want {
			t.Errorf("PDX_RELAY_IDLE_THRESHOLD=%q → %d, want %d", c.env, got, c.want)
		}
	}
	t.Setenv("PDX_RELAY_IDLE_MINUTES", "2")
	t.Setenv("PDX_RELAY_IDLE_THRESHOLD", "5")
	f := noticeFixture(t)
	f.usage.setPct("sid-ma", 6)
	f.usage.setStatus("tm-op-a", "idle")
	f.idleCheck()
	f.advance(2 * 60_000)
	f.idleCheck()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("2 min / 5%% from the environment: %d notices, want 1", n)
	}
}
