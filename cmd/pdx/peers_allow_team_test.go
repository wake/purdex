// cmd/pdx/peers_allow_team_test.go
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParsePeersInvocation_AllowTeamGrammar(t *testing.T) {
	bad := [][]string{
		{"host", "allow-team"},
		{"host", "allow-team", "a"},
		{"host", "allow-team", "a", "maybe"},
		{"host", "allow-team", "a", "on", "extra"},
		{"host", "allow-team", "a", "on", "--root"}, // missing value
		{"host", "allow-team", "a", "on", "--json"},
		{"host", "allow-team", "a", "on", "--token", "x"},
		{"host", "allow-team", "a", "on", "--allow-bypass=true"},
		{"host", "rename", "a", "b", "--root", "/x"}, // --root belongs to allow-team alone
		{"host", "list", "--root", "/x"},
		{"--root", "/x"},
		{"alias", "--root", "/x"},
	}
	for _, args := range bad {
		if _, _, ok := parsePeersInvocation(args); ok {
			t.Errorf("accepted %v", args)
		}
	}
	inv, _, ok := parsePeersInvocation([]string{"host", "allow-team", "air", "on", "--root", "/a", "--root", "/b"})
	if !ok || inv.verb != "allow-team" || !reflect.DeepEqual(inv.positionals, []string{"air", "on"}) ||
		!reflect.DeepEqual(inv.roots, []string{"/a", "/b"}) {
		t.Fatalf("inv=%+v ok=%v", inv, ok)
	}
	if inv, _, ok = parsePeersInvocation([]string{"host", "allow-team", "air", "off"}); !ok || inv.roots != nil {
		t.Fatalf("off: inv=%+v ok=%v", inv, ok)
	}
}

// listedRev is the team_roots_rev the fake daemon's list gives the host "air"; getsSeen counts the list reads of the last run.
const listedRev = 3

var getsSeen int

func runAllowTeam(t *testing.T, status int, resp any, args ...string) (code int, body map[string]any, method, path, out, errOut string) {
	t.Helper()
	getsSeen = 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet { // the list a whole-set --root reads its revision from (#2340)
			getsSeen++
			_ = json.NewEncoder(w).Encode(map[string]any{"hosts": []map[string]any{
				{"alias": "other", "team_roots_rev": 99}, {"alias": "air", "team_roots_rev": listedRev}}})
			return
		}
		method, path = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code = runPeersCmd(append([]string{"host", "allow-team"}, append(args, "--config", cfgPath)...), &stdout, &stderr)
	return code, body, method, path, stdout.String(), stderr.String()
}

func TestRunPeersCmd_AllowTeamOnWithRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	row := map[string]any{"alias": "air", "allow_team": true, "team_roots": []string{"/r/a", filepath.Join(home, "w")}}
	cwd, _ := os.Getwd()
	code, body, method, path, out, errOut := runAllowTeam(t, 200, row, "air", "on", "--root", "/r/a", "--root", "~/w", "--root", "rel")
	if code != 0 || method != http.MethodPut || path != "/api/peers/hosts/air" {
		t.Fatalf("code=%d %s %s err=%q", code, method, path, errOut)
	}
	if body["allow_team"] != true {
		t.Fatalf("body = %v", body)
	}
	// ~ is expanded and a relative root made absolute by the CLI; the API gets absolute paths only.
	// #2340: --root keeps its meaning (set the roots to exactly these), but it now reads the revision first and sends it.
	want := []any{"/r/a", filepath.Join(home, "w"), filepath.Join(cwd, "rel")}
	if !reflect.DeepEqual(body["team_roots"], want) {
		t.Fatalf("team_roots = %v want %v", body["team_roots"], want)
	}
	if body["team_roots_rev"] != float64(listedRev) || getsSeen != 1 {
		t.Fatalf("revision sent %v after %d list reads, want %d after 1", body["team_roots_rev"], getsSeen, listedRev)
	}
	for _, k := range []string{"add_team_roots", "remove_team_roots"} {
		if _, has := body[k]; has {
			t.Fatalf("%s sent by --root: %v", k, body)
		}
	}
	if !strings.Contains(out, "air") || !strings.Contains(out, "on") {
		t.Fatalf("stdout = %q", out)
	}
}

