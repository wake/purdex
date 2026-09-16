package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- helpers --------------------------------------------------------------

// writeExec creates an executable regular file at path (parents included).
func writeExec(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// writeFileMode creates a regular file with an explicit mode.
func writeFileMode(t *testing.T, path, content string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	return path
}

// runPathT drives runPathCmd and returns exit code, stdout, stderr.
func runPathT(t *testing.T, env pathEnv, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runPathCmd(env, args, &out, &errb)
	return code, out.String(), errb.String()
}

// --- Task 1: the report ---------------------------------------------------

func TestPathReport_ResolvesToThisBinary(t *testing.T) {
	home := t.TempDir()
	self := writeExec(t, filepath.Join(home, ".local", "bin", "pdx"))

	code, out, _ := runPathT(t, pathEnv{
		self: self,
		home: home,
		path: filepath.Join(home, ".local", "bin"),
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, self) {
		t.Errorf("report does not name the resolved binary %q:\n%s", self, out)
	}
	if strings.Contains(out, "path link") || strings.Contains(out, "path add-to-shell") {
		t.Errorf("a healthy machine should not be told to run a fix:\n%s", out)
	}
}

func TestPathReport_ResolvesToDifferentBinary(t *testing.T) {
	home := t.TempDir()
	self := writeExec(t, filepath.Join(home, "repo", "bin", "pdx"))
	other := writeExec(t, filepath.Join(home, "usr", "bin", "pdx"))

	code, out, _ := runPathT(t, pathEnv{
		self: self,
		home: home,
		path: filepath.Join(home, "usr", "bin"),
	})
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, other) {
		t.Errorf("the other pdx must be named:\n%s", out)
	}
}

func TestPathReport_DoesNotResolve(t *testing.T) {
	home := t.TempDir()
	self := writeExec(t, filepath.Join(home, "repo", "bin", "pdx"))

	code, out, _ := runPathT(t, pathEnv{
		self: self,
		home: home,
		path: filepath.Join(home, "empty"),
	})
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	for _, want := range []string{"path link", "path add-to-shell"} {
		if !strings.Contains(out, want) {
			t.Errorf("report must name the %q fix:\n%s", want, out)
		}
	}
}

func TestResolveSelfPath_FollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := writeExec(t, filepath.Join(dir, "real", "pdx"))
	link := filepath.Join(dir, "link-pdx")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, note := resolveSelfPath(link)
	if note != "" {
		t.Errorf("unexpected resolve note %q", note)
	}
	wantReal, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if got != wantReal {
		t.Errorf("resolveSelfPath(%q) = %q, want %q", link, got, wantReal)
	}
}

func TestResolveSelfPath_KeepsUnresolvedPathOnError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone", "pdx")
	got, note := resolveSelfPath(missing)
	if got != missing {
		t.Errorf("got %q, want the unresolved path %q", got, missing)
	}
	if note == "" {
		t.Error("an EvalSymlinks failure must be noted in the report")
	}
}

func TestPathReport_IdentifiesBinaryThroughSymlink(t *testing.T) {
	home := t.TempDir()
	real := writeExec(t, filepath.Join(home, "repo", "bin", "pdx"))
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(localBin, "pdx")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	// Invoked through the symlink: pathEnv.self is what resolveSelfPath
	// produced, i.e. the real binary.
	self, _ := resolveSelfPath(link)
	code, out, _ := runPathT(t, pathEnv{self: self, home: home, path: localBin})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	realResolved, _ := filepath.EvalSymlinks(real)
	if !strings.Contains(out, realResolved) {
		t.Errorf("report must identify the resolved binary %q:\n%s", realResolved, out)
	}
}

