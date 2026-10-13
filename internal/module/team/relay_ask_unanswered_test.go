package teammod

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// A member's final ask the lead lets lapse (RT-2b T23, S-6): the member pauses, relay_unanswered, until the lead answers.

// finalAsked is a member team whose member has an open ask at pct, its notice delivered and forgotten, then past its window.
func finalAsked(t *testing.T, pct int) (*fixture, RelayAsk) {
	t.Helper()
	f := newFixture(t)
	f.memberTeam("3")
	if code, _, _ := f.postAsk(rid(900), "sid-m1", pct); code != http.StatusOK {
		t.Fatal("ask failed")
	}
	time.Sleep(100 * time.Millisecond)
	f.forgetSent()
	return f, f.ask(rid(900))
}

func (f *fixture) lapse(a RelayAsk) {
	f.t.Helper()
	f.clock.Store(a.CreatedAt + team.RelayAskHoldS*1000)
	f.livenessTick()
}

func TestRelayAskNotice_FinalSuffixAndDenyHint(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("3")
	f.postAsk(rid(901), "sid-m1", 90)
	time.Sleep(100 * time.Millisecond)
	calls := f.sender.calls()
	want := wantAskNotice(90, 5) + team.RelayAskFinalSuffix
	if len(calls) != 1 || calls[0].Text != want || !strings.Contains(calls[0].Text, "--deny") {
		t.Fatalf("notice = %+v, want %q", calls, want)
	}
}

// Mutation gates: expire a final ask as any other → no pause (red); mark twice → a second notice (red).
func TestFinalAsk_PastItsWindowPausesTheMemberAndTellsBoth(t *testing.T) {
	f, a := finalAsked(t, 91)
	f.lapse(a)
	got := f.ask(a.ID)
	if got.State != team.RelayAskOpen || got.UnansweredAt == 0 || got.ExpiresAt != team.UnansweredDeadlineMs {
		t.Fatalf("ask = %+v, want open, marked, sentinel expiry", got)
	}
	p, ok := f.pauses()["sid-m1/relay_unanswered"]
	if !ok || p.Role != team.PauseRoleMember {
		t.Fatalf("pauses = %+v, want a member relay_unanswered row", f.pauses())
	}
	if n := f.noticesOf("sid-m1"); len(n) != 1 || n[0] != team.PauseNoticeUnansweredMember {
		t.Fatalf("member notices = %q", n)
	}
	ref := "mem001"
	wantLead := fmt.Sprintf(team.RelayAskUnansweredLeadFmt, "self/w-one", ref, "worker", ref, ref)
	var told int
	for _, c := range f.sender.calls() {
		if c.Text == wantLead {
			told++
		}
	}
	if told != 1 {
		t.Fatalf("lead notices = %+v, want %q once", f.sender.calls(), wantLead)
	}
	f.livenessTick() // marked once
	f.livenessTick()
	if n := f.noticesOf("sid-m1"); len(n) != 1 {
		t.Fatalf("member notices after more ticks = %q", n)
	}
	told = 0
	for _, c := range f.sender.calls() {
		if c.Text == wantLead {
			told++
		}
	}
	if told != 1 {
		t.Fatalf("lead told %d times", told)
	}
}

// Mutation gate: drop the used_pct condition → an 85% ask pauses the member (red).
func TestFinalAsk_BelowTheLastAskPctExpiresAsToday(t *testing.T) {
	f, a := finalAsked(t, 85)
	f.lapse(a)
	if got := f.ask(a.ID); got.State != team.RelayAskExpired || got.UnansweredAt != 0 {
		t.Fatalf("ask = %+v, want expired", got)
	}
	if len(f.pauses()) != 0 {
		t.Fatalf("pauses = %+v", f.pauses())
	}
}

// S-8: the mirror of a remote member's final ask on the lead host expires as it always did. Mutation gate: skip the local-member
// condition → the mirror never expires (red).
func TestFinalAsk_ARemoteMirrorExpiresAsToday(t *testing.T) {
	f, _ := leadAskFixture(t)
	f.postFact(leadPrincipal(), relayAskFact(factUUID1, "mk1", rid(910), 95, 300))
	a := f.ask(rid(910))
	f.clock.Store(a.CreatedAt + team.RelayAskHoldS*1000)
	f.livenessTick()
	if got := f.ask(a.ID); got.State != team.RelayAskExpired || got.UnansweredAt != 0 {
		t.Fatalf("mirror = %+v, want expired", got)
	}
}

