package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

// --- Task 2: the lockfile helper -----------------------------------------

func TestPathLock_AcquireReleaseAndPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".local", "bin")

	lock, err := acquirePathLock(dir, lockPolicy{})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	want := filepath.Join(dir, ".pdx-path.lock")
	if lock.path != want {
		t.Errorf("lock path = %q, want %q (one lock, shared by link and add-to-shell)", lock.path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("lockfile not present while held: %v", err)
	}
	if err := lock.release(); err != nil {
		t.Errorf("release: %v", err)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Errorf("lockfile survived release: %v", err)
	}
}

func TestPathLock_DefaultPolicy(t *testing.T) {
	p := lockPolicy{}.withDefaults()
	if p.retryEvery != 50*time.Millisecond {
		t.Errorf("retryEvery = %v, want 50ms", p.retryEvery)
	}
	if p.timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", p.timeout)
	}
	if p.staleAfter != 30*time.Second {
		t.Errorf("staleAfter = %v, want 30s", p.staleAfter)
	}
}

func TestPathLock_HeldTimesOutNamingTheLock(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".pdx-path.lock")
	if err := os.WriteFile(lockPath, []byte("held\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := acquirePathLock(dir, lockPolicy{retryEvery: 10 * time.Millisecond, timeout: 120 * time.Millisecond, staleAfter: time.Hour})
	if err == nil {
		t.Fatal("acquire succeeded while the lock was held")
	}
	if !strings.Contains(err.Error(), lockPath) {
		t.Errorf("error must name the lock path %q: %v", lockPath, err)
	}
	if _, statErr := os.Stat(lockPath); statErr != nil {
		t.Errorf("a held lock must not be stolen: %v", statErr)
	}
}

func TestPathLock_StaleIsBroken(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".pdx-path.lock")
	if err := os.WriteFile(lockPath, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-40 * time.Second)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}

	lock, err := acquirePathLock(dir, lockPolicy{retryEvery: 10 * time.Millisecond, timeout: 200 * time.Millisecond, staleAfter: 30 * time.Second})
	if err != nil {
		t.Fatalf("a stale lock must be broken, not deadlocked on: %v", err)
	}
	if err := lock.release(); err != nil {
		t.Fatal(err)
	}
}

// --- Task 2: pdx path link ------------------------------------------------