// #2340: --add-root / --remove-root are the atomic edits: no list read, no revision, never the whole-set field.
func TestRunPeersCmd_AllowTeamAddAndRemoveRootAreAtomicEdits(t *testing.T) {
	code, body, _, _, _, errOut := runAllowTeam(t, 200, map[string]any{"alias": "air", "allow_team": true, "team_roots": []string{"/r/b"}},
		"air", "on", "--remove-root", "/r/a", "--add-root", "/r/b", "--add-root", "/r/c")
	if code != 0 {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	if !reflect.DeepEqual(body["remove_team_roots"], []any{"/r/a"}) || !reflect.DeepEqual(body["add_team_roots"], []any{"/r/b", "/r/c"}) {
		t.Fatalf("body = %v", body)
	}
	for _, k := range []string{"team_roots", "team_roots_rev"} {
		if _, has := body[k]; has {
			t.Fatalf("%s sent by an atomic edit: %v", k, body)
		}
	}
	if getsSeen != 0 {
		t.Fatalf("an atomic edit read the list %d times", getsSeen)
	}
}

// A whole-set --root that meets a changed set is not applied over it: the daemon's 409 is reported with the current set and
// the command is to be run again (never a silent overwrite).
func TestRunPeersCmd_AllowTeamWholeSetConflictAsksForARetry(t *testing.T) {
	code, _, _, _, out, errOut := runAllowTeam(t, 409,
		map[string]any{"error": "team_roots_conflict", "team_roots": []string{"/r/x", "/r/y"}, "team_roots_rev": 4}, "air", "on", "--root", "/r/a")
	if code == 0 || out != "" || !strings.Contains(errOut, "team_roots_conflict") || !strings.Contains(errOut, "/r/x, /r/y") || !strings.Contains(errOut, "again") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestParsePeersInvocation_AddAndRemoveRoot(t *testing.T) {
	inv, _, ok := parsePeersInvocation([]string{"host", "allow-team", "air", "off", "--remove-root", "/a", "--add-root", "/b", "--remove-root", "/c"})
	if !ok || !reflect.DeepEqual(inv.removeRoots, []string{"/a", "/c"}) || !reflect.DeepEqual(inv.addRoots, []string{"/b"}) || inv.roots != nil {
		t.Fatalf("inv=%+v ok=%v", inv, ok)
	}
	for _, args := range [][]string{
		{"host", "allow-team", "air", "on", "--remove-root"}, // missing value
		{"host", "allow-team", "air", "on", "--add-root"},
		{"host", "allow-team", "air", "on", "--root", "/a", "--add-root", "/b"}, // whole set or edits, not both
		{"host", "allow-team", "air", "on", "--root", "/a", "--remove-root", "/b"},
		{"host", "rename", "a", "b", "--remove-root", "/x"}, // allow-team's alone
		{"host", "rename", "a", "b", "--add-root", "/x"},
		{"host", "list", "--remove-root", "/x"},
		{"--add-root", "/x"},
	} {
		if _, _, ok := parsePeersInvocation(args); ok {
			t.Errorf("accepted %v", args)
		}
	}
}

func TestRunPeersCmd_AllowTeamOffLeavesRootsAlone(t *testing.T) {
	code, body, _, _, _, errOut := runAllowTeam(t, 200, map[string]any{"alias": "air", "allow_team": false, "team_roots": []string{}}, "air", "off")
	if code != 0 || body["allow_team"] != false {
		t.Fatalf("code=%d body=%v err=%q", code, body, errOut)
	}
	if _, has := body["team_roots"]; has {
		t.Fatalf("team_roots sent without --root: %v", body)
	}
}

func TestRunPeersCmd_AllowTeamBadRootIsReported(t *testing.T) {
	code, _, _, _, _, errOut := runAllowTeam(t, 400,
		map[string]string{"error": "bad_root", "root": "/nope", "detail": "not_found"}, "air", "on", "--root", "/nope")
	if code == 0 || !strings.Contains(errOut, "/nope") || !strings.Contains(errOut, "not_found") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}
