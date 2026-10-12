// internal/module/team/host_roots_routes_test.go — the lead host's routes for the directories it may use on other hosts, the in-use check,
// the pending-remove guard, the parallel read and the dismiss (cross-host roots spec 2026-10-12 §3.2, §5 XR-2b).
package teammod

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// routesFixture: sid-1 leads team uid(1); air26 = hostM and other = hostN are paired and verified, ghost has no host id.
func routesFixture(t *testing.T, aliases ...string) (*fixture, *fakeHostCaller) {
	t.Helper()
	f, fc := leadSpawnFixture(t)
	fc.paired = map[string]bool{"hostM": true, "hostN": true}
	hosts := []config.PeerHost{{Alias: "air26", URL: "https://air26.example", HostID: "hostM"}, {Alias: "other", URL: "https://other.example", HostID: "hostN"},
		{Alias: "ghost", URL: "https://ghost.example"}}
	f.core.CfgMu.Lock()
	f.core.Cfg.Peers.Hosts = hosts
	f.core.CfgMu.Unlock()
	return f, fc
}

func (f *fixture) postHostRoots(host, op, path string) (int, []byte) {
	f.t.Helper()
	return f.do(http.MethodPost, team.HostRootsRoute, team.HostRootsPostRequest{Host: host, Op: op, Path: path, Client: team.Client{Kind: "app", Label: "Purdex.app @ air26"}})
}

func (f *fixture) cache(host string, rev int64, roots ...string) {
	f.t.Helper()
	if err := f.m.store.UpsertHostRootsSeen(host, roots, rev, f.clock.Load()); err != nil {
		f.t.Fatal(err)
	}
}

