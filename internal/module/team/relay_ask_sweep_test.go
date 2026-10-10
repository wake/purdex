package teammod

import (
	"net/http"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// Expiry, withdrawal and compaction (spec 2026-10-10-member-relay-ask §3.3): the sweeper closes an ask after five
// minutes, after the tick's own team-end and gone-member settlement; a compaction withdraws it and sends only today's
// compaction notice. Nobody is told about an expiry or a withdrawal.

// askedFixture is a member team (protocol 3) whose member has an open ask, with its notice delivered and forgotten.
func askedFixture(t *testing.T) (*fixture, RelayAsk) {
	t.Helper()
	f := newFixture(t)
	f.memberTeam("3")
	if code, _, _ := f.postAsk(rid(800), "sid-m1", 71); code != http.StatusOK {
		t.Fatalf("ask: %d", code)
	}
	time.Sleep(100 * time.Millisecond)
	f.forgetSent()
	return f, f.ask(rid(800))
}

func (f *fixture) forgetSent() {
	f.sender.mu.Lock()
	f.sender.sent = nil
	f.sender.mu.Unlock()
}

func TestRelayAsk_ExpiresAfterFiveMinutesAndTellsNobody(t *testing.T) {
	f, a := askedFixture(t)
	f.clock.Store(a.CreatedAt + 299_999)
	f.livenessTick()
	if got := f.ask(a.ID); got.State != team.RelayAskOpen {
		t.Fatalf("one ms before the deadline: %+v", got)
	}
	f.clock.Store(a.CreatedAt + 300_000)
	f.livenessTick()
	if got := f.ask(a.ID); got.State != team.RelayAskExpired || got.ClosedAt != a.CreatedAt+300_000 {
		t.Fatalf("at the deadline: %+v", got)
	}
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("an expiry sent %d messages", n)
	}
}

// Each way a member stops being an active member of a live team withdraws its ask as member_left.
func TestRelayAsk_WithdrawnWhenTheMemberLeaves(t *testing.T) {
	for name, leave := range map[string]func(f *fixture){
		"released": func(f *fixture) {
			if ok, err := f.m.store.ReleaseMember("op-m1", "sid-m1", f.clock.Load()); err != nil || !ok {
				t.Fatalf("release: %v %v", ok, err)
			}
		},
		"killing": func(f *fixture) {
			if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'killing' WHERE spawn_op = 'op-m1'`); err != nil {
				t.Fatal(err)
			}
		},
		"killed": func(f *fixture) {
			if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'killed' WHERE spawn_op = 'op-m1'`); err != nil {
				t.Fatal(err)
			}
		},
		"gone": func(f *fixture) {
			if ok, err := f.m.store.MarkMemberGone("op-m1", "sid-m1", f.clock.Load()); err != nil || !ok {
				t.Fatalf("gone: %v %v", ok, err)
			}
		},
		"team ended": func(f *fixture) {
			if _, err := f.m.store.db.Exec(`UPDATE teams SET ended_at = ? WHERE id = ?`, f.clock.Load(), uid(1)); err != nil {
				t.Fatal(err)
			}
		},
	} {
		f, a := askedFixture(t)
		leave(f)
		f.livenessTick()
		if got := f.ask(a.ID); got.State != team.RelayAskWithdrawn || got.Reason != team.RelayAskWithdrawMemberLeft {
			t.Errorf("%s: %+v", name, got)
		}
		if n := len(f.sender.calls()); n != 0 {
			t.Errorf("%s: a withdrawal sent %d messages", name, n)
		}
	}
}

// The ask sweep runs after the tick's own gone-member settlement: a member whose process vanished is marked gone and
// its ask withdrawn in the SAME tick. Mutation gate: run the ask sweep before markGoneMembers → still open (red).
func TestRelayAsk_TheSweepRunsAfterTheGoneMemberSettlement(t *testing.T) {
	f, a := askedFixture(t)
	f.origins.markDead("sid-m1")
	f.livenessTick()
	if got := f.ask(a.ID); got.State != team.RelayAskWithdrawn || got.Reason != team.RelayAskWithdrawMemberLeft {
		t.Fatalf("ask after one tick: %+v", got)
	}
}

// Release against `pdx relay`, both orders. The member leaves first → the relay is refused and the ask is withdrawn at
// the next tick; the relay commits first → the ask is accepted and the release finds the open op and is refused.
func TestRelayAsk_ReleaseVersusRelayBothOrders(t *testing.T) {
	f := gateFixture(t, false, false, 0)
	f.openAsk(rid(810))
	if ok, err := f.m.store.ReleaseMember("op-m1", "sid-m1", f.clock.Load()); err != nil || !ok {
		t.Fatalf("release: %v %v", ok, err)
	}
	if code, _, ae := f.createRelay(rid(811), "/tmp/10.sock", "_mem001"); code != 409 {
		t.Fatalf("relay after the release: %d %+v", code, ae)
	}
	if a := f.ask(rid(810)); a.State != team.RelayAskOpen || a.OpID != "" {
		t.Fatalf("a refused relay touched the ask: %+v", a)
	}
	f.livenessTick()
	if a := f.ask(rid(810)); a.State != team.RelayAskWithdrawn || a.Reason != team.RelayAskWithdrawMemberLeft {
		t.Fatalf("after the tick: %+v", a)
	}

	g := gateFixture(t, false, false, 0)
	g.openAsk(rid(820))
	code, op, _ := g.createRelay(rid(821), "/tmp/10.sock", "_mem001")
	if code != 201 {
		t.Fatalf("relay: %d", code)
	}
	if ok, err := g.m.store.ReleaseMember("op-m1", "sid-m1", g.clock.Load()); err != nil || ok {
		t.Fatalf("release with the op open: ok=%v err=%v", ok, err)
	}
	if a := g.ask(rid(820)); a.State != team.RelayAskAccepted || a.OpID != op.ID {
		t.Fatalf("ask: %+v", a)
	}
	g.livenessTick()
	if a := g.ask(rid(820)); a.State != team.RelayAskAccepted {
		t.Fatalf("a tick changed an accepted ask: %+v", a)
	}
}

// An auto compaction withdraws the ask and the compaction notice is the only message. A manual one leaves the ask.
func TestRelayAsk_AutoCompactionWithdrawsAndSendsOnlyTheCompactionNotice(t *testing.T) {
	f, a := askedFixture(t)
	if code, res := f.compacted("sid-m1", "manual"); code != 200 || res.Noticed {
		t.Fatalf("manual: %d %+v", code, res)
	}
	if got := f.ask(a.ID); got.State != team.RelayAskOpen {
		t.Fatalf("a manual compaction withdrew the ask: %+v", got)
	}
	if code, res := f.compacted("sid-m1", "auto"); code != 200 || !res.Noticed {
		t.Fatalf("auto: %d %+v", code, res)
	}
	waitFor(t, func() bool { return len(f.sender.calls()) == 1 })
	time.Sleep(100 * time.Millisecond)
	calls := f.sender.calls()
	if len(calls) != 1 || calls[0].Text != compactedNotice("_mem001") {
		t.Fatalf("messages = %+v, want only the compaction notice", calls)
	}
	if got := f.ask(a.ID); got.State != team.RelayAskWithdrawn || got.Reason != team.RelayAskWithdrawCompacted {
		t.Fatalf("ask: %+v", got)
	}
}
