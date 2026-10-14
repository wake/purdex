package teammod

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// ---- RT-4 T42: the member idle intercept ----

func (f *fixture) setModVersion(sid, ver string) {
	f.m.mu.Lock()
	f.m.modSeen[sid] = helloInfo{ModVersion: ver, Agent: "cc", At: f.clock.Load()}
	f.m.mu.Unlock()
}

func (f *fixture) intercepted(sid string) (int, team.RelayCompactedResponse) {
	f.t.Helper()
	code, raw := f.do(http.MethodPost, "/api/relay/compacted", team.RelayCompactedRequest{SessionID: sid, Trigger: "idle", Intercepted: true})
	var res team.RelayCompactedResponse
	if code == http.StatusOK {
		if err := json.Unmarshal(raw, &res); err != nil {
			f.t.Fatal(err)
		}
	}
	return code, res
}

func interceptText(f *fixture, pct int) string {
	alias, _ := f.m.selfHost()
	return fmt.Sprintf(team.IdleInterceptNoticeFmt, alias+"/_memop-a", "worker", fmt.Sprintf("%d%%", pct), "memop-a", "memop-a", "memop-a")
}

// The notice, word for word.
func TestIntercept_NoticeFormatIsPinned(t *testing.T) {
	got := fmt.Sprintf(team.IdleInterceptNoticeFmt, "mlab/work-1", "A 線", "55%", "abc123", "abc123", "abc123")
	want := "[pdx team] member mlab/work-1「A 線」閒置，快取快過期，context 55%。請決定：接力（pdx relay _abc123）、交接後關閉（pdx kill _abc123 --handoff）、直接關閉（pdx kill _abc123）。"
	if got != want {
		t.Fatalf("notice = %q\nwant     %q", got, want)
	}
	if team.MinIdleInterceptModVersion != 9 {
		t.Fatalf("MinIdleInterceptModVersion = %d", team.MinIdleInterceptModVersion)
	}
}

// An intercepted idle compaction of a member tells the lead ONCE per idle period; the member runs, goes idle again: another.
// Mutation gates: no notice → red; a second notice in one period → red; no new period after the member ran → red.
func TestIntercept_OneNoticePerIdlePeriod(t *testing.T) {
	f := idleSetup(t) // member sid-ma idle at 55 %, its idle clock started
	f.setModVersion("sid-ma", "9")
	f.forgetSent()
	if code, res := f.intercepted("sid-ma"); code != http.StatusOK || !res.Noticed {
		t.Fatalf("intercepted = %d %+v, want 200 noticed", code, res)
	}
	waitFor(t, func() bool { return len(f.sender.calls()) == 1 })
	if got := f.sender.calls()[0].Text; got != interceptText(f, 55) {
		t.Fatalf("notice = %q\nwant     %q", got, interceptText(f, 55))
	}
	// the same period, again: nothing more
	if code, res := f.intercepted("sid-ma"); code != http.StatusOK || res.Noticed {
		t.Fatalf("second intercept = %d %+v, want 200 not noticed", code, res)
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices in one idle period, want 1", n)
	}
	// the member runs and is idle again: a new period, a new notice
	f.usage.setStatus("tm-op-a", "running")
	f.idleCheck()
	f.usage.setStatus("tm-op-a", "idle")
	f.idleCheck()
	if code, res := f.intercepted("sid-ma"); code != http.StatusOK || !res.Noticed {
		t.Fatalf("intercept in the next period = %d %+v, want noticed", code, res)
	}
	waitFor(t, func() bool { return len(f.sender.calls()) == 2 })
}

// An interception is not a compaction: the fresh half stays, an open ask is not withdrawn, the 70 % notice is not disarmed.
// Mutation gate: run the compaction's bookkeeping for it → the half is gone (red).
func TestIntercept_IsNotACompaction(t *testing.T) {
	f := idleSetup(t)
	f.setModVersion("sid-ma", "9")
	f.halfOnDisk("half-ma", "sid-ma", f.m.now())
	if code, _ := f.intercepted("sid-ma"); code != http.StatusOK {
		t.Fatalf("intercepted = %d", code)
	}
	if _, _, ok := f.freshHalf("sid-ma"); !ok {
		t.Fatal("an interception dropped the member's fresh half: it was not compacted")
	}
}

// Only a trigger that can be an idle compaction is intercepted.
func TestIntercept_RefusesAManualTrigger(t *testing.T) {
	f := idleSetup(t)
	code, _ := f.do(http.MethodPost, "/api/relay/compacted", team.RelayCompactedRequest{SessionID: "sid-ma", Trigger: "manual", Intercepted: true})
	if code != http.StatusBadRequest {
		t.Fatalf("manual + intercepted = %d, want 400", code)
	}
}

// A context-full (auto) compaction of a member runs and is reported as ever: the notice is the old one.
func TestIntercept_AContextFullCompactionIsReportedAsToday(t *testing.T) {
	f := idleSetup(t)
	f.setModVersion("sid-ma", "9")
	f.forgetSent()
	if code, res := f.compacted("sid-ma", "auto"); code != http.StatusOK || !res.Noticed {
		t.Fatalf("auto compaction = %d %+v", code, res)
	}
	waitFor(t, func() bool { return len(f.sender.calls()) == 1 })
	if got := f.sender.calls()[0].Text; got != compactedNotice("_memop-a") {
		t.Fatalf("notice = %q, want the compaction's own", got)
	}
}

// A session that is not an active local member tells no one.
func TestIntercept_OfANonMemberNoticesNothing(t *testing.T) {
	f := newFixture(t)
	if code, res := f.intercepted("sid-1"); code != http.StatusOK || res.Noticed {
		t.Fatalf("intercepted = %d %+v, want 200 not noticed", code, res)
	}
}

// The two old notices stay for a member whose mod is older than 9 (a mod that asks for itself, from protocol 3, never had the usage one: so
// the idle one only) and are skipped for one at 9 or more (the intercept replaces them).
// Mutation gates: skip nothing → mod 9 gets both (red); skip everyone → mod 8 gets none (red).
func TestIntercept_OldNoticesOnlyForOlderMods(t *testing.T) {
	for _, c := range []struct {
		ver  string
		want int
	}{{"2", 2}, {"", 2}, {"8", 1}, {"9", 0}} {
		f := noticeFixture(t)
		if c.ver != "" {
			f.setModVersion("sid-ma", c.ver)
		}
		f.usage.setPct("sid-ma", 85)
		f.usage.setStatus("tm-op-a", "idle")
		f.idleCheck() // the idle clock starts
		f.m.noticeUsage()
		time.Sleep(60 * time.Millisecond)
		f.advance(idleMin)
		f.idleCheck()
		if n := len(f.sender.calls()); n != c.want {
			t.Errorf("mod %q: %d notices (usage + idle), want %d", c.ver, n, c.want)
		}
	}
}

// The intercept needs no earlier look of the liveness tick: a member never seen idle still gets its one notice, and only one.
// Mutation gate: no period created → nothing is told (red).
func TestIntercept_NeedsNoEarlierTick(t *testing.T) {
	f := noticeFixture(t)
	f.setModVersion("sid-ma", "9")
	f.usage.setPct("sid-ma", 30)
	if code, res := f.intercepted("sid-ma"); code != http.StatusOK || !res.Noticed {
		t.Fatalf("intercepted = %d %+v, want noticed", code, res)
	}
	if code, res := f.intercepted("sid-ma"); code != http.StatusOK || res.Noticed {
		t.Fatalf("again = %d %+v, want not noticed", code, res)
	}
}
