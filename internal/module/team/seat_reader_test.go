package teammod

import (
	"testing"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/team"
)

// WB-1a-i: the per-session readers the workbook stands on.

func TestRootSessionOf(t *testing.T) {
	s := openTestStore(t)
	// no lineage → itself
	if got, err := s.RootSessionOf("solo"); err != nil || got != "solo" {
		t.Fatalf("solo = %q err=%v", got, err)
	}
	// a 3-hop chain c → b → a → root
	lineage(t, s, "c", "b")
	lineage(t, s, "b", "a")
	lineage(t, s, "a", "root")
	for _, sid := range []string{"c", "b", "a", "root"} {
		if got, err := s.RootSessionOf(sid); err != nil || got != "root" {
			t.Fatalf("root(%s) = %q err=%v", sid, got, err)
		}
	}
	// a cycle (raw insert) has one answer from every starting point, with a tail leading into it
	lineage(t, s, "x", "y")
	lineage(t, s, "y", "z")
	lineage(t, s, "z", "x")
	lineage(t, s, "tail", "z")
	for _, sid := range []string{"x", "y", "z", "tail"} {
		if got, err := s.RootSessionOf(sid); err != nil || got != "x" {
			t.Fatalf("cycle root(%s) = %q err=%v, want x", sid, got, err)
		}
	}
}

func TestRootSessionOf_AgreesWithChainRoots(t *testing.T) {
	s := openTestStore(t)
	lineage(t, s, "c", "b")
	lineage(t, s, "b", "a")
	lineage(t, s, "x", "y") // a damaged cycle must agree too
	lineage(t, s, "y", "x")
	roots, err := s.ChainRoots()
	if err != nil {
		t.Fatal(err)
	}
	for sid, want := range roots {
		if got, err := s.RootSessionOf(sid); err != nil || got != want {
			t.Fatalf("root(%s) = %q err=%v, ChainRoots says %q", sid, got, err, want)
		}
	}
}

func TestRootSessionOf_DBErrorSurfaces(t *testing.T) {
	s := openTestStore(t)
	s.db.Close()
	if _, err := s.RootSessionOf("a"); err == nil {
		t.Fatal("a closed db must be an error")
	}
}

func TestSeatOf(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, "team-1", "lead-1", 1000)
	seedMember(t, s, "op-1", "team-1", "local-m", 1000)
	seedRemote(t, s, "mk-1", "remote-m", 1000)
	for sid, want := range map[string]team.Seat{
		"lead-1": {TeamID: "team-1", Role: team.SeatLead},
		// a local member also names its lead's session (name pool v2 backfill: the member's rule is the lead's)
		"local-m": {TeamID: "team-1", Role: team.SeatMember, LeadSessionID: "lead-1"},
		// the team id on the lead's host, and where that lead is: its host and its session there (the journal asks the lead's host)
		"remote-m": {TeamID: "team-L", Role: team.SeatMemberRemote, LeadHostID: "host-L", LeadSessionID: "lead-sid"},
		"nobody":   {Role: team.SeatNone},
	} {
		got, err := s.SeatOf(sid)
		if err != nil || got != want {
			t.Fatalf("seat(%s) = %+v err=%v, want %+v", sid, got, err, want)
		}
	}
}

// An ended member reads what the store holds now: none.
func TestSeatOf_EndedIsNone(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, "team-1", "lead-1", 1000)
	seedMember(t, s, "op-1", "team-1", "local-m", 1000)
	seedRemote(t, s, "mk-1", "remote-m", 1000)
	if _, err := s.SetRemoteMemberState("mk-1", []string{remoteActive}, remoteEnded, 2000); err != nil {
		t.Fatal(err)
	}
	if ended, err := s.EndTeam("team-1", "lead-1", team.TeamEndLeadGone, 2000); err != nil || !ended {
		t.Fatalf("end: %v %v", ended, err)
	}
	for _, sid := range []string{"lead-1", "local-m", "remote-m"} {
		if got, err := s.SeatOf(sid); err != nil || got != (team.Seat{Role: team.SeatNone}) {
			t.Fatalf("seat(%s) = %+v err=%v", sid, got, err)
		}
	}
}

// The relay guard (name pool v2 backfill): a session in an unfinished relay, as the old session or as the new one.
func TestInRelay(t *testing.T) {
	s := openTestStore(t)
	op := selfOp("op-1", "old-sid", "_aaaaaa", 1000)
	op.State = team.RelayClaimed
	if err := s.CreateRelayOp(op); err != nil {
		t.Fatal(err)
	}
	check := func(sid string, want bool) {
		t.Helper()
		if got, err := s.InRelay(sid); err != nil || got != want {
			t.Fatalf("InRelay(%s) = %v err=%v, want %v", sid, got, err, want)
		}
	}
	check("old-sid", true)
	check("new-sid", false)
	check("other", false)
	mustReport(t, s, "op-1", RelayReport{State: team.RelayCleared, NewSessionID: "new-sid", NewRef: "_bbbbbb", At: 1500})
	check("old-sid", true)
	check("new-sid", true) // cleared, not done: the successor is still settling
	mustReport(t, s, "op-1", RelayReport{State: team.RelayDone, At: 2000})
	check("old-sid", false)
	check("new-sid", false)
}

func TestInRelay_DBErrorSurfaces(t *testing.T) {
	s := openTestStore(t)
	s.db.Close()
	if _, err := s.InRelay("a"); err == nil {
		t.Fatal("a closed db must be an error")
	}
}

func TestSeatOf_DBErrorSurfaces(t *testing.T) {
	s := openTestStore(t)
	s.db.Close()
	if _, err := s.SeatOf("a"); err == nil {
		t.Fatal("a closed db must be an error")
	}
}

// The service is the store, under its own keys (a consumer reads the narrow interface only).
func TestSeatAndLineageRootAreTheStore(t *testing.T) {
	s := openTestStore(t)
	var _ team.SeatReader = s
	var _ team.LineageRootResolver = s
	var _ team.RelayGuard = s
}

// Today's behaviour kept: a failed turn end records nothing on the team side (workbook D4).
// Mutation gate: drop the Failed return in onTurnEnd → red.
func TestLastTurn_IgnoresAFailedTurnEnd(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "own", nil)
	w.start(tk.ID)
	w.m.onTurnEnd(agent.TurnEndEvent{SessionID: "sid-ma", Text: "撞到上限了。", At: 100, Seq: 1, Failed: true})
	if sum, at := w.taskTurn(tk.ID); sum != "" || at != 0 {
		t.Fatalf("a failed turn end was recorded: %q @%d", sum, at)
	}
}

func TestPredecessorOf(t *testing.T) {
	s := openTestStore(t)
	var _ team.PredecessorReader = s
	if _, _, ok, err := s.PredecessorOf("b"); err != nil || ok {
		t.Fatalf("no lineage: ok=%v err=%v", ok, err)
	}
	if _, err := s.db.Exec(`INSERT INTO session_lineage (session_id, predecessor_session_id, predecessor_ref, op_id, at) VALUES ('b', 'a', '_old000', 'op', 1234)`); err != nil {
		t.Fatal(err)
	}
	prev, at, ok, err := s.PredecessorOf("b")
	if err != nil || !ok || prev != "a" || at != 1234 {
		t.Fatalf("got %q %d %v %v", prev, at, ok, err)
	}
	// only the new side is a key: the predecessor itself has none
	if _, _, ok, _ := s.PredecessorOf("a"); ok {
		t.Fatal("a predecessor is not its own successor")
	}
}