func mkdirAllT(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

// linkFixture builds a home with a self binary and returns (env, linkPath).
func linkFixture(t *testing.T) (pathEnv, string) {
	t.Helper()
	home := t.TempDir()
	self := writeExec(t, filepath.Join(home, "repo", "bin", "pdx"))
	selfReal, _ := filepath.EvalSymlinks(self)
	env := pathEnv{self: selfReal, home: home, path: filepath.Join(home, "usr", "bin")}
	return env, filepath.Join(env.localBinDir(), "pdx")
}

func TestPathLink_TableOfStates(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(t *testing.T, env pathEnv, link string)
		force    bool
		wantExit int
		wantOut  []string // substrings required in stdout+stderr
		wantLink bool     // afterwards: link points at env.self
	}{
		{
			name:     "nothing there",
			setup:    func(t *testing.T, env pathEnv, link string) {},
			wantExit: 0,
			wantLink: true,
		},
		{
			name: "already correct",
			setup: func(t *testing.T, env pathEnv, link string) {
				mkdirAllT(t, filepath.Dir(link))
				if err := os.Symlink(env.self, link); err != nil {
					t.Fatal(err)
				}
			},
			wantExit: 0,
			wantOut:  []string{"already"},
			wantLink: true,
		},
		{
			name: "symlink to something else",
			setup: func(t *testing.T, env pathEnv, link string) {
				mkdirAllT(t, filepath.Dir(link))
				other := writeExec(t, filepath.Join(env.home, "other", "pdx"))
				if err := os.Symlink(other, link); err != nil {
					t.Fatal(err)
				}
			},
			wantExit: 1,
			wantOut:  []string{"other", "--force"},
		},
		{
			name: "dangling symlink",
			setup: func(t *testing.T, env pathEnv, link string) {
				mkdirAllT(t, filepath.Dir(link))
				if err := os.Symlink(filepath.Join(env.home, "gone", "pdx"), link); err != nil {
					t.Fatal(err)
				}
			},
			wantExit: 1,
			wantOut:  []string{"gone"},
		},
		{
			name: "regular file",
			setup: func(t *testing.T, env pathEnv, link string) {
				writeFileMode(t, link, "mine\n", 0o644)
			},
			wantExit: 1,
			wantOut:  []string{"regular file"},
		},
		{
			name: "regular file with --force",
			setup: func(t *testing.T, env pathEnv, link string) {
				writeFileMode(t, link, "mine\n", 0o644)
			},
			force:    true,
			wantExit: 1,
			wantOut:  []string{"regular file"},
		},
		{
			name: "directory",
			setup: func(t *testing.T, env pathEnv, link string) {
				mkdirAllT(t, link)
			},
			wantExit: 1,
			wantOut:  []string{"directory"},
		},
		{
			name: "directory with --force",
			setup: func(t *testing.T, env pathEnv, link string) {
				mkdirAllT(t, link)
			},
			force:    true,
			wantExit: 1,
			wantOut:  []string{"directory"},
		},
		{
			name: "symlink to something else with --force",
			setup: func(t *testing.T, env pathEnv, link string) {
				mkdirAllT(t, filepath.Dir(link))
				other := writeExec(t, filepath.Join(env.home, "other", "pdx"))
				if err := os.Symlink(other, link); err != nil {
					t.Fatal(err)
				}
			},
			force:    true,
			wantExit: 0,
			wantLink: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, link := linkFixture(t)
			tc.setup(t, env, link)

			args := []string{"link"}
			if tc.force {
				args = append(args, "--force")
			}
			code, out, errOut := runPathT(t, env, args...)
			if code != tc.wantExit {
				t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, tc.wantExit, out, errOut)
			}
			both := out + errOut
			for _, want := range tc.wantOut {
				if !strings.Contains(both, want) {
					t.Errorf("output must mention %q:\n%s", want, both)
				}
			}
			if tc.wantLink {
				fi, err := os.Lstat(link)
				if err != nil {
					t.Fatalf("no symlink at %s: %v", link, err)
				}
				if fi.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("%s is not a symlink", link)
				}
				got, err := filepath.EvalSymlinks(link)
				if err != nil {
					t.Fatal(err)
				}
				if got != env.self {
					t.Errorf("link -> %q, want %q", got, env.self)
				}
			}
			// The lockfile is never left behind.
			if _, err := os.Stat(filepath.Join(env.localBinDir(), ".pdx-path.lock")); err == nil {
				t.Error("lockfile left behind")
			}
			// A refusal must not damage what was there.
			if tc.wantExit != 0 {
				if _, err := os.Lstat(link); err != nil {
					t.Errorf("a refusal removed the existing path: %v", err)
				}
			}
		})
	}
}

func TestPathLink_CreatesLocalBinDir(t *testing.T) {
	env, link := linkFixture(t)
	if _, err := os.Stat(env.localBinDir()); !os.IsNotExist(err) {
		t.Fatalf("fixture should start without ~/.local/bin: %v", err)
	}
	code, out, errOut := runPathT(t, env, "link")
	if code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, out, errOut)
	}
	if fi, err := os.Stat(env.localBinDir()); err != nil || !fi.IsDir() {
		t.Fatalf("~/.local/bin not created: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("symlink not created: %v", err)
	}
}

