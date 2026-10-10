package teammod

import (
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// The 70% idle notice never goes to a member that asks for itself (spec 2026-10-10-member-relay-ask §3.4, D11): its mod
// speaks protocol >= 3, or an ask is open. Older mods keep the notice.

func (f *fixture) helloAs(sid, ver string) {
	f.t.Helper()
	f.liveMember(sid, "_memop-a", "worker", "self/w-a", "tm-op-a")
	if code, body := f.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{SessionID: sid, ModVersion: ver, Agent: "cc"}); code != http.StatusOK {
		f.t.Fatalf("hello: %d %s", code, body)
	}
}

func (f *fixture) idleAt(pct float64) {
	f.usage.setPct("sid-ma", pct)
	f.usage.setStatus("tm-op-a", "idle")
}

// Mutation gate: drop the protocol check in noticeUsage → the v3 member is told (red).
func TestNotice70_AMemberThatAsksForItselfGetsNoOldNotice(t *testing.T) {
	f := noticeFixture(t)
	f.helloAs("sid-ma", "3")
	f.idleAt(80)
	f.usageCheck()
	f.usageCheck()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d old notices for a protocol 3 member", n)
	}
}

func TestNotice70_AnOlderModKeepsTheOldNotice(t *testing.T) {
	f := noticeFixture(t)
	f.helloAs("sid-ma", "2")
	f.idleAt(80)
	f.usageCheck()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d old notices for a protocol 2 member, want 1", n)
	}
}

// The hello is persisted: after a restart (modSeen reloaded from team.db) the member is still known to speak protocol 3.
func TestNotice70_ThePersistedHelloStillSuppressesAfterARestart(t *testing.T) {
	f := noticeFixture(t)
	f.helloAs("sid-ma", "3")
	f.m.mu.Lock()
	f.m.modSeen = map[string]helloInfo{} // the restart forgot the memory
	f.m.mu.Unlock()
	seen, err := f.m.store.LoadModHello(modSeenCap)
	if err != nil {
		t.Fatal(err)
	}
	f.m.mu.Lock()
	for sid, h := range seen {
		f.m.modSeen[sid] = h
	}
	f.m.mu.Unlock()
	f.idleAt(80)
	f.usageCheck()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d old notices after a restart", n)
	}
}

// DisarmNotice refuses while the session has an open ask, as it does for an open relay op. Mutation gate: drop the
// clause → the disarm wins (red).
func TestDisarmNotice_RefusedWhileAnAskIsOpen(t *testing.T) {
	f := noticeFixture(t)
	if _, _, err := f.m.store.CreateRelayAsk(RelayAsk{ID: rid(700), SessionID: "sid-ma", UsedPct: 71, CreatedAt: f.clock.Load(), ExpiresAt: f.clock.Load() + 300_000}); err != nil {
		t.Fatal(err)
	}
	if won, err := f.m.store.DisarmNotice("op-a", "sid-ma"); err != nil || won {
		t.Fatalf("disarm with an open ask: won=%v err=%v", won, err)
	}
	if _, err := f.m.store.ExpireRelayAsks(f.clock.Load() + 300_000); err != nil {
		t.Fatal(err)
	}
	if won, err := f.m.store.DisarmNotice("op-a", "sid-ma"); err != nil || !won {
		t.Fatalf("disarm once the ask is closed: won=%v err=%v", won, err)
	}
}

// A notice already queued is dropped, and the member armed again, when a protocol 3 hello or an ask landed after the
// check that disarmed it. Mutation gate: drop the protocol / ask check from usageNotice's re-check → red.
func TestNotice70_AQueuedNoticeIsDroppedWhenAnAskOrAV3HelloLandedFirst(t *testing.T) {
	for name, land := range map[string]func(f *fixture){
		"a v3 hello": func(f *fixture) { f.helloAs("sid-ma", "3") },
		"an open ask": func(f *fixture) {
			if _, _, err := f.m.store.CreateRelayAsk(RelayAsk{ID: rid(710), SessionID: "sid-ma", UsedPct: 71, CreatedAt: f.clock.Load(), ExpiresAt: f.clock.Load() + 300_000}); err != nil {
				t.Fatal(err)
			}
		},
	} {
		f := noticeFixture(t)
		f.idleAt(80)
		rows, err := f.m.store.ActiveMembersOfLiveTeams()
		if err != nil || len(rows) != 1 {
			t.Fatalf("%s: members = %v, %v", name, rows, err)
		}
		if won, err := f.m.store.DisarmNotice("op-a", "sid-ma"); err != nil || !won { // the check that queued the notice
			t.Fatalf("%s: disarm = %v, %v", name, won, err)
		}
		land(f)
		f.m.usageNotice(rows[0], 80)
		if n := len(f.sender.calls()); n != 0 {
			t.Fatalf("%s: %d notices sent, want 0", name, n)
		}
		var armed int
		f.m.store.db.QueryRow(`SELECT notice_armed FROM team_members WHERE spawn_op = 'op-a'`).Scan(&armed)
		if armed != 1 {
			t.Fatalf("%s: the member was not armed again", name)
		}
	}
}