// Each way out resumes the member with its text and the bounce list; a pause of another cause stands. Mutation gates: skip the
// leave in the accept → the accept case is red; in the compaction → red.
func TestFinalAsk_EveryWayOutResumesTheMember(t *testing.T) {
	cases := []struct {
		name string
		out  func(t *testing.T, f *fixture)
		want string
	}{
		{"the lead accepts", func(t *testing.T, f *fixture) {
			if code, _, ae := f.createRelay(rid(920), "/tmp/10.sock", "_mem001"); code != http.StatusCreated {
				t.Fatalf("relay: %d %+v", code, ae)
			}
		}, team.PauseResumedMemberAccepted},
		{"the lead denies", func(t *testing.T, f *fixture) {
			if code, ae := f.deny("/tmp/10.sock", "_mem001"); code != http.StatusOK {
				t.Fatalf("deny: %d %+v", code, ae)
			}
		}, team.PauseResumedMemberDenied},
		{"a compaction", func(t *testing.T, f *fixture) {
			if code, _ := f.compacted("sid-m1", "auto"); code != http.StatusOK {
				t.Fatalf("compacted: %d", code)
			}
		}, team.PauseResumedUnansweredEnded},
		{"the member leaves", func(t *testing.T, f *fixture) {
			if ok, err := f.m.store.ReleaseMember("op-m1", "sid-m1", f.clock.Load()); err != nil || !ok {
				t.Fatalf("release: %v %v", ok, err)
			}
			f.livenessTick()
		}, team.PauseResumedUnansweredEnded},
	}
	for _, c := range cases {
		f, a := finalAsked(t, 92)
		f.lapse(a)
		if _, err := f.m.store.db.Exec(`INSERT INTO pause_bounces (session_id, from_addr, text_hash, at, first_line) VALUES ('sid-m1', 'a', 'h1', 1, 'x')`); err != nil {
			t.Fatal(err)
		}
		c.out(t, f)
		if _, ok := f.pauses()["sid-m1/relay_unanswered"]; ok {
			t.Errorf("%s: the pause row is still there", c.name)
		}
		n := f.noticesOf("sid-m1")
		if len(n) < 3 || n[len(n)-2] != c.want || !strings.HasPrefix(n[len(n)-1], "[pdx team] 暫停期間有 1 則訊息被退回") {
			t.Errorf("%s: notices = %q, want the resume text then the bounce list", c.name, n)
		}
	}
}

// The bounce text of a paused member names its lead. Mutation gate: the member role answers the solo text (red, in pause_verdict_test too).
func TestFinalAsk_ThePausedMemberBouncesWithTheLeadText(t *testing.T) {
	f, a := finalAsked(t, 92)
	f.lapse(a)
	paused, detail, err := f.m.store.PauseVerdict("sid-m1", "x", "hi", f.clock.Load())
	if err != nil || !paused || detail != team.PauseBounceUnansweredMember {
		t.Fatalf("verdict = %v %q %v", paused, detail, err)
	}
}