// TestPathLink_ConcurrentRunsSerialise asserts pdx-level serialisation only:
// spec §3.2 states that an external process racing the rename is out of
// scope, and a test claiming otherwise would assert a property the code does
// not have.
func TestPathLink_ConcurrentRunsSerialise(t *testing.T) {
	env, link := linkFixture(t)

	var wg sync.WaitGroup
	codes := make([]int, 4)
	outs := make([]string, 4)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var out, errb bytes.Buffer
			codes[i] = runPathCmd(env, []string{"link"}, &out, &errb)
			outs[i] = out.String() + errb.String()
		}(i)
	}
	wg.Wait()

	for i, c := range codes {
		if c != 0 {
			t.Errorf("run %d exited %d: %s", i, c, outs[i])
		}
	}
	fi, err := os.Lstat(link)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected exactly one symlink at %s: %v", link, err)
	}
	got, _ := filepath.EvalSymlinks(link)
	if got != env.self {
		t.Errorf("link -> %q, want %q", got, env.self)
	}
	if _, err := os.Stat(filepath.Join(env.localBinDir(), ".pdx-path.lock")); err == nil {
		t.Error("lockfile left behind")
	}
}

func TestPathLink_ReportsLockTimeout(t *testing.T) {
	env, _ := linkFixture(t)
	mkdirAllT(t, env.localBinDir())
	lockPath := filepath.Join(env.localBinDir(), ".pdx-path.lock")
	if err := os.WriteFile(lockPath, []byte("held\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env.lock = lockPolicy{retryEvery: 10 * time.Millisecond, timeout: 100 * time.Millisecond, staleAfter: time.Hour}

	code, _, errOut := runPathT(t, env, "link")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut, lockPath) {
		t.Errorf("stderr must name the lock path:\n%s", errOut)
	}
}

// --- Task 3: pdx path add-to-shell ---------------------------------------

const (
	testMarkerStart = "# >>> pdx path >>>"
	testExportLine  = `export PATH="$HOME/.local/bin:$PATH"`
)

// shellFixture builds a home with a self binary, an injected $SHELL and GOOS,
// and a PATH that does NOT contain ~/.local/bin.
func shellFixture(t *testing.T, shell, goos string) pathEnv {
	t.Helper()
	home := filepath.Join(t.TempDir(), "my home") // a space in $HOME is normal
	mkdirAllT(t, home)
	self := writeExec(t, filepath.Join(home, "repo", "bin", "pdx"))
	selfReal, _ := filepath.EvalSymlinks(self)
	return pathEnv{
		self:  selfReal,
		home:  home,
		path:  filepath.Join(home, "usr", "bin"),
		shell: shell,
		goos:  goos,
	}
}

func readFileT(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func TestAddToShell_PicksTheRightRcFile(t *testing.T) {
	cases := []struct {
		shell, goos, want string
	}{
		{"/bin/zsh", "darwin", ".zshrc"},
		{"/usr/bin/zsh", "linux", ".zshrc"},
		{"/bin/bash", "darwin", ".bash_profile"},
		{"/usr/bin/bash", "linux", ".bashrc"},
	}
	for _, tc := range cases {
		t.Run(tc.shell+"/"+tc.goos, func(t *testing.T) {
			env := shellFixture(t, tc.shell, tc.goos)
			code, out, errOut := runPathT(t, env, "add-to-shell")
			if code != 0 {
				t.Fatalf("exit = %d\n%s%s", code, out, errOut)
			}
			rc := filepath.Join(env.home, tc.want)
			body := readFileT(t, rc)
			if !strings.Contains(body, testMarkerStart) || !strings.Contains(body, testExportLine) {
				t.Errorf("%s does not contain the block:\n%s", rc, body)
			}
			if !strings.Contains(out, rc) {
				t.Errorf("output must name the file it edited:\n%s", out)
			}
		})
	}
}

func TestAddToShell_RefusesUnknownAndUnsetShell(t *testing.T) {
	for _, shell := range []string{"/usr/local/bin/fish", ""} {
		t.Run("shell="+shell, func(t *testing.T) {
			env := shellFixture(t, shell, "darwin")
			code, out, errOut := runPathT(t, env, "add-to-shell")
			if code != 1 {
				t.Fatalf("exit = %d, want 1", code)
			}
			both := out + errOut
			if !strings.Contains(both, testExportLine) {
				t.Errorf("a refusal must print the line to add manually:\n%s", both)
			}
			entries, err := os.ReadDir(env.home)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".") {
					t.Errorf("a refusal wrote %s", e.Name())
				}
			}
		})
	}
}

