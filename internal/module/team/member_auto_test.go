package teammod

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/team"
)

// relay.member_auto (spec 2026-10-24 member-relay-switch §2): off, the daemon does none of the five automatic things for a
// member of this host. Every test flips the switch through the fixture's fake reader, as the host config would.

func (f *fixture) memberAuto(on bool) {
	sw := hostconfig.DefaultRelaySwitches
	sw.MemberAuto = on
	f.switches.set(sw)
}

func TestMemberAuto_HelloAdvertisesIdleInterceptOnlyWhileOn(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1")
	if _, h, _ := f.hello("sid-1"); h.IdleIntercept != team.IdleInterceptMembers {
		t.Fatalf("on: idle_intercept = %d, want %d", h.IdleIntercept, team.IdleInterceptMembers)
	}
	f.memberAuto(false)
	code, h, body := f.hello("sid-1")
	if code != http.StatusOK || h.IdleIntercept != 0 || h.Role != "member" {
		t.Fatalf("off: %d %s, want role member and no idle_intercept", code, body)
	}
	if strings.Contains(string(body), "idle_intercept") {
		t.Fatalf("off: the field is on the wire: %s", body)
	}
	// an unreadable switch is off for the automatic help (fail closed), and it is logged once, not on every call
	logs := f.logs()
	f.switches.mu.Lock()
	f.switches.sw, f.switches.err = hostconfig.RelaySwitches{}, errors.New("store down")
	f.switches.mu.Unlock()
	for i := 0; i < 3; i++ {
		if c, h, body := f.hello("sid-1"); c != http.StatusOK || h.IdleIntercept != 0 {
			t.Fatalf("unreadable: %d %s", c, body)
		}
	}
	n := 0
	for _, l := range logs() {
		if strings.Contains(l, "member_auto") {
			n++
		}
	}
	if n > 1 {
		t.Fatalf("%d member_auto log lines for one unchanged error, want at most 1", n)
	}
}

func TestMemberAuto_MemberAutoOnIsTrueWithNoReader(t *testing.T) {
	f := newFixture(t)
	f.m.switches = nil
	if !f.m.memberAutoOn() {
		t.Fatal("memberAutoOn with no reader = false, want true")
	}
}

func TestMemberAuto_InterceptedIsSilentWhileOff(t *testing.T) {
	f := idleSetup(t)
	f.setModVersion("sid-ma", "9")
	f.forgetSent()
	f.memberAuto(false)
	code, res := f.intercepted("sid-ma")
	if code != http.StatusOK || res.Noticed || res.Route != team.InterceptRouteSilent {
		t.Fatalf("off: %d %+v, want 200 silent, not noticed", code, res)
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices while off, want 0", n)
	}
	// the claim was not taken: back on, the same idle period still tells the lead once
	f.memberAuto(true)
	if code, res := f.intercepted("sid-ma"); code != http.StatusOK || !res.Noticed || res.Route != team.InterceptRouteMember {
		t.Fatalf("on again: %d %+v, want noticed", code, res)
	}
	waitFor(t, func() bool { return len(f.sender.calls()) == 1 })
}

