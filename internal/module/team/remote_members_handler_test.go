// internal/module/team/remote_members_handler_test.go
package teammod

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func remoteMembersOf(t *testing.T, f *fixture) []team.RemoteMemberView {
	t.Helper()
	code, body := f.do(http.MethodGet, team.RemoteMembersRoute, nil)
	if code != http.StatusOK {
		t.Fatalf("GET: %d %s", code, body)
	}
	var out team.RemoteMembersResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if out.Members == nil {
		t.Fatalf("members is null in %s", body)
	}
	return out.Members
}

func TestRemoteMembersGet_ListsLiveRowsOldestFirstWithTheLeadAlias(t *testing.T) {
	f := newFixture(t)
	f.setLeadHost(true) // alias "lead", host id "lead:1"
	if got := remoteMembersOf(t, f); len(got) != 0 {
		t.Fatalf("empty = %+v", got)
	}
	// created_at decides the order, not the insert order or the key.
	for _, r := range []remoteMemberRow{
		newRemote("mk-b", "sid-b", "lead:1", 3000),
		newRemote("mk-a", "sid-a", "lead:1", 1000),
		newRemote("mk-gone-host", "sid-g", "unpaired-host", 2000),
		newRemote("mk-released", "sid-r", "lead:1", 500),
	} {
		if err := f.m.store.InsertRemoteMember(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.m.store.SetRemoteMemberState("mk-released", []string{remoteActive}, remoteReleased, 600); err != nil {
		t.Fatal(err)
	}
	got := remoteMembersOf(t, f)
	if len(got) != 3 || got[0].MK != "mk-a" || got[1].MK != "mk-gone-host" || got[2].MK != "mk-b" {
		t.Fatalf("members = %+v, want mk-a, mk-gone-host, mk-b (live only, oldest first)", got)
	}
	a := got[0]
	if a.MemberSessionID != "sid-a" || a.Ref != "_rmk-a" || a.TeamID != "team-L" || a.TeamName != "T" || a.LeadHostID != "lead:1" ||
		a.LeadAlias != "lead" || a.LeadAddress != "lead/x [lead01]" || a.Origin != "adopted" || a.State != "active" || a.CreatedAt != 1000 || a.Cwd != "/w" {
		t.Fatalf("row = %+v", a)
	}
	if got[1].LeadAlias != "" {
		t.Fatalf("an unpaired lead host has alias %q, want empty", got[1].LeadAlias)
	}
}

func TestRemoteMembersEnd_EndsWithItsFactAndNotice(t *testing.T) {
	f := newFixture(t)
	seedRemote(t, f.m.store, "mk-1", "sid-1", 1000)
	code, body := f.do(http.MethodPost, team.RemoteMembersEndRoute, team.RemoteMemberEndRequest{MK: "mk-1"})
	var out team.RemoteMemberEndResponse
	if err := json.Unmarshal(body, &out); err != nil || code != http.StatusOK || out.MK != "mk-1" || out.State != "ended" {
		t.Fatalf("%d %s (%v)", code, body, err)
	}
	if row, _, _ := f.m.store.RemoteMember("mk-1"); row.State != remoteEnded {
		t.Fatalf("row = %+v", row)
	}
	if role, _ := f.m.store.SessionRole("sid-1"); role != sessionRoleNone {
		t.Fatalf("role = %s", role)
	}
	facts := factsOf(t, f.m.store, "host-L")
	if len(facts) != 1 || decodeFact(t, facts[0]).Reason != team.FactReasonLocalEnd {
		t.Fatalf("facts = %+v", facts)
	}
	if n := noticesOf(t, f.m.store, "mk-1"); len(n) != 1 || n[0].Kind != noticeLocalEnd {
		t.Fatalf("notices = %+v", n)
	}
	if got := remoteMembersOf(t, f); len(got) != 0 {
		t.Fatalf("an ended member is still listed: %+v", got)
	}
}

func TestRemoteMembersEnd_Refusals(t *testing.T) {
	f := newFixture(t)
	seedRemote(t, f.m.store, "mk-1", "sid-1", 1000)
	if _, err := f.m.store.SetRemoteMemberState("mk-1", []string{remoteActive}, remoteReleased, 1500); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		body   any
		status int
		code   string
	}{
		"no mk":       {team.RemoteMemberEndRequest{}, http.StatusBadRequest, team.ErrBadRequest},
		"not json":    {"{nope", http.StatusBadRequest, team.ErrBadRequest},
		"unknown":     {team.RemoteMemberEndRequest{MK: "mk-nope"}, http.StatusNotFound, "not_found"},
		"not live":    {team.RemoteMemberEndRequest{MK: "mk-1"}, http.StatusConflict, "not_live"},
		"mk too long": {team.RemoteMemberEndRequest{MK: string(make([]byte, 300))}, http.StatusBadRequest, team.ErrBadRequest},
	} {
		code, body := f.do(http.MethodPost, team.RemoteMembersEndRoute, tc.body)
		var e team.RemoteMemberEndError
		_ = json.Unmarshal(body, &e)
		if code != tc.status || e.Error != tc.code {
			t.Fatalf("%s: %d %s, want %d %s", name, code, body, tc.status, tc.code)
		}
		if tc.code == "not_live" && e.State != remoteReleased {
			t.Fatalf("not_live carries state %q, want released", e.State)
		}
	}
	if len(factsOf(t, f.m.store, "host-L")) != 0 {
		t.Fatal("a refused end wrote a fact")
	}
}
