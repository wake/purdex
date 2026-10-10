package teammod

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// #2561: the App changes a live team's roots (PUT /api/team/roots).

var appClientRoots = team.Client{Kind: "app", Label: "Purdex.app @ air26"}

func (f *fixture) putRoots(teamID string, ifRev int, roots []string, c team.Client) (int, []byte) {
	f.t.Helper()
	return f.do(http.MethodPut, team.RootsRoute, team.RootsPutRequest{TeamID: teamID, IfRev: ifRev, Roots: roots, Client: c})
}

// grantOf is the live team's grant as GET /api/team shows it.
func (f *fixture) grantOf() team.Grant {
	f.t.Helper()
	code, body := f.do(http.MethodGet, "/api/team?origin_inbox=%2Ftmp%2F10.sock", nil)
	var v team.TeamView
	if code != 200 || json.Unmarshal(body, &v) != nil {
		f.t.Fatalf("GET /api/team = %d %s", code, body)
	}
	return v.Team.Grant
}

func mkdir(t *testing.T, name string) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if name != "" {
		d = filepath.Join(d, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func decodeRootsView(t *testing.T, body []byte) team.RootsView {
	t.Helper()
	var v team.RootsView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode RootsView: %v; %s", err, body)
	}
	return v
}

func TestRootsPut_OnlyTheAppAndAWellFormedList(t *testing.T) {
	f, root := newSpawnFixture(t, 3)
	file := filepath.Join(mkdir(t, ""), "a-file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	many := make([]string, team.MaxRoots+1)
	for i := range many {
		many[i] = mkdir(t, "")
	}
	for name, c := range map[string]team.Client{"terminal": {Kind: "terminal", Label: "x"}, "no label": {Kind: "app"}, "none": {}} {
		if code, body := f.putRoots(uid(1), 0, []string{root}, c); code != 400 || decodeErr(t, body).Error != team.ErrBadRequest {
			t.Errorf("%s: %d %s, want 400", name, code, body)
		}
	}
	cases := []struct {
		name, team string
		roots      []string
		field      string
	}{
		{"no team_id", "", []string{root}, ""},
		{"empty", uid(1), []string{}, ""},
		{"nil", uid(1), nil, ""},
		{"too many", uid(1), many, ""},
		{"relative", uid(1), []string{root, "rel/path"}, "roots[1]"},
		{"missing", uid(1), []string{filepath.Join(root, "nope")}, "roots[0]"},
		{"not a directory", uid(1), []string{root, file}, "roots[1]"},
	}
	for _, c := range cases {
		code, body := f.putRoots(c.team, 0, c.roots, appClientRoots)
		var e team.RootsBadRequest
		_ = json.Unmarshal(body, &e)
		if code != 400 || e.Error != team.ErrBadRequest || e.Field != c.field {
			t.Errorf("%s: %d %s, want 400 bad_request field %q", c.name, code, body, c.field)
		}
	}
	if g := f.grantOf(); len(g.Roots) != 1 || g.Roots[0] != root || g.RootsRev != 0 {
		t.Errorf("refused requests changed the grant: %+v", g)
	}
}

func TestRootsPut_UnknownAndEndedTeamAre404(t *testing.T) {
	f, root := newSpawnFixture(t, 3)
	if code, body := f.putRoots("no-such-team", 0, []string{root}, appClientRoots); code != 404 || decodeErr(t, body).Error != team.ErrNotFound {
		t.Errorf("unknown: %d %s", code, body)
	}
	if ok, err := f.m.store.EndTeam(uid(1), "sid-1", "lead_gone", 5); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if code, body := f.putRoots(uid(1), 0, []string{root}, appClientRoots); code != 404 || decodeErr(t, body).Error != team.ErrNotFound {
		t.Errorf("ended: %d %s", code, body)
	}
}

// Add a root, then remove the first: the stored list and rev follow, the grant becomes canonical, and the log names the app.
func TestRootsPut_AddAndRemoveBumpTheRev(t *testing.T) {
	f, root := newSpawnFixture(t, 3)
	logs := f.logs()
	second := mkdir(t, "")
	code, body := f.putRoots(uid(1), 0, []string{root, second}, appClientRoots)
	if v := decodeRootsView(t, body); code != 200 || v.TeamID != uid(1) || len(v.Roots) != 2 || v.RootsRev != 1 {
		t.Fatalf("add: %d %s", code, body)
	}
	code, body = f.putRoots(uid(1), 1, []string{second}, appClientRoots)
	if v := decodeRootsView(t, body); code != 200 || len(v.Roots) != 1 || v.Roots[0] != second || v.RootsRev != 2 {
		t.Fatalf("remove: %d %s", code, body)
	}
	g := f.grantOf()
	if len(g.Roots) != 1 || g.Roots[0] != second || g.RootsRev != 2 || !g.RootsCanonical || g.MaxMembers != 3 {
		t.Fatalf("grant = %+v", g)
	}
	if l := strings.Join(logs(), "\n"); !strings.Contains(l, `"Purdex.app @ air26"`) || !strings.Contains(l, "100.64.0.4") || !strings.Contains(l, root) {
		t.Errorf("log lacks the label, address or old root: %v", logs())
	}
}

// Mutation gate: do not resolve symlinks -> red.
func TestRootsPut_StoresTheRealPathOfASymlink(t *testing.T) {
	f, root := newSpawnFixture(t, 3)
	target := mkdir(t, "real")
	link := filepath.Join(mkdir(t, ""), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	code, body := f.putRoots(uid(1), 0, []string{root, link}, appClientRoots)
	if v := decodeRootsView(t, body); code != 200 || len(v.Roots) != 2 || v.Roots[1] != target {
		t.Fatalf("%d %s, want the real path %s", code, body, target)
	}
}

// Mutation gate: no collapse -> red. Duplicates and a root inside another go; the outer stays; order is kept.
func TestRootsPut_CollapsesDuplicatesAndNestedRoots(t *testing.T) {
	f, root := newSpawnFixture(t, 3)
	b := mkdir(t, "")
	inner := filepath.Join(b, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	code, body := f.putRoots(uid(1), 0, []string{inner, b, root, b, filepath.Join(root, ".")}, appClientRoots)
	v := decodeRootsView(t, body)
	if code != 200 || len(v.Roots) != 2 || v.Roots[0] != b || v.Roots[1] != root {
		t.Fatalf("%d %s, want [%s %s]", code, body, b, root)
	}
}

// Mutation gate: skip the rev check -> red. Nothing changes and the answer carries what is stored.
func TestRootsPut_WrongRevIs409AndComesBeforeInUse(t *testing.T) {
	f, root := newSpawnFixture(t, 3)
	other := mkdir(t, "")
	if code, body := f.putRoots(uid(1), 0, []string{root, other}, appClientRoots); code != 200 {
		t.Fatalf("first: %d %s", code, body)
	}
	f.acceptOp(9, root, nil) // a spawn under root: removing root would be in use too
	for _, rev := range []int{0, 2, -1} {
		code, body := f.putRoots(uid(1), rev, []string{other}, appClientRoots)
		var m team.RootsRevMismatch
		_ = json.Unmarshal(body, &m)
		if code != 409 || m.Error != team.ErrRevMismatch || m.RootsRev != 1 || len(m.Roots) != 2 {
			t.Errorf("rev %d: %d %s, want 409 rev_mismatch with the stored roots and rev 1", rev, code, body)
		}
	}
	if g := f.grantOf(); len(g.Roots) != 2 || g.RootsRev != 1 {
		t.Errorf("a refused put changed the grant: %+v", g)
	}
}

// Mutation gate: skip the in_use check -> red. A member, a starting spawn and an open adoption each hold their root.
func TestRootsPut_RemovingARootInUseIs409NamingWhatHoldsIt(t *testing.T) {
	f, root := newSpawnFixture(t, 4)
	other := mkdir(t, "")
	f.holdRunners()
	mrow := newMember("op-1", uid(1), "sid-m1", "_mem1", 1)
	mrow.Cwd = filepath.Join(root, "sub")
	if err := f.m.store.InsertMember(mrow); err != nil {
		t.Fatal(err)
	}
	f.acceptOp(9, root, nil)
	f.origins.show(team.Origin{SessionID: "sid-2", Ref: "_def456", PID: 20, ProcStart: "Sun Sep 13 15:22:36 2026", Cwd: root})
	f.adoptOK(uid(10), "_def456")

	code, body := f.putRoots(uid(1), 0, []string{other}, appClientRoots)
	var r team.RootsRefusal
	_ = json.Unmarshal(body, &r)
	if code != 409 || r.Error != team.ErrRootsInUse {
		t.Fatalf("%d %s, want 409 roots_in_use", code, body)
	}
	kinds := map[string]team.RootUse{}
	for _, u := range r.InUse {
		kinds[u.Kind] = u
		if u.Root != root {
			t.Errorf("use %+v names root %q, want %q", u, u.Root, root)
		}
	}
	if kinds[team.RootUseMember].Ref != "_mem1" || kinds[team.RootUseMember].Cwd != mrow.Cwd ||
		kinds[team.RootUseSpawn].Cwd != root || kinds[team.RootUseAdoption].Cwd != root {
		t.Errorf("in_use = %+v, want a member (_mem1), a spawn and an adoption", r.InUse)
	}
	if g := f.grantOf(); len(g.Roots) != 1 || g.Roots[0] != root || g.RootsRev != 0 {
		t.Errorf("a refused put changed the grant: %+v", g)
	}
	// Keeping the root (adding another) is fine: nothing is removed.
	if code, body := f.putRoots(uid(1), 0, []string{root, other}, appClientRoots); code != 200 {
		t.Errorf("adding: %d %s", code, body)
	}
	// A cwd that stays under a remaining root is not in use: moving to a parent that contains root is a widening.
	parent := filepath.Dir(root)
	if code, body := f.putRoots(uid(1), 1, []string{parent, other}, appClientRoots); code != 200 {
		t.Errorf("widening to the parent: %d %s", code, body)
	}
}

// A member of another host has its cwd on that host: it does not hold a local root.
func TestRootsPut_ARemoteMembersCwdIsNotLocal(t *testing.T) {
	f, root := newSpawnFixture(t, 3)
	mrow := newMember("op-r", uid(1), "sid-r", "_memr", 1)
	mrow.HostID, mrow.Cwd = "h:remote", filepath.Join(root, "x")
	if err := f.m.store.InsertMember(mrow); err != nil {
		t.Fatal(err)
	}
	if code, body := f.putRoots(uid(1), 0, []string{mkdir(t, "")}, appClientRoots); code != 200 {
		t.Fatalf("%d %s, want 200", code, body)
	}
}

// Mutation gate: a spawn judged on the old grant -> red.
func TestRootsPut_ASpawnPassesUnderAJustAddedRootAndIsRefusedUnderARemovedOne(t *testing.T) {
	f, root := newSpawnFixture(t, 3)
	f.holdRunners()
	added := mkdir(t, "")
	if code, _, e := f.spawn(1, added, nil); code != 409 || e.Error != team.ErrCwdOutsideGrant {
		t.Fatalf("before: %d %+v, want cwd_outside_grant", code, e)
	}
	if code, body := f.putRoots(uid(1), 0, []string{root, added}, appClientRoots); code != 200 {
		t.Fatalf("add: %d %s", code, body)
	}
	if code, _, e := f.spawn(1, added, nil); code != 200 {
		t.Fatalf("after add: %d %+v, want the spawn to pass", code, e)
	}
	// the spawn op above holds `added`; remove `root` (unused) instead
	if code, body := f.putRoots(uid(1), 1, []string{added}, appClientRoots); code != 200 {
		t.Fatalf("remove: %d %s", code, body)
	}
	if code, _, e := f.spawn(2, root, nil); code != 409 || e.Error != team.ErrCwdOutsideGrant {
		t.Fatalf("after remove: %d %+v, want cwd_outside_grant", code, e)
	}
}

// Race: a spawn read the grant, then the root was removed, then the spawn's op is written. The op's own transaction judges the
// cwd against the grant as it is NOW: refused, no op written. Mutation gate: no check in the transaction -> red.
func TestRootsPut_ASpawnThatReadTheOldGrantIsRefusedWhenItsRootWasRemoved(t *testing.T) {
	f, root := newSpawnFixture(t, 3)
	f.holdRunners()
	other := mkdir(t, "")
	f.m.afterSpawnTeamRead = func() {
		if code, body := f.putRoots(uid(1), 0, []string{other}, appClientRoots); code != 200 {
			t.Errorf("remove in the window: %d %s", code, body)
		}
	}
	code, _, e := f.spawn(1, root, nil)
	if code != 409 || e.Error != team.ErrCwdOutsideGrant {
		t.Fatalf("%d %+v, want 409 cwd_outside_grant", code, e)
	}
	if _, found, err := f.m.store.GetSpawnOp(spawnID(1)); err != nil || found {
		t.Fatalf("an op was written under a removed root: found=%v err=%v", found, err)
	}
}

// Two PUTs with the same if_rev: exactly one wins. Mutation gate: no compare-and-set -> red.
func TestRootsPut_TwoConcurrentPutsOnlyOneSucceeds(t *testing.T) {
	f, root := newSpawnFixture(t, 3)
	const n = 6
	dirs := make([]string, n)
	for i := range dirs {
		dirs[i] = mkdir(t, "")
	}
	codes := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i], _ = f.putRoots(uid(1), 0, []string{root, dirs[i]}, appClientRoots)
		}(i)
	}
	close(start)
	wg.Wait()
	ok, mismatch := 0, 0
	for _, c := range codes {
		switch c {
		case 200:
			ok++
		case 409:
			mismatch++
		}
	}
	if ok != 1 || mismatch != n-1 {
		t.Fatalf("codes %v, want one 200 and %d 409", codes, n-1)
	}
	if g := f.grantOf(); g.RootsRev != 1 || len(g.Roots) != 2 {
		t.Fatalf("grant = %+v", g)
	}
}

// Existing members are never touched by a change, and the roster (event and GET) carries roots and roots_rev.
func TestRootsPut_AnnouncesTheRosterAndLeavesMembersAlone(t *testing.T) {
	f, root := newSpawnFixture(t, 3)
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 1) // cwd /w/x: under no root
	if r := f.getRoster(); len(r.Teams) != 1 || len(r.Teams[0].Roots) != 1 || r.Teams[0].RootsRev != 0 {
		t.Fatalf("roster before = %+v", r.Teams)
	}
	f.rosterBaselineNow()
	w := f.watchRoster()
	w.drain()
	other := mkdir(t, "")
	if code, body := f.putRoots(uid(1), 0, []string{root, other}, appClientRoots); code != 200 {
		t.Fatalf("put: %d %s", code, body)
	}
	ev := w.one("roots")
	if len(ev.Teams) != 1 || len(ev.Teams[0].Roots) != 2 || ev.Teams[0].RootsRev != 1 || len(ev.Teams[0].Members) != 1 {
		t.Fatalf("roster event = %+v", ev.Teams)
	}
	raw, _ := json.Marshal(f.getRoster())
	if !strings.Contains(string(raw), fmt.Sprintf(`"roots_rev":%d`, 1)) {
		t.Fatalf("GET roster lacks roots_rev: %s", raw)
	}
}
