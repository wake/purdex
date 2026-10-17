package teammod

import (
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// The sessions of a team on THIS host: its lead when the lead is here, and its active members here. Not a remote member, not a
// member that is going or gone, nothing of an ended team (assist plan X1b.2).
func TestLocalLiveSessionsOfTeam(t *testing.T) {
	s := openTestStore(t)
	s.localHostID = "h:1"
	seedTeam(t, s, "team-1", "lead-1", 1000)
	seedTeam(t, s, "team-2", "lead-2", 1000)
	seedMember(t, s, "op-a", "team-1", "sid-a", 2000)
	seedMember(t, s, "op-killing", "team-1", "sid-killing", 2000)
	if err := s.SetMemberState("op-killing", team.MemberKilling, 3000); err != nil {
		t.Fatal(err)
	}
	remote := newMember("op-remote", "team-1", "sid-remote", "_remote", 2000)
	remote.HostID = "other:2"
	if err := s.InsertMember(remote); err != nil {
		t.Fatal(err)
	}
	seedMember(t, s, "op-other", "team-2", "sid-other", 2000)

	got, err := s.LocalLiveSessionsOfTeam("team-1")
	if err != nil || !reflect.DeepEqual(got, []string{"lead-1", "sid-a"}) {
		t.Fatalf("team-1 = %v err=%v", got, err)
	}
	// a team led on another host: its members here are told, its lead is not ours
	if _, err := s.db.Exec(`UPDATE teams SET host_id = 'other:2' WHERE id = 'team-2'`); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.LocalLiveSessionsOfTeam("team-2"); !reflect.DeepEqual(got, []string{"sid-other"}) {
		t.Fatalf("team-2 = %v", got)
	}
	// an ended team tells nobody; neither does one that does not exist
	if ended, err := s.EndTeam("team-1", "lead-1", "done", 4000); err != nil || !ended {
		t.Fatal(ended, err)
	}
	for _, id := range []string{"team-1", "nope"} {
		if got, err := s.LocalLiveSessionsOfTeam(id); err != nil || len(got) != 0 {
			t.Fatalf("%s = %v err=%v", id, got, err)
		}
	}
}
