package teammod

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// MR-4 (member relay spec v2 D6, D8, §3.2, §3.5, §3.6): a remote member's 70% ask reaches its lead. M reads the lead host's
// capabilities first and writes nothing without `relay_ask`; the ask and the fact are one transaction; the pump drops a fact
// whose ask is no longer open. L mirrors the ask by M's ask id and tells the lead `pdx relay <alias>/_<ref>`.

const askFactUUID = "a2222222-2222-4222-8222-222222222222"

// remoteAskFixture is M's side: a remote member of host-L whose capabilities announce relay_ask, a facts pump nobody runs.
func remoteAskFixture(t *testing.T, kinds ...string) (*fixture, *fakeHostCaller) {
	t.Helper()
	f := newFixture(t)
	if len(kinds) == 0 {
		kinds = []string{team.FactEnded, team.FactRelayAsk}
	}
	fc := &fakeHostCaller{caps: map[string]ipeers.TeamCaps{"host-L": {FactKinds: kinds}}, script: doneFor("host-L")}
	f.m.cmdCaller = fc
	f.m.factPump = newOutboxPump("facts", fc, f.m.newFactOutbox(), f.m.now, f.m.logf, f.m.stopCtx, &f.m.sweepWG)
	seedRemote(t, f.m.store, "mk-1", "sid-1", f.clock.Load())
	return f, fc
}

func askFactsOf(t *testing.T, s *Store) []team.TeamFact {
	t.Helper()
	var out []team.TeamFact
	for _, fr := range factsOf(t, s, "host-L") {
		if fr.Kind != team.FactRelayAsk {
			continue
		}
		var f team.TeamFact
		if err := json.Unmarshal([]byte(fr.BodyJSON), &f); err != nil {
			t.Fatal(err)
		}
		out = append(out, f)
	}
	return out
}

func askRows(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM relay_asks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The ask and its fact are one transaction, after the capability read. Mutations: no remote branch (not_member) → red;
// the fact without expires_in_s / spawn_op not the mk → red.
func TestRemoteAsk_WritesTheAskAndTheFactTogether(t *testing.T) {
	f, fc := remoteAskFixture(t)
	code, resp, _ := f.postAsk(rid(700), "sid-1", 71)
	if code != http.StatusOK || resp.ID != rid(700) || resp.State != team.RelayAskOpen || resp.Replay {
		t.Fatalf("ask: %d %+v", code, resp)
	}
	if fc.capsAsked() != 1 {
		t.Fatalf("%d capability reads, want 1", fc.capsAsked())
	}
	a := f.ask(rid(700))
	if a.SpawnOp != "mk-1" || a.TeamID != "team-L" || a.SessionID != "sid-1" || a.UsedPct != 71 {
		t.Fatalf("ask row = %+v", a)
	}
	got := askFactsOf(t, f.m.store)
	if len(got) != 1 || got[0].AskID != rid(700) || got[0].UsedPct != 71 || got[0].Window != 1000000 || got[0].ExpiresInS != team.RelayAskHoldS || got[0].MK != "mk-1" || got[0].TeamID != "team-L" {
		t.Fatalf("facts = %+v", got)
	}
}

// Not announced, unreachable, or no caller at all: relay_unsupported and NOTHING written — no ask row, no fact. Mutation:
// write first and read after → the row is there (red).
func TestRemoteAsk_WithoutTheAnnouncementNothingIsWritten(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(f *fixture, fc *fakeHostCaller)
	}{
		{"not announced", func(f *fixture, fc *fakeHostCaller) {
			fc.caps["host-L"] = ipeers.TeamCaps{FactKinds: []string{team.FactEnded}}
		}},
		{"unreachable", func(f *fixture, fc *fakeHostCaller) { fc.capsErr = http.ErrHandlerTimeout }},
		{"no caller", func(f *fixture, fc *fakeHostCaller) { f.m.cmdCaller = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, fc := remoteAskFixture(t)
			tc.edit(f, fc)
			code, _, ae := f.postAsk(rid(701), "sid-1", 71)
			if code != http.StatusConflict || ae.Error != team.ErrRelayUnsupported {
				t.Fatalf("ask: %d %+v, want 409 %s", code, ae, team.ErrRelayUnsupported)
			}
			if n := askRows(t, f.m.store); n != 0 {
				t.Fatalf("%d ask rows written", n)
			}
			if got := askFactsOf(t, f.m.store); len(got) != 0 {
				t.Fatalf("facts written: %+v", got)
			}
		})
	}
}