func TestMemberAuto_CompactedKeepsTheBookkeepingAndTellsNobodyWhileOff(t *testing.T) {
	for _, trigger := range []string{"auto", "idle"} {
		f := noticeFixture(t)
		f.usage.setStatus("tm-op-a", "idle")
		f.usage.setPct("sid-ma", 80) // stale reading over 70 %
		now := f.clock.Load()
		if _, _, err := f.m.store.CreateRelayAsk(RelayAsk{ID: rid(901), SessionID: "sid-ma", UsedPct: 72, Window: 1000000, CreatedAt: now, ExpiresAt: now + team.RelayAskHoldS*1000}); err != nil {
			t.Fatal(err)
		}
		f.halfOnDisk("half-ma", "sid-ma", f.m.now())
		f.memberAuto(false)
		code, res := f.compacted("sid-ma", trigger)
		if code != http.StatusOK || res.Noticed {
			t.Fatalf("%s: %d %+v, want 200 not noticed", trigger, code, res)
		}
		time.Sleep(100 * time.Millisecond)
		if n := len(f.sender.calls()); n != 0 {
			t.Fatalf("%s: %d messages to the lead, want 0", trigger, n)
		}
		if a := f.ask(rid(901)); a.State != team.RelayAskWithdrawn {
			t.Fatalf("%s: ask = %+v, want withdrawn", trigger, a)
		}
		if _, _, ok := f.freshHalf("sid-ma"); ok {
			t.Fatalf("%s: the fresh half is still there", trigger)
		}
		// the 70 % notice was disarmed: the stale reading does not make a tick send one once the switch is back on
		f.memberAuto(true)
		f.usageCheck()
		f.usageCheck()
		if n := len(f.sender.calls()); n != 0 {
			t.Fatalf("%s: %d notices after the next ticks, want 0 (disarmed)", trigger, n)
		}
	}
}

func TestMemberAuto_AskIsRefusedForAMemberWhileOff(t *testing.T) {
	f := gateFixture(t, true, true, 2) // unattended on: it would accept
	f.memberAuto(false)
	code, _, ae := f.postAsk(rid(1000), "sid-m1", 72)
	if code != http.StatusConflict || ae.Error != team.ErrRelayUnsupported || !strings.Contains(ae.Detail, "member") {
		t.Fatalf("member ask off: %d %+v, want 409 relay_unsupported", code, ae)
	}
	if n := askRows(t, f.m.store); n != 0 {
		t.Fatalf("%d ask rows, want none", n)
	}
	time.Sleep(60 * time.Millisecond)
	if n := len(f.sender.calls()); n != 0 || countRelayOps(t, f) != 0 {
		t.Fatalf("a refused ask told the lead (%d) or made an op", n)
	}
	// a session that is no member keeps the old answer
	if code, _, ae := f.postAsk(rid(1001), "sid-nobody", 72); code != http.StatusConflict || ae.Error != team.ErrNotMember {
		t.Fatalf("non-member off: %d %+v, want 409 not_member", code, ae)
	}
	if code, _, ae := f.postAsk(rid(1002), "sid-1", 72); code != http.StatusConflict || ae.Error != team.ErrNotMember {
		t.Fatalf("the lead off: %d %+v, want 409 not_member", code, ae)
	}
	// back on: the ask opens
	f.memberAuto(true)
	if code, _, _ := f.postAsk(rid(1003), "sid-m1", 72); code != http.StatusOK {
		t.Fatalf("on again: %d", code)
	}
}

func TestMemberAuto_RemoteMemberAskIsRefusedBeforeTheForward(t *testing.T) {
	f, fc := remoteAskFixture(t)
	f.memberAuto(false)
	code, _, ae := f.postAsk(rid(1010), "sid-1", 71)
	if code != http.StatusConflict || ae.Error != team.ErrRelayUnsupported {
		t.Fatalf("remote off: %d %+v, want 409 relay_unsupported", code, ae)
	}
	if fc.capsAsked() != 0 {
		t.Fatalf("%d capability reads, want 0 (nothing is forwarded)", fc.capsAsked())
	}
	if askRows(t, f.m.store) != 0 || len(askFactsOf(t, f.m.store)) != 0 {
		t.Fatal("an ask row or fact was written")
	}
}

func TestMemberAuto_UsageNoticeStillArmsAndSendsAfterOffToOn(t *testing.T) {
	f := noticeFixture(t)
	f.usage.setStatus("tm-op-a", "idle")
	f.memberAuto(false)
	f.usage.setPct("sid-ma", 75)
	f.usageCheck()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices over the threshold while off, want 0", n)
	}
	// the arm state kept following the usage: drop under the threshold (arm), then over again, still off
	f.usage.setPct("sid-ma", 30)
	f.usageCheck()
	f.usage.setPct("sid-ma", 75)
	f.usageCheck()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices while off, want 0", n)
	}
	f.memberAuto(true)
	f.usageCheck()
	if calls := f.sender.calls(); len(calls) != 1 || calls[0].Text != wantNotice(f, 75) {
		t.Fatalf("after on again: %+v, want the one 70%% notice", calls)
	}
	f.usageCheck()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices, want once", n)
	}
}

