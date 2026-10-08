package teammod

import (
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// The roster carries each live team's current name, "" for a team without
// one. Mutation gate: drop `TeamName:` in the roster build → red.
func TestRoster_ListsTheTeamName(t *testing.T) {
	f := newFixture(t)
	f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamName = "build" }
	f.approveLead(uid(1))
	seedTeam(t, f.m.store, uid(2), "sid-2", 5000) // no name

	r := f.getRoster()
	if len(r.Teams) != 2 {
		t.Fatalf("roster = %+v, want 2 teams", r)
	}
	byID := map[string]team.TeamRoster{}
	for _, tr := range r.Teams {
		byID[tr.ID] = tr
	}
	if got := byID[uid(1)].TeamName; got != "build" {
		t.Errorf("named team in the roster = %q, want build", got)
	}
	if got := byID[uid(2)].TeamName; got != "" {
		t.Errorf("unnamed team in the roster = %q, want none", got)
	}
}

// GET /api/team serves the lead's team with its current name, from the
// teams.team_name column. (A member gets 409 not_lead there, as before.)
func TestTeamGet_ShowsTheTeamName(t *testing.T) {
	f := newFixture(t)
	f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamName = "build" }
	f.create(uid(1))
	code, body := f.decideRaw(uid(1), `{"decision":"approve","grant":{"team_name":"renamed"},`+nameClient+`}`)
	if code != http.StatusOK {
		t.Fatalf("approve: %d %s", code, body)
	}
	code, v, e := f.teamView("/tmp/10.sock")
	if code != http.StatusOK || v.Team.TeamName != "renamed" || v.Team.Grant.TeamName == nil || *v.Team.Grant.TeamName != "renamed" {
		t.Fatalf("GET /api/team = %d %+v %+v, want team_name renamed", code, v.Team, e)
	}
	// The wire always has the key, even for an unnamed team.
	g := newFixture(t)
	g.approveLead(uid(1))
	_, raw := g.do(http.MethodGet, "/api/team?origin_inbox=%2Ftmp%2F10.sock", "")
	if s := string(raw); !strings.Contains(s, `"team_name":""`) {
		t.Fatalf("unnamed team body = %s, want team_name \"\" present", s)
	}
}