// A replay (the same request, or another one while the ask is open) answers the stored ask and queues no second fact.
func TestRemoteAsk_AReplayQueuesNoSecondFact(t *testing.T) {
	f, _ := remoteAskFixture(t)
	f.postAsk(rid(702), "sid-1", 71)
	for _, id := range []string{rid(702), rid(703)} {
		code, resp, _ := f.postAsk(id, "sid-1", 72)
		if code != http.StatusOK || resp.ID != rid(702) || !resp.Replay {
			t.Fatalf("replay %s: %d %+v", id, code, resp)
		}
	}
	if got := askFactsOf(t, f.m.store); len(got) != 1 {
		t.Fatalf("%d facts, want 1", len(got))
	}
}

// An open op for the member refuses the ask (relay_open), as for a local one.
func TestRemoteAsk_AnOpenOpRefusesIt(t *testing.T) {
	f, _ := remoteAskFixture(t)
	op := team.RelayOp{ID: "op-open", Kind: team.RelayKindMember, HostID: "h:1", SessionID: "sid-1", Ref: "_rmk-1", TeamID: "team-L", State: team.RelayRequested, CreatedAt: 1000, UpdatedAt: 1000}
	if err := f.m.store.CreateRelayOp(op); err != nil {
		t.Fatal(err)
	}
	code, _, ae := f.postAsk(rid(704), "sid-1", 71)
	if code != http.StatusConflict || ae.Error != team.ErrRelayOpen {
		t.Fatalf("ask: %d %+v, want 409 %s", code, ae, team.ErrRelayOpen)
	}
	if n := askRows(t, f.m.store); n != 0 {
		t.Fatalf("%d ask rows", n)
	}
}