// The column on an old relay_asks table. Mutation gate: skip the ensureColumn → opening a pre-RT-2b database fails (red).
func TestRelayAsks_UnansweredAtColumnOnAnOldTable(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec(`ALTER TABLE relay_asks DROP COLUMN unanswered_at`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO relay_asks (id, team_id, spawn_op, session_id, used_pct, window, state, created_at, expires_at) VALUES ('a-1', 't', 'op', 's', 90, 1, 'open', 1, 2)`); err != nil {
		t.Fatal(err)
	}
	if err := ensureColumn(s.db, "relay_asks", "unanswered_at", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.GetRelayAsk("a-1")
	if err != nil || !ok || a.UnansweredAt != 0 || a.UsedPct != 90 {
		t.Fatalf("old row = %+v ok=%v err=%v", a, ok, err)
	}
}

// codex R1: a lead that accepts a final ask after its window passed but before the sweeper's mark gets its relay, the ask is accepted
// and nothing pauses the member afterwards. Mutation gate: require expires_at > now for a final ask too → the ask stays open and the next
// tick pauses the member (red).
func TestFinalAsk_AcceptedInTheGapBeforeTheMark(t *testing.T) {
	f, a := finalAsked(t, 93)
	f.clock.Store(a.CreatedAt + team.RelayAskHoldS*1000 + 1) // past the window, no tick yet
	if code, _, ae := f.createRelay(rid(930), "/tmp/10.sock", "_mem001"); code != http.StatusCreated {
		t.Fatalf("relay: %d %+v", code, ae)
	}
	if got := f.ask(a.ID); got.State != team.RelayAskAccepted {
		t.Fatalf("ask = %+v, want accepted", got)
	}
	f.livenessTick()
	if len(f.pauses()) != 0 {
		t.Fatalf("pauses = %+v, want none", f.pauses())
	}
}

// codex R1: the lead's "paused" notice is not sent for an ask that was answered after the mark. Mutation gate: send without the re-read →
// the stale notice goes out (red).
func TestFinalAsk_NoStaleNoticeAfterAnAnswer(t *testing.T) {
	f, a := finalAsked(t, 93)
	f.m.store.db.Exec(`UPDATE relay_asks SET state = 'withdrawn', reason = 'denied', closed_at = 1 WHERE id = ?`, a.ID)
	f.m.markFinalAsksNoticeFor(unansweredAsk{Ask: RelayAsk{ID: a.ID}, Member: memberRow{Ref: "_mem001", SessionID: "sid-m1"}}, "x")
	time.Sleep(100 * time.Millisecond)
	for _, c := range f.sender.calls() {
		if c.Text == "x" {
			t.Fatalf("a notice went out for a closed ask: %+v", c)
		}
	}
}

// #2816: a person's release of a member its lead's silence paused denies the ask (decided by the person), removes the pause row,
// tells the member and the lead, and releases other causes without a reask. Mutation gates: skip the ask branch in releaseOne → the
// row stays (red); release with the reask → a reask control (red); no lead notice → red.
func TestFinalAsk_APersonsReleaseDeniesTheAsk(t *testing.T) {
	for _, withQuota := range []bool{false, true} {
		f, a := finalAsked(t, 94)
		f.modHello("sid-m1", "5")
		f.lapse(a)
		if withQuota {
			f.seedPause("sid-m1", team.PauseCauseQuota, team.PauseRoleMember, "_mem001", "worker", 1)
		}
		f.forgetSent()
		code, raw := f.releasePaused("sid-m1", appRelease())
		if code != http.StatusOK {
			t.Fatalf("quota=%v release: %d %s", withQuota, code, raw)
		}
		time.Sleep(100 * time.Millisecond)
		got := f.ask(a.ID)
		if got.State != team.RelayAskWithdrawn || got.Reason != team.RelayAskWithdrawDenied {
			t.Fatalf("quota=%v ask = %+v, want withdrawn / denied", withQuota, got)
		}
		if len(f.pauses()) != 0 {
			t.Fatalf("quota=%v pauses = %+v, want none", withQuota, f.pauses())
		}
		n := f.noticesOf("sid-m1")
		// with another cause the member is told by that cause's own resume text (a member's quota release), not twice
		if len(n) == 0 || (!withQuota && n[len(n)-1] != team.PauseResumedMemberDeniedByUser) {
			t.Fatalf("quota=%v member notices = %q, want the user's denial last", withQuota, n)
		}
		for _, o := range f.outbox() {
			if o.Kind == team.PauseOutboxReask {
				t.Fatalf("quota=%v: a reask control was written", withQuota)
			}
		}
		want := fmt.Sprintf(team.RelayAskDeniedByUserLeadFmt, "self/w-one")
		var told bool
		for _, c := range f.sender.calls() {
			told = told || c.Text == want
		}
		if !told {
			t.Fatalf("quota=%v lead calls = %+v, want %q", withQuota, f.sender.calls(), want)
		}
	}
}
