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

func runAllowTeam(t *testing.T, status int, resp any, args ...string) (code int, body map[string]any, method, path, out, errOut string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	// #2340: --root ADDS (a read-modify-write of the whole set could drop another writer's root); the whole-set field is never sent.
	want := []any{"/r/a", filepath.Join(home, "w"), filepath.Join(cwd, "rel")}
	if !reflect.DeepEqual(body["add_team_roots"], want) {
		t.Fatalf("add_team_roots = %v want %v", body["add_team_roots"], want)
	}
	if _, has := body["team_roots"]; has {
		t.Fatalf("the whole-set team_roots was sent: %v", body)
	}
	if _, has := body["remove_team_roots"]; has {
		t.Fatalf("remove_team_roots sent without --remove-root: %v", body)
	}
	if !strings.Contains(out, "air") || !strings.Contains(out, "on") {
		t.Fatalf("stdout = %q", out)
	}
}

// #2340: revoking a root is `--remove-root <dir>` (repeatable), sent as remove_team_roots; it can be combined with --root.
func TestRunPeersCmd_AllowTeamRemoveRootSendsAnAtomicRemoval(t *testing.T) {
	code, body, _, _, _, errOut := runAllowTeam(t, 200, map[string]any{"alias": "air", "allow_team": true, "team_roots": []string{"/r/b"}},
		"air", "on", "--remove-root", "/r/a", "--root", "/r/b")
	if code != 0 {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	if !reflect.DeepEqual(body["remove_team_roots"], []any{"/r/a"}) || !reflect.DeepEqual(body["add_team_roots"], []any{"/r/b"}) {
		t.Fatalf("body = %v", body)
	}
	if _, has := body["team_roots"]; has {
		t.Fatalf("the whole-set team_roots was sent: %v", body)
	}
}

func TestParsePeersInvocation_RemoveRoot(t *testing.T) {
	inv, _, ok := parsePeersInvocation([]string{"host", "allow-team", "air", "off", "--remove-root", "/a", "--remove-root", "/b"})
	if !ok || !reflect.DeepEqual(inv.removeRoots, []string{"/a", "/b"}) || inv.roots != nil {
		t.Fatalf("inv=%+v ok=%v", inv, ok)
	}
	for _, args := range [][]string{
		{"host", "allow-team", "air", "on", "--remove-root"}, // missing value
		{"host", "rename", "a", "b", "--remove-root", "/x"},  // allow-team's alone
		{"host", "list", "--remove-root", "/x"},
		{"--remove-root", "/x"},
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
