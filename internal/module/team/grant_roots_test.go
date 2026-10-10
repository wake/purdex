package teammod

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// #2450: a local grant stores the REAL path of each root (as a forwarded one does, config.CanonicalTeamRoots); a spawn
// refuses a root that no longer resolves to itself. A grant made before this (no roots_canonical) keeps judging its roots
// the way it always did: resolved at each spawn.

func realTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func symlinkTo(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func leadPayloadOf(t *testing.T, a team.Approval) team.LeadPayload {
	t.Helper()
	var p team.LeadPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// Mutation gate: create storing normaliseRoots' result without canonicalRoots → the link is stored (red).
func TestCreate_StoresTheRealPathOfARootThatIsASymlink(t *testing.T) {
	base := realTemp(t)
	real := mustMkdir(t, filepath.Join(base, "x", "work"))
	link := filepath.Join(base, "work")
	symlinkTo(t, real, link)
	missing := filepath.Join(base, "not-there")
	f := newFixture(t)
	f.createReqEdit = func(r *team.CreateApprovalRequest) { r.Roots = []string{link, missing + "/"} }
	p := leadPayloadOf(t, f.create(uid(1)))
	if want := []string{real, missing}; !reflect.DeepEqual(p.Roots, want) {
		t.Fatalf("payload roots = %v, want %v (a link resolved, a missing root kept Clean)", p.Roots, want)
	}
	if !p.RootsCanonical {
		t.Fatal("a payload made now must say its roots are canonical")
	}
}

// What the user saw on the card is what a decide without edited roots grants: not resolved again. Mutation gate:
// decide re-resolving the payload's roots → the grant follows the swap to elsewhere (red).
func TestDecide_UneditedRootsAreTheCardsNotResolvedAgain(t *testing.T) {
	base := realTemp(t)
	root := mustMkdir(t, filepath.Join(base, "granted"))
	elsewhere := mustMkdir(t, filepath.Join(base, "elsewhere"))
	f := newFixture(t)
	f.createReqEdit = func(r *team.CreateApprovalRequest) { r.Roots = []string{root} }
	f.create(uid(1))
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	symlinkTo(t, elsewhere, root) // swapped between the card and the tap
	f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide", appApprove(&team.Grant{MaxMembers: 2}))
	tm, ok, err := f.m.store.LiveTeamByLead("sid-1")
	if err != nil || !ok {
		t.Fatalf("team: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(tm.Grant.Roots, []string{root}) || !tm.Grant.RootsCanonical {
		t.Fatalf("grant = %+v, want roots [%s] canonical", tm.Grant, root)
	}
	if underGrant(elsewhere, tm.Grant) {
		t.Fatal("a root swapped for a symlink after the card admits the link's target")
	}
}

// Roots the App edited are resolved at the tap. Mutation gate: no canonicalRoots in decide → the link is stored (red).
func TestDecide_EditedRootsAreResolved(t *testing.T) {
	base := realTemp(t)
	real := mustMkdir(t, filepath.Join(base, "x", "work"))
	link := filepath.Join(base, "work")
	symlinkTo(t, real, link)
	f := newFixture(t)
	f.create(uid(1))
	f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide", appApprove(&team.Grant{MaxMembers: 2, Roots: []string{link}}))
	tm, ok, _ := f.m.store.LiveTeamByLead("sid-1")
	if !ok || !reflect.DeepEqual(tm.Grant.Roots, []string{real}) || !tm.Grant.RootsCanonical {
		t.Fatalf("grant = %+v ok=%v, want roots [%s] canonical", tm.Grant, ok, real)
	}
	if !underGrant(real, tm.Grant) {
		t.Fatal("the resolved root admits nothing")
	}
}

// A pending approval made before this version has Clean-only roots in its payload: approved unedited, its grant is a
// legacy one (not canonical), so its symlinked root keeps working.
func TestDecide_LegacyPayloadStaysLegacy(t *testing.T) {
	base := realTemp(t)
	real := mustMkdir(t, filepath.Join(base, "x", "work"))
	link := filepath.Join(base, "work")
	symlinkTo(t, real, link)
	f := newFixture(t)
	f.create(uid(1))
	old, _ := json.Marshal(map[string]any{"reason": "r", "max_members": 3, "roots": []string{link}, "team_name": "", "team_label": ""})
	if _, err := f.m.store.db.Exec(`UPDATE approval_requests SET payload_json = ? WHERE id = ?`, string(old), uid(1)); err != nil {
		t.Fatal(err)
	}
	f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide", appApprove(nil))
	tm, ok, _ := f.m.store.LiveTeamByLead("sid-1")
	if !ok || tm.Grant.RootsCanonical || !reflect.DeepEqual(tm.Grant.Roots, []string{link}) {
		t.Fatalf("grant = %+v ok=%v, want legacy roots [%s]", tm.Grant, ok, link)
	}
	if !underGrant(real, tm.Grant) {
		t.Fatal("a legacy grant's symlinked root stopped admitting its target")
	}
}

// The spawn rule. Mutation gate: underGrant without the live-root filter → the swapped root admits (red).
func TestUnderGrant_CanonicalRootMustStillBeItself(t *testing.T) {
	base := realTemp(t)
	root := mustMkdir(t, filepath.Join(base, "granted"))
	inside := mustMkdir(t, filepath.Join(root, "p"))
	elsewhere := mustMkdir(t, filepath.Join(base, "elsewhere"))
	g := team.Grant{Roots: []string{root}, RootsCanonical: true}
	if !underGrant(inside, g) {
		t.Fatal("a directory under an unchanged canonical root is refused")
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	symlinkTo(t, elsewhere, root)
	if underGrant(elsewhere, g) {
		t.Fatal("a canonical root replaced by a symlink admits the link's target")
	}
	if !underGrant(elsewhere, team.Grant{Roots: []string{root}}) { // legacy: resolved at spawn, as before
		t.Fatal("a legacy grant must keep its spawn-time resolution")
	}
}