// The sweeper's member_left withdrawal looks at the remote row too: an ask of an active remote member stays open, a released
// one is withdrawn. Mutation: the old query (team_members only) → the remote ask is withdrawn at the first sweep (red).
func TestRemoteAsk_TheSweeperKeepsTheAskOfAnActiveRemoteMember(t *testing.T) {
	f, _ := remoteAskFixture(t)
	f.postAsk(rid(705), "sid-1", 71)
	if n, err := f.m.store.WithdrawAsksOfInactiveMembers(f.clock.Load()); err != nil || n != 0 {
		t.Fatalf("withdrawn %d (%v), want 0 for an active remote member", n, err)
	}
	if _, err := casRemoteMemberStateIn(f.m.store.db, "mk-1", []string{remoteActive}, remoteReleased, f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	if n, err := f.m.store.WithdrawAsksOfInactiveMembers(f.clock.Load()); err != nil || n != 1 {
		t.Fatalf("withdrawn %d (%v), want 1 once released", n, err)
	}
}

// §3.6: the pump sends an open ask's fact, and drops it (no call) once the ask is accepted, expired or withdrawn. Mutation:
// send it anyway → a call is made (red).
func TestRemoteAskPump_DropsAFactWhoseAskIsNoLongerOpen(t *testing.T) {
	for _, tc := range []struct {
		name  string
		close func(f *fixture)
	}{
		{"accepted", func(f *fixture) {
			f.m.store.db.Exec(`UPDATE relay_asks SET state = 'accepted', closed_at = 1 WHERE id = ?`, rid(706))
		}},
		{"withdrawn", func(f *fixture) {
			f.m.store.db.Exec(`UPDATE relay_asks SET state = 'withdrawn', closed_at = 1 WHERE id = ?`, rid(706))
		}},
		{"expired", func(f *fixture) {
			f.m.store.db.Exec(`UPDATE relay_asks SET state = 'expired', closed_at = 1 WHERE id = ?`, rid(706))
		}},
		{"window passed", func(f *fixture) { f.clock.Add(team.RelayAskHoldS*1000 + 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, fc := remoteAskFixture(t)
			f.postAsk(rid(706), "sid-1", 71)
			tc.close(f)
			f.m.factPump.drain("host-L")
			if calls := fc.sent(); len(calls) != 0 {
				t.Fatalf("calls = %d, want none for a closed ask", len(calls))
			}
			if st := factsOf(t, f.m.store, "host-L"); len(st) != 1 || st[0].State != factDropped {
				t.Fatalf("fact = %+v, want dropped", st)
			}
		})
	}
	f, fc := remoteAskFixture(t)
	f.postAsk(rid(707), "sid-1", 71)
	f.m.factPump.drain("host-L")
	if calls := fc.sent(); len(calls) != 1 {
		t.Fatalf("an open ask's fact: %d calls, want 1", len(calls))
	}
}

// D8: relay_ask is dropped, not held, when the lead host's fresh capabilities lack it (a downgrade after the read), so it never
// holds the host's FIFO; unreachable waits. Mutation: not in dropIfUnannounced → held (red).
func TestRemoteAskPump_ADowngradedLeadHostDropsItAndNeverHoldsTheQueue(t *testing.T) {
	f, fc := remoteAskFixture(t)
	f.postAsk(rid(708), "sid-1", 71)
	fc.caps["host-L"] = ipeers.TeamCaps{FactKinds: []string{team.FactEnded}}
	f.m.factPump.drain("host-L")
	if len(fc.sent()) != 0 {
		t.Fatal("a fact the host does not announce was sent")
	}
	if st := factsOf(t, f.m.store, "host-L"); len(st) != 1 || st[0].State != factDropped {
		t.Fatalf("fact = %+v, want dropped", st)
	}
	if !dropIfUnannounced(team.FactRelayAsk) {
		t.Fatal("relay_ask is not a drop-if-unannounced kind")
	}
}

// ---- L: the mirror ----

func relayAskFact(id, mk, ask string, pct, expiresInS int) team.TeamFact {
	return team.TeamFact{ID: id, Kind: team.FactRelayAsk, ToHostID: "h:1", TeamID: uid(1), MK: mk, AskID: ask, UsedPct: pct, Window: 200000, ExpiresInS: expiresInS}
}

func leadAskFixture(t *testing.T) (*fixture, *fakeHostCaller) {
	t.Helper()
	f, fc := factsFixture(t)
	f.setLeadHost(true)
	fc.aliases = map[string]string{"lead": "lead:1"}
	f.m.cmdCaller = fc
	f.approveLead(uid(1))
	f.remoteRow("abc12", "lead:1", "mk1", rowActive)
	return f, fc
}

// The mirror: id = M's ask id, spawn_op = the row's, session = the member's on M, expiry = receipt + min(expires_in_s, 300).
// Mutations: id from the fact id → red; no cap → the 9999 case is red; expiry from anything but receipt → red.
func TestRelayAskFact_MirrorsTheAskByTheMembersAskID(t *testing.T) {
	for _, tc := range []struct{ in, wantS int }{{300, 300}, {60, 60}, {9999, 300}} {
		f, _ := leadAskFixture(t)
		now := f.clock.Load()
		if code, body := f.postFact(leadPrincipal(), relayAskFact(factUUID1, "mk1", rid(800), 71, tc.in)); code != http.StatusOK {
			t.Fatalf("fact: %d %s", code, body)
		}
		a := f.ask(rid(800))
		if a.SpawnOp != "abc12" || a.SessionID != "sid-abc12" || a.TeamID != uid(1) || a.State != team.RelayAskOpen || a.UsedPct != 71 || a.ExpiresAt != now+int64(tc.wantS)*1000 {
			t.Fatalf("expires_in_s %d: mirror = %+v (now %d)", tc.in, a, now)
		}
	}
}

// A second fact for the same ask (another fact id) makes neither a second mirror nor a second notice.
func TestRelayAskFact_ASecondFactForTheSameAskIsIgnored(t *testing.T) {
	f, _ := leadAskFixture(t)
	f.postFact(leadPrincipal(), relayAskFact(factUUID1, "mk1", rid(801), 71, 300))
	time.Sleep(100 * time.Millisecond)
	code, body := f.postFact(leadPrincipal(), relayAskFact(askFactUUID, "mk1", rid(801), 72, 300))
	if code != http.StatusOK || !strings.Contains(string(body), team.FactIgnored) {
		t.Fatalf("second fact: %d %s, want ignored", code, body)
	}
	time.Sleep(100 * time.Millisecond)
	var n int
	f.m.store.db.QueryRow(`SELECT COUNT(*) FROM relay_asks`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d mirrors, want 1", n)
	}
	if got := len(f.sender.calls()); got != 1 {
		t.Fatalf("%d notices, want 1", got)
	}
}

// Binding: the member row of THIS host, this team, this mk, active. Mutations: one per predicate → a mirror appears (red).
func TestRelayAskFact_BindingPredicates(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(f *fixture, fact *team.TeamFact)
	}{
		{"another mk", func(f *fixture, fact *team.TeamFact) { fact.MK = "mk-x" }},
		{"another team", func(f *fixture, fact *team.TeamFact) { fact.TeamID = uid(2) }},
		{"a released row", func(f *fixture, fact *team.TeamFact) {
			f.m.store.db.Exec(`UPDATE team_members SET state = 'released' WHERE mk = 'mk1'`)
		}},
		{"a local row", func(f *fixture, fact *team.TeamFact) {
			f.m.store.db.Exec(`UPDATE team_members SET host_id = ? WHERE mk = 'mk1'`, f.m.store.localHostID)
		}},
		{"another host's row", func(f *fixture, fact *team.TeamFact) {
			f.m.store.db.Exec(`UPDATE team_members SET host_id = 'other:9' WHERE mk = 'mk1'`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := leadAskFixture(t)
			fact := relayAskFact(factUUID1, "mk1", rid(802), 71, 300)
			tc.edit(f, &fact)
			f.postFact(leadPrincipal(), fact)
			var n int
			f.m.store.db.QueryRow(`SELECT COUNT(*) FROM relay_asks`).Scan(&n)
			if n != 0 {
				t.Fatalf("%d mirrors written", n)
			}
		})
	}
}

// Shape: an ask id, a percentage 0-100 and a positive window in seconds.
func TestRelayAskFact_ShapeIsValidated(t *testing.T) {
	f, _ := leadAskFixture(t)
	for _, fact := range []team.TeamFact{
		relayAskFact(factUUID1, "mk1", "", 71, 300),
		relayAskFact(factUUID1, "mk1", rid(803), 101, 300),
		relayAskFact(factUUID1, "mk1", rid(803), 71, 0),
		relayAskFact(factUUID1, "", rid(803), 71, 300),
	} {
		if code, body := f.postFact(leadPrincipal(), fact); code != http.StatusBadRequest {
			t.Fatalf("%+v: %d %s, want 400", fact, code, body)
		}
	}
}

// The lead hears of the mirror as `pdx relay <alias>/_<ref>` (the same words as a local ask), once; if it cannot be sent the
// sweeper sends it again. Mutations: the local ref form → red; no retry → red.
func TestRelayAskFact_TheLeadIsToldWithTheRemoteRefAndTheSweeperRetries(t *testing.T) {
	f, _ := leadAskFixture(t)
	f.sendFails(http.ErrHandlerTimeout)
	f.postFact(leadPrincipal(), relayAskFact(factUUID1, "mk1", rid(804), 71, 300))
	time.Sleep(100 * time.Millisecond)
	if a := f.ask(rid(804)); a.NotifiedAt != 0 {
		t.Fatalf("notified though the send failed: %+v", a)
	}
	f.sendFails(nil)
	f.livenessTick()
	calls := f.sender.calls()
	if len(calls) == 0 {
		t.Fatal("no notice after the sweeper's retry")
	}
	last := calls[len(calls)-1].Text
	if !strings.Contains(last, "pdx relay lead/_rabc12") || !strings.Contains(last, "已用 71%") || strings.Contains(last, "pdx relay _") {
		t.Fatalf("notice = %q", last)
	}
	if a := f.ask(rid(804)); a.NotifiedAt == 0 {
		t.Fatal("not marked notified after the retry")
	}
}

// The mirror's sweep: it expires with its window and is withdrawn when the member row is gone, like a local ask.
func TestRelayAskFact_TheMirrorExpiresAndIsWithdrawnLikeALocalAsk(t *testing.T) {
	f, _ := leadAskFixture(t)
	f.postFact(leadPrincipal(), relayAskFact(factUUID1, "mk1", rid(805), 71, 60))
	if n, _ := f.m.store.WithdrawAsksOfInactiveMembers(f.clock.Load()); n != 0 {
		t.Fatalf("an active remote row's mirror was withdrawn (%d)", n)
	}
	f.clock.Add(61_000)
	if n, _ := f.m.store.ExpireRelayAsks(f.clock.Load()); n != 1 {
		t.Fatalf("expired %d, want 1", n)
	}
}
