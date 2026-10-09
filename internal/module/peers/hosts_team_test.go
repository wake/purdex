// internal/module/peers/hosts_team_test.go
package peers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/config"
)

type teamRow struct {
	AllowTeam *bool     `json:"allow_team"`
	TeamRoots *[]string `json:"team_roots"`
}

func realDir(t *testing.T, name string) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(base, name)
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestHostsList_RowsCarryTeamFieldsNeverNull(t *testing.T) {
	hosts := []config.PeerHost{
		{Alias: "a", URL: "https://a.example", AllowTeam: true, TeamRoots: []string{"/srv/x"}},
		{Alias: "b", URL: "https://b.example"},
	}
	c, _ := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers/hosts", nil, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var env struct {
		Hosts []teamRow `json:"hosts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	rows := env.Hosts
	if rows[0].AllowTeam == nil || !*rows[0].AllowTeam || !reflect.DeepEqual(*rows[0].TeamRoots, []string{"/srv/x"}) {
		t.Fatalf("row a = %+v", rows[0])
	}
	if rows[1].AllowTeam == nil || *rows[1].AllowTeam || rows[1].TeamRoots == nil || len(*rows[1].TeamRoots) != 0 {
		t.Fatalf("row b = %+v (team_roots must be [] not null)", rows[1])
	}
}

func TestHandlePutHost_TeamFields(t *testing.T) {
	r1, r2 := realDir(t, "one"), realDir(t, "two")
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", HostID: "air:1", InboundToken: "inbound-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	put := func(body map[string]any) (int, teamRow, string) {
		rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", body, adminPrincipal())
		var row teamRow
		_ = json.Unmarshal(rr.Body.Bytes(), &row)
		return rr.Code, row, rr.Body.String()
	}

	// Turning it on needs no roots; the row comes back updated.
	code, row, body := put(map[string]any{"allow_team": true})
	if code != 200 || !*row.AllowTeam || len(*row.TeamRoots) != 0 {
		t.Fatalf("on: %d %s", code, body)
	}
	// Roots are set (a symlink-free canonical form comes back); allow_team untouched.
	code, row, body = put(map[string]any{"team_roots": []string{r1 + "/", r2}})
	if code != 200 || !*row.AllowTeam || !reflect.DeepEqual(*row.TeamRoots, []string{r1, r2}) {
		t.Fatalf("roots: %d %s", code, body)
	}
	// Omitted = unchanged, even on an unrelated PUT.
	code, row, body = put(map[string]any{"allow_bypass": true})
	if code != 200 || !*row.AllowTeam || len(*row.TeamRoots) != 2 {
		t.Fatalf("unrelated: %d %s", code, body)
	}
	// Off keeps the roots; [] clears them.
	if code, row, body = put(map[string]any{"allow_team": false}); code != 200 || *row.AllowTeam || len(*row.TeamRoots) != 2 {
		t.Fatalf("off: %d %s", code, body)
	}
	if code, row, body = put(map[string]any{"team_roots": []string{}}); code != 200 || len(*row.TeamRoots) != 0 {
		t.Fatalf("clear: %d %s", code, body)
	}
	got := loadCfg(t, cfgPath).Peers.Hosts[0]
	if got.AllowTeam || len(got.TeamRoots) != 0 {
		t.Fatalf("persisted = %+v", got)
	}
}

func TestHandlePutHost_BadRootNamesTheRoot(t *testing.T) {
	file := filepath.Join(realDir(t, "d"), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	good := realDir(t, "good")
	cases := []struct{ name, root, detail string }{
		{"relative", "rel/dir", "not_absolute"},
		{"tilde", "~/work", "not_absolute"},
		{"missing", filepath.Join(good, "nope"), "not_found"},
		{"file", file, "not_a_directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", HostID: "air:1", InboundToken: "i", TeamRoots: []string{"/keep"}}}
			c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
			m := newHostsTestModule(t, c, failIfCalledFetch(t))
			rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air",
				map[string]any{"allow_team": true, "team_roots": []string{good, tc.root}}, adminPrincipal())
			var e struct{ Error, Root, Detail string }
			_ = json.Unmarshal(rr.Body.Bytes(), &e)
			if rr.Code != 400 || e.Error != "bad_root" || e.Root != tc.root || e.Detail != tc.detail {
				t.Fatalf("%d %s", rr.Code, rr.Body.String())
			}
			// Nothing was applied, not even allow_team.
			if h := loadCfg(t, cfgPath).Peers.Hosts[0]; h.AllowTeam || !reflect.DeepEqual(h.TeamRoots, []string{"/keep"}) {
				t.Fatalf("persisted = %+v", h)
			}
		})
	}
}

// A consent for one peer must never land on a different entry that took the
// alias meanwhile (codex R1) — even one re-created at the same URL.
func TestHandlePutHost_TeamFieldsNotAppliedToRecreatedEntry(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", HostID: "air:1", InboundToken: "inbound-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	m.putHostAfterSnapshot = func() {
		// Same alias, URL and even host id — only the minted inbound token differs.
		err := c.UpdateConfig(func(cfg *config.Config) error {
			cfg.Peers.Hosts[0].InboundToken = "inbound-new"
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{"allow_team": true}, adminPrincipal())
	if rr.Code != http.StatusConflict {
		t.Fatalf("PUT = %d, want 409: %s", rr.Code, rr.Body.String())
	}
	if h := loadCfg(t, cfgPath).Peers.Hosts[0]; h.AllowTeam {
		t.Fatalf("consent landed on the re-created entry: %+v", h)
	}
}

// Consent is for a verified host: an entry that has not learned its host id
// cannot be switched on, or whoever verifies first would inherit it (codex attack).
func TestHandlePutHost_AllowTeamNeedsVerifiedEntry(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "i"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{"allow_team": true}, adminPrincipal())
	var e struct{ Error string }
	_ = json.Unmarshal(rr.Body.Bytes(), &e)
	if rr.Code != http.StatusConflict || e.Error != "host_unverified" {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	if loadCfg(t, cfgPath).Peers.Hosts[0].AllowTeam {
		t.Fatal("consent stored on an unverified entry")
	}
	// Turning it off, and setting roots (inert without consent), stay possible.
	rr = doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{"allow_team": false}, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("off: %d %s", rr.Code, rr.Body.String())
	}
}

func TestHandlePutHost_TooManyRoots(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "i"}}
	c, _ := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	roots := make([]string, config.MaxTeamRoots+1)
	for i := range roots {
		roots[i] = "/"
	}
	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{"team_roots": roots}, adminPrincipal())
	var e struct{ Error, Detail string }
	_ = json.Unmarshal(rr.Body.Bytes(), &e)
	if rr.Code != 400 || e.Error != "bad_root" || e.Detail != "too_many" {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
}