func TestAddToShell_IsIdempotent(t *testing.T) {
	env := shellFixture(t, "/bin/zsh", "darwin")
	rc := filepath.Join(env.home, ".zshrc")

	if code, out, errOut := runPathT(t, env, "add-to-shell"); code != 0 {
		t.Fatalf("first run exit = %d\n%s%s", code, out, errOut)
	}
	first := readFileT(t, rc)

	code, out, errOut := runPathT(t, env, "add-to-shell")
	if code != 0 {
		t.Fatalf("second run exit = %d\n%s%s", code, out, errOut)
	}
	if got := readFileT(t, rc); got != first {
		t.Errorf("second run rewrote the file:\n%s", got)
	}
	if !strings.Contains(out+errOut, "already") {
		t.Errorf("the second run should say the block is already there:\n%s%s", out, errOut)
	}
	if n := strings.Count(readFileT(t, rc), testMarkerStart); n != 1 {
		t.Errorf("marker appears %d times, want 1", n)
	}
}

func TestAddToShell_AlreadyOnPathWritesNothing(t *testing.T) {
	env := shellFixture(t, "/bin/zsh", "darwin")
	env.path = env.localBinDir() + string(os.PathListSeparator) + filepath.Join(env.home, "usr", "bin")
	rc := filepath.Join(env.home, ".zshrc")
	writeFileMode(t, rc, "# mine\n", 0o644)

	code, out, errOut := runPathT(t, env, "add-to-shell")
	if code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, out, errOut)
	}
	if got := readFileT(t, rc); got != "# mine\n" {
		t.Errorf("rc file was modified although ~/.local/bin is already on PATH:\n%s", got)
	}
	if _, err := os.Stat(rc + ".pdx-backup"); err == nil {
		t.Error("a no-op run created a backup")
	}
}

func TestAddToShell_RcFileIsSymlink(t *testing.T) {
	env := shellFixture(t, "/bin/zsh", "darwin")
	rc := filepath.Join(env.home, ".zshrc")
	target := filepath.Join(env.home, "dotfiles", "zshrc")
	writeFileMode(t, target, "# managed\n", 0o644)
	if err := os.Symlink(target, rc); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runPathT(t, env, "add-to-shell")
	if code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, out, errOut)
	}

	fi, err := os.Lstat(rc)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the rc symlink was replaced by a regular file — a dotfiles repo would be silently detached")
	}
	body := readFileT(t, target)
	if !strings.Contains(body, testMarkerStart) {
		t.Errorf("the symlink's target was not edited:\n%s", body)
	}
	if !strings.Contains(body, "# managed") {
		t.Errorf("the target's original content was lost:\n%s", body)
	}
	if _, err := os.Stat(target + ".pdx-backup"); err != nil {
		t.Errorf("the backup must sit beside the file actually written: %v", err)
	}
}

func TestAddToShell_RefusesSymlinkOutsideHome(t *testing.T) {
	env := shellFixture(t, "/bin/zsh", "darwin")
	outside := filepath.Join(t.TempDir(), "elsewhere-zshrc")
	writeFileMode(t, outside, "# not yours\n", 0o644)
	rc := filepath.Join(env.home, ".zshrc")
	if err := os.Symlink(outside, rc); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runPathT(t, env, "add-to-shell")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out+errOut, testExportLine) {
		t.Errorf("a refusal must print the block for manual use:\n%s%s", out, errOut)
	}
	if got := readFileT(t, outside); got != "# not yours\n" {
		t.Errorf("a file outside $HOME was edited:\n%s", got)
	}
}

