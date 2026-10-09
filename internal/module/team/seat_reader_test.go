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
	// a cycle (raw insert) stops at the last unseen session
	lineage(t, s, "x", "y")
	lineage(t, s, "y", "x")
	got, err := s.RootSessionOf("x")
	if err != nil || (got != "x" && got != "y") {
		t.Fatalf("cycle = %q err=%v", got, err)
	}
	if want, _ := s.RootSessionOf("x"); want != got {
		t.Fatal("cycle answer must be stable")
	}
}

func TestRootSessionOf_AgreesWithChainRoots(t *testing.T) {
	s := openTestStore(t)
	lineage(t, s, "c", "b")
	lineage(t, s, "b", "a")
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
		"lead-1":   {TeamID: "team-1", Role: team.SeatLead},
		"local-m":  {TeamID: "team-1", Role: team.SeatMember},
		"remote-m": {TeamID: "team-L", Role: team.SeatMemberRemote}, // the team id on the lead's host
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
