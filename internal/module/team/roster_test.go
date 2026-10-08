package teammod

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// getRoster is GET /api/team/roster, decoded.
func (f *fixture) getRoster() team.Roster {
	f.t.Helper()
	code, body := f.do(http.MethodGet, RosterRoute, nil)
	if code != http.StatusOK {
		f.t.Fatalf("GET roster: %d %s", code, body)
	}
	var r team.Roster
	if err := json.Unmarshal(body, &r); err != nil {
		f.t.Fatalf("decode roster: %v; %s", err, body)
	}
	return r
}

// approveLead creates lead request id for sid-1 (switch off) and clicks approve.
func (f *fixture) approveLead(id string) {
	f.t.Helper()
	f.create(id)
	if code, body := f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide", appApprove(nil)); code != http.StatusOK {
		f.t.Fatalf("approve %s: %d %s", id, code, body)
	}
}

// liveMember makes the registry list sid as a member session: ref, title
// and a tmux session of its own.
func (f *fixture) liveMember(sid, ref, title, address, tmuxName string) {
	f.origins.show(team.Origin{SessionID: sid, Ref: ref, Title: title, Name: "reg-" + sid, Address: address,
		Tmux: tmuxName + ":@1.%2", PID: 42})
}

// Rule 1 / 2: live teams oldest first; each with its lead and its active
// members in join order, every session from the live registry (address,
// title, registry name, the tmux session NAME before the first colon).
func TestRoster_LiveTeamsActiveMembersWithTmuxNames(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(2), "sid-2", 500) // older, no members
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 3000) // joined second
	seedMember(t, f.m.store, "op-2", uid(1), "sid-m2", 2000) // joined first
	f.liveMember("sid-m1", "_mem001", "one", "self/w-one", "tm-0000000100")
	f.liveMember("sid-m2", "_mem002", "two", "self/w-two", "tm-0000000200")
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Name: "n10", Title: "boss", Address: "self/boss", Tmux: "mt0:@1.%1", PID: 10})

	got := f.getRoster()
	member := func(sid, ref, title, addr, tm string, at int64) team.RosterMember {
		return team.RosterMember{
			RosterSession: team.RosterSession{SessionID: sid, Ref: ref, Address: addr, Title: title, Name: "reg-" + sid, TmuxSession: tm, Live: true},
			State:         team.MemberActive, Origin: team.MemberOriginSpawned, JoinedAt: at}
	}
	want := team.Roster{Teams: []team.TeamRoster{
		{ID: uid(2), HostID: "h:1", CreatedAt: 500, // a lead the registry lists without an address: <self alias>/<ref>
			Lead:    team.RosterSession{SessionID: "sid-2", Ref: "_def456", Address: "h/_def456", Live: true},
			Members: []team.RosterMember{}},
		{ID: uid(1), HostID: "h:1", CreatedAt: 1000,
			Lead: team.RosterSession{SessionID: "sid-1", Ref: "_abc123", Address: "self/boss", Title: "boss", Name: "n10", TmuxSession: "mt0", Live: true},
			Members: []team.RosterMember{
				member("sid-m2", "_mem002", "two", "self/w-two", "tm-0000000200", 2000),
				member("sid-m1", "_mem001", "one", "self/w-one", "tm-0000000100", 3000),
			}},
	}}
	if !reflect.DeepEqual(got, want) {
		gj, _ := json.Marshal(got)
		wj, _ := json.Marshal(want)
		t.Fatalf("roster = %s\nwant     %s", gj, wj)
	}
}

// Rule 1: killed and gone members, and the members of an ended team, are
// out at once. Mutation gate: include killed members → red.
func TestRoster_KilledGoneAndEndedTeamsAreOut(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 2000)
	seedMember(t, f.m.store, "op-2", uid(1), "sid-m2", 2100)
	seedMember(t, f.m.store, "op-3", uid(1), "sid-m3", 2200)
	if err := f.m.store.SetMemberState("op-2", team.MemberKilled, 3000); err != nil {
		t.Fatal(err)
	}
	if err := f.m.store.SetMemberState("op-3", team.MemberGone, 3000); err != nil {
		t.Fatal(err)
	}
	seedTeam(t, f.m.store, uid(2), "sid-2", 1500)
	seedMember(t, f.m.store, "op-4", uid(2), "sid-m4", 2000)
	if ended, err := f.m.store.EndTeam(uid(2), "sid-2", team.TeamEndLeadGone, 3000); err != nil || !ended {
		t.Fatalf("end team: %v %v", ended, err)
	}

	got := f.getRoster()
	if len(got.Teams) != 1 || got.Teams[0].ID != uid(1) {
		t.Fatalf("teams = %+v, want only the live team", got.Teams)
	}
	if ms := got.Teams[0].Members; len(ms) != 1 || ms[0].SessionID != "sid-m1" {
		t.Fatalf("members = %+v, want only the active sid-m1", ms)
	}
}