func TestAddToShell_RefusesDanglingSymlink(t *testing.T) {
	env := shellFixture(t, "/bin/zsh", "darwin")
	rc := filepath.Join(env.home, ".zshrc")
	if err := os.Symlink(filepath.Join(env.home, "gone", "zshrc"), rc); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runPathT(t, env, "add-to-shell")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out+errOut, testExportLine) {
		t.Errorf("a refusal must print the block:\n%s%s", out, errOut)
	}
}

func TestAddToShell_AddsMissingTrailingNewline(t *testing.T) {
	env := shellFixture(t, "/bin/zsh", "darwin")
	rc := filepath.Join(env.home, ".zshrc")
	writeFileMode(t, rc, "alias ll='ls -l'", 0o644) // no trailing newline

	if code, out, errOut := runPathT(t, env, "add-to-shell"); code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, out, errOut)
	}
	body := readFileT(t, rc)
	if !strings.Contains(body, "alias ll='ls -l'\n") {
		t.Errorf("the last original line lost its integrity:\n%q", body)
	}
	if strings.Contains(body, "alias ll='ls -l'"+testMarkerStart) {
		t.Errorf("the marker was spliced onto a live command:\n%q", body)
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, testMarkerStart) && line != testMarkerStart {
			t.Errorf("the marker must start on its own line, got %q", line)
		}
	}
}

func TestAddToShell_BackupCreatedOnceAndNeverOverwritten(t *testing.T) {
	env := shellFixture(t, "/bin/zsh", "darwin")
	rc := filepath.Join(env.home, ".zshrc")
	writeFileMode(t, rc, "# original\n", 0o644)

	if code, _, errOut := runPathT(t, env, "add-to-shell"); code != 0 {
		t.Fatalf("exit = %d: %s", code, errOut)
	}
	backup := rc + ".pdx-backup"
	if got := readFileT(t, backup); got != "# original\n" {
		t.Fatalf("backup = %q, want the pre-pdx content", got)
	}

	// Remove the block so the second run writes again, then check the
	// backup still holds the *original* state.
	writeFileMode(t, rc, "# edited later\n", 0o644)
	if code, _, errOut := runPathT(t, env, "add-to-shell"); code != 0 {
		t.Fatalf("second run exit = %d: %s", code, errOut)
	}
	if got := readFileT(t, backup); got != "# original\n" {
		t.Errorf("backup was overwritten: %q", got)
	}
}

func TestAddToShell_DryRunWritesNothing(t *testing.T) {
	env := shellFixture(t, "/bin/zsh", "darwin")
	rc := filepath.Join(env.home, ".zshrc")
	writeFileMode(t, rc, "# original\n", 0o644)

	code, out, errOut := runPathT(t, env, "add-to-shell", "--dry-run")
	if code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, rc) || !strings.Contains(out, testExportLine) {
		t.Errorf("--dry-run must print the file and the block:\n%s", out)
	}
	if got := readFileT(t, rc); got != "# original\n" {
		t.Errorf("--dry-run wrote to the rc file:\n%s", got)
	}
	if _, err := os.Stat(rc + ".pdx-backup"); err == nil {
		t.Error("--dry-run created a backup")
	}
	if _, err := os.Stat(env.localBinDir()); err == nil {
		t.Error("--dry-run created ~/.local/bin")
	}
}

