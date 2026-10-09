package teammod

import (
	"testing"

	"github.com/wake/purdex/internal/team"
)

// TI-5a: what the mod socket asks the team module — the current session's role, the lead's active member count and label.

func seedLeadWithMembers(t *testing.T, s *Store) {
	t.Helper()
	seedTeam(t, s, "team-1", "lead-1", 1000)
	if _, err := s.db.Exec(`UPDATE teams SET team_label = '資源線' WHERE id = 'team-1'`); err != nil {
		t.Fatal(err)
	}
	seedMember(t, s, "op-a", "team-1", "m-a", 1000)
	seedMember(t, s, "op-b", "team-1", "m-b", 1000)
	seedMember(t, s, "op-c", "team-1", "m-c", 1000)
	seedMember(t, s, "op-r", "team-1", "m-released", 1000)
	seedMember(t, s, "op-k", "team-1", "m-gone", 1000)
	if _, err := s.db.Exec(`UPDATE team_members SET state = 'released' WHERE spawn_op = 'op-r'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE team_members SET state = 'gone' WHERE spawn_op = 'op-k'`); err != nil {
		t.Fatal(err)
	}
}

// Only active members count: released and gone are not members any more.
// Mutation gate: count every state → red.
func TestModTeamRead_LeadCountsActiveMembersOnly(t *testing.T) {
	s := openTestStore(t)
	seedLeadWithMembers(t, s)
	got, err := s.ModTeamRead("lead-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != "lead" || got.Members != 3 || got.TeamLabel != "資源線" {
		t.Fatalf("lead = %+v, want lead / 3 / 資源線", got)
	}
}

// A member of the team on another host is a member of the team too (the lead's host rows count whole-team).
func TestModTeamRead_CountsMembersOnOtherHostsToo(t *testing.T) {
	s := openTestStore(t)
	seedLeadWithMembers(t, s)
	if _, err := s.db.Exec(`UPDATE team_members SET host_id = 'other-host' WHERE spawn_op = 'op-c'`); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ModTeamRead("lead-1"); got.Members != 3 {
		t.Fatalf("members = %d, want 3 (the remote active row counts)", got.Members)
	}
}

func TestModTeamRead_MembersAndOthers(t *testing.T) {
	s := openTestStore(t)
	seedLeadWithMembers(t, s)
	seedRemote(t, s, "mk-1", "remote-m", 1000)
	for sid, want := range map[string]team.ModRead{
		"m-a":        {Role: "member"},
		"remote-m":   {Role: "member"}, // a member_remote is a member
		"m-released": {Role: "none"},
		"nobody":     {Role: "none"},
		"":           {Role: "none"},
	} {
		got, err := s.ModTeamRead(sid)
		if err != nil || got != want {
			t.Fatalf("%q = %+v err=%v, want %+v", sid, got, err, want)
		}
	}
}

// The answer is for the current session id only: after a relay the lead's session id moves, and the old id is nobody.
func TestModTeamRead_AnsweredForTheCurrentSessionIDOnly(t *testing.T) {
	s := openTestStore(t)
	seedLeadWithMembers(t, s)
	if _, err := s.db.Exec(`UPDATE teams SET lead_session_id = 'lead-2' WHERE id = 'team-1'`); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ModTeamRead("lead-1"); got.Role != "none" {
		t.Fatalf("the old lead session = %+v", got)
	}
	if got, _ := s.ModTeamRead("lead-2"); got.Role != "lead" || got.Members != 3 {
		t.Fatalf("the new lead session = %+v", got)
	}
}

func TestModTeamRead_EndedTeamIsNone(t *testing.T) {
	s := openTestStore(t)
	seedLeadWithMembers(t, s)
	if ended, err := s.EndTeam("team-1", "lead-1", team.TeamEndLeadGone, 2000); err != nil || !ended {
		t.Fatal(ended, err)
	}
	for _, sid := range []string{"lead-1", "m-a"} {
		if got, _ := s.ModTeamRead(sid); got.Role != "none" || got.Members != 0 {
			t.Fatalf("%s after the team ended = %+v", sid, got)
		}
	}
}

func TestModTeamRead_DBErrorSurfaces(t *testing.T) {
	s := openTestStore(t)
	s.db.Close()
	if _, err := s.ModTeamRead("lead-1"); err == nil {
		t.Fatal("a closed db must be an error, never none")
	}
}

// The module hands the store out under its registry key.
func TestModTeamRead_RegisteredByTheModule(t *testing.T) {
	f := newFixture(t)
	svc, ok := f.core.Registry.Get(team.ModReadKey)
	if !ok {
		t.Fatalf("no service under %q", team.ModReadKey)
	}
	if _, ok := svc.(team.ModReader); !ok {
		t.Fatalf("%T is not a team.ModReader", svc)
	}
}
