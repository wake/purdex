// internal/module/team/pause_handler_test.go — GET /api/team/paused and the roster's `paused` (unattended quota pause spec §3.1, UP-1a).
package teammod

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func (f *fixture) getPaused() team.PausedView {
	f.t.Helper()
	code, body := f.do(http.MethodGet, team.PausedRoute, nil)
	if code != http.StatusOK {
		f.t.Fatalf("GET paused: %d %s", code, body)
	}
	var v team.PausedView
	if err := json.Unmarshal(body, &v); err != nil {
		f.t.Fatalf("decode: %v; %s", err, body)
	}
	return v
}

func (f *fixture) addPause(p PauseRow) {
	f.t.Helper()
	if _, err := f.m.store.AddPause(p); err != nil {
		f.t.Fatal(err)
	}
}

func TestPaused_NoneIsAnEmptyList(t *testing.T) {
	f := newFixture(t)
	code, body := f.do(http.MethodGet, team.PausedRoute, nil)
	if code != http.StatusOK || string(body) != "{\"sessions\":[]}\n" {
		t.Fatalf("%d %q", code, body)
	}
}

// A lead with a member, both paused (the member for two causes), and a solo session: the answer names each with its role,
// causes, since, the ref of the lead and the number of bounced messages; titles and refs come from the registry, else from team.db.
func TestPaused_ListsEachPausedSession(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000) // lead ref _abc123
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 2000)
	seedMember(t, f.m.store, "op-2", uid(1), "sid-m2", 2000) // not paused
	f.liveMember("sid-m1", "_mem001", "worker one", "self/w-one", "tm-1")
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Title: "boss", Address: "self/boss", PID: 10})
	f.origins.show(team.Origin{SessionID: "sid-solo", Ref: "_solo01", Title: "alone", Address: "self/alone", PID: 11})
	f.addPause(PauseRow{SessionID: "sid-1", Cause: team.PauseCauseQuota, Role: team.PauseRoleLead, TeamID: uid(1), LeadSessionID: "sid-1", OnSince: 5, Since: 100})
	f.addPause(PauseRow{SessionID: "sid-m1", Cause: team.PauseCauseLead, Role: team.PauseRoleMember, TeamID: uid(1), LeadSessionID: "sid-1", OnSince: 5, Since: 100})
	f.addPause(PauseRow{SessionID: "sid-m1", Cause: team.PauseCauseQuota, Role: team.PauseRoleMember, TeamID: uid(1), LeadSessionID: "sid-1", OnSince: 5, Since: 300})
	f.addPause(PauseRow{SessionID: "sid-solo", Cause: team.PauseCauseQuota, Role: team.PauseRoleSolo, OnSince: 5, Since: 50})
	if _, err := f.m.store.db.Exec(`INSERT INTO pause_bounces (session_id, from_addr, text_hash, at, first_line) VALUES ('sid-m1', 'a', 'h1', 1, 'x')`); err != nil {
		t.Fatal(err)
	}

	got := f.getPaused()
	want := team.PausedView{Sessions: []team.PausedSession{
		{SessionID: "sid-solo", Ref: "_solo01", Title: "alone", Role: team.PauseRoleSolo, Causes: []string{"quota"}, Since: 50},
		{SessionID: "sid-1", Ref: "_abc123", Title: "boss", Role: team.PauseRoleLead, Causes: []string{"quota"}, Since: 100, LeadRef: "_abc123"},
		{SessionID: "sid-m1", Ref: "_mem001", Title: "worker one", Role: team.PauseRoleMember, Causes: []string{"lead", "quota"}, Since: 100, LeadRef: "_abc123", Bounced: 1},
	}}
	if !reflect.DeepEqual(got, want) {
		gj, _ := json.Marshal(got)
		wj, _ := json.Marshal(want)
		t.Fatalf("paused = %s\nwant     %s", gj, wj)
	}
}

// A solo session has no team row to fall back on: the ref and title recorded when it was paused answer when the registry does
// not list it (a registry that cannot be read, or a session that left). The registry's live values win while it lists it.
// Mutation gate: no fallback to the recorded values -> blanks (red); the recorded values over the live ones -> the stale title (red).
func TestPaused_ASoloSessionKeepsTheRefAndTitleRecordedAtThePause(t *testing.T) {
	f := newFixture(t)
	f.addPause(PauseRow{SessionID: "sid-solo", Cause: team.PauseCauseQuota, Role: team.PauseRoleSolo, Ref: "_solo01", Title: "alone then", OnSince: 5, Since: 50})
	got := f.getPaused()
	if len(got.Sessions) != 1 || got.Sessions[0].Ref != "_solo01" || got.Sessions[0].Title != "alone then" {
		t.Fatalf("not in the registry: %+v", got)
	}
	f.origins.show(team.Origin{SessionID: "sid-solo", Ref: "_solo01", Title: "alone now", Address: "self/alone", PID: 11})
	got = f.getPaused()
	if len(got.Sessions) != 1 || got.Sessions[0].Ref != "_solo01" || got.Sessions[0].Title != "alone now" {
		t.Fatalf("the registry lists it: %+v (live values first)", got)
	}
}