func TestAddToShell_PreservesMode(t *testing.T) {
	env := shellFixture(t, "/bin/zsh", "darwin")
	rc := filepath.Join(env.home, ".zshrc")
	writeFileMode(t, rc, "# private\n", 0o600)

	if code, _, errOut := runPathT(t, env, "add-to-shell"); code != 0 {
		t.Fatalf("exit = %d: %s", code, errOut)
	}
	fi, err := os.Stat(rc)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}

	// A file that did not exist is created 0644.
	env2 := shellFixture(t, "/bin/zsh", "darwin")
	if code, _, errOut := runPathT(t, env2, "add-to-shell"); code != 0 {
		t.Fatalf("exit = %d: %s", code, errOut)
	}
	fi2, err := os.Stat(filepath.Join(env2.home, ".zshrc"))
	if err != nil {
		t.Fatal(err)
	}
	if fi2.Mode().Perm() != 0o644 {
		t.Errorf("new file mode = %v, want 0644", fi2.Mode().Perm())
	}
}

func TestAddToShell_ConcurrentRunsWriteOneBlock(t *testing.T) {
	env := shellFixture(t, "/bin/zsh", "darwin")
	rc := filepath.Join(env.home, ".zshrc")
	writeFileMode(t, rc, "# original\n", 0o644)

	var wg sync.WaitGroup
	codes := make([]int, 4)
	outs := make([]string, 4)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var out, errb bytes.Buffer
			codes[i] = runPathCmd(env, []string{"add-to-shell"}, &out, &errb)
			outs[i] = out.String() + errb.String()
		}(i)
	}
	wg.Wait()

	for i, c := range codes {
		if c != 0 {
			t.Errorf("run %d exited %d: %s", i, c, outs[i])
		}
	}
	if n := strings.Count(readFileT(t, rc), testMarkerStart); n != 1 {
		t.Errorf("marker appears %d times, want 1:\n%s", n, readFileT(t, rc))
	}
}

// TestAddToShell_StaleReadDoesNotDropAConcurrentEdit is the property
// TestAddToShell_ConcurrentRunsWriteOneBlock cannot prove: that the content
// written is built from the read taken *inside* the lock (spec §3.3, §7.3).
// The seam rewrites the rc file after the command has resolved it but before
// it takes the lock.
func TestAddToShell_StaleReadDoesNotDropAConcurrentEdit(t *testing.T) {
	env := shellFixture(t, "/bin/zsh", "darwin")
	rc := filepath.Join(env.home, ".zshrc")
	writeFileMode(t, rc, "original\n", 0o644)
	env.beforeLock = func() {
		writeFileMode(t, rc, "original\nuser-edit\n", 0o644)
	}

	if code, out, errOut := runPathT(t, env, "add-to-shell"); code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, out, errOut)
	}
	body := readFileT(t, rc)
	if !strings.Contains(body, "user-edit") {
		t.Errorf("a concurrent edit was dropped:\n%s", body)
	}
	if !strings.Contains(body, testMarkerStart) {
		t.Errorf("the block was not written:\n%s", body)
	}
}

// TestPathCommandsShareOneLock asserts spec §3.2's "one lock, not two":
// add-to-shell cannot proceed while link holds it.
func TestPathCommandsShareOneLock(t *testing.T) {
	env := shellFixture(t, "/bin/zsh", "darwin")
	env.lock = lockPolicy{retryEvery: 5 * time.Millisecond, timeout: 60 * time.Millisecond, staleAfter: time.Hour}

	var innerCode int
	var innerErr bytes.Buffer
	env.afterLock = func() {
		inner := env
		inner.afterLock = nil
		var out bytes.Buffer
		innerCode = runPathCmd(inner, []string{"add-to-shell"}, &out, &innerErr)
	}

	if code, out, errOut := runPathT(t, env, "link"); code != 0 {
		t.Fatalf("link exit = %d\n%s%s", code, out, errOut)
	}
	if innerCode != 1 {
		t.Errorf("add-to-shell exited %d while link held the lock, want 1", innerCode)
	}
	if !strings.Contains(innerErr.String(), pathLockName) {
		t.Errorf("the contention must name the shared lock:\n%s", innerErr.String())
	}
	if _, err := os.Stat(filepath.Join(env.home, ".zshrc")); err == nil {
		t.Error("the blocked add-to-shell still wrote the rc file")
	}
}