// remoteMemberAt seeds a remote member of the fixture's team on host in state, running in cwd.
func (f *fixture) remoteMemberAt(spawnOp, host, state, cwd string) {
	f.t.Helper()
	f.remoteRow(spawnOp, host, "mk-"+spawnOp, state)
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET cwd = ?, title = ? WHERE spawn_op = ?`, cwd, "t-"+spawnOp, spawnOp); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) pendingChanges(host string) []HostRootChange {
	f.t.Helper()
	rows, err := f.m.store.RootChangesToShow(host)
	if err != nil {
		f.t.Fatal(err)
	}
	return rows
}

func decodeInUse(t *testing.T, raw []byte) team.HostRootsInUse {
	t.Helper()
	var u team.HostRootsInUse
	if err := json.Unmarshal(raw, &u); err != nil {
		t.Fatalf("body %s: %v", raw, err)
	}
	return u
}

// ---- POST ----

func TestHostRootsPost_TheClaimTheHostAndThePathAreChecked(t *testing.T) {
	f, fc := routesFixture(t)
	fc.paired["hostN"] = false // other is configured but no longer paired
	app := team.Client{Kind: "app", Label: "x"}
	for name, c := range map[string]struct {
		req  team.HostRootsPostRequest
		code string
	}{
		"no claim":      {team.HostRootsPostRequest{Host: "air26", Op: "add", Path: "/w"}, team.ErrBadRequest},
		"blank label":   {team.HostRootsPostRequest{Host: "air26", Op: "add", Path: "/w", Client: team.Client{Kind: "app", Label: " "}}, team.ErrBadRequest},
		"another kind":  {team.HostRootsPostRequest{Host: "air26", Op: "add", Path: "/w", Client: team.Client{Kind: "cli", Label: "x"}}, team.ErrBadRequest},
		"op unknown":    {team.HostRootsPostRequest{Host: "air26", Op: "read", Path: "/w", Client: app}, team.ErrBadRequest},
		"no such host":  {team.HostRootsPostRequest{Host: "nowhere", Op: "add", Path: "/w", Client: app}, team.ErrUnknownHost},
		"no host":       {team.HostRootsPostRequest{Op: "add", Path: "/w", Client: app}, team.ErrUnknownHost},
		"unverified":    {team.HostRootsPostRequest{Host: "ghost", Op: "add", Path: "/w", Client: app}, team.ErrUnknownHost},
		"not paired":    {team.HostRootsPostRequest{Host: "other", Op: "add", Path: "/w", Client: app}, team.ErrUnknownHost},
		"relative path": {team.HostRootsPostRequest{Host: "air26", Op: "add", Path: "w/a", Client: app}, team.ErrRootsBadRoot},
		"empty path":    {team.HostRootsPostRequest{Host: "air26", Op: "add", Path: "", Client: app}, team.ErrRootsBadRoot},
		"unclean path":  {team.HostRootsPostRequest{Host: "air26", Op: "add", Path: "/w/../x", Client: app}, team.ErrRootsBadRoot},
		"trailing /":    {team.HostRootsPostRequest{Host: "air26", Op: "add", Path: "/w/", Client: app}, team.ErrRootsBadRoot},
	} {
		code, raw := f.do(http.MethodPost, team.HostRootsRoute, c.req)
		if code != http.StatusBadRequest || decodeErr(t, raw).Error != c.code {
			t.Errorf("%s: %d %s, want 400 %s", name, code, raw, c.code)
		}
	}
	if rows, _ := f.m.store.PendingRootChangeHosts(); len(rows) != 0 {
		t.Fatalf("a refused request wrote rows for %v", rows)
	}
}

// The host is an alias (any case) first, else a host id; the answer is 202 with the pending row and the reconciler is woken.
func TestHostRootsPost_AcceptsAnAliasInAnyCaseOrAHostID(t *testing.T) {
	f, _ := routesFixture(t)
	for i, host := range []string{"air26", "AIR26", "hostM"} {
		code, raw := f.postHostRoots(host, "add", fmt.Sprintf("/w/%d", i))
		var row team.HostRootChange
		if code != http.StatusAccepted || json.Unmarshal(raw, &row) != nil || row.State != RootChangePending || row.HostID != "hostM" || row.Op != "add" || row.ID == "" {
			t.Fatalf("%s: %d %s", host, code, raw)
		}
		select {
		case <-f.m.rootsSig:
		default:
			t.Fatalf("%s: the reconciler was not woken", host)
		}
	}
	if got := len(f.pendingChanges("hostM")); got != 3 {
		t.Fatalf("%d pending, want 3", got)
	}
}

// A remove names a root the host last said it has, or the path of a pending add; anything else is not a root.
func TestHostRootsPost_RemoveTargets(t *testing.T) {
	f, _ := routesFixture(t)
	f.cache("hostM", 3, "/w/cached")
	if code, raw := f.postHostRoots("air26", "remove", "/w/cached"); code != http.StatusAccepted {
		t.Fatalf("a cached root: %d %s", code, raw)
	}
	if code, _ := f.postHostRoots("air26", "add", "/w/new"); code != http.StatusAccepted {
		t.Fatal("add")
	}
	if code, raw := f.postHostRoots("air26", "remove", "/w/new"); code != http.StatusAccepted { // the path of a pending add
		t.Fatalf("a pending add: %d %s", code, raw)
	}
	code, raw := f.postHostRoots("air26", "remove", "/w/other")
	if code != http.StatusBadRequest || decodeErr(t, raw).Error != team.ErrNotARoot {
		t.Fatalf("anything else: %d %s, want 400 not_a_root", code, raw)
	}
	if code, raw = f.postHostRoots("other", "remove", "/w/cached"); code != http.StatusBadRequest { // another host's cache does not count (hostN unpaired? it is paired here)
		t.Fatalf("another host's root: %d %s", code, raw)
	}
}

// ---- the in-use check ----

// Four live states of a member of this lead refuse the removal with its ref; the rest let it through.
// Mutation gate: count `failed` as live → the failed case is refused (red).
func TestHostRootsPost_MembersUnderTheRemovedPathRefuseItWhileTheyAreLive(t *testing.T) {
	for state, live := range map[string]bool{rowJoining: true, rowActive: true, rowReleasing: true, rowKilling: true,
		rowFailed: false, "released": false, "killed": false, rowGone: false} {
		f, _ := routesFixture(t)
		f.cache("hostM", 1, "/w/a")
		f.remoteMemberAt("m1", "hostM", state, "/w/a/proj")
		code, raw := f.postHostRoots("air26", "remove", "/w/a")
		if live {
			u := decodeInUse(t, raw)
			if code != http.StatusConflict || u.Error != team.ErrRootsInUse || len(u.Uses) != 1 || u.Uses[0].Cwd != "/w/a/proj" || u.Uses[0].Ref == "" || u.Uses[0].Title != "t-m1" {
				t.Fatalf("%s: %d %s, want 409 roots_in_use with the member", state, code, raw)
			}
			if len(f.pendingChanges("hostM")) != 0 {
				t.Fatalf("%s: a refused removal wrote a row", state)
			}
		} else if code != http.StatusAccepted {
			t.Fatalf("%s: %d %s, want 202 (not live)", state, code, raw)
		}
	}
}

func TestHostRootsPost_WhatDoesNotCount(t *testing.T) {
	cases := map[string]func(f *fixture){
		"a member of an ended team": func(f *fixture) {
			f.remoteMemberAt("m1", "hostM", rowActive, "/w/a/x")
			f.m.store.db.Exec(`UPDATE teams SET ended_at = 5`)
		},
		"a member of another host": func(f *fixture) { f.remoteMemberAt("m1", "hostN", rowActive, "/w/a/x") },
		"a member under another kept root": func(f *fixture) {
			f.cache("hostM", 2, "/w/a", "/w")
			f.remoteMemberAt("m1", "hostM", rowActive, "/w/a/x")
		},
		"a sibling by prefix only": func(f *fixture) { f.remoteMemberAt("m1", "hostM", rowActive, "/w/ab/x") },
		"a failed spawn": func(f *fixture) {
			f.remoteSpawn("s1", "hostM", "t")
			f.m.store.db.Exec(`UPDATE remote_spawns SET state='failed', cwd='/w/a/y'`)
		},
		"a spawn of another host": func(f *fixture) {
			f.remoteSpawn("s1", "hostN", "t")
			f.m.store.db.Exec(`UPDATE remote_spawns SET cwd='/w/a/y'`)
		},
	}
	for name, setup := range cases {
		f, _ := routesFixture(t)
		f.cache("hostM", 1, "/w/a")
		setup(f)
		if code, raw := f.postHostRoots("air26", "remove", "/w/a"); code != http.StatusAccepted {
			t.Errorf("%s: %d %s, want 202", name, code, raw)
		}
	}
}

// A forwarded spawn that is still running refuses the removal with its op id.
func TestHostRootsPost_ARunningRemoteSpawnRefusesTheRemoval(t *testing.T) {
	f, _ := routesFixture(t)
	f.cache("hostM", 1, "/w/a")
	f.remoteSpawn("op-run-1", "hostM", "t")
	f.m.store.db.Exec(`UPDATE remote_spawns SET cwd = '/w/a/y' WHERE id = 'op-run-1'`)
	code, raw := f.postHostRoots("air26", "remove", "/w/a")
	u := decodeInUse(t, raw)
	if code != http.StatusConflict || len(u.Uses) != 1 || u.Uses[0].OpID != "op-run-1" || u.Uses[0].Cwd != "/w/a/y" {
		t.Fatalf("%d %s", code, raw)
	}
}

// Without a cache the answer is the stricter one: only the pending adds are known to stay.
func TestHostRootsPost_WithoutACacheTheStricterAnswerIsGiven(t *testing.T) {
	f, _ := routesFixture(t)
	f.postHostRoots("air26", "add", "/w/a") // the target of the removal is a pending add; the host has said nothing yet
	f.remoteMemberAt("m1", "hostM", rowActive, "/w/a/x")
	if code, raw := f.postHostRoots("air26", "remove", "/w/a"); code != http.StatusConflict {
		t.Fatalf("no cache: %d %s, want 409", code, raw)
	}
	f.postHostRoots("air26", "add", "/w") // a pending add that covers the member keeps it
	if code, raw := f.postHostRoots("air26", "remove", "/w/a"); code != http.StatusAccepted {
		t.Fatalf("covered by a pending add: %d %s", code, raw)
	}
}

// ---- the pending-remove guard ----

// A spawn under a path whose removal is pending is refused 409 root_being_removed and creates nothing; once the removal has settled it goes.
// Mutation gate: no guard in AcceptRemoteSpawn → the spawn is accepted (red).
func TestHostRootsGuard_ASpawnUnderAPendingRemovalIsRefused(t *testing.T) {
	f, _ := routesFixture(t)
	f.cache("hostM", 1, "/w")
	f.change("rm1", "hostM", "remove", "/w")
	code, op, e := f.spawnRemote(1, "air26", nil) // cwd /w/r
	if code != http.StatusConflict || e.Error != team.ErrRootBeingRemoved {
		t.Fatalf("spawn: %d %+v %+v", code, op, e)
	}
	var n int
	_ = f.m.store.db.QueryRow(`SELECT COUNT(*) FROM remote_spawns`).Scan(&n)
	if n != 0 || len(f.commandsOf(CmdSpawn)) != 0 {
		t.Fatalf("a refused spawn left %d op(s), %d command(s)", n, len(f.commandsOf(CmdSpawn)))
	}
	if err := f.m.store.SettleRootChangeDone("rm1", "/w", []string{}, 2, f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	if code, _, e := f.spawnRemote(2, "air26", nil); code != http.StatusOK {
		t.Fatalf("after the removal settled: %d %+v", code, e)
	}
}

// A path that another root keeps is not "being removed" for a cwd under that other root.
func TestHostRootsGuard_ACwdUnderAKeptRootIsNotRefused(t *testing.T) {
	f, _ := routesFixture(t)
	f.cache("hostM", 1, "/w", "/w/r")
	f.change("rm1", "hostM", "remove", "/w")
	if code, _, e := f.spawnRemote(1, "air26", nil); code != http.StatusOK { // /w/r stays
		t.Fatalf("spawn under a kept root: %d %+v", code, e)
	}
}

// An adopt approved under a path whose removal is pending is refused (the row is cancelled with the code); after the settle it goes.
// Mutation gate: no guard in adoptApprovedIn → the approve writes the joining row (red).
func TestHostRootsGuard_AnAdoptUnderAPendingRemovalIsRefused(t *testing.T) {
	f, _ := adoptFixture(t)
	f.cache("hostM", 1, "/w")
	f.change("rm1", "hostM", "remove", "/w") // the target's cwd is /w/ios
	f.adoptOK(uid(30), remoteTarget)
	code, raw := f.decide(uid(30), "approve")
	if code != http.StatusConflict || decodeErr(t, raw).Error != team.ErrRootBeingRemoved {
		t.Fatalf("approve: %d %s", code, raw)
	}
	var n int
	_ = f.m.store.db.QueryRow(`SELECT COUNT(*) FROM team_members WHERE spawn_op = ?`, uid(30)).Scan(&n)
	if n != 0 || len(f.commandsOf(CmdAdopt)) != 0 {
		t.Fatalf("a refused adopt left %d row(s), %d command(s)", n, len(f.commandsOf(CmdAdopt)))
	}
	if st := f.rowState(uid(30)); st != team.StateCancelled {
		t.Fatalf("the adopt request = %s, want cancelled", st)
	}
	if err := f.m.store.SettleRootChangeDone("rm1", "/w", []string{}, 2, f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	f.adoptOK(uid(31), remoteTarget)
	f.approveClick(uid(31))
	if state, _ := f.memberRowState(uid(31)); state != team.AdoptionJoining {
		t.Fatalf("after the removal settled: %s", state)
	}
}

// ---- the races: exactly one side wins, and nobody sees a busy database ----

// Remove first (holding the writer lock), a spawn accept arriving meanwhile: the removal is recorded, the spawn is refused.
func TestHostRootsRace_RemoveHoldsTheLockThenASpawnIsRefused(t *testing.T) {
	f, _ := routesFixture(t)
	f.cache("hostM", 1, "/w")
	inLock, release := make(chan struct{}), make(chan struct{})
	f.m.store.afterRootsLock = func() { close(inLock); <-release }
	var rmCode, spCode int
	var spErr team.APIError
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); rmCode, _ = f.postHostRoots("air26", "remove", "/w") }()
	<-inLock
	go func() { defer wg.Done(); spCode, _, spErr = f.spawnRemote(1, "air26", nil) }()
	time.Sleep(150 * time.Millisecond) // the spawn is waiting for the lock
	close(release)
	wg.Wait()
	if rmCode != http.StatusAccepted || spCode != http.StatusConflict || spErr.Error != team.ErrRootBeingRemoved {
		t.Fatalf("remove %d, spawn %d %+v: want the removal recorded and the spawn refused", rmCode, spCode, spErr)
	}
}

// Spawn first (holding the writer lock), a removal arriving meanwhile: the spawn is accepted, the removal is refused for it.
// Mutation gate: look at the uses before the writer lock (a plain SELECT first) → both win (red).
func TestHostRootsRace_ASpawnHoldsTheLockThenTheRemovalIsRefused(t *testing.T) {
	f, _ := routesFixture(t)
	f.cache("hostM", 1, "/w")
	inLock, release := make(chan struct{}), make(chan struct{})
	f.m.store.afterSpawnLock = func() { close(inLock); <-release }
	var rmCode, spCode int
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); spCode, _, _ = f.spawnRemote(1, "air26", nil) }()
	<-inLock
	var rmRaw []byte
	go func() { defer wg.Done(); rmCode, rmRaw = f.postHostRoots("air26", "remove", "/w") }()
	time.Sleep(150 * time.Millisecond)
	close(release)
	wg.Wait()
	if spCode != http.StatusOK || rmCode != http.StatusConflict {
		t.Fatalf("spawn %d, remove %d %s: want the spawn accepted and the removal refused", spCode, rmCode, rmRaw)
	}
}

// Remove first, an adopt approval arriving meanwhile: refused. Adopt first: the joining member refuses the removal.
// Mutation gate: no guard in adoptApprovedIn → the adopt wins after a removal was recorded (red).
func TestHostRootsRace_RemoveAndAdoptApproveBothOrders(t *testing.T) {
	f, _ := adoptFixture(t)
	f.core.CfgMu.Lock()
	f.core.Cfg.Peers.Hosts = []config.PeerHost{{Alias: "air26", URL: "https://air26.example", HostID: "hostM"}}
	f.core.CfgMu.Unlock()
	f.cache("hostM", 1, "/w")
	f.adoptOK(uid(30), remoteTarget)
	inLock, release := make(chan struct{}), make(chan struct{})
	f.m.store.afterRootsLock = func() { close(inLock); <-release }
	var rmCode, apCode int
	var apRaw []byte
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); rmCode, _ = f.postHostRoots("air26", "remove", "/w") }()
	<-inLock
	go func() { defer wg.Done(); apCode, apRaw = f.decide(uid(30), "approve") }()
	time.Sleep(150 * time.Millisecond)
	close(release)
	wg.Wait()
	f.m.store.afterRootsLock = nil
	if rmCode != http.StatusAccepted || apCode != http.StatusConflict || decodeErr(t, apRaw).Error != team.ErrRootBeingRemoved {
		t.Fatalf("remove first: remove %d, adopt %d %s", rmCode, apCode, apRaw)
	}
	// the other order: the adoption first, then the removal
	g, _ := adoptFixture(t)
	g.core.CfgMu.Lock()
	g.core.Cfg.Peers.Hosts = []config.PeerHost{{Alias: "air26", URL: "https://air26.example", HostID: "hostM"}}
	g.core.CfgMu.Unlock()
	g.cache("hostM", 1, "/w")
	g.adoptOK(uid(30), remoteTarget)
	g.approveClick(uid(30))
	code, raw := g.postHostRoots("air26", "remove", "/w")
	u := decodeInUse(t, raw)
	if code != http.StatusConflict || len(u.Uses) != 1 || u.Uses[0].Cwd != "/w/ios" {
		t.Fatalf("adopt first: remove %d %s, want 409 with the joining member", code, raw)
	}
}

// Unsynchronised, repeatedly: a removal and a spawn on the same directory never both win, and neither sees a 5xx.
// (Run with -count=50 for the mutation gate.)
func TestHostRootsRace_RemoveAgainstSpawnNeverBothWin(t *testing.T) {
	for i := 0; i < 8; i++ {
		f, _ := routesFixture(t)
		f.cache("hostM", 1, "/w")
		var rmCode, spCode int
		var wg sync.WaitGroup
		wg.Add(2)
		start := make(chan struct{})
		go func() { defer wg.Done(); <-start; rmCode, _ = f.postHostRoots("air26", "remove", "/w") }()
		go func() { defer wg.Done(); <-start; spCode, _, _ = f.spawnRemote(1, "air26", nil) }()
		close(start)
		wg.Wait()
		removed, spawned := rmCode == http.StatusAccepted, spCode == http.StatusOK
		if removed == spawned {
			t.Fatalf("iteration %d: remove %d, spawn %d — exactly one must win", i, rmCode, spCode)
		}
		if rmCode >= 500 || spCode >= 500 {
			t.Fatalf("iteration %d: remove %d, spawn %d", i, rmCode, spCode)
		}
	}
}

// ---- GET ----

func rootsReadAnswer(body map[string]any, roots []string, rev int64) peersmod.CallResult {
	return rootsOK(body, "unchanged", "", roots, rev)
}

// Three hosts: one answers at once, one is slower than the bound, one refuses. The whole answer comes in about the bound; the slow host
// keeps its (empty) cache and its status; the refusing host is consent_off; the changes list shows pending, refused and dropped only.
func TestHostRootsGet_ReadsEveryHostInParallelWithinTheBound(t *testing.T) {
	f, fc := routesFixture(t)
	f.core.CfgMu.Lock()
	f.core.Cfg.Peers.Hosts = append(f.core.Cfg.Peers.Hosts, config.PeerHost{Alias: "third", URL: "https://third.example", HostID: "hostT"})
	f.core.CfgMu.Unlock()
	fc.paired["hostT"] = true
	fc.script = func(host string, body map[string]any) peersmod.CallResult {
		switch host {
		case "hostM":
			return rootsReadAnswer(body, []string{"/w/m"}, 4)
		case "hostN":
			time.Sleep(3 * time.Second)
			return rootsReadAnswer(body, []string{"/w/slow"}, 1)
		}
		return refusedWith("host_not_allowed", "")
	}
	for _, c := range []HostRootChange{{ID: "p1", HostID: "hostM", Op: "add", Path: "/w/p"}, {ID: "r1", HostID: "hostM", Op: "remove", Path: "/w/r"},
		{ID: "d1", HostID: "hostM", Op: "add", Path: "/w/d"}, {ID: "x1", HostID: "hostM", Op: "add", Path: "/w/x"}} {
		c.CreatedAt = f.clock.Load()
		if err := f.m.store.InsertHostRootChange(c); err != nil {
			t.Fatal(err)
		}
	}
	_ = f.m.store.RefuseRootChange("r1", "roots_in_use", "_abc123", 5)
	_, _ = f.m.store.DropRootChangesOfHost("hostZ", 5)
	_ = f.m.store.SettleRootChangeDone("x1", "/w/x", []string{"/w/x"}, 1, 5) // done: not listed (and it moved the cache; overwritten by the read)
	if _, err := f.m.store.db.Exec(`UPDATE host_root_changes SET state = 'dropped' WHERE id = 'd1'`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	code, raw := f.do(http.MethodGet, team.HostRootsRoute, nil)
	took := time.Since(start)
	if code != http.StatusOK || took > 2600*time.Millisecond || took < 1900*time.Millisecond {
		t.Fatalf("%d in %v", code, took)
	}
	var v team.HostRootsView
	if err := json.Unmarshal(raw, &v); err != nil || len(v.Hosts) != 3 {
		t.Fatalf("%s: %v", raw, err)
	}
	by := map[string]team.HostRootsHost{}
	for _, h := range v.Hosts {
		by[h.HostID] = h
	}
	m, n, tt := by["hostM"], by["hostN"], by["hostT"]
	if m.Alias != "air26" || m.Status != RootsStatusOK || m.Rev != 4 || len(m.Roots) != 1 || m.Roots[0] != "/w/m" {
		t.Fatalf("hostM = %+v", m)
	}
	if len(m.Changes) != 3 || m.Changes[0].ID != "p1" || m.Changes[1].ID != "r1" || m.Changes[2].ID != "d1" || m.Changes[1].Code != "roots_in_use" {
		t.Fatalf("hostM changes = %+v, want pending p1, refused r1, dropped d1 (not the done x1)", m.Changes)
	}
	if n.Status != RootsStatusUnknown || len(n.Roots) != 0 || n.Roots == nil || n.Changes == nil {
		t.Fatalf("hostN = %+v, want the slow host unknown with an empty cache and no nulls", n)
	}
	if tt.Status != RootsStatusConsentOff {
		t.Fatalf("hostT = %+v", tt)
	}
}

func TestHostRootsGet_NoPairedHostIsAnEmptyList(t *testing.T) {
	f, fc := routesFixture(t)
	fc.paired = map[string]bool{}
	code, raw := f.do(http.MethodGet, team.HostRootsRoute, nil)
	if code != http.StatusOK || string(raw) != "{\"hosts\":[]}\n" && string(raw) != "{\"hosts\":[]}" {
		t.Fatalf("%d %q", code, raw)
	}
}

// ---- the cache rebuild rule (read path only) ----

func (f *fixture) readScript(fc *fakeHostCaller, revs ...int64) *int {
	n := 0
	var mu sync.Mutex
	fc.script = func(host string, body map[string]any) peersmod.CallResult {
		mu.Lock()
		i := n
		n++
		mu.Unlock()
		if i >= len(revs) {
			i = len(revs) - 1
		}
		if revs[i] < 0 {
			return peersmod.CallResult{Class: peersmod.ClassTransient}
		}
		return rootsReadAnswer(body, []string{fmt.Sprintf("/w/rev%d", revs[i])}, revs[i])
	}
	return &n
}

// A read that comes back with a rev below the cache's is read again at once; two lower ones in a row mean the relation was rebuilt and the
// cache is replaced. Mutation gate: replace on the first lower answer → the late-answer case below is red.
func TestHostRootsRead_TwoLowerAnswersRebuildTheCache(t *testing.T) {
	f, fc := routesFixture(t)
	f.cache("hostM", 9, "/w/old")
	calls := f.readScript(fc, 0, 0)
	f.m.readHostRoots(t.Context(), "hostM")
	seen, _, _ := f.m.store.HostRootsSeen("hostM")
	if seen.Rev != 0 || len(seen.Roots) != 1 || seen.Roots[0] != "/w/rev0" || *calls != 2 {
		t.Fatalf("cache = %+v after %d calls, want it replaced by the rebuilt relation's roots", seen, *calls)
	}
}

func TestHostRootsRead_OneLowerAnswerIsALateAnswerAndChangesNothing(t *testing.T) {
	f, fc := routesFixture(t)
	f.cache("hostM", 9, "/w/old")
	calls := f.readScript(fc, 3, 10) // a late, older answer, then the current one
	f.m.readHostRoots(t.Context(), "hostM")
	seen, _, _ := f.m.store.HostRootsSeen("hostM")
	if seen.Rev != 10 || *calls != 2 {
		t.Fatalf("cache = %+v after %d calls, want the second (current) answer applied the ordinary way", seen, *calls)
	}
	// a lower answer whose confirmation fails: nothing is replaced
	g, gc := routesFixture(t)
	g.cache("hostM", 9, "/w/old")
	g.readScript(gc, 3, -1)
	g.m.readHostRoots(t.Context(), "hostM")
	if seen, _, _ := g.m.store.HostRootsSeen("hostM"); seen.Rev != 9 || seen.Roots[0] != "/w/old" {
		t.Fatalf("cache = %+v, an unconfirmed lower answer must not replace it", seen)
	}
}

// The confirming read is the latest word on the host: a lower answer followed by a refusal reports the refusal, not the first read's ok.
// Mutation gate: drop the second read's status → the host stays "ok" (red).
func TestHostRootsRead_TheConfirmingReadsStatusStands(t *testing.T) {
	f, fc := routesFixture(t)
	f.cache("hostM", 9, "/w/old")
	n := 0
	fc.script = func(host string, body map[string]any) peersmod.CallResult {
		n++
		if n == 1 {
			return rootsReadAnswer(body, []string{"/w/low"}, 3)
		}
		return peersmod.CallResult{Class: peersmod.ClassUnauthorized, Status: 401}
	}
	f.m.readHostRoots(t.Context(), "hostM")
	if got := f.m.RootsHostStatus("hostM"); got != RootsStatusUnauthorized {
		t.Fatalf("status = %q, want unauthorized", got)
	}
	if seen, _, _ := f.m.store.HostRootsSeen("hostM"); seen.Rev != 9 {
		t.Fatalf("cache = %+v, an unconfirmed lower answer must not replace it", seen)
	}
}

// The rebuild overwrites only the cache it judged by: a newer rev written meanwhile (a settle) is not taken back.
// Mutation gate: overwrite unconditionally → the newer rev is lost (red).
func TestHostRootsRead_TheRebuildNeverUndoesANewerCache(t *testing.T) {
	f, fc := routesFixture(t)
	f.cache("hostM", 9, "/w/old")
	n := 0
	fc.script = func(host string, body map[string]any) peersmod.CallResult {
		n++
		if n == 2 { // while the confirming read is out, a settle moves the cache on
			_ = f.m.store.UpsertHostRootsSeen("hostM", []string{"/w/newest"}, 20, f.clock.Load())
		}
		return rootsReadAnswer(body, []string{"/w/low"}, 1)
	}
	f.m.readHostRoots(t.Context(), "hostM")
	if seen, _, _ := f.m.store.HostRootsSeen("hostM"); seen.Rev != 20 || seen.Roots[0] != "/w/newest" {
		t.Fatalf("cache = %+v, want the newer settle kept", seen)
	}
}

func TestHostRootsRead_AnEqualOrHigherRevIsTheOrdinaryUpsertWithOneCall(t *testing.T) {
	f, fc := routesFixture(t)
	f.cache("hostM", 9, "/w/old")
	calls := f.readScript(fc, 9)
	f.m.readHostRoots(t.Context(), "hostM")
	if seen, _, _ := f.m.store.HostRootsSeen("hostM"); seen.Rev != 9 || *calls != 1 {
		t.Fatalf("cache = %+v after %d calls", seen, *calls)
	}
}

// ---- dismiss ----

func TestHostRootsDismiss(t *testing.T) {
	f, _ := routesFixture(t)
	for _, c := range []HostRootChange{{ID: "r1", HostID: "hostM", Op: "add", Path: "/a"}, {ID: "d1", HostID: "hostM", Op: "add", Path: "/b"}, {ID: "p1", HostID: "hostM", Op: "add", Path: "/c"}} {
		c.CreatedAt = f.clock.Load()
		if err := f.m.store.InsertHostRootChange(c); err != nil {
			t.Fatal(err)
		}
	}
	_ = f.m.store.RefuseRootChange("r1", "bad_root", "not_found", 5)
	if _, err := f.m.store.db.Exec(`UPDATE host_root_changes SET state = 'dropped' WHERE id = 'd1'`); err != nil {
		t.Fatal(err)
	}
	dismiss := func(id string) (int, []byte) {
		return f.do(http.MethodPost, team.HostRootsDismissRoute, team.HostRootsDismissRequest{ID: id})
	}
	for _, id := range []string{"r1", "d1"} {
		if code, raw := dismiss(id); code != http.StatusOK {
			t.Fatalf("dismiss %s: %d %s", id, code, raw)
		}
		if _, ok, _ := f.m.store.RootChange(id); ok {
			t.Fatalf("%s is still there", id)
		}
	}
	if code, raw := dismiss("p1"); code != http.StatusConflict || decodeErr(t, raw).Error != team.ErrChangePending {
		t.Fatalf("pending: %d %s, want 409 pending", code, raw)
	}
	if _, ok, _ := f.m.store.RootChange("p1"); !ok {
		t.Fatal("a pending change was deleted")
	}
	if code, _ := dismiss("nope"); code != http.StatusNotFound {
		t.Fatalf("unknown: %d, want 404", code)
	}
	if code, _ := dismiss(" "); code != http.StatusBadRequest {
		t.Fatalf("blank id: %d, want 400", code)
	}
}
