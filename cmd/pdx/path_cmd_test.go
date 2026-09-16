package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- helpers --------------------------------------------------------------
//
// The helpers shared by every path_*_test.go file live here, in the test
// file for the command entry point, because that is where the one they are
// all built around lives: runPathT wraps runPathCmd, the single door into
// the whole command. Putting them in a sixth file would add a file that
// tests nothing, and duplicating them per file would let five copies drift.

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

// assertLockFree proves the lock was released, by taking it. With flock the
// lockfile's existence means nothing; only the kernel lock does.
func assertLockFree(t *testing.T, dir string) {
	t.Helper()
	l, err := acquirePathLock(dir, lockPolicy{retryEvery: 5 * time.Millisecond, timeout: 200 * time.Millisecond})
	if err != nil {
		t.Errorf("lock was not released: %v", err)
		return
	}
	if err := l.release(); err != nil {
		t.Errorf("release: %v", err)
	}
}

func mkdirAllT(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
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

// TestPathIsOffline asserts, at the source level, that **every** path*.go file
// is free of an import-level dependency on the config loader, the store, or
// HTTP (spec §7.0).
//
// It globs rather than naming one file on purpose: round 2's file-health
// review pointed out that a guard reading only path.go becomes false
// confidence the moment the file is split — a new path_shell.go could import
// the config loader while the test named "IsOffline" stayed green. A grep is
// weak evidence in general; for the *absence* of a dependency across a known
// set of files it is exactly the evidence it claims to be, and
// TestPathSubcommandsRunOffline below exercises the behaviour as well.
func TestPathIsOffline(t *testing.T) {
	files, err := filepath.Glob("path*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no path*.go files found — the guard would pass vacuously")
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, banned := range []string{"config.Load", "net/http", "http.", "store.", "internal/store", "internal/config"} {
			if strings.Contains(string(src), banned) {
				t.Errorf("%s must not reference %q — `pdx path` is an offline repair command (spec §3)", f, banned)
			}
		}
	}
}

// All three subcommands must actually run with no config file, no daemon and
// no network — the source guard above proves the imports are absent, this
// proves the code paths do not need them.
func TestPathSubcommandsRunOffline(t *testing.T) {
	home := t.TempDir()
	self := writeExec(t, filepath.Join(home, ".config", "pdx", "bin", "pdx"))
	env := pathEnv{self: self, home: home, path: filepath.Join(home, "empty"), shell: "/bin/zsh", goos: "darwin"}

	for _, args := range [][]string{{"--json"}, {"link"}, {"add-to-shell", "--dry-run"}} {
		var out, errb bytes.Buffer
		// Nothing here creates a config, starts a daemon, or opens a socket;
		// a subcommand that needed one would fail or hang rather than return.
		_ = runPathCmd(env, args, &out, &errb)
		if out.Len() == 0 && errb.Len() == 0 {
			t.Errorf("pdx path %v produced no output at all", args)
		}
	}
}
