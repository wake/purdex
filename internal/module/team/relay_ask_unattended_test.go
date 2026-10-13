package teammod

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// Unattended accepts a member's ask (RT-2b T22, S-4): against the lead's pool, or a quota pause for the member at 0.

func wantAutoAcceptNotice(pct, left int) string {
	return fmt.Sprintf(team.UnattendedAskAcceptedFmt, "self/w-one", "worker", pct, left)
}

func askNotices(f *fixture) (ask, auto int) {
	for _, c := range f.sender.calls() {
		switch c.Text {
		case wantAskNotice(72, 5):
			ask++
		case wantAutoAcceptNotice(72, 1):
			auto++
		}
	}
	return
}

// Mutation gates: leave the ask to the lead → no op (red); skip the spend → pool stays 2 (red).
func TestRelayAsk_UnattendedAcceptsAgainstThePool(t *testing.T) {
	f := gateFixture(t, true, true, 2)
	code, resp, _ := f.postAsk(rid(700), "sid-m1", 72)
	if code != http.StatusOK {
		t.Fatalf("ask: %d", code)
	}
	time.Sleep(100 * time.Millisecond)
	a := f.ask(rid(700))
	if a.State != team.RelayAskAccepted || a.OpID == "" || resp.State != team.RelayAskAccepted {
		t.Fatalf("ask = %+v (response %+v), want accepted with an op", a, resp)
	}
	op, ok, err := f.m.store.GetRelayOp(a.OpID)
	if err != nil || !ok || op.State != team.RelayRequested || op.SessionID != "sid-m1" {
		t.Fatalf("op = %+v ok=%v err=%v", op, ok, err)
	}
	if got := f.poolLeft("sid-1"); got != 1 {
		t.Fatalf("pool = %d, want 1", got)
	}
	if askN, autoN := askNotices(f); askN != 0 || autoN != 1 {
		t.Fatalf("notices = %+v, want only the auto-accept one", f.sender.calls())
	}
}

// Mutation gate: accept without the gate's spend-or-pause → the member is relayed on an empty pool (red).
func TestRelayAsk_UnattendedWithAnEmptyPoolPausesTheMember(t *testing.T) {
	f := gateFixture(t, true, true, 0)
	if code, _, _ := f.postAsk(rid(710), "sid-m1", 72); code != http.StatusOK {
		t.Fatalf("ask: %d", code)
	}
	time.Sleep(100 * time.Millisecond)
	if _, ok := f.pauses()["sid-m1/quota"]; !ok {
		t.Fatalf("pauses = %+v, want the member paused for quota", f.pauses())
	}
	if n := countRelayOps(t, f); n != 0 {
		t.Fatalf("%d relay ops, want none", n)
	}
	if a := f.ask(rid(710)); a.State == team.RelayAskOpen {
		t.Fatalf("ask still open after the member was paused: %+v", a)
	}
	if askN, _ := askNotices(f); askN != 0 {
		t.Fatalf("the lead was asked to decide for a member that is paused: %+v", f.sender.calls())
	}
}

// Switch-on accepts the asks already open. Mutation gate: skip the ask sweep → still open (red).
func TestRelayAsk_SwitchOnAcceptsTheOpenAsks(t *testing.T) {
	f := gateFixture(t, false, true, 2)
	f.postAsk(rid(720), "sid-m1", 72)
	time.Sleep(100 * time.Millisecond)
	if a := f.ask(rid(720)); a.State != team.RelayAskOpen {
		t.Fatalf("attended ask = %+v, want open", a)
	}
	f.switchTo(true)
	if a := f.ask(rid(720)); a.State != team.RelayAskAccepted || a.OpID == "" {
		t.Fatalf("after switch-on: %+v, want accepted", a)
	}
	if got := f.poolLeft("sid-1"); got != 1 {
		t.Fatalf("pool = %d, want 1", got)
	}
}

// Off: only the lead's notice, as today. Mutation gate: accept regardless of the switch → red.
func TestRelayAsk_AttendedLeavesItToTheLead(t *testing.T) {
	f := gateFixture(t, false, true, 2)
	f.postAsk(rid(730), "sid-m1", 72)
	time.Sleep(100 * time.Millisecond)
	if a := f.ask(rid(730)); a.State != team.RelayAskOpen || countRelayOps(t, f) != 0 || f.poolLeft("sid-1") != 2 {
		t.Fatalf("ask = %+v ops=%d pool=%d", a, countRelayOps(t, f), f.poolLeft("sid-1"))
	}
	if askN, autoN := askNotices(f); askN != 1 || autoN != 0 {
		t.Fatalf("notices = %+v", f.sender.calls())
	}
}

// A remote member's mirror ask on the lead host is accepted by the next liveness tick while the switch is on (RT-2b T22).
// Mutation gate: skip the tick's accept → the mirror stays open (red).
func TestRelayAskFact_UnattendedAcceptsTheMirrorOnTheTick(t *testing.T) {
	f, fc := leadAskFixture(t)
	fc.caps["lead:1"] = ipeers.TeamCaps{Kinds: append(append([]string{}, allKinds...), CmdRelay), AllowTeam: true, FactKinds: []string{team.FactRelayAsk}}
	f.setPool("sid-1", 2)
	f.setQuota("sid-1", 5)
	f.unatt.set(true)
	f.qrule.set(true, nil)
	if code, body := f.postFact(leadPrincipal(), relayAskFact(factUUID1, "mk1", rid(810), 71, 300)); code != http.StatusOK {
		t.Fatalf("fact: %d %s", code, body)
	}
	f.livenessTick()
	a := f.ask(rid(810))
	if a.State != team.RelayAskAccepted || a.OpID == "" {
		t.Fatalf("mirror = %+v, want accepted by the tick", a)
	}
	if got := f.poolLeft("sid-1"); got != 1 {
		t.Fatalf("pool = %d, want 1", got)
	}
}