func TestPathReport_LocalBinStatesSuggestDifferentFixes(t *testing.T) {
	type want struct{ link, addToShell bool }
	cases := []struct {
		name  string
		setup func(t *testing.T, home string) (pathVar string)
		want  want
	}{
		{
			name: "local bin absent",
			setup: func(t *testing.T, home string) string {
				return filepath.Join(home, "usr", "bin")
			},
			want: want{link: true, addToShell: true},
		},
		{
			name: "local bin present, no symlink, not on PATH",
			setup: func(t *testing.T, home string) string {
				if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o755); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(home, "usr", "bin")
			},
			want: want{link: true, addToShell: true},
		},
		{
			name: "local bin on PATH, no symlink",
			setup: func(t *testing.T, home string) string {
				localBin := filepath.Join(home, ".local", "bin")
				if err := os.MkdirAll(localBin, 0o755); err != nil {
					t.Fatal(err)
				}
				return localBin
			},
			want: want{link: true, addToShell: false},
		},
		{
			name: "symlink present, local bin not on PATH",
			setup: func(t *testing.T, home string) string {
				localBin := filepath.Join(home, ".local", "bin")
				if err := os.MkdirAll(localBin, 0o755); err != nil {
					t.Fatal(err)
				}
				self, _ := filepath.EvalSymlinks(filepath.Join(home, "repo", "bin", "pdx"))
				if err := os.Symlink(self, filepath.Join(localBin, "pdx")); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(home, "usr", "bin")
			},
			want: want{link: false, addToShell: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			self := writeExec(t, filepath.Join(home, "repo", "bin", "pdx"))
			pathVar := tc.setup(t, home)

			code, out, _ := runPathT(t, pathEnv{self: self, home: home, path: pathVar}, "--json")
			if code != 1 {
				t.Fatalf("exit = %d, want 1\n%s", code, out)
			}
			var rep struct {
				Fixes []string `json:"fixes"`
			}
			if err := json.Unmarshal([]byte(out), &rep); err != nil {
				t.Fatalf("json: %v\n%s", err, out)
			}
			has := func(s string) bool {
				for _, f := range rep.Fixes {
					if f == s {
						return true
					}
				}
				return false
			}
			if has("link") != tc.want.link {
				t.Errorf("fixes = %v, want link=%v", rep.Fixes, tc.want.link)
			}
			if has("add-to-shell") != tc.want.addToShell {
				t.Errorf("fixes = %v, want add-to-shell=%v", rep.Fixes, tc.want.addToShell)
			}
		})
	}
}

func TestPathReport_DegeneratePathEntries(t *testing.T) {
	home := t.TempDir()
	self := writeExec(t, filepath.Join(home, "repo", "bin", "pdx"))

	for _, pathVar := range []string{
		"",
		filepath.Join(home, "nope"),
		":" + filepath.Join(home, "nope") + "::",
	} {
		code, out, _ := runPathT(t, pathEnv{self: self, home: home, path: pathVar})
		if code != 1 {
			t.Errorf("PATH %q: exit = %d, want 1\n%s", pathVar, code, out)
		}
	}
}