func TestMemberAuto_UsageNoticeArmsWhileOff(t *testing.T) {
	f := noticeFixture(t)
	f.usage.setStatus("tm-op-a", "idle")
	// disarm first by a notice that went while on
	f.usage.setPct("sid-ma", 75)
	f.usageCheck()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("setup: %d notices, want 1", n)
	}
	f.memberAuto(false)
	f.usage.setPct("sid-ma", 30) // the arm branch runs even while off
	f.usageCheck()
	f.memberAuto(true)
	f.usage.setPct("sid-ma", 75)
	f.usageCheck()
	if n := len(f.sender.calls()); n != 2 {
		t.Fatalf("%d notices, want 2 (armed during the off stretch)", n)
	}
}

func TestMemberAuto_IdleNoticeSkippedWhileOffAndSentOnceAfterOn(t *testing.T) {
	f := idleSetup(t)
	f.memberAuto(false)
	f.advance(idleMin)
	f.idleCheck()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d idle notices while off, want 0", n)
	}
	f.memberAuto(true)
	f.idleCheck()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d idle notices after on again, want 1 (the claim was not spent)", n)
	}
	f.idleCheck()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d idle notices, want once per period", n)
	}
}

// An ask that was open before the switch went off: no retry of its notice, no unattended accept (tick and switch-on sweep).
func TestMemberAuto_OpenAskIsNotRetriedOrAcceptedWhileOff(t *testing.T) {
	f := gateFixture(t, false, true, 2)
	f.sendFails(errors.New("lead inbox down"))
	f.postAsk(rid(1020), "sid-m1", 72)
	time.Sleep(100 * time.Millisecond)
	f.sendFails(nil)
	f.forgetSent()
	f.memberAuto(false)
	f.livenessTick()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices retried while off, want 0", n)
	}
	f.switchTo(true) // the unattended switch-on sweep
	f.livenessTick() // and the tick
	if a := f.ask(rid(1020)); a.State != team.RelayAskOpen || countRelayOps(t, f) != 0 {
		t.Fatalf("ask = %+v, ops %d: accepted while off", a, countRelayOps(t, f))
	}
	f.switchTo(false)
	f.memberAuto(true)
	f.livenessTick()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices after on again, want the retried one", n)
	}
}

// ExpireRelayAsks never closes a final ask of a local member (it stays open for MarkFinalAsksUnanswered), so while off it simply
// stays open and unmarked; on again the ordinary rule marks it.
func TestMemberAuto_FinalAskIsNotMarkedWhileOff(t *testing.T) {
	f, a := finalAsked(t, 91)
	f.memberAuto(false)
	f.lapse(a)
	got := f.ask(a.ID)
	if got.State != team.RelayAskOpen || got.UnansweredAt != 0 {
		t.Fatalf("ask = %+v, want open and unmarked", got)
	}
	if _, ok := f.pauses()["sid-m1/relay_unanswered"]; ok {
		t.Fatalf("pauses = %+v, the member was paused while off", f.pauses())
	}
	f.memberAuto(true)
	f.livenessTick()
	if got := f.ask(a.ID); got.UnansweredAt == 0 {
		t.Fatalf("ask = %+v, want marked once on again", got)
	}
}

// The lead's own `pdx relay _<ref>` is a person's request: the switch does not touch it.
func TestMemberAuto_LeadCanStillRelayAMember(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("9")
	f.memberAuto(false)
	if code, _, ae := f.createRelay(rid(1030), "/tmp/10.sock", "_mem001"); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("lead relay while off: %d %+v", code, ae)
	}
}
