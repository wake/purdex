// internal/module/team/roots_change_test.go — POST /api/peers/team/roots-change (cross-host roots spec 2026-10-12 §3.1, XR-1).
package teammod

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/team"
)

// setLeadGrant puts the paired lead host's entry in the live config with the grant as given.
func (f *fixture) setLeadGrant(allow bool, roots []string, rev int64) {
	f.t.Helper()
	f.core.CfgMu.Lock()
	defer f.core.CfgMu.Unlock()
	f.core.Cfg.Peers.Hosts = []config.PeerHost{{Alias: "lead", URL: "https://lead.example", HostID: "lead:1", InboundToken: "i", AllowTeam: allow, TeamRoots: roots, TeamRootsRev: rev}}
}

func (f *fixture) leadGrant() (bool, []string, int64) {
	f.core.CfgMu.RLock()
	defer f.core.CfgMu.RUnlock()
	h := f.core.Cfg.Peers.Hosts[0]
	return h.AllowTeam, append([]string{}, h.TeamRoots...), h.TeamRootsRev
}

func (f *fixture) postRoots(p *middleware.Principal, body any) (int, []byte) {
	f.t.Helper()
	var rd *bytes.Reader
	if s, ok := body.(string); ok {
		rd = bytes.NewReader([]byte(s))
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(http.MethodPost, team.RootsChangeRoute, rd)
	if p != nil {
		req = req.WithContext(middleware.WithPrincipal(req.Context(), *p))
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func rootsReq(id, op, path string) team.RootsChangeRequest {
	return team.RootsChangeRequest{ID: id, ToHostID: "h:1", Op: op, Path: path}
}

func (f *fixture) rootsOK(body team.RootsChangeRequest) team.RootsChangeResponse {
	f.t.Helper()
	code, raw := f.postRoots(leadPrincipal(), body)
	if code != http.StatusOK {
		f.t.Fatalf("%+v: %d %s", body, code, raw)
	}
	var out team.RootsChangeResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func wireErr(t *testing.T, raw []byte) (code, detail string) {
	t.Helper()
	var e struct{ Error, Detail string }
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("body %q: %v", raw, err)
	}
	return e.Error, e.Detail
}

func (f *fixture) refusal(body any, p *middleware.Principal, wantStatus int, wantCode string) string {
	f.t.Helper()
	code, raw := f.postRoots(p, body)
	c, d := wireErr(f.t, raw)
	if code != wantStatus || c != wantCode {
		f.t.Fatalf("%d %s, want %d %s", code, raw, wantStatus, wantCode)
	}
	return d
}

func TestRootsChange_BindingRefusals(t *testing.T) {
	f := newFixture(t)
	f.setLeadGrant(true, nil, 0)
	good := rootsReq(cmdUUID1, team.RootsOpRead, "")
	for name, tc := range map[string]struct {
		p    *middleware.Principal
		code string
	}{
		"no principal":              {nil, "host_unverified"},
		"admin":                     {&middleware.Principal{Kind: middleware.PrincipalAdmin}, "admin_not_allowed"},
		"unverified host":           {&middleware.Principal{Kind: middleware.PrincipalHost, Alias: "lead"}, "host_unverified"},
		"entry re-created":          {&middleware.Principal{Kind: middleware.PrincipalHost, Alias: "lead", HostID: "other:9"}, "host_unverified"},
		"alias no longer in config": {&middleware.Principal{Kind: middleware.PrincipalHost, Alias: "gone", HostID: "gone:1"}, "host_unverified"},
	} {
		f.refusal(good, tc.p, http.StatusForbidden, tc.code)
		_ = name
	}
}

func TestRootsChange_WrongHost(t *testing.T) {
	f := newFixture(t)
	f.setLeadGrant(true, nil, 0)
	r := rootsReq(cmdUUID1, team.RootsOpRead, "")
	r.ToHostID = "somebody-else"
	f.refusal(r, leadPrincipal(), http.StatusConflict, "wrong_host")
}

func TestRootsChange_BodyCapIsBeforeDecodeAndRateLimitBeforeBody(t *testing.T) {
	f := newFixture(t)
	f.setLeadGrant(true, nil, 0)
	big := `{"id":"` + cmdUUID1 + `","path":"` + strings.Repeat("a", 70<<10) + `"}`
	if code, _ := f.postRoots(leadPrincipal(), big); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize body: %d", code)
	}
	// the limit is spent before the body is read: a flood of junk is refused with 429
	var last int
	for i := 0; i < 200; i++ {
		last, _ = f.postRoots(leadPrincipal(), "{}")
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("after 200 requests: %d, want 429", last)
	}
}

// Mutation gate: drop the AllowTeam check → the add lands on an entry whose consent is off (and the 403 test goes red).
func TestRootsChange_ConsentOffIs403AndChangesNothing(t *testing.T) {
	f := newFixture(t)
	dir := realTemp(t)
	f.setLeadGrant(false, []string{dir}, 3)
	for _, r := range []team.RootsChangeRequest{rootsReq(cmdUUID1, team.RootsOpAdd, realTemp(t)), rootsReq(cmdUUID2, team.RootsOpRemove, dir), rootsReq(cmdUUID3, team.RootsOpRead, "")} {
		f.refusal(r, leadPrincipal(), http.StatusForbidden, "host_not_allowed")
	}
	// consent off wins over a root that does not exist
	f.refusal(rootsReq(cmdUUID1, team.RootsOpAdd, filepath.Join(dir, "missing")), leadPrincipal(), http.StatusForbidden, "host_not_allowed")
	if allow, roots, rev := f.leadGrant(); allow || len(roots) != 1 || rev != 3 {
		t.Fatalf("grant after refusals: %v %v %d", allow, roots, rev)
	}
}

func TestRootsChange_BadRequestShapes(t *testing.T) {
	f := newFixture(t)
	f.setLeadGrant(true, nil, 0)
	dir := realTemp(t)
	for name, r := range map[string]team.RootsChangeRequest{
		"unknown op":          rootsReq(cmdUUID1, "toggle", dir),
		"empty op":            rootsReq(cmdUUID1, "", dir),
		"add without path":    rootsReq(cmdUUID1, team.RootsOpAdd, ""),
		"remove without path": rootsReq(cmdUUID1, team.RootsOpRemove, ""),
		"add relative":        rootsReq(cmdUUID1, team.RootsOpAdd, "a/b"),
		"remove relative":     rootsReq(cmdUUID1, team.RootsOpRemove, "a/b"),
		"add unclean":         rootsReq(cmdUUID1, team.RootsOpAdd, dir+"/../x"),
		"read with a path":    rootsReq(cmdUUID1, team.RootsOpRead, dir),
		"bad id":              rootsReq("not-a-uuid", team.RootsOpRead, ""),
		"empty id":            rootsReq("", team.RootsOpRead, ""),
		"path with a NUL":     rootsReq(cmdUUID1, team.RootsOpAdd, dir+"\x00"),
	} {
		code, raw := f.postRoots(leadPrincipal(), r)
		if c, _ := wireErr(t, raw); code != http.StatusBadRequest || c != "bad_request" {
			t.Errorf("%s: %d %s", name, code, raw)
		}
	}
	if code, raw := f.postRoots(leadPrincipal(), "not json"); code != http.StatusBadRequest {
		t.Errorf("junk body: %d %s", code, raw)
	}
}

func TestRootsChange_AddMissingFileAndTooMany(t *testing.T) {
	f := newFixture(t)
	base := realTemp(t)
	f.setLeadGrant(true, nil, 0)
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for path, reason := range map[string]string{filepath.Join(base, "missing"): "not_found", file: "not_a_directory"} {
		d := f.refusal(rootsReq(cmdUUID1, team.RootsOpAdd, path), leadPrincipal(), http.StatusBadRequest, "bad_root")
		if d != reason {
			t.Errorf("%s: detail %q, want %q", path, d, reason)
		}
	}
	var roots []string
	for i := 0; i < config.MaxTeamRoots; i++ {
		d := filepath.Join(base, fmt.Sprintf("d%02d", i))
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
		roots = append(roots, d)
		f.rootsOK(rootsReq(cmdUUID1, team.RootsOpAdd, d))
	}
	extra := filepath.Join(base, "extra")
	if err := os.Mkdir(extra, 0o755); err != nil {
		t.Fatal(err)
	}
	if d := f.refusal(rootsReq(cmdUUID2, team.RootsOpAdd, extra), leadPrincipal(), http.StatusBadRequest, "bad_root"); d != "too_many" {
		t.Errorf("17th root: detail %q", d)
	}
	if _, got, rev := f.leadGrant(); len(got) != config.MaxTeamRoots || rev != int64(config.MaxTeamRoots) {
		t.Fatalf("grant after the refused 17th: %d roots, rev %d", len(got), rev)
	}
}

// Mutation gate: bump the revision on `unchanged` → the rev assertions go red.
func TestRootsChange_AddThenAddAgainIsUnchangedAndKeepsTheRevision(t *testing.T) {
	f := newFixture(t)
	dir := realTemp(t)
	f.setLeadGrant(true, nil, 4)
	a := f.rootsOK(rootsReq(cmdUUID1, team.RootsOpAdd, dir))
	if a.Result != team.RootsResultAdded || a.Path != dir || a.TeamRootsRev != 5 || len(a.TeamRoots) != 1 || a.HostID != "h:1" || a.ID != cmdUUID1 || a.Op != "add" {
		t.Fatalf("add: %+v", a)
	}
	b := f.rootsOK(rootsReq(cmdUUID2, team.RootsOpAdd, dir))
	if b.Result != team.RootsResultUnchanged || b.TeamRootsRev != 5 || b.Path != dir {
		t.Fatalf("add again: %+v", b)
	}
	if _, roots, rev := f.leadGrant(); rev != 5 || len(roots) != 1 {
		t.Fatalf("grant: %v %d", roots, rev)
	}
	// remove of something absent is not an error and not a change either
	c := f.rootsOK(rootsReq(cmdUUID3, team.RootsOpRemove, filepath.Join(dir, "nothing-here")))
	if c.Result != team.RootsResultUnchanged || c.TeamRootsRev != 5 || c.Path != "" {
		t.Fatalf("remove absent: %+v", c)
	}
}

func TestRootsChange_AddViaSymlinkStoresAndAnswersTheCanonicalPath(t *testing.T) {
	f := newFixture(t)
	base := realTemp(t)
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	f.setLeadGrant(true, nil, 0)
	got := f.rootsOK(rootsReq(cmdUUID1, team.RootsOpAdd, link))
	if got.Path != real || len(got.TeamRoots) != 1 || got.TeamRoots[0] != real {
		t.Fatalf("add via a link: %+v", got)
	}
	if _, roots, _ := f.leadGrant(); len(roots) != 1 || roots[0] != real {
		t.Fatalf("stored: %v", roots)
	}
}

func TestRootsChange_RemoveBySymlinkSpellingAndByDanglingLink(t *testing.T) {
	f := newFixture(t)
	base := realTemp(t)
	real := filepath.Join(base, "real")
	gone := filepath.Join(base, "gone")
	for _, d := range []string{real, gone} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "link")
	dangling := filepath.Join(base, "dangling")
	for from, to := range map[string]string{link: real, dangling: gone} {
		if err := os.Symlink(to, from); err != nil {
			t.Fatal(err)
		}
	}
	f.setLeadGrant(true, []string{real, gone}, 1)
	if r := f.rootsOK(rootsReq(cmdUUID1, team.RootsOpRemove, link)); r.Result != team.RootsResultRemoved || r.Path != real || r.TeamRootsRev != 2 {
		t.Fatalf("remove by a link: %+v", r)
	}
	// the directory behind the link is deleted, the link dangles: the spelling it was granted under still revokes
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if r := f.rootsOK(rootsReq(cmdUUID2, team.RootsOpRemove, dangling)); r.Result != team.RootsResultRemoved || r.Path != gone || r.TeamRootsRev != 3 {
		t.Fatalf("remove by a dangling link: %+v", r)
	}
	if _, roots, _ := f.leadGrant(); len(roots) != 0 {
		t.Fatalf("roots left: %v", roots)
	}
}

// A replay converges: the same id after an admin changed the set in between answers the state now, not the old answer.
func TestRootsChange_ReplayAfterAnAdminChangeAnswersTheCurrentState(t *testing.T) {
	f := newFixture(t)
	dir := realTemp(t)
	f.setLeadGrant(true, nil, 0)
	first := f.rootsOK(rootsReq(cmdUUID1, team.RootsOpAdd, dir))
	if first.Result != team.RootsResultAdded {
		t.Fatalf("first: %+v", first)
	}
	f.setLeadGrant(true, nil, 7) // an admin removed it (rev moved on)
	again := f.rootsOK(rootsReq(cmdUUID1, team.RootsOpAdd, dir))
	if again.Result != team.RootsResultAdded || again.TeamRootsRev != 8 || len(again.TeamRoots) != 1 {
		t.Fatalf("replay after an admin removal: %+v", again)
	}
	f.setLeadGrant(true, []string{dir}, 9) // an admin added it back
	third := f.rootsOK(rootsReq(cmdUUID1, team.RootsOpAdd, dir))
	if third.Result != team.RootsResultUnchanged || third.TeamRootsRev != 9 {
		t.Fatalf("replay after an admin add: %+v", third)
	}
}

func TestRootsChange_ReadAnswersTheStateAndWritesNothing(t *testing.T) {
	f := newFixture(t)
	dir := realTemp(t)
	var logs []string
	f.m.logf = func(format string, a ...any) { logs = append(logs, fmt.Sprintf(format, a...)) }
	f.setLeadGrant(true, []string{dir}, 6)
	r := f.rootsOK(rootsReq(cmdUUID1, team.RootsOpRead, ""))
	if r.Result != team.RootsResultUnchanged || r.Path != "" || r.TeamRootsRev != 6 || len(r.TeamRoots) != 1 || r.TeamRoots[0] != dir {
		t.Fatalf("read: %+v", r)
	}
	if _, _, rev := f.leadGrant(); rev != 6 {
		t.Fatalf("rev after a read: %d", rev)
	}
	for _, l := range logs {
		if strings.Contains(l, "team roots:") {
			t.Fatalf("a read logged a change: %q", l)
		}
	}
}

// Only a change logs: `team roots: <alias> (<host id>) added|removed <path>, rev N`.
func TestRootsChange_OnlyAChangeIsLogged(t *testing.T) {
	f := newFixture(t)
	dir := realTemp(t)
	var mu sync.Mutex
	var logs []string
	f.m.logf = func(format string, a ...any) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, fmt.Sprintf(format, a...))
	}
	f.setLeadGrant(true, nil, 0)
	f.rootsOK(rootsReq(cmdUUID1, team.RootsOpAdd, dir))
	f.rootsOK(rootsReq(cmdUUID2, team.RootsOpAdd, dir)) // unchanged
	f.rootsOK(rootsReq(cmdUUID3, team.RootsOpRemove, dir))
	mu.Lock()
	defer mu.Unlock()
	var got []string
	for _, l := range logs {
		if strings.Contains(l, "team roots:") {
			got = append(got, l)
		}
	}
	want := []string{
		fmt.Sprintf("[team] team roots: lead (lead:1) added %s, rev 1", dir),
		fmt.Sprintf("[team] team roots: lead (lead:1) removed %s, rev 2", dir),
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("log lines %q, want %q", got, want)
	}
}

