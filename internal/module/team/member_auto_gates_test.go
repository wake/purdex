package teammod

import (
	"net/http"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// Final gates (codex R1-1, A-1, A-2, A-3; spec member-relay-switch §2): every point that sends or writes reads the switch again
// right before the action, so a switch turned off after an earlier check still wins.

func (f *fixture) seedAsk(id string, pct int) RelayAsk {
	f.t.Helper()
	now := f.m.now()
	a, _, err := f.m.store.CreateRelayAsk(RelayAsk{ID: id, SessionID: "sid-m1", UsedPct: pct, Window: 1000000, CreatedAt: now, ExpiresAt: now + team.RelayAskHoldS*1000})
	if err != nil {
		f.t.Fatal(err)
	}
	return a
}

// The switch went off after the ask was created and before its notice was sent: nothing is sent, the ask stays unnotified, and
// it is sent once the switch is back on.
func TestMemberAuto_SendAskNoticeReadsTheSwitchAgainBeforeSending(t *testing.T) {
	f := gateFixture(t, false, true, 2)
	a := f.seedAsk(rid(1040), 72)
	f.memberAuto(false)
	f.m.sendAskNotice(a)
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices while off, want 0", n)
	}
	if got := f.ask(a.ID); got.NotifiedAt != 0 {
		t.Fatalf("ask = %+v, want unnotified", got)
	}
	f.memberAuto(true)
	f.m.sendAskNotice(a)
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices after on again, want 1", n)
	}
}

// A relay_ask fact from a member host is mirrored whatever the lead host's switch says, but the lead is not told while it is off.
func TestMemberAuto_MirrorOfARemoteAskIsAppliedButNotNotifiedWhileOff(t *testing.T) {
	f, _ := leadAskFixture(t)
	f.memberAuto(false)
	if code, body := f.postFact(leadPrincipal(), relayAskFact(factUUID1, "mk1", rid(1050), 71, 300)); code != http.StatusOK {
		t.Fatalf("fact: %d %s", code, body)
	}
	time.Sleep(100 * time.Millisecond)
	a := f.ask(rid(1050))
	if a.State != team.RelayAskOpen || a.NotifiedAt != 0 {
		t.Fatalf("mirror = %+v, want open and unnotified", a)
	}
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d messages to the lead while off, want 0", n)
	}
	f.memberAuto(true)
	f.livenessTick()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices after on again, want 1", n)
	}
}

// The switch goes off while the request waits for createMu (as an adopt would hold it): the ask is refused, nothing is written.
func TestMemberAuto_AskReadsTheSwitchUnderCreateMu(t *testing.T) {
	f := gateFixture(t, true, true, 2)
	f.m.createMu.Lock()
	type res struct {
		code int
		ae   team.APIError
	}
	done := make(chan res, 1)
	go func() {
		code, _, ae := f.postAsk(rid(1060), "sid-m1", 72)
		done <- res{code, ae}
	}()
	time.Sleep(150 * time.Millisecond) // the handler is parked on the lock, past any check outside it
	f.memberAuto(false)
	f.m.createMu.Unlock()
	r := <-done
	if r.code != http.StatusConflict || r.ae.Error != team.ErrRelayUnsupported {
		t.Fatalf("%d %+v, want 409 relay_unsupported", r.code, r.ae)
	}
	if n := askRows(t, f.m.store); n != 0 || countRelayOps(t, f) != 0 {
		t.Fatalf("%d ask rows, %d ops, want none", n, countRelayOps(t, f))
	}
}

func TestMemberAuto_RemoteAskReadsTheSwitchUnderCreateMu(t *testing.T) {
	f, _ := remoteAskFixture(t)
	f.m.createMu.Lock()
	done := make(chan int, 1)
	go func() {
		code, _, _ := f.postAsk(rid(1061), "sid-1", 71)
		done <- code
	}()
	time.Sleep(300 * time.Millisecond)
	f.memberAuto(false)
	f.m.createMu.Unlock()
	if code := <-done; code != http.StatusConflict {
		t.Fatalf("%d, want 409", code)
	}
	if askRows(t, f.m.store) != 0 || len(askFactsOf(t, f.m.store)) != 0 {
		t.Fatal("an ask row or fact was written")
	}
}

func TestMemberAuto_AcceptAskUnattendedFailsClosedWhileOff(t *testing.T) {
	f := gateFixture(t, true, true, 2)
	a := f.seedAsk(rid(1070), 72)
	f.memberAuto(false)
	if f.m.acceptAskUnattended(a) || countRelayOps(t, f) != 0 {
		t.Fatal("accepted while off")
	}
	f.memberAuto(true)
	if !f.m.acceptAskUnattended(a) {
		t.Fatal("not accepted once on again")
	}
}

// The 70 % notice was claimed and scheduled while on; the switch went off before the goroutine sent: nothing is sent and the
// member is armed again, so the next round after on sends once.
func TestMemberAuto_ScheduledUsageNoticeIsRearmedWhenTheSwitchWentOff(t *testing.T) {
	f := noticeFixture(t)
	f.usage.setStatus("tm-op-a", "idle")
	f.usage.setPct("sid-ma", 75)
	members, err := f.m.store.ActiveMembersOfLiveTeams()
	if err != nil || len(members) != 1 {
		t.Fatalf("members %v %v", members, err)
	}
	if won, err := f.m.store.DisarmNotice(members[0].SpawnOp, "sid-ma"); err != nil || !won {
		t.Fatalf("claim: %v %v", won, err)
	}
	f.memberAuto(false)
	f.m.usageNotice(members[0], 75)
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices, want 0", n)
	}
	f.memberAuto(true)
	f.usageCheck()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices after on again, want 1 (rearmed)", n)
	}
}

func TestMemberAuto_ScheduledIdleNoticeIsReleasedWhenTheSwitchWentOff(t *testing.T) {
	f := idleSetup(t)
	members, err := f.m.store.ActiveMembersOfLiveTeams()
	if err != nil || len(members) != 1 {
		t.Fatalf("members %v %v", members, err)
	}
	gen, won := f.m.claimIdleTold("sid-ma")
	if !won {
		t.Fatal("claim failed")
	}
	f.memberAuto(false)
	f.m.idleNotice(members[0], 55, 40, gen)
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices, want 0", n)
	}
	f.memberAuto(true)
	f.advance(idleMin)
	f.idleCheck()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices after on again, want 1 (released)", n)
	}
}
