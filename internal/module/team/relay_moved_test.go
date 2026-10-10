package teammod

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// Member relay MR-2 (docs/specs/2026-10-10-member-relay-manual-and-cross-host-spec-plan.md D4, D7, D8, §3.2, §3.6, §6):
// a person's /relay in a member whose lead is on another host. M moves its remote_members row in the lineage transaction and
// queues a `moved` fact in it; the pump drops (never holds) that fact when the lead host does not announce the kind; L applies
// it to the row, keeps the old ref, and tells the lead.

// ---- M: the cleared of a remote member ----

func movedFactsOf(t *testing.T, s *Store) []team.TeamFact {
	t.Helper()
	var out []team.TeamFact
	for _, fr := range factsOf(t, s, "host-L") {
		if fr.Kind != team.FactMoved {
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

func openRemoteStore(t *testing.T) *Store {
	t.Helper()
	s := openTestStore(t)
	n := 0
	s.newID = func() string { n++; return "moved-fact-" + string(rune('0'+n)) }
	s.localHostID = "host-M"
	return s
}

// The row move and the fact are ONE transaction. Mutation gates: the fact queued after the commit (or not at all) → red; the
// seam between them does not roll the row back → red.
func TestMoved_ClearedOfARemoteMemberMovesTheRowAndQueuesTheFactTogether(t *testing.T) {
	s := openRemoteStore(t)
	seedRemote(t, s, "mk-1", "sid-r", 1000)
	claimedOp(t, s, "op-r", "sid-r", "_abc123")
	if _, res, err := s.ReportRelay("op-r", RelayReport{State: team.RelayCleared, NewSessionID: "sid-new", NewRef: "_nnn222", At: 5000}); err != nil || res != ReportApplied {
		t.Fatalf("cleared: res=%v err=%v", res, err)
	}
	got, ok, err := s.RemoteMember("mk-1")
	if err != nil || !ok || got.MemberSessionID != "sid-new" || got.Ref != "_nnn222" || got.State != remoteActive || got.UpdatedAt != 5000 {
		t.Fatalf("row = %+v ok=%v err=%v, want the new session and ref, still active", got, ok, err)
	}
	if got.PID != 42 || got.ProcStart != "ps2" || got.Title != "m" || got.TeamID != "team-L" || got.LeadHostID != "host-L" {
		t.Fatalf("row = %+v: the process, title and team do not change with a /clear", got)
	}
	moved := movedFactsOf(t, s)
	if len(moved) != 1 {
		t.Fatalf("%d moved facts, want 1", len(moved))
	}
	f := moved[0]
	if f.ToHostID != "host-L" || f.TeamID != "team-L" || f.MK != "mk-1" || f.NewSession != "sid-new" || f.NewRef != "_nnn222" ||
		f.PID != 42 || f.ProcStart != "ps2" || f.Title != "m" || f.OpID != "" || !f.Manual {
		t.Fatalf("moved fact = %+v", f)
	}
}

func TestMoved_ARollbackBetweenTheRowMoveAndTheFactLeavesNeither(t *testing.T) {
	s := openRemoteStore(t)
	seedRemote(t, s, "mk-1", "sid-r", 1000)
	claimedOp(t, s, "op-r", "sid-r", "_abc123")
	s.failAfterMovedFact = func() error { return errors.New("injected crash") }
	if _, _, err := s.ReportRelay("op-r", RelayReport{State: team.RelayCleared, NewSessionID: "sid-new", NewRef: "_nnn222", At: 5000}); err == nil {
		t.Fatal("the injected failure did not fail the cleared")
	}
	if got, _, _ := s.RemoteMember("mk-1"); got.MemberSessionID != "sid-r" || got.Ref != "_rmk-1" {
		t.Fatalf("row = %+v: it moved although the fact was not queued", got)
	}
	if n := len(movedFactsOf(t, s)); n != 0 {
		t.Fatalf("%d moved facts survived a rolled-back cleared", n)
	}
	if op, _, _ := s.GetRelayOp("op-r"); op.State == team.RelayCleared {
		t.Fatalf("the op is %s after a rolled-back cleared", op.State)
	}
	// and the retry (the mod re-sends a cleared that did not land) does both
	s.failAfterMovedFact = nil
	if _, res, err := s.ReportRelay("op-r", RelayReport{State: team.RelayCleared, NewSessionID: "sid-new", NewRef: "_nnn222", At: 5000}); err != nil || res != ReportApplied {
		t.Fatalf("retry: res=%v err=%v", res, err)
	}
	if got, _, _ := s.RemoteMember("mk-1"); got.MemberSessionID != "sid-new" || len(movedFactsOf(t, s)) != 1 {
		t.Fatalf("retry: row %+v, %d facts", got, len(movedFactsOf(t, s)))
	}
}

// A cleared into a session that already holds a role still fails whole (the unique index would otherwise).
func TestMoved_ClearedIntoASessionThatHoldsARoleFailsWhole(t *testing.T) {
	s := openRemoteStore(t)
	seedRemote(t, s, "mk-1", "sid-r", 1000)
	seedRemote(t, s, "mk-2", "sid-other", 1000)
	claimedOp(t, s, "op-r", "sid-r", "_abc123")
	if _, _, err := s.ReportRelay("op-r", RelayReport{State: team.RelayCleared, NewSessionID: "sid-other", NewRef: "_nnn222", At: 5000}); !errors.Is(err, ErrClearedTargetHasRole) {
		t.Fatalf("err = %v, want ErrClearedTargetHasRole", err)
	}
	if got, _, _ := s.RemoteMember("mk-1"); got.MemberSessionID != "sid-r" || len(movedFactsOf(t, s)) != 0 {
		t.Fatalf("row %+v, %d facts: a refused cleared changed something", got, len(movedFactsOf(t, s)))
	}
}

// A local member's cleared queues no fact: only a remote member has a lead host to tell.
func TestMoved_ALocalMembersClearedQueuesNoFact(t *testing.T) {
	s := openRemoteStore(t)
	seedTeam(t, s, "team-1", "L1", 1000)
	seedMember(t, s, "op-m", "team-1", "sid-m", 1000)
	claimedOp(t, s, "op-m2", "sid-m", "_abc123")
	if _, res, err := s.ReportRelay("op-m2", RelayReport{State: team.RelayCleared, NewSessionID: "sid-new", NewRef: "_nnn222", At: 5000}); err != nil || res != ReportApplied {
		t.Fatalf("cleared: res=%v err=%v", res, err)
	}
	if n := len(factsOf(t, s, "host-L")); n != 0 {
		t.Fatalf("%d facts for a local member's cleared", n)
	}
}

// ---- the pump: drop_if_unannounced (§3.6, D8) ----

func queueMoved(t *testing.T, f *fixture, mk, sid string) {
	t.Helper()
	s := f.m.store
	s.newID = func() string { return "fact-" + mk }
	seedRemote(t, s, mk, sid, f.clock.Load())
	claimedOp(t, s, "op-"+mk, sid, "_abc123")
	if _, res, err := s.ReportRelay("op-"+mk, RelayReport{State: team.RelayCleared, NewSessionID: sid + "-new", NewRef: "_nnn222", At: f.clock.Load()}); err != nil || res != ReportApplied {
		t.Fatalf("queue %s: res=%v err=%v", mk, res, err)
	}
}

// L is reachable and does not list `moved` (an older version): the fact is dropped, once, and the `ended` fact behind it goes.
// Mutations: hold instead of drop → the ended stays pending → red; drop an unannounced `ended` too → its own test red.
func TestFactPump_AMovedTheLeadHostDoesNotAnnounceIsDroppedAndTheNextFactGoes(t *testing.T) {
	f, fc := factsFixture(t) // host-L announces ended only
	fc.script = doneFor("host-L")
	queueMoved(t, f, "mk-1", "sid-1")
	f.queueEnded("mk-2", "sid-2")
	f.m.factPump.drain("host-L")
	if st := f.factState("fact-mk-1").State; st != factDropped {
		t.Fatalf("moved = %s, want dropped", st)
	}
	if st := f.factState("fact-mk-2").State; st != factDone {
		t.Fatalf("ended behind it = %s, want done", st)
	}
	for _, c := range fc.sent() {
		if strings.Contains(string(c.Body), `"moved"`) {
			t.Fatalf("a moved fact was sent to a host that does not announce it: %s", c.Body)
		}
	}
}

// When the host announces the kind, it is sent and settled like any fact.
func TestFactPump_AMovedTheLeadHostAnnouncesIsSent(t *testing.T) {
	f, fc := factsFixture(t)
	fc.caps["host-L"] = ipeers.TeamCaps{FactKinds: []string{team.FactEnded, team.FactMoved}}
	fc.script = doneFor("host-L")
	queueMoved(t, f, "mk-1", "sid-1")
	f.m.factPump.drain("host-L")
	if st := f.factState("fact-mk-1").State; st != factDone || len(fc.sent()) != 1 {
		t.Fatalf("state %s, %d calls", st, len(fc.sent()))
	}
}

// Unreachable is not "unannounced": the capabilities cannot be read, so the fact waits like any other. Mutation: drop on a
// capabilities error → red.
func TestFactPump_AMovedWaitsWhenTheLeadHostsCapabilitiesCannotBeRead(t *testing.T) {
	f, fc := factsFixture(t)
	fc.capsErr = errors.New("connection refused")
	queueMoved(t, f, "mk-1", "sid-1")
	f.m.factPump.drain("host-L")
	if st := f.factState("fact-mk-1"); st.State != factPending || st.Attempts != 1 {
		t.Fatalf("moved = %+v, want pending after one attempt", st)
	}
	// the host comes back announcing it: it goes
	fc.capsErr = nil
	fc.caps["host-L"] = ipeers.TeamCaps{FactKinds: []string{team.FactEnded, team.FactMoved}}
	fc.script = doneFor("host-L")
	f.clock.Add(31_000)
	f.m.factPump.drain("host-L")
	if st := f.factState("fact-mk-1").State; st != factDone {
		t.Fatalf("moved = %s after the host announced it, want done", st)
	}
}

// Every other kind keeps today's hold: an unannounced `ended` is not dropped (it would lose a fact that is true).
func TestFactPump_AnUnannouncedEndedIsStillHeld(t *testing.T) {
	f, fc := factsFixture(t)
	fc.caps["host-L"] = ipeers.TeamCaps{FactKinds: []string{team.FactRegistered}}
	f.queueEnded("mk-1", "sid-1")
	f.m.factPump.drain("host-L")
	if st := f.factState("fact-mk-1").State; st != factPending {
		t.Fatalf("ended = %s, want pending (held)", st)
	}
}

// The announcement is read again before a drop: a host that was upgraded since the cached answer must not lose the fact.
func TestFactPump_ADropReadsTheCapabilitiesAgainFirst(t *testing.T) {
	f, fc := factsFixture(t)
	fc.script = doneFor("host-L")
	f.queueEnded("mk-0", "sid-0") // fills the capabilities cache: moved is not announced
	f.m.factPump.drain("host-L")
	fc.caps["host-L"] = ipeers.TeamCaps{FactKinds: []string{team.FactEnded, team.FactMoved}} // upgraded meanwhile
	queueMoved(t, f, "mk-1", "sid-1")
	f.m.factPump.drain("host-L")
	if st := f.factState("fact-mk-1").State; st != factDone {
		t.Fatalf("moved = %s, want done: the stale cached capabilities decided a drop", st)
	}
}

// ---- L: applying `moved` ----

func movedFact(id, mk string) team.TeamFact {
	return team.TeamFact{ID: id, Kind: team.FactMoved, ToHostID: "h:1", TeamID: uid(1), MK: mk,
		NewSession: "sid-new", NewRef: "_nnn222", PID: 77, ProcStart: "ps-new", Pane: "%9", Title: "worker", Manual: true}
}

type memberDump struct {
	Session, Ref, State, Title, ProcStart, Pane string
	PID                                         int
}

func (f *fixture) dumpMember(spawnOp string) memberDump {
	f.t.Helper()
	var d memberDump
	if err := f.m.store.db.QueryRow(`SELECT session_id, ref, state, title, proc_start, pane_id, pid FROM team_members WHERE spawn_op = ?`, spawnOp).
		Scan(&d.Session, &d.Ref, &d.State, &d.Title, &d.ProcStart, &d.Pane, &d.PID); err != nil {
		f.t.Fatal(err)
	}
	return d
}

func (f *fixture) refsOf(hostID string) map[string]string {
	f.t.Helper()
	rows, err := f.m.store.db.Query(`SELECT ref, mk FROM remote_member_refs WHERE host_id = ?`, hostID)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var r, mk string
		if err := rows.Scan(&r, &mk); err != nil {
			f.t.Fatal(err)
		}
		out[r] = mk
	}
	return out
}

func TestMovedFact_AppliesToTheRowAndKeepsTheOldRef(t *testing.T) {
	f := factFixture(t)
	f.remoteRow("abc12", "lead:1", "mk1", rowActive)
	code, body := f.postFact(leadPrincipal(), movedFact(factUUID1, "mk1"))
	if code != http.StatusOK || !strings.Contains(string(body), `"applied"`) {
		t.Fatalf("moved = %d %s", code, body)
	}
	got := f.dumpMember("abc12")
	if got.Session != "sid-new" || got.Ref != "_nnn222" || got.State != rowActive || got.Title != "worker" || got.ProcStart != "ps-new" || got.Pane != "%9" || got.PID != 77 {
		t.Fatalf("row = %+v", got)
	}
	if refs := f.refsOf("lead:1"); refs["_rabc12"] != "mk1" || len(refs) != 1 {
		t.Fatalf("remote_member_refs = %v, want the old ref _rabc12 → mk1", refs)
	}
}

// A replayed fact answers the stored outcome and applies nothing twice.
func TestMovedFact_ReplayAnswersTheStoredOutcome(t *testing.T) {
	f := factFixture(t)
	f.remoteRow("abc12", "lead:1", "mk1", rowActive)
	_, first := f.postFact(leadPrincipal(), movedFact(factUUID1, "mk1"))
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET session_id = 'sid-behind', ref = '_bbb333' WHERE spawn_op = 'abc12'`); err != nil {
		t.Fatal(err)
	}
	code, again := f.postFact(leadPrincipal(), movedFact(factUUID1, "mk1"))
	if code != 200 || string(first) != string(again) {
		t.Fatalf("replay = %d %s, want %s", code, again, first)
	}
	if got := f.dumpMember("abc12"); got.Session != "sid-behind" {
		t.Fatalf("a replay re-applied the fact: %+v", got)
	}
}

// Binding: the row is (principal host, team, mk). Each predicate by itself keeps the fact from moving anything.
// Mutations: drop the host predicate / the team predicate / the mk predicate → the matching case moves a row → red.
func TestMovedFact_BindingMovesNothingForAnotherHostTeamOrMember(t *testing.T) {
	setup := func(t *testing.T) *fixture {
		f := factFixture(t)
		f.remoteRow("abc12", "lead:1", "mk1", rowActive)   // the row the principal may move
		f.remoteRow("def34", "other:9", "mk-o", rowActive) // another host's row, same team
		return f
	}
	unchanged := func(t *testing.T, f *fixture, spawnOps ...string) {
		t.Helper()
		for _, op := range spawnOps {
			if got := f.dumpMember(op); got.Session != "sid-"+op || got.Ref != "_r"+op {
				t.Fatalf("%s moved: %+v", op, got)
			}
		}
		if refs := f.refsOf("lead:1"); len(refs) != 0 {
			t.Fatalf("refs written: %v", refs)
		}
	}
	t.Run("another host's row (mk of a row of host other:9)", func(t *testing.T) {
		f := setup(t)
		code, body := f.postFact(leadPrincipal(), movedFact(factUUID1, "mk-o"))
		if code != http.StatusConflict || errCode(t, body) != "not_your_member" {
			t.Fatalf("= %d %s", code, body)
		}
		unchanged(t, f, "abc12", "def34")
	})
	t.Run("another team", func(t *testing.T) {
		f := setup(t)
		ft := movedFact(factUUID1, "mk1")
		ft.TeamID = uid(9)
		code, body := f.postFact(leadPrincipal(), ft)
		if code != http.StatusConflict || errCode(t, body) != "not_your_member" {
			t.Fatalf("= %d %s", code, body)
		}
		unchanged(t, f, "abc12", "def34")
	})
	t.Run("another member key", func(t *testing.T) {
		f := setup(t)
		code, body := f.postFact(leadPrincipal(), movedFact(factUUID1, "mk-unknown"))
		if code != http.StatusConflict || errCode(t, body) != "not_your_member" {
			t.Fatalf("= %d %s", code, body)
		}
		unchanged(t, f, "abc12", "def34")
	})
	t.Run("a local row (this host's own)", func(t *testing.T) {
		f := setup(t)
		f.remoteRow("loc56", "h:1", "mk-local", rowActive) // host_id = this host: not a remote row
		code, body := f.postFact(leadPrincipal(), movedFact(factUUID1, "mk-local"))
		if code != http.StatusConflict || errCode(t, body) != "not_your_member" {
			t.Fatalf("= %d %s", code, body)
		}
		unchanged(t, f, "abc12", "def34", "loc56")
	})
}

// A row that is no longer active is not moved ("ignored"); neither is a row of an ended team.
func TestMovedFact_OnlyAnActiveRowOfALiveTeamMoves(t *testing.T) {
	for _, state := range []string{"released", "gone", "killed", "joining"} {
		t.Run(state, func(t *testing.T) {
			f := factFixture(t)
			f.remoteRow("abc12", "lead:1", "mk1", state)
			code, body := f.postFact(leadPrincipal(), movedFact(factUUID1, "mk1"))
			if code != 200 || !strings.Contains(string(body), `"ignored"`) {
				t.Fatalf("= %d %s, want ignored", code, body)
			}
			if got := f.dumpMember("abc12"); got.Session != "sid-abc12" || got.Ref != "_rabc12" {
				t.Fatalf("a %s row moved: %+v", state, got)
			}
			if len(f.refsOf("lead:1")) != 0 {
				t.Fatal("a ref was kept for a row that did not move")
			}
		})
	}
	t.Run("ended team", func(t *testing.T) {
		f := factFixture(t)
		f.remoteRow("abc12", "lead:1", "mk1", rowActive)
		if _, err := f.m.store.db.Exec(`UPDATE teams SET ended_at = 5 WHERE id = ?`, uid(1)); err != nil {
			t.Fatal(err)
		}
		code, body := f.postFact(leadPrincipal(), movedFact(factUUID1, "mk1"))
		if code != 200 || !strings.Contains(string(body), `"ignored"`) {
			t.Fatalf("= %d %s, want ignored", code, body)
		}
		if got := f.dumpMember("abc12"); got.Session != "sid-abc12" {
			t.Fatalf("moved in an ended team: %+v", got)
		}
	})
}

// The new session is already an active member here: nothing moves (the unique index would fail the fact for good).
func TestMovedFact_ANewSessionThatIsAlreadyAMemberIsIgnored(t *testing.T) {
	f := factFixture(t)
	f.remoteRow("abc12", "lead:1", "mk1", rowActive)
	f.remoteRow("def34", "lead:1", "mk2", rowActive)
	ft := movedFact(factUUID1, "mk1")
	ft.NewSession = "sid-def34"
	code, body := f.postFact(leadPrincipal(), ft)
	if code != 200 || !strings.Contains(string(body), `"ignored"`) {
		t.Fatalf("= %d %s, want ignored", code, body)
	}
	if got := f.dumpMember("abc12"); got.Session != "sid-abc12" {
		t.Fatalf("moved onto another member's session: %+v", got)
	}
}

func TestMovedFact_Validation(t *testing.T) {
	f := factFixture(t)
	f.remoteRow("abc12", "lead:1", "mk1", rowActive)
	for name, mut := range map[string]func(*team.TeamFact){
		"no new session":       func(x *team.TeamFact) { x.NewSession = "" },
		"no new ref":           func(x *team.TeamFact) { x.NewRef = "" },
		"no mk":                func(x *team.TeamFact) { x.MK = "" },
		"a ref that is no ref": func(x *team.TeamFact) { x.NewRef = "nnn222" },
	} {
		ft := movedFact(factUUID1, "mk1")
		mut(&ft)
		if code, body := f.postFact(leadPrincipal(), ft); code != http.StatusBadRequest {
			t.Fatalf("%s = %d %s, want 400", name, code, body)
		}
	}
	if got := f.dumpMember("abc12"); got.Session != "sid-abc12" {
		t.Fatalf("an invalid fact moved the row: %+v", got)
	}
}

// ---- L: the old ref still names the row (D7) ----

func TestMovedFact_TheOldRefStillMatchesTheMemberAndTheNewOneToo(t *testing.T) {
	f, fc := factsFixture(t)
	f.setLeadHost(true)
	fc.aliases = map[string]string{"lead": "lead:1"}
	f.m.cmdCaller = fc
	f.approveLead(uid(1))
	f.remoteRow("abc12", "lead:1", "mk1", rowActive)
	if code, body := f.postFact(leadPrincipal(), movedFact(factUUID1, "mk1")); code != 200 {
		t.Fatalf("moved = %d %s", code, body)
	}
	tm, ok, err := f.m.store.TeamByID(uid(1))
	if err != nil || !ok {
		t.Fatal(err)
	}
	for _, ref := range []string{"_nnn222", "_rabc12"} { // the new ref, and the one before the move
		row, ok, err := f.m.matchRemoteMember(tm, "lead", ref)
		if err != nil || !ok || row.SpawnOp != "abc12" || row.Ref != "_nnn222" {
			t.Fatalf("%s: row %+v ok=%v err=%v", ref, row, ok, err)
		}
	}
}

// An old ref names only the row it was kept for: not another host's, not another team's, and never a row that holds that ref
// now. Mutations: look the old ref up without the host → red.
func TestMovedFact_AnOldRefNeverPointsAtSomeoneElse(t *testing.T) {
	f, fc := factsFixture(t)
	f.setLeadHost(true)
	fc.aliases = map[string]string{"lead": "lead:1", "other": "other:9"}
	f.m.cmdCaller = fc
	f.approveLead(uid(1))
	f.remoteRow("abc12", "lead:1", "mk1", rowActive)
	f.postFact(leadPrincipal(), movedFact(factUUID1, "mk1"))
	tm, _, _ := f.m.store.TeamByID(uid(1))
	if _, ok, _ := f.m.matchRemoteMember(tm, "other", "_rabc12"); ok {
		t.Fatal("an old ref of host lead:1 matched through host other:9")
	}
	// a later member of the same host that now holds the very ref: the live row wins, the stale mapping is not consulted
	f.remoteRow("zzz99", "lead:1", "mk-z", rowActive)
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET ref = '_rabc12' WHERE spawn_op = 'zzz99'`); err != nil {
		t.Fatal(err)
	}
	row, ok, err := f.m.matchRemoteMember(tm, "lead", "_rabc12")
	if err != nil || !ok || row.SpawnOp != "zzz99" {
		t.Fatalf("row %+v ok=%v err=%v, want the row that holds the ref now", row, ok, err)
	}
	// the mapping of another team's member is not followed into this team
	if _, err := f.m.store.db.Exec(`INSERT INTO remote_member_refs (host_id, ref, mk, at) VALUES ('lead:1', '_old777', 'mk-of-another-team', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := f.m.matchRemoteMember(tm, "lead", "_old777"); ok {
		t.Fatal("a ref kept for a member that is not in this team matched")
	}
}

// ---- L: the lead is told ----

func TestMovedFact_TheLeadIsToldOnceAfterTheCommitWithAddressableRefs(t *testing.T) {
	f, fc := factsFixture(t)
	f.setLeadHost(true)
	fc.aliases = map[string]string{"lead": "lead:1"}
	f.m.cmdCaller = fc
	f.approveLead(uid(1))
	f.remoteRow("abc12", "lead:1", "mk1", rowActive)
	if code, body := f.postFact(leadPrincipal(), movedFact(factUUID1, "mk1")); code != 200 {
		t.Fatalf("moved = %d %s", code, body)
	}
	waitFor(t, func() bool { return len(f.sender.calls()) >= 1 })
	var texts []string
	for _, c := range f.sender.calls() {
		texts = append(texts, c.Text)
	}
	want := "[pdx team] member 由使用者手動接力：lead/_rabc12 → lead/_nnn222"
	if len(texts) != 1 || texts[0] != want {
		t.Fatalf("notices = %q, want [%q]", texts, want)
	}
	// a replay of the same fact tells nobody again
	f.postFact(leadPrincipal(), movedFact(factUUID1, "mk1"))
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices after a replay, want 1", n)
	}
}

// A fact that moved nothing tells nobody.
func TestMovedFact_NoNoticeWhenNothingMoved(t *testing.T) {
	f, fc := factsFixture(t)
	f.setLeadHost(true)
	fc.aliases = map[string]string{"lead": "lead:1"}
	f.m.cmdCaller = fc
	f.approveLead(uid(1))
	f.remoteRow("abc12", "lead:1", "mk1", "gone")
	f.postFact(leadPrincipal(), movedFact(factUUID1, "mk1"))
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices for an ignored fact", n)
	}
}

// ---- capabilities ----

func TestMovedFact_TheLeadHostAnnouncesTheKind(t *testing.T) {
	if !factKindApplied(team.FactMoved) {
		t.Fatal("factKindApplied does not list moved")
	}
}

var _ = peersmod.ClassDone