// PL-1f′ "Origin": every member row today is spawned.
func TestRoster_OriginIsSpawned(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 2000)
	seedMember(t, f.m.store, "op-2", uid(1), "sid-m2", 2100)
	f.liveMember("sid-m2", "_mem002", "two", "self/w-two", "tm-0000000200")
	ms := f.getRoster().Teams[0].Members
	if len(ms) != 2 {
		t.Fatalf("members = %+v", ms)
	}
	for _, m := range ms {
		if m.Origin != team.MemberOriginSpawned {
			t.Errorf("member %s origin = %q, want spawned", m.SessionID, m.Origin)
		}
	}
	_, body := f.do(http.MethodGet, RosterRoute, nil)
	if n := strings.Count(string(body), `"origin":"spawned"`); n != 2 {
		t.Errorf("origin on the wire %d times in %s, want 2", n, body)
	}
}

// Rule 2: a session the registry does not list shows what team.db stored,
// live:false — a member its row's ref, title and tmux session (address
// <self alias>/<ref>), the lead teams.lead_ref and the origin its request
// recorded (address, title, name, tmux name before the colon).
func TestRoster_StoredFallbackWhenNotLive(t *testing.T) {
	f := newFixture(t)
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Name: "n10", Title: "boss", Address: "self/boss",
		Tmux: "mt0:@1.%1", PID: 10, ProcStart: "Sun Sep 13 15:22:36 2026", Cwd: "/w"})
	f.approveLead(uid(1))
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 2000) // the registry lists no sid-m1
	f.origins.hide("sid-1")

	alias, _ := f.m.selfHost()
	tm := f.getRoster().Teams[0]
	wantLead := team.RosterSession{SessionID: "sid-1", Ref: "_abc123", Address: "self/boss", Title: "boss", Name: "n10", TmuxSession: "mt0", Live: false}
	if tm.Lead != wantLead {
		t.Fatalf("lead = %+v, want %+v", tm.Lead, wantLead)
	}
	wantMember := team.RosterSession{SessionID: "sid-m1", Ref: "_memop-1", Address: alias + "/_memop-1", Title: "worker", TmuxSession: "tm-op-1", Live: false}
	if len(tm.Members) != 1 || tm.Members[0].RosterSession != wantMember {
		t.Fatalf("members = %+v, want %+v", tm.Members, wantMember)
	}
}

// GET /api/team/roster: 200 with teams [] when none, the shape of the
// event's teams otherwise; 503 not_ready when the registry cannot be read;
// 500 storage_error when team.db fails.
func TestRosterRoute_Answers(t *testing.T) {
	f := newFixture(t)
	code, body := f.do(http.MethodGet, RosterRoute, nil)
	if code != http.StatusOK || strings.TrimSpace(string(body)) != `{"teams":[]}` {
		t.Fatalf("empty: %d %s, want 200 {\"teams\":[]}", code, body)
	}
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	code, body = f.do(http.MethodGet, RosterRoute, nil)
	var raw struct {
		Teams []map[string]json.RawMessage `json:"teams"`
	}
	if err := json.Unmarshal(body, &raw); err != nil || code != http.StatusOK || len(raw.Teams) != 1 {
		t.Fatalf("one team: %d %s (%v)", code, body, err)
	}
	for _, k := range []string{"id", "host_id", "created_at", "lead", "members"} {
		if _, ok := raw.Teams[0][k]; !ok {
			t.Errorf("team has no %q: %s", k, body)
		}
	}
	if string(raw.Teams[0]["members"]) != "[]" {
		t.Errorf("members = %s, want []", raw.Teams[0]["members"])
	}

	f.origins.mu.Lock()
	f.origins.readErr = true
	f.origins.mu.Unlock()
	if code, body := f.do(http.MethodGet, RosterRoute, nil); code != http.StatusServiceUnavailable || decodeErr(t, body).Error != team.ErrNotReady {
		t.Fatalf("registry down: %d %s, want 503 not_ready", code, body)
	}
	f.origins.mu.Lock()
	f.origins.readErr = false
	f.origins.mu.Unlock()

	if err := f.m.store.Close(); err != nil {
		t.Fatal(err)
	}
	if code, body := f.do(http.MethodGet, RosterRoute, nil); code != http.StatusInternalServerError || decodeErr(t, body).Error != errStorage {
		t.Fatalf("store down: %d %s, want 500 %s", code, body, errStorage)
	}
}

// A build is ONE registry read however many sessions the teams hold: every
// lead and active member is resolved in a single ResolveOriginsBySession
// call, never one ResolveOriginBySession per session (each of those is a
// full registry read). Mutation gate: resolve per session → red.
func TestRoster_BuildResolvesOncePerBuild(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	seedTeam(t, f.m.store, uid(2), "sid-2", 1500)
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 2000)
	seedMember(t, f.m.store, "op-2", uid(1), "sid-m2", 2100)
	seedMember(t, f.m.store, "op-3", uid(2), "sid-m3", 2200)

	f.origins.mu.Lock()
	batch0, single0 := f.origins.batchCalls, f.origins.singleCalls
	f.origins.mu.Unlock()
	if n := len(f.getRoster().Teams); n != 2 {
		t.Fatalf("teams = %d, want 2", n)
	}
	f.origins.mu.Lock()
	batch, single := f.origins.batchCalls-batch0, f.origins.singleCalls-single0
	f.origins.mu.Unlock()
	if batch != 1 || single != 0 {
		t.Fatalf("two leads and three members resolved with %d batch call(s) and %d single call(s), want exactly 1 and 0", batch, single)
	}
}
