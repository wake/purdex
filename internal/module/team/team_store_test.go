package teammod

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// approveClose is the Close an approve of a lead row writes.
func approveClose(at int64, g team.Grant) Close {
	return Close{State: team.StateApproved, DecidedAt: at, DecidedBy: &team.Client{Kind: "app", Label: "Purdex.app @ air26"}, Grant: &g}
}

// leadTeam is the team an approve of request id by session sid creates.
func leadTeam(id, sid, ref string, g team.Grant, at int64) team.Team {
	return team.Team{ID: id, HostID: "h:1", LeadSessionID: sid, LeadRef: ref, Grant: g, RequestID: id, CreatedAt: at}
}

// getTeam reads one team row by id, live or ended (tests only).
func getTeam(t *testing.T, s *Store, id string) (team.Team, bool) {
	t.Helper()
	tm, err := scanTeam(s.db.QueryRow(`SELECT `+teamCols+` FROM teams WHERE id = ?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return team.Team{}, false
		}
		t.Fatalf("get team %s: %v", id, err)
	}
	return tm, true
}

func countTeams(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM teams`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// seedTeam makes sid the lead of live team id: an open row, approved
// through the store (no request_open / already_lead checks).
func seedTeam(t *testing.T, s *Store, id, sid string, at int64) {
	t.Helper()
	g := team.Grant{MaxMembers: 3, Roots: []string{"/w"}}
	if _, _, _, err := s.Create(openApproval(id, sid, at), "seed-"+id); err != nil {
		t.Fatal(err)
	}
	if _, won, err := s.CloseLeadApproved(id, approveClose(at, g), leadTeam(id, sid, "_abc123", g, at)); err != nil || !won {
		t.Fatalf("seed team %s for %s: won=%v err=%v", id, sid, won, err)
	}
}

// Spec §6.2 "Approval creates the team (§7.1) in the same transaction":
// the approved row and the team row land together; a second approve loses
// the CAS and adds no second team.
func TestStore_CloseLeadApprovedCreatesTheTeamInOneTx(t *testing.T) {
	s := openTestStore(t)
	if _, _, _, err := s.Create(openApproval("id-1", "sid-1", 1000), "h1"); err != nil {
		t.Fatal(err)
	}
	g := team.Grant{MaxMembers: 2, Roots: []string{"/w/x", "/y"}}
	want := leadTeam("id-1", "sid-1", "_abc123", g, 2000)
	a, won, err := s.CloseLeadApproved("id-1", approveClose(2000, g), want)
	if err != nil || !won || a.State != team.StateApproved || a.Grant == nil || !reflect.DeepEqual(*a.Grant, g) || a.DecidedAt != 2000 {
		t.Fatalf("approve: won=%v err=%v row=%+v", won, err, a)
	}
	got, ok, err := s.LiveTeamByLead("sid-1")
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("live team of sid-1: %+v ok=%v err=%v, want %+v", got, ok, err, want)
	}
	live, err := s.ListLiveTeams()
	if err != nil || len(live) != 1 || !reflect.DeepEqual(live[0], want) {
		t.Fatalf("live teams = %+v err=%v", live, err)
	}

	// A second approve (another client's click) loses the CAS: the row it
	// answers is the winner's, and no second team appears.
	again, won, err := s.CloseLeadApproved("id-1", approveClose(3000, team.Grant{MaxMembers: 5, Roots: []string{"/z"}}), leadTeam("id-1", "sid-1", "_abc123", g, 3000))
	if err != nil || won || again.State != team.StateApproved || again.DecidedAt != 2000 || again.Grant.MaxMembers != 2 {
		t.Fatalf("second approve: won=%v err=%v row=%+v (want the winner's row)", won, err, again)
	}
	if n := countTeams(t, s); n != 1 {
		t.Fatalf("teams after a lost CAS = %d, want 1", n)
	}
	// A row closed another way first: the approve loses and makes no team.
	if _, _, _, err := s.Create(openApproval("id-2", "sid-2", 1000), "h2"); err != nil {
		t.Fatal(err)
	}
	if _, won, err := s.CloseIfOpen("id-2", Close{State: team.StateDenied, DecidedAt: 1500}); err != nil || !won {
		t.Fatalf("deny id-2: won=%v err=%v", won, err)
	}
	if a, won, err := s.CloseLeadApproved("id-2", approveClose(2000, g), leadTeam("id-2", "sid-2", "_def456", g, 2000)); err != nil || won || a.State != team.StateDenied {
		t.Fatalf("approve after deny: won=%v err=%v row=%+v", won, err, a)
	}
	if _, ok, _ := s.LiveTeamByLead("sid-2"); ok {
		t.Fatal("an approve that lost to a deny created a team")
	}
	if _, _, err := s.CloseLeadApproved("nope", approveClose(2000, g), leadTeam("nope", "sid-9", "_zzzzzz", g, 2000)); !errors.Is(err, ErrNoSuchApproval) {
		t.Fatalf("unknown id: err=%v, want ErrNoSuchApproval", err)
	}
}

// The store refuses a call that would break the team's invariants: only an
// approval with a grant makes a team, and the team id is the request id
// (plan v3 deviation 1). Nothing is written.
func TestStore_CloseLeadApprovedRefusesAMisuse(t *testing.T) {
	s := openTestStore(t)
	if _, _, _, err := s.Create(openApproval("id-1", "sid-1", 1000), "h1"); err != nil {
		t.Fatal(err)
	}
	g := team.Grant{MaxMembers: 3, Roots: []string{"/w"}}
	noGrant := approveClose(2000, g)
	noGrant.Grant = nil
	for name, tc := range map[string]struct {
		c  Close
		tm team.Team
	}{
		"a deny":          {Close{State: team.StateDenied, DecidedAt: 2000}, leadTeam("id-1", "sid-1", "_abc123", g, 2000)},
		"no grant":        {noGrant, leadTeam("id-1", "sid-1", "_abc123", g, 2000)},
		"another team id": {approveClose(2000, g), leadTeam("id-9", "sid-1", "_abc123", g, 2000)},
	} {
		if _, _, err := s.CloseLeadApproved("id-1", tc.c, tc.tm); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if a, _, _ := s.Get("id-1"); a.State != team.StateOpen {
		t.Fatalf("row after refused calls = %s, want open", a.State)
	}
	if n := countTeams(t, s); n != 0 {
		t.Fatalf("teams after refused calls = %d, want 0", n)
	}
}

// One live team per lead (teams_one_live_per_lead): approving a second
// request of a session that already leads a live team rolls back both the
// close and the insert — the row stays open, the team is not added — and
// says ErrLeadHasTeam. An ended team does not count.
func TestStore_CloseLeadApprovedRollsBackWhenTheLeadHasATeam(t *testing.T) {
	s := openTestStore(t)
	for _, id := range []string{"id-1", "id-2"} { // the store does not apply request_open; the handler does
		if _, _, _, err := s.Create(openApproval(id, "sid-1", 1000), "h-"+id); err != nil {
			t.Fatal(err)
		}
	}
	g := team.Grant{MaxMembers: 3, Roots: []string{"/w"}}
	if _, won, err := s.CloseLeadApproved("id-1", approveClose(2000, g), leadTeam("id-1", "sid-1", "_abc123", g, 2000)); err != nil || !won {
		t.Fatalf("first approve: won=%v err=%v", won, err)
	}
	_, won, err := s.CloseLeadApproved("id-2", approveClose(3000, g), leadTeam("id-2", "sid-1", "_abc123", g, 3000))
	if !errors.Is(err, ErrLeadHasTeam) || won {
		t.Fatalf("second approve of the same lead: won=%v err=%v, want ErrLeadHasTeam", won, err)
	}
	if a, _, _ := s.Get("id-2"); a.State != team.StateOpen || a.Grant != nil || a.DecidedBy != nil || a.DecidedAt != 0 {
		t.Fatalf("id-2 after the rollback = %+v, want open and untouched", a)
	}
	if _, ok := getTeam(t, s, "id-2"); ok {
		t.Fatal("the second team was inserted")
	}
	if n := countTeams(t, s); n != 1 {
		t.Fatalf("teams = %d, want 1", n)
	}

	// Once the first team has ended, the same session may lead again.
	if ended, err := s.EndTeam("id-1", "sid-1", team.TeamEndLeadGone, 4000); err != nil || !ended {
		t.Fatalf("end id-1: ended=%v err=%v", ended, err)
	}
	if _, won, err := s.CloseLeadApproved("id-2", approveClose(5000, g), leadTeam("id-2", "sid-1", "_abc123", g, 5000)); err != nil || !won {
		t.Fatalf("approve after the first team ended: won=%v err=%v", won, err)
	}
	if got, ok, _ := s.LiveTeamByLead("sid-1"); !ok || got.ID != "id-2" {
		t.Fatalf("live team of sid-1 = %+v ok=%v, want id-2", got, ok)
	}
}

// EndTeam (spec §7.1) ends a live team once: the second call changes
// nothing. It is also guarded on the lead the caller saw, so a sweeper that
// read the team before a relay moved its lead (P4-3's cleared transaction)
// cannot end the team its new lead now holds.
func TestStore_EndTeamOnceAndOnlyForTheLeadItSaw(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, "id-1", "sid-1", 2000)
	seedTeam(t, s, "id-2", "sid-2", 2000)
	ended, err := s.EndTeam("id-1", "sid-1", team.TeamEndLeadGone, 3000)
	if err != nil || !ended {
		t.Fatalf("end: ended=%v err=%v", ended, err)
	}
	got, ok := getTeam(t, s, "id-1")
	if !ok || got.EndedAt != 3000 || got.EndReason != team.TeamEndLeadGone {
		t.Fatalf("ended team = %+v", got)
	}
	if ended, err := s.EndTeam("id-1", "sid-1", "other", 4000); err != nil || ended {
		t.Fatalf("second end: ended=%v err=%v, want false", ended, err)
	}
	if got, _ := getTeam(t, s, "id-1"); got.EndedAt != 3000 || got.EndReason != team.TeamEndLeadGone {
		t.Fatalf("a second end rewrote the team: %+v", got)
	}
	if _, ok, _ := s.LiveTeamByLead("sid-1"); ok {
		t.Fatal("an ended team is still the lead's live team")
	}
	if live, err := s.ListLiveTeams(); err != nil || len(live) != 1 || live[0].ID != "id-2" {
		t.Fatalf("live teams = %+v err=%v, want only id-2", live, err)
	}

	// id-2's lead moves to sid-2b (what P4-3's cleared transaction does)
	// after the sweeper read the team with sid-2: its end loses.
	if _, err := s.db.Exec(`UPDATE teams SET lead_session_id = 'sid-2b' WHERE id = 'id-2'`); err != nil {
		t.Fatal(err)
	}
	if ended, err := s.EndTeam("id-2", "sid-2", team.TeamEndLeadGone, 5000); err != nil || ended {
		t.Fatalf("end with the old lead: ended=%v err=%v, want false", ended, err)
	}
	if got, ok, _ := s.LiveTeamByLead("sid-2b"); !ok || got.ID != "id-2" || got.EndedAt != 0 {
		t.Fatalf("the moved team = %+v ok=%v, want live", got, ok)
	}
	if ended, err := s.EndTeam("nope", "sid-9", team.TeamEndLeadGone, 5000); err != nil || ended {
		t.Fatalf("end unknown: ended=%v err=%v", ended, err)
	}
	if live, err := s.ListLiveTeams(); err != nil || live == nil {
		t.Fatalf("live teams = %#v err=%v (must be [] not nil)", live, err)
	}
}

// The deploy path: a team.db written before P4-2 has no teams table.
// OpenStore adds it (CREATE … IF NOT EXISTS) and keeps every existing row.
func TestOpenStore_AddsTeamsToAnExistingDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Create(openApproval("id-1", "sid-1", 1000), "h1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP INDEX teams_one_live_per_lead; DROP TABLE teams`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatalf("reopen a pre-P4-2 db: %v", err)
	}
	defer s.Close()
	if a, ok, err := s.Get("id-1"); err != nil || !ok || a.State != team.StateOpen {
		t.Fatalf("existing row after the migration: %+v ok=%v err=%v", a, ok, err)
	}
	g := team.Grant{MaxMembers: 3, Roots: []string{"/w"}}
	if _, won, err := s.CloseLeadApproved("id-1", approveClose(2000, g), leadTeam("id-1", "sid-1", "_abc123", g, 2000)); err != nil || !won {
		t.Fatalf("approve on the migrated db: won=%v err=%v", won, err)
	}
	if _, ok, err := s.LiveTeamByLead("sid-1"); err != nil || !ok {
		t.Fatalf("team on the migrated db: ok=%v err=%v", ok, err)
	}
}
