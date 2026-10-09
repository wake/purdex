// internal/module/peers/team_caps_test.go
package peers

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
)

func hostPrincipalID(alias, hostID string) *middleware.Principal {
	p := middleware.Principal{Kind: middleware.PrincipalHost, Alias: alias, HostID: hostID}
	return &p
}

func teamHosts() []config.PeerHost {
	return []config.PeerHost{
		{Alias: "lead", URL: "https://l.example", HostID: "lead:1", InboundToken: "i1", AllowTeam: true, TeamRoots: []string{"/srv/a", "/srv/b"}},
		{Alias: "other", URL: "https://o.example", HostID: "other:1", InboundToken: "i2"},
	}
}

// wantTeamKinds is written out, not derived from teamKinds(): taking one away (or announcing spawn before X4a applies
// it) must fail here.
var wantTeamKinds = []string{"adopt", "release", "kill", "spawn", "end", "lead_moved", "void"}

func TestInventory_TeamCapsPerPrincipal(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", teamHosts())
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	cases := []struct {
		name string
		p    *middleware.Principal
		want bool
	}{
		{"allowed host", hostPrincipalID("lead", "lead:1"), true},
		{"host with team off", hostPrincipalID("other", "other:1"), false},
		{"admin", adminPrincipal(), false},
		{"stale host id", hostPrincipalID("lead", "someone-else:9"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, tc.p)
			var env struct {
				Team *struct {
					Kinds     []string `json:"kinds"`
					AllowTeam bool     `json:"allow_team"`
				} `json:"team"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil || env.Team == nil {
				t.Fatalf("%d %s", rr.Code, rr.Body.String())
			}
			// Every principal reads the same kinds (X3d-3): what this daemon applies, which is everything this version applies.
			if !reflect.DeepEqual(env.Team.Kinds, wantTeamKinds) || env.Team.AllowTeam != tc.want {
				t.Fatalf("team = %+v", env.Team)
			}
		})
	}
}

func TestTeamRoots_Route(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", teamHosts())
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	get := func(p *middleware.Principal) (int, map[string]any) {
		rr := doHostsRequest(t, m, http.MethodGet, "/api/peers/team/roots", nil, p)
		var body map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &body)
		return rr.Code, body
	}

	code, body := get(hostPrincipalID("lead", "lead:1"))
	if code != 200 || body["host_id"] != "local:1" || !reflect.DeepEqual(body["roots"], []any{"/srv/a", "/srv/b"}) {
		t.Fatalf("allowed: %d %v", code, body)
	}
	for name, tc := range map[string]struct {
		p    *middleware.Principal
		want string
	}{
		"team off":     {hostPrincipalID("other", "other:1"), "host_not_allowed"},
		"admin":        {adminPrincipal(), "admin_not_allowed"},
		"unverified":   {hostPrincipalID("lead", ""), "host_unverified"},
		"entry moved":  {hostPrincipalID("lead", "someone-else:9"), "host_unverified"},
		"unknown host": {hostPrincipalID("gone", "gone:1"), "host_unverified"},
	} {
		code, body := get(tc.p)
		if code != 403 || body["error"] != tc.want {
			t.Fatalf("%s: %d %v", name, code, body)
		}
	}
}

func TestTeamRoots_AllowedWithNoRootsIsEmptyList(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "lead", URL: "https://l.example", HostID: "lead:1", InboundToken: "i1", AllowTeam: true}}
	c, _ := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers/team/roots", nil, hostPrincipalID("lead", "lead:1"))
	var body struct {
		Roots []string `json:"roots"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if rr.Code != 200 || body.Roots == nil || len(body.Roots) != 0 {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
}
