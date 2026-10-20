package teammod

import (
	"testing"

	"github.com/wake/purdex/internal/team"
)

// notify-title spec §1, §3.1: the display name of the team a session sits in, the one the App's panel shows.

func setTeamName(t *testing.T, s *Store, name string) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE teams SET team_name = ? WHERE id = 'team-1'`, name); err != nil {
		t.Fatal(err)
	}
}

func TestNotifySeatOf_LocalLeadMemberAndRemote(t *testing.T) {
	s := openTestStore(t)
	seedLeadWithMembers(t, s)
	setTeamName(t, s, "Purdex A team")
	seedRemote(t, s, "mk-1", "remote-m", 1000)
	got, err := s.NotifySeatOf("lead-1")
	if err != nil || got.Role != team.SeatLead || got.TeamName != "Purdex A team" || got.LeadSessionID != "lead-1" || got.LeadRef != "_abc123" {
		t.Fatalf("lead = %+v err=%v", got, err)
	}
	got, err = s.NotifySeatOf("m-a")
	if err != nil || got.Role != team.SeatMember || got.TeamName != "Purdex A team" || got.LeadSessionID != "lead-1" {
		t.Fatalf("local member = %+v err=%v", got, err)
	}
	// a remote member: what the lead's host told it (the fixture: team "T", lead "lead" at "lead/x [lead01]")
	got, err = s.NotifySeatOf("remote-m")
	if err != nil || got.Role != team.SeatMemberRemote || got.TeamName != "T" || got.LeadSessionID != "" ||
		got.LeadTitle != "lead" || got.LeadAddress != "lead/x [lead01]" || got.LeadRef != "_lead01" {
		t.Fatalf("remote member = %+v err=%v", got, err)
	}
}

func TestNotifySeatOf_NoSeatAndEnded(t *testing.T) {
	s := openTestStore(t)
	seedLeadWithMembers(t, s)
	for _, sid := range []string{"nobody", "", "m-released"} {
		if got, err := s.NotifySeatOf(sid); err != nil || got.Role != team.SeatNone {
			t.Fatalf("%q = %+v err=%v", sid, got, err)
		}
	}
	if ended, err := s.EndTeam("team-1", "lead-1", team.TeamEndLeadGone, 2000); err != nil || !ended {
		t.Fatal(ended, err)
	}
	for _, sid := range []string{"lead-1", "m-a"} {
		if got, _ := s.NotifySeatOf(sid); got.Role != team.SeatNone {
			t.Fatalf("%s after the team ended = %+v", sid, got)
		}
	}
}

func TestNotifySeatOf_DBErrorSurfaces(t *testing.T) {
	s := openTestStore(t)
	s.db.Close()
	if _, err := s.NotifySeatOf("lead-1"); err == nil {
		t.Fatal("a closed db must be an error, never none")
	}
}

// The lead's label follows the App's labelOf: title, else the address's name part, else the ref.
func TestLeadLabel(t *testing.T) {
	for _, c := range []struct{ title, address, ref, want string }{
		{"資源線", "mlab/purdex-1f-vq", "_abc123", "資源線"},
		{"", "mlab/purdex-1f-vq", "_abc123", "purdex-1f-vq"},
		{"", "purdex-1f-vq", "_abc123", "purdex-1f-vq"},
		{"", "", "_abc123", "_abc123"},
		{"", "mlab/", "_abc123", "_abc123"},
	} {
		if got := team.LeadLabel(c.title, c.address, c.ref); got != c.want {
			t.Errorf("LeadLabel(%q,%q,%q) = %q, want %q", c.title, c.address, c.ref, got, c.want)
		}
	}
}

// NotifyGroupOf: team_name first; with none the lead's live label (the registry's title), else what was recorded.
func TestNotifyGroupOf(t *testing.T) {
	f := newFixture(t)
	s := f.m.store
	seedTeam(t, s, "team-1", "lead-1", 1000)
	seedMember(t, s, "op-a", "team-1", "m-a", 1000)
	f.origins.shown = map[string]team.Origin{"lead-1": {SessionID: "lead-1", Ref: "_abc123", Title: "Live lead", Address: "mlab/live-lead"}}

	if got, err := f.m.NotifyGroupOf("m-a"); err != nil || got != "Live lead" {
		t.Fatalf("unnamed team, live lead = %q err=%v", got, err)
	}
	f.origins.shown = map[string]team.Origin{"lead-1": {SessionID: "lead-1", Ref: "_abc123", Address: "mlab/live-lead"}}
	if got, _ := f.m.NotifyGroupOf("lead-1"); got != "live-lead" {
		t.Fatalf("live lead with no title = %q", got)
	}
	f.origins.hidden = map[string]bool{"lead-1": true} // offline: the ref recorded with the team
	if got, _ := f.m.NotifyGroupOf("lead-1"); got != "_abc123" {
		t.Fatalf("offline lead = %q", got)
	}
	setTeamName(t, s, "Purdex A team")
	if got, _ := f.m.NotifyGroupOf("m-a"); got != "Purdex A team" {
		t.Fatalf("named team = %q", got)
	}
	if got, err := f.m.NotifyGroupOf("nobody"); err != nil || got != "" {
		t.Fatalf("no seat = %q err=%v", got, err)
	}
	seedRemote(t, s, "mk-1", "remote-m", 1000)
	if got, _ := f.m.NotifyGroupOf("remote-m"); got != "T" {
		t.Fatalf("remote member = %q", got)
	}
	if _, err := s.db.Exec(`UPDATE remote_members SET team_name = '' WHERE mk = 'mk-1'`); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.m.NotifyGroupOf("remote-m"); got != "lead" {
		t.Fatalf("remote member of an unnamed team = %q (the lead's recorded title)", got)
	}
}

// The module hands out the grouper under its registry key.
func TestNotifyGroupOf_RegisteredByTheModule(t *testing.T) {
	f := newFixture(t)
	svc, ok := f.core.Registry.Get(team.NotifyGroupKey)
	if !ok {
		t.Fatalf("no service under %q", team.NotifyGroupKey)
	}
	if _, ok := svc.(team.NotifyGrouper); !ok {
		t.Fatalf("%T is not a team.NotifyGrouper", svc)
	}
}
