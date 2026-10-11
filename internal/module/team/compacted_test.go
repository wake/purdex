package teammod

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// The member auto-compact notice (plan v3 P7-2; spec §8.5): the mod reports every compaction it does not intercept; the
// daemon tells the lead only for an active local member's AUTO one, and disarms the 70% notice without arming it again.

func (f *fixture) compacted(sid, trigger string) (int, team.RelayCompactedResponse) {
	f.t.Helper()
	code, raw := f.do(http.MethodPost, "/api/relay/compacted", team.RelayCompactedRequest{SessionID: sid, Trigger: trigger})
	var res team.RelayCompactedResponse
	if code == http.StatusOK {
		if err := json.Unmarshal(raw, &res); err != nil {
			f.t.Fatal(err)
		}
	}
	return code, res
}

func compactedNotice(ref string) string { return fmt.Sprintf(CompactedNoticeFmt, ref) }

func TestCompacted_NoticeFormatIsPinned(t *testing.T) {
	if got, want := compactedNotice("_abc123"), "[pdx team] _abc123 已自動壓縮（lead 未在 70% 時接力）"; got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
}

func TestCompacted_MemberAutoNotifiesOnce(t *testing.T) {
	f := noticeFixture(t)
	code, res := f.compacted("sid-ma", "auto")
	if code != http.StatusOK || !res.Noticed {
		t.Fatalf("auto compaction = %d %+v, want 200 noticed", code, res)
	}
	waitFor(t, func() bool { return len(f.sender.calls()) == 1 })
	if got := f.sender.calls()[0].Text; got != compactedNotice("_memop-a") {
		t.Fatalf("notice = %q, want %q", got, compactedNotice("_memop-a"))
	}
}

// Mutation gate: notice on manual → red.
func TestCompacted_ManualDoesNot(t *testing.T) {
	f := noticeFixture(t)
	code, res := f.compacted("sid-ma", "manual")
	if code != http.StatusOK || res.Noticed {
		t.Fatalf("manual compaction = %d %+v, want 200 not noticed", code, res)
	}
	f.usageCheck()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices for a manual compaction, want 0", n)
	}
}

func TestCompacted_NonMemberDoesNot(t *testing.T) {
	f := noticeFixture(t)
	for _, sid := range []string{"sid-1" /* the lead */, "sid-nobody"} {
		if code, res := f.compacted(sid, "auto"); code != http.StatusOK || res.Noticed {
			t.Fatalf("%s: %d %+v, want 200 not noticed", sid, code, res)
		}
	}
	if code, _ := f.compacted("", "auto"); code != http.StatusBadRequest {
		t.Fatalf("empty session = %d, want 400", code)
	}
	if code, _ := f.compacted("sid-ma", "sideways"); code != http.StatusBadRequest {
		t.Fatalf("unknown trigger = %d, want 400", code)
	}
	f.usageCheck()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices, want 0", n)
	}
}

// Mutation gate: arm instead of disarm → the stale-reading tick sends the 70% notice → red.
func TestCompacted_NoticeIsNotFollowedByA70NoticeOnTheNextTick(t *testing.T) {
	f := noticeFixture(t)
	f.usage.setStatus("tm-op-a", "idle")
	f.usage.setPct("sid-ma", 80) // the last reading is stale: still over the threshold until the next statusline refresh
	f.compacted("sid-ma", "auto")
	waitFor(t, func() bool { return len(f.sender.calls()) == 1 })
	f.usageCheck()
	f.usageCheck()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices after the next ticks, want only the compaction's", n)
	}
	f.usage.setPct("sid-ma", 30) // a fresh reading under the threshold arms again
	f.usageCheck()
	f.usage.setPct("sid-ma", 75)
	f.usageCheck()
	if n := len(f.sender.calls()); n != 2 {
		t.Fatalf("%d notices after 30%% then 75%% idle, want 2 (one 70%% notice)", n)
	}
}

