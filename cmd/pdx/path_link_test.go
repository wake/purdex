package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Task 2: pdx path link ------------------------------------------------

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
			// The lock is always released. The lockfile itself stays on
			// disk by design — the lock is an flock on a descriptor, not
			// the file's existence — so the property worth asserting is
			// that it can be taken again, not that the file is gone.
			assertLockFree(t, env.localBinDir())
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
	assertLockFree(t, env.localBinDir())
}

func TestPathLink_ReportsLockTimeout(t *testing.T) {
	env, _ := linkFixture(t)
	mkdirAllT(t, env.localBinDir())
	lockPath := filepath.Join(env.localBinDir(), ".pdx-path.lock")
	// A file sitting at that path is no longer a held lock: holding the lock
	// means holding the flock, so the test has to hold a real one. That this
	// test had to change is the point — the old one would have passed against
	// an implementation that never locked anything at all.
	held, err := acquirePathLock(env.localBinDir(), lockPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	defer held.release()
	env.lock = lockPolicy{retryEvery: 10 * time.Millisecond, timeout: 100 * time.Millisecond}

	code, _, errOut := runPathT(t, env, "link")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut, lockPath) {
		t.Errorf("stderr must name the lock path:\n%s", errOut)
	}
}