func TestPathReport_SkipsNonExecutableAndDirectory(t *testing.T) {
	home := t.TempDir()
	self := writeExec(t, filepath.Join(home, "repo", "bin", "pdx"))

	// entry 1: a `pdx` that is not executable — a shell would skip it.
	notExec := filepath.Join(home, "a")
	writeFileMode(t, filepath.Join(notExec, "pdx"), "not executable\n", 0o644)
	// entry 2: a *directory* named pdx — also skipped.
	if err := os.MkdirAll(filepath.Join(home, "b", "pdx"), 0o755); err != nil {
		t.Fatal(err)
	}
	// entry 3: the real one.
	realDir := filepath.Join(home, "c")
	real := writeExec(t, filepath.Join(realDir, "pdx"))
	if err := os.Symlink(self, real+".link"); err != nil {
		t.Fatal(err)
	}

	pathVar := strings.Join([]string{notExec, filepath.Join(home, "b"), realDir}, string(os.PathListSeparator))
	code, out, _ := runPathT(t, pathEnv{self: self, home: home, path: pathVar}, "--json")
	var rep struct {
		Resolved *string `json:"resolved"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if rep.Resolved == nil || *rep.Resolved != real {
		t.Errorf("resolved = %v, want %q (non-executable and directory entries must be skipped)", rep.Resolved, real)
	}
	if code != 1 {
		t.Errorf("exit = %d, want 1 (a different pdx wins)", code)
	}
}

func TestPathReport_FirstOnPathWins(t *testing.T) {
	home := t.TempDir()
	self := writeExec(t, filepath.Join(home, "repo", "bin", "pdx"))
	firstDir := filepath.Join(home, "first")
	first := writeExec(t, filepath.Join(firstDir, "pdx"))
	secondDir := filepath.Join(home, "second")
	writeExec(t, filepath.Join(secondDir, "pdx"))

	pathVar := firstDir + string(os.PathListSeparator) + secondDir
	code, out, _ := runPathT(t, pathEnv{self: self, home: home, path: pathVar})
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, first) {
		t.Errorf("report must name the winning pdx %q:\n%s", first, out)
	}
	if strings.Contains(out, secondDir) {
		t.Errorf("report should not name the shadowed entry:\n%s", out)
	}
}

func TestPathReport_JSONShape(t *testing.T) {
	home := t.TempDir()
	localBin := filepath.Join(home, ".local", "bin")
	self := writeExec(t, filepath.Join(home, "repo", "bin", "pdx"))
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	selfReal, _ := filepath.EvalSymlinks(self)
	if err := os.Symlink(selfReal, filepath.Join(localBin, "pdx")); err != nil {
		t.Fatal(err)
	}

	code, out, _ := runPathT(t, pathEnv{self: self, home: home, path: localBin}, "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	for _, k := range []string{"self", "resolved", "isSelf", "localBin", "localBinExists", "localBinOnPath", "link", "fixes", "ok"} {
		if _, present := raw[k]; !present {
			t.Errorf("--json is missing key %q: %v", k, raw)
		}
	}
	if raw["isSelf"] != true || raw["ok"] != true {
		t.Errorf("isSelf/ok should be true: %v", raw)
	}
	if raw["link"] != "ok" {
		t.Errorf("link = %v, want \"ok\"", raw["link"])
	}
	if raw["localBinOnPath"] != true || raw["localBinExists"] != true {
		t.Errorf("localBin flags wrong: %v", raw)
	}
}

func TestPathGrammarErrors(t *testing.T) {
	home := t.TempDir()
	self := writeExec(t, filepath.Join(home, "pdx"))
	env := pathEnv{self: self, home: home, path: home}

	cases := [][]string{
		{"bogus"},
		{"--nope"},
		{"link", "--nope"},
		{"add-to-shell", "--nope"},
		{"link", "extra"},
	}
	for _, args := range cases {
		code, out, errOut := runPathT(t, env, args...)
		if code != 2 {
			t.Errorf("args %v: exit = %d, want 2", args, code)
		}
		if errOut == "" {
			t.Errorf("args %v: nothing on stderr", args)
		}
		if out != "" {
			t.Errorf("args %v: grammar error wrote to stdout: %q", args, out)
		}
	}
}

func TestMainUsageListsPath(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `case "path":`) {
		t.Error("main.go's switch has no `path` case")
	}
	// The usage list is hand-written; nothing but this test keeps it honest.
	line := ""
	for _, l := range strings.Split(string(src), "\n") {
		if strings.Contains(l, "Commands:") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatal("main.go has no Commands: usage line")
	}
	if !strings.Contains(line, "path") {
		t.Errorf("Commands usage line does not mention path: %s", line)
	}
}

// TestPathIsOffline asserts, at the source level, that cmd/pdx/path.go has no
// import-level dependency on the config loader, the store, or HTTP. A grep is
// weak evidence in general; here it is checking for the *absence* of a
// dependency in one small file, which is exactly what it can prove (spec §7.0).
func TestPathIsOffline(t *testing.T) {
	src, err := os.ReadFile("path.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"config.Load", "net/http", "http.", "store.", "internal/store", "internal/config"} {
		if strings.Contains(string(src), banned) {
			t.Errorf("path.go must not reference %q — `pdx path` is an offline repair command (spec §3)", banned)
		}
	}
}