// A session the registry no longer lists keeps the ref and title team.db has for it.
func TestPaused_FallsBackToWhatTeamDBStored(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 2000) // ref "_memop-1", title "worker"
	f.addPause(PauseRow{SessionID: "sid-m1", Cause: team.PauseCauseLead, Role: team.PauseRoleMember, TeamID: uid(1), LeadSessionID: "sid-1", OnSince: 5, Since: 100})
	got := f.getPaused()
	if len(got.Sessions) != 1 || got.Sessions[0].Ref != "_memop-1" || got.Sessions[0].Title != "worker" || got.Sessions[0].LeadRef != "_abc123" {
		t.Fatalf("paused = %+v", got)
	}
}

// Every roster session has `paused`: the causes and since for a paused lead / member, null for the others.
func TestRoster_ShowsThePauseOfEachSession(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 2000)
	seedMember(t, f.m.store, "op-2", uid(1), "sid-m2", 3000)
	f.addPause(PauseRow{SessionID: "sid-1", Cause: team.PauseCauseQuota, Role: team.PauseRoleLead, TeamID: uid(1), LeadSessionID: "sid-1", OnSince: 5, Since: 100})
	f.addPause(PauseRow{SessionID: "sid-m1", Cause: team.PauseCauseLead, Role: team.PauseRoleMember, TeamID: uid(1), LeadSessionID: "sid-1", OnSince: 5, Since: 100})
	f.addPause(PauseRow{SessionID: "sid-m1", Cause: team.PauseCauseQuota, Role: team.PauseRoleMember, TeamID: uid(1), LeadSessionID: "sid-1", OnSince: 5, Since: 300})

	r := f.getRoster()
	if len(r.Teams) != 1 || len(r.Teams[0].Members) != 2 {
		t.Fatalf("roster = %+v", r)
	}
	tr := r.Teams[0]
	if !reflect.DeepEqual(tr.Lead.Paused, &team.RosterPause{Causes: []string{"quota"}, Since: 100}) {
		t.Errorf("lead paused = %+v", tr.Lead.Paused)
	}
	if !reflect.DeepEqual(tr.Members[0].Paused, &team.RosterPause{Causes: []string{"lead", "quota"}, Since: 100}) {
		t.Errorf("paused member = %+v", tr.Members[0].Paused)
	}
	if tr.Members[1].Paused != nil {
		t.Errorf("a member that is not paused shows %+v", tr.Members[1].Paused)
	}
	// on the wire the field is always there, null when not paused
	_, body := f.do(http.MethodGet, RosterRoute, nil)
	var raw struct {
		Teams []struct {
			Members []map[string]json.RawMessage `json:"members"`
		} `json:"teams"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if v, ok := raw.Teams[0].Members[1]["paused"]; !ok || string(v) != "null" {
		t.Errorf("an unpaused member's paused on the wire: %q present=%v", v, ok)
	}
}

// Codex R1 (P2): a paused member whose row the lead has since moved to killing / releasing / killed still shows the ref and
// title team.db has for it, not blanks. Mutation gate: require the row to be active again -> blanks (red).
func TestPaused_AMemberLeavingActiveKeepsItsStoredRefAndTitle(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 2000)
	f.addPause(PauseRow{SessionID: "sid-m1", Cause: team.PauseCauseLead, Role: team.PauseRoleMember, TeamID: uid(1), LeadSessionID: "sid-1", OnSince: 5, Since: 100})
	for _, st := range []team.MemberState{team.MemberKilling, team.MemberReleasing, team.MemberKilled} {
		if err := f.m.store.SetMemberState("op-1", st, 3000); err != nil {
			t.Fatal(err)
		}
		got := f.getPaused()
		if len(got.Sessions) != 1 || got.Sessions[0].Ref != "_memop-1" || got.Sessions[0].Title != "worker" {
			t.Fatalf("member %s: paused = %+v", st, got)
		}
	}
}