// ---- the in-use pre-check ----

func (f *fixture) seedRemoteMemberAt(mk, leadHost, state, cwd string) {
	f.t.Helper()
	r := newRemote(mk, "sid-"+mk, leadHost, 1000)
	r.Cwd, r.State = cwd, state
	if err := f.m.store.InsertRemoteMember(r); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) seedRunningSpawn(id, leadHost, state, cwd string) {
	f.t.Helper()
	if _, err := f.m.store.db.Exec(`INSERT INTO spawn_ops (id, team_id, host_id, request_hash, origin_session_id, cwd, tmux_name, step, state, created_at, updated_at, lead_host_id)
		VALUES (?, 'team-L', 'h:1', 'x', 'lead-sid', ?, 'tm', 'launch', ?, 1, 1, ?)`, id, cwd, state, leadHost); err != nil {
		f.t.Fatal(err)
	}
}

func TestRootsChange_RemoveUnderAnActiveMemberOfThisLeadIs409WithItsRef(t *testing.T) {
	f := newFixture(t)
	root := realTemp(t)
	work := filepath.Join(root, "proj")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	f.setLeadGrant(true, []string{root}, 2)
	f.seedRemoteMemberAt("mk1", "lead:1", remoteActive, work)
	f.seedRunningSpawn("op-run", "lead:1", "running", work)
	d := f.refusal(rootsReq(cmdUUID1, team.RootsOpRemove, root), leadPrincipal(), http.StatusConflict, team.ErrRootsInUse)
	if !strings.Contains(d, "_rmk1") || !strings.Contains(d, "op-run") || len(d) > 200 {
		t.Fatalf("detail %q names the member ref and the op id, within 200 bytes", d)
	}
	if _, roots, rev := f.leadGrant(); len(roots) != 1 || rev != 2 {
		t.Fatalf("a refused removal changed the grant: %v %d", roots, rev)
	}
}