// Codex finding 5 again: a compaction notice that did not go does NOT arm the 70% notice (the reading is stale).
func TestCompacted_AFailedSendDoesNotArmTheUsageNotice(t *testing.T) {
	f := noticeFixture(t)
	f.usage.setStatus("tm-op-a", "idle")
	f.usage.setPct("sid-ma", 85) // from before the compaction
	f.sender.setErr(fmt.Errorf("lead inbox down"))
	f.compacted("sid-ma", "auto")
	time.Sleep(100 * time.Millisecond)
	f.sender.setErr(nil)
	f.usageCheck()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices after a failed compaction notice, want 0 (no stale 70%% notice)", n)
	}
}

// An idle compaction (idle relay spec §3.4, D4): the member's mod tells an idle compaction from a context-full one.

func compactedIdleNotice(ref string) string { return fmt.Sprintf(CompactedIdleNoticeFmt, ref) }

// Mutation gate: the idle trigger takes the auto text → red.
func TestRelayCompacted_IdleTellsLead(t *testing.T) {
	f := noticeFixture(t)
	code, res := f.compacted("sid-ma", "idle")
	if code != http.StatusOK || !res.Noticed {
		t.Fatalf("idle compaction = %d %+v, want 200 noticed", code, res)
	}
	waitFor(t, func() bool { return len(f.sender.calls()) == 1 })
	if got := f.sender.calls()[0].Text; got != compactedIdleNotice("_memop-a") {
		t.Fatalf("notice = %q, want %q", got, compactedIdleNotice("_memop-a"))
	}
	if compactedIdleNotice("_memop-a") == compactedNotice("_memop-a") {
		t.Fatal("the idle text is the auto text")
	}
}

// `idle` disarms the 70% notice (not arming it again) and withdraws an open ask, exactly as `auto` does.
func TestRelayCompacted_IdleDisarmsAndWithdrawsLikeAuto(t *testing.T) {
	for _, trigger := range []string{"auto", "idle"} {
		f := noticeFixture(t)
		f.usage.setStatus("tm-op-a", "idle")
		f.usage.setPct("sid-ma", 80) // the stale reading, over 70%
		now := f.clock.Load()
		if _, _, err := f.m.store.CreateRelayAsk(RelayAsk{ID: rid(901), SessionID: "sid-ma", UsedPct: 72, Window: 1000000, CreatedAt: now, ExpiresAt: now + team.RelayAskHoldS*1000}); err != nil {
			t.Fatal(err)
		}
		f.compacted("sid-ma", trigger)
		waitFor(t, func() bool { return len(f.sender.calls()) == 1 })
		if a := f.ask(rid(901)); a.State != team.RelayAskWithdrawn {
			t.Fatalf("%s: ask after the compaction = %+v, want withdrawn", trigger, a)
		}
		f.usageCheck()
		f.usageCheck()
		if n := len(f.sender.calls()); n != 1 {
			t.Fatalf("%s: %d notices after the next ticks, want only the compaction's (no stale 70%% notice)", trigger, n)
		}
	}
}

func TestRelayCompacted_IdleNonMemberDoesNot(t *testing.T) {
	f := noticeFixture(t)
	for _, sid := range []string{"sid-1", "sid-nobody"} {
		if code, res := f.compacted(sid, "idle"); code != http.StatusOK || res.Noticed {
			t.Fatalf("%s: %d %+v, want 200 not noticed", sid, code, res)
		}
	}
	f.usageCheck()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices, want 0", n)
	}
}

// Mutation gate: manual tells somebody → red.
func TestRelayCompacted_ManualTellsNobodyAndAnUnknownTriggerIs400(t *testing.T) {
	f := noticeFixture(t)
	if code, res := f.compacted("sid-ma", "manual"); code != http.StatusOK || res.Noticed {
		t.Fatalf("manual = %d %+v", code, res)
	}
	if code, _ := f.compacted("sid-ma", "sideways"); code != http.StatusBadRequest {
		t.Fatalf("sideways = %d, want 400", code)
	}
	f.usageCheck()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices, want 0", n)
	}
}
