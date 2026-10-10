// internal/module/peers/hosts_team_roots_test.go
package peers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/config"
)

// #2340: team_roots has a revision and atomic add / remove. Two writers on a stale snapshot can no longer silently drop an
// authorised root or bring back a revoked one; an older client (no revision, whole-set team_roots) behaves as before.

type rootsRow struct {
	Error    string    `json:"error"`
	Roots    *[]string `json:"team_roots"`
	Rev      *int64    `json:"team_roots_rev"`
	Detail   string    `json:"detail"`
	BadRoot  string    `json:"root"`
	AllowTmp *bool     `json:"allow_team"`
}

type rootsFixture struct {
	t       *testing.T
	m       *Module
	cfgPath string
}

func newRootsFixture(t *testing.T, roots ...string) *rootsFixture {
	t.Helper()
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", HostID: "air:1", InboundToken: "inbound-a", AllowTeam: true, TeamRoots: roots}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	return &rootsFixture{t: t, m: newHostsTestModule(t, c, failIfCalledFetch(t)), cfgPath: cfgPath}
}

func (f *rootsFixture) put(body map[string]any) (int, rootsRow) {
	f.t.Helper()
	rr := doHostsRequest(f.t, f.m, http.MethodPut, "/api/peers/hosts/air", body, adminPrincipal())
	var row rootsRow
	if err := json.Unmarshal(rr.Body.Bytes(), &row); err != nil {
		f.t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	return rr.Code, row
}

func (f *rootsFixture) stored() config.PeerHost { return loadCfg(f.t, f.cfgPath).Peers.Hosts[0] }

func (f *rootsFixture) rev(row rootsRow) int64 {
	f.t.Helper()
	if row.Rev == nil {
		f.t.Fatal("the row carries no team_roots_rev")
	}
	return *row.Rev
}

func TestTeamRootsRev_StartsAtZeroAndRisesOnlyWhenTheSetChanges(t *testing.T) {
	a, b := realDir(t, "a"), realDir(t, "b")
	f := newRootsFixture(t)
	code, row := f.put(map[string]any{"allow_bypass": false})
	if code != 200 || f.rev(row) != 0 {
		t.Fatalf("fresh: %d %+v", code, row)
	}
	code, row = f.put(map[string]any{"team_roots": []string{a}})
	if code != 200 || f.rev(row) != 1 {
		t.Fatalf("set: %d rev %v", code, row.Rev)
	}
	code, row = f.put(map[string]any{"team_roots": []string{a}}) // the same set again
	if code != 200 || f.rev(row) != 1 {
		t.Fatalf("same set: rev %v, want 1", row.Rev)
	}
	code, row = f.put(map[string]any{"team_roots": []string{a, b}})
	if f.rev(row) != 2 {
		t.Fatalf("grown: rev %v, want 2", row.Rev)
	}
	// an unrelated PUT leaves it alone
	code, row = f.put(map[string]any{"allow_bypass": true})
	if code != 200 || f.rev(row) != 2 {
		t.Fatalf("unrelated: rev %v", row.Rev)
	}
	if got := f.stored().TeamRootsRev; got != 2 {
		t.Fatalf("persisted rev = %d", got)
	}
	// and the list serves it
	rr := doHostsRequest(t, f.m, http.MethodGet, "/api/peers/hosts", nil, adminPrincipal())
	var env struct {
		Hosts []rootsRow `json:"hosts"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if len(env.Hosts) != 1 || env.Hosts[0].Rev == nil || *env.Hosts[0].Rev != 2 {
		t.Fatalf("list: %s", rr.Body.String())
	}
}

// Two writers on one snapshot: the second whole-set write names the revision it read and is refused with the current set.
// Mutation gate: ignore the revision → the stale write wins and drops b (red).
func TestTeamRootsRev_AStaleWholeSetWriteIsRefusedWithTheCurrentSet(t *testing.T) {
	a, b, c := realDir(t, "a"), realDir(t, "b"), realDir(t, "c")
	f := newRootsFixture(t, a)
	snapshot := int64(0) // both writers read rev 0 and {a}
	_, row := f.put(map[string]any{"team_roots": []string{a, b}, "team_roots_rev": snapshot})
	if f.rev(row) != 1 {
		t.Fatalf("first writer: %+v", row)
	}
	code, row := f.put(map[string]any{"team_roots": []string{a, c}, "team_roots_rev": snapshot})
	if code != http.StatusConflict || row.Error != "team_roots_conflict" || row.Roots == nil || !reflect.DeepEqual(*row.Roots, []string{a, b}) || f.rev(row) != 1 {
		t.Fatalf("stale write: %d %+v", code, row)
	}
	if got := f.stored(); !reflect.DeepEqual(got.TeamRoots, []string{a, b}) || got.TeamRootsRev != 1 {
		t.Fatalf("the stale write changed the config: %+v", got)
	}
	// re-reading and writing with the current revision works
	code, row = f.put(map[string]any{"team_roots": []string{a, b, c}, "team_roots_rev": 1})
	if code != 200 || f.rev(row) != 2 {
		t.Fatalf("with the current rev: %d %+v", code, row)
	}
}

// An older client sends no revision: the whole-set write is applied as it always was.
func TestTeamRootsRev_NoRevisionKeepsTheOldBehaviour(t *testing.T) {
	a, b, c := realDir(t, "a"), realDir(t, "b"), realDir(t, "c")
	f := newRootsFixture(t, a)
	f.put(map[string]any{"team_roots": []string{a, b}})
	code, row := f.put(map[string]any{"team_roots": []string{c}})
	if code != 200 || !reflect.DeepEqual(*row.Roots, []string{c}) {
		t.Fatalf("no rev: %d %+v", code, row)
	}
}

func TestTeamRootsAddRemove_TwoWritersOnOneSnapshotDoNotOverwriteEachOther(t *testing.T) {
	a, b, c := realDir(t, "a"), realDir(t, "b"), realDir(t, "c")
	f := newRootsFixture(t, a)
	_, r1 := f.put(map[string]any{"add_team_roots": []string{b}})
	code, r2 := f.put(map[string]any{"add_team_roots": []string{c}})
	if code != 200 || !reflect.DeepEqual(*r2.Roots, []string{a, b, c}) || f.rev(r1) != 1 || f.rev(r2) != 2 {
		t.Fatalf("adds: %d %+v / %+v", code, r1, r2)
	}
	// a removal does not bring back or drop anything else
	code, r3 := f.put(map[string]any{"remove_team_roots": []string{b}})
	if code != 200 || !reflect.DeepEqual(*r3.Roots, []string{a, c}) || f.rev(r3) != 3 {
		t.Fatalf("remove: %d %+v", code, r3)
	}
	if got := f.stored(); !reflect.DeepEqual(got.TeamRoots, []string{a, c}) || got.TeamRootsRev != 3 {
		t.Fatalf("persisted: %+v", got)
	}
}

func TestTeamRootsAddRemove_NoOpsDoNotBumpTheRevision(t *testing.T) {
	a, b := realDir(t, "a"), realDir(t, "b")
	f := newRootsFixture(t, a)
	_, row := f.put(map[string]any{"add_team_roots": []string{a}}) // already there
	if f.rev(row) != 0 {
		t.Fatalf("adding a present root: rev %v", row.Rev)
	}
	_, row = f.put(map[string]any{"remove_team_roots": []string{b}}) // not there
	if f.rev(row) != 0 || !reflect.DeepEqual(*row.Roots, []string{a}) {
		t.Fatalf("removing an absent root: %+v", row)
	}
}

// Removal does not need the directory to exist: a root whose directory is gone must still be revocable. A symlinked
// spelling removes the canonical root it points at.
func TestTeamRootsRemove_WorksForAGoneDirectoryAndASymlinkSpelling(t *testing.T) {
	gone, kept := realDir(t, "gone"), realDir(t, "kept")
	f := newRootsFixture(t, gone, kept)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	code, row := f.put(map[string]any{"remove_team_roots": []string{gone + "/"}})
	if code != 200 || !reflect.DeepEqual(*row.Roots, []string{kept}) {
		t.Fatalf("gone dir: %d %+v", code, row)
	}
	link := kept + "-link"
	if err := os.Symlink(kept, link); err != nil {
		t.Fatal(err)
	}
	code, row = f.put(map[string]any{"remove_team_roots": []string{link}})
	if code != 200 || len(*row.Roots) != 0 {
		t.Fatalf("symlink spelling: %d %+v", code, row)
	}
}

func TestTeamRootsAdd_IsValidatedLikeTheWholeSet(t *testing.T) {
	a, b := realDir(t, "a"), realDir(t, "b")
	f := newRootsFixture(t, a)
	for name, c := range map[string]struct {
		body   map[string]any
		status int
		code   string
	}{
		"relative add":        {map[string]any{"add_team_roots": []string{"rel/dir"}}, 400, "bad_root"},
		"missing add":         {map[string]any{"add_team_roots": []string{b + "/nope"}}, 400, "bad_root"},
		"relative remove":     {map[string]any{"remove_team_roots": []string{"rel/dir"}}, 400, "bad_root"},
		"whole set plus add":  {map[string]any{"team_roots": []string{a}, "add_team_roots": []string{b}}, 400, ""},
		"whole set plus drop": {map[string]any{"team_roots": []string{a}, "remove_team_roots": []string{a}}, 400, ""},
	} {
		code, row := f.put(c.body)
		if code != c.status || (c.code != "" && row.Error != c.code) {
			t.Errorf("%s: %d %+v", name, code, row)
		}
	}
	if got := f.stored(); !reflect.DeepEqual(got.TeamRoots, []string{a}) || got.TeamRootsRev != 0 {
		t.Fatalf("a refused request changed the config: %+v", got)
	}
	// the bound: 16 roots at most
	var many []string
	for i := 0; i < config.MaxTeamRoots; i++ {
		many = append(many, realDir(t, fmt.Sprintf("m%d", i)))
	}
	if code, row := f.put(map[string]any{"add_team_roots": many}); code != 400 || row.Detail != "too_many" {
		t.Fatalf("over the bound: %d %+v", code, row)
	}
}

func TestTeamRootsAddRemove_OneRequestRemovesThenAdds(t *testing.T) {
	a, b, c := realDir(t, "a"), realDir(t, "b"), realDir(t, "c")
	f := newRootsFixture(t, a, b)
	code, row := f.put(map[string]any{"remove_team_roots": []string{a}, "add_team_roots": []string{c}})
	if code != 200 || !reflect.DeepEqual(*row.Roots, []string{b, c}) || f.rev(row) != 1 {
		t.Fatalf("%d %+v", code, row)
	}
}

// A revision may also guard an add / remove.
func TestTeamRootsAddRemove_ARevisionIsAPrecondition(t *testing.T) {
	a, b := realDir(t, "a"), realDir(t, "b")
	f := newRootsFixture(t, a)
	f.put(map[string]any{"add_team_roots": []string{b}}) // rev 1
	code, row := f.put(map[string]any{"remove_team_roots": []string{a}, "team_roots_rev": 0})
	if code != http.StatusConflict || row.Error != "team_roots_conflict" {
		t.Fatalf("%d %+v", code, row)
	}
	if code, row = f.put(map[string]any{"remove_team_roots": []string{a}, "team_roots_rev": 1}); code != 200 {
		t.Fatalf("with the current rev: %d %+v", code, row)
	}
}

// Many concurrent adds under -race: every one lands (the read and the write are one transaction of the config lock).
// Mutation gate: read the roots outside UpdateConfig → adds are lost (red).
func TestTeamRootsAdd_ConcurrentAddsAllLand(t *testing.T) {
	f := newRootsFixture(t)
	const n = 12
	var dirs []string
	for i := 0; i < n; i++ {
		dirs = append(dirs, realDir(t, fmt.Sprintf("d%d", i)))
	}
	var wg sync.WaitGroup
	for _, d := range dirs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if code, row := f.put(map[string]any{"add_team_roots": []string{d}}); code != 200 {
				t.Errorf("add %s: %d %+v", d, code, row)
			}
		}()
	}
	wg.Wait()
	got := f.stored()
	if len(got.TeamRoots) != n || got.TeamRootsRev != n {
		t.Fatalf("after %d concurrent adds: %d roots, rev %d", n, len(got.TeamRoots), got.TeamRootsRev)
	}
}