// Mutation gate: count another lead host's member → the first case is refused (red).
func TestRootsChange_RemoveIgnoresWhatDoesNotCount(t *testing.T) {
	f := newFixture(t)
	base := realTemp(t)
	root := filepath.Join(base, "root")
	kept := filepath.Join(root, "kept") // another granted root nested inside the one being removed
	for _, d := range []string{filepath.Join(root, "p"), filepath.Join(kept, "q")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// a link inside root pointing under the kept root: its real cwd is covered by the kept root
	link := filepath.Join(root, "to-kept")
	if err := os.Symlink(filepath.Join(kept, "q"), link); err != nil {
		t.Fatal(err)
	}
	f.setLeadGrant(true, []string{root, kept}, 1)
	f.seedRemoteMemberAt("other-lead", "someone-else:7", remoteActive, filepath.Join(root, "p")) // another lead host's member
	f.seedRemoteMemberAt("finished", "lead:1", remoteReleased, filepath.Join(root, "p"))         // not active any more
	f.seedRemoteMemberAt("covered", "lead:1", remoteActive, link)                                // still under the kept root
	f.seedRemoteMemberAt("covered2", "lead:1", remoteActive, filepath.Join(kept, "q"))
	f.seedRunningSpawn("op-done", "lead:1", "done", filepath.Join(root, "p"))
	f.seedRunningSpawn("op-other", "other:3", "running", filepath.Join(root, "p"))
	f.seedRunningSpawn("op-local", "", "running", filepath.Join(root, "p")) // a local spawn has no lead host
	r := f.rootsOK(rootsReq(cmdUUID1, team.RootsOpRemove, root))
	if r.Result != team.RootsResultRemoved || r.Path != root || len(r.TeamRoots) != 1 || r.TeamRoots[0] != kept {
		t.Fatalf("remove: %+v", r)
	}
}

// The path is judged by components: a member in /a/bc is not under /a/b.
func TestRootsChange_RemoveInUseIsByPathComponents(t *testing.T) {
	f := newFixture(t)
	base := realTemp(t)
	root := filepath.Join(base, "b")
	sibling := filepath.Join(base, "bc")
	for _, d := range []string{root, sibling} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.setLeadGrant(true, []string{root}, 0)
	f.seedRemoteMemberAt("sib", "lead:1", remoteActive, sibling)
	if r := f.rootsOK(rootsReq(cmdUUID1, team.RootsOpRemove, root)); r.Result != team.RootsResultRemoved {
		t.Fatalf("remove: %+v", r)
	}
}

// The pre-check reads team.db and must never run inside UpdateConfig (it would hold the config lock across a database read).
// The seam runs at the start of the pre-check: the config write lock is free there, so TryLock succeeds.
// Mutation gate: run the pre-check inside the UpdateConfig closure → TryLock fails (red).
func TestRootsChange_PreCheckRunsOutsideTheConfigLock(t *testing.T) {
	f := newFixture(t)
	root := realTemp(t)
	f.setLeadGrant(true, []string{root}, 0)
	ran := false
	f.m.rootsPrecheckHook = func() {
		ran = true
		if !f.core.CfgMu.TryLock() {
			t.Error("the in-use pre-check ran while the config lock was held")
			return
		}
		f.core.CfgMu.Unlock()
	}
	f.rootsOK(rootsReq(cmdUUID1, team.RootsOpRemove, root))
	if !ran {
		t.Fatal("the pre-check never ran for a removal that matches a root")
	}
}

// A spawn accepted before the removal is stopped at create / launch: underOpRoots re-reads the grant.
func TestRootsChange_ASpawnAcceptedBeforeTheRemovalFailsAtLaunch(t *testing.T) {
	f := newFixture(t)
	root := realTemp(t)
	work := filepath.Join(root, "w")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	f.setLeadGrant(true, []string{root}, 0)
	op := spawnRow{ID: "op-1", LeadHostID: "lead:1", Cwd: work}
	if _, ok := f.m.underOpRoots(op, work); !ok {
		t.Fatal("setup: the cwd is not under the grant")
	}
	f.rootsOK(rootsReq(cmdUUID1, team.RootsOpRemove, root))
	if _, ok := f.m.underOpRoots(op, work); ok {
		t.Fatal("a spawn launched under a root that was removed after it was accepted")
	}
}

// Consent is looked at again under the config lock: an admin withdrawing it while the pre-check runs changes nothing.
// Mutation gate: drop the closure's AllowTeam check → the removal lands on a withdrawn consent (red).
func TestRootsChange_ConsentWithdrawnDuringThePreCheckIsRefused(t *testing.T) {
	f := newFixture(t)
	root := realTemp(t)
	f.setLeadGrant(true, []string{root}, 2)
	f.m.rootsPrecheckHook = func() { f.setLeadGrant(false, []string{root}, 2) }
	f.refusal(rootsReq(cmdUUID1, team.RootsOpRemove, root), leadPrincipal(), http.StatusForbidden, "host_not_allowed")
	if allow, roots, rev := f.leadGrant(); allow || len(roots) != 1 || rev != 2 {
		t.Fatalf("grant after the refused removal: %v %v %d", allow, roots, rev)
	}
}
