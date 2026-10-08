package teammod

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// seedNamedTeam approves request id (led by sid) into a team called name.
func seedNamedTeam(t *testing.T, s *Store, id, sid, name string, at int64) {
	t.Helper()
	g := team.Grant{MaxMembers: 3, Roots: []string{"/w"}, TeamName: &name}
	if _, _, _, err := s.Create(openApproval(id, sid, at), "seed-"+id); err != nil {
		t.Fatal(err)
	}
	tm := leadTeam(id, sid, "_abc123", g, at)
	tm.TeamName = name
	if _, won, err := s.CloseLeadApproved(id, approveClose(at, g), tm); err != nil || !won {
		t.Fatalf("seed team %s: won=%v err=%v", id, won, err)
	}
}

// The team's current name is its own column (D-N5): written by the approving
// insert and read back by every path that scans a team — scanTeam, the
// member-to-team join and the live list.
func TestStore_TeamNameRoundTrip(t *testing.T) {
	s := openTestStore(t)
	seedNamedTeam(t, s, "id-1", "sid-1", "驗收 team", 1000)
	seedTeam(t, s, "id-2", "sid-2", 2000) // no name

	if got, ok := getTeam(t, s, "id-1"); !ok || got.TeamName != "驗收 team" {
		t.Fatalf("scanTeam id-1 = %+v ok=%v, want the name", got, ok)
	}
	if got, ok := getTeam(t, s, "id-2"); !ok || got.TeamName != "" {
		t.Fatalf("scanTeam id-2 = %+v ok=%v, want no name", got, ok)
	}
	if got, ok, err := s.LiveTeamByLead("sid-1"); err != nil || !ok || got.TeamName != "驗收 team" {
		t.Fatalf("LiveTeamByLead = %+v ok=%v err=%v", got, ok, err)
	}
	seedMember(t, s, "op-1", "id-1", "sid-m1", 1500)
	if _, tm, ok, err := s.ActiveMemberInLiveTeam("sid-m1"); err != nil || !ok || tm.TeamName != "驗收 team" {
		t.Fatalf("member-to-team join = %+v ok=%v err=%v, want the name", tm, ok, err)
	}
	live, err := s.ListLiveTeamsWithLeadUsage()
	if err != nil || len(live) != 2 || live[0].TeamName != "驗收 team" || live[1].TeamName != "" {
		t.Fatalf("live list = %+v err=%v", live, err)
	}
	if all, err := s.ListLiveTeams(); err != nil || len(all) != 2 || all[0].TeamName != "驗收 team" {
		t.Fatalf("ListLiveTeams = %+v err=%v", all, err)
	}
}

// A team.db written before names has no team_name column: opening it adds the
// column, an old row reads "" and its grant (no team_name key) decodes with a
// nil TeamName; opening again is a no-op.
func TestOpenStore_AddsTeamNameToAnOldDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	seedTeam(t, s, "id-1", "sid-1", 1000)
	oldGrant := `{"max_members":3,"roots":["/w"]}`
	if _, err := s.db.Exec(`UPDATE teams SET grant_json = ? WHERE id = 'id-1'`, oldGrant); err != nil {
		t.Fatal(err)
	}
	// Drop the column the way an old binary never had it.
	if _, err := s.db.Exec(`ALTER TABLE teams DROP COLUMN team_name`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if columnsOf(t, s.db, "teams")["team_name"] {
		t.Fatal("setup: the column is still there")
	}
	s.Close()

	for i := 0; i < 2; i++ { // the second open is a no-op
		s, err = OpenStore(path)
		if err != nil {
			t.Fatalf("open %d: %v", i+1, err)
		}
		if !columnsOf(t, s.db, "teams")["team_name"] {
			t.Fatalf("open %d: no team_name column", i+1)
		}
		got, ok := getTeam(t, s, "id-1")
		if !ok || got.TeamName != "" || got.Grant.TeamName != nil || got.Grant.MaxMembers != 3 {
			t.Fatalf("open %d: old row = %+v ok=%v, want no name and a nil grant name", i+1, got, ok)
		}
		s.Close()
	}
}

// grant_json keeps the approved name as a record: a named grant survives the
// column and the JSON, an unnamed one has no key.
func TestStore_GrantJSONKeepsTheApprovedName(t *testing.T) {
	s := openTestStore(t)
	seedNamedTeam(t, s, "id-1", "sid-1", "build", 1000)
	var raw string
	if err := s.db.QueryRow(`SELECT grant_json FROM teams WHERE id = 'id-1'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var g map[string]any
	if err := json.Unmarshal([]byte(raw), &g); err != nil || g["team_name"] != "build" {
		t.Fatalf("grant_json = %s (%v), want team_name build", raw, err)
	}
	var col string
	if err := s.db.QueryRow(`SELECT team_name FROM teams WHERE id = 'id-1'`).Scan(&col); err != nil || col != "build" {
		t.Fatalf("teams.team_name = %q (%v)", col, err)
	}
}
