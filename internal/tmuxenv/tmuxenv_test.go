package tmuxenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTmux puts an executable (or not) file named "tmux" in dir and returns
// dir, so a test can build a probe list out of temp directories instead of
// depending on what this machine happens to have in /opt/homebrew/bin.
func writeTmux(t *testing.T, dir string, executable bool) string {
	t.Helper()
	mode := os.FileMode(0o644)
	if executable {
		mode = 0o755
	}
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
		t.Fatalf("writing fake tmux in %s: %v", dir, err)
	}
	return dir
}

// emptyPathDir returns a directory guaranteed to hold no tmux. Real system
// directories must not be used for this: `/usr/bin/tmux` exists on most Linux
// and CI images, which would make LookPath succeed and quietly turn the
// Appended and NotFound cases into Found — a green test proving nothing.
func emptyPathDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// isolate gives the test a PATH with no tmux on it and no inherited tmux
// variables, so each case starts from a known environment.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir()) // empty dir: LookPath("tmux") must fail
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
}

// Acceptance 1: an inherited PATH that already works is left exactly as it is.
// Rewriting it would be a side effect on every other exec the daemon makes.
func TestPrepare_AlreadyOnPath_LeavesPathUntouched(t *testing.T) {
	isolate(t)
	binDir := writeTmux(t, t.TempDir(), true)
	want := binDir + string(os.PathListSeparator) + emptyPathDir(t)
	t.Setenv("PATH", want)

	got := prepare([]string{"/nowhere-a", "/nowhere-b"})

	if got.Action != Found {
		t.Errorf("Action = %v, want Found", got.Action)
	}
	if os.Getenv("PATH") != want {
		t.Errorf("PATH = %q, want it untouched (%q)", os.Getenv("PATH"), want)
	}
	if got.Resolved == "" {
		t.Error("Resolved is empty; the caller has nothing to log")
	}
}

// Acceptance 2: a probed directory is APPENDED, and the PATH it was added to
// survives intact and in order.
//
// Appended, not prepended, because the daemon's panes inherit this PATH and are
// sent bare `pdx relay ...`: winning precedence here would let an unrelated
// `pdx` outrank the one the daemon is running. This test pins the position so
// that a well-meaning change back to prepending fails here, rather than as an
// agent timeout weeks later.
func TestPrepare_NotOnPath_AppendsProbedDir(t *testing.T) {
	isolate(t)
	original := emptyPathDir(t) + string(os.PathListSeparator) + emptyPathDir(t)
	t.Setenv("PATH", original)
	found := writeTmux(t, t.TempDir(), true)

	got := prepare([]string{found})

	if got.Action != Appended {
		t.Fatalf("Action = %v, want Appended", got.Action)
	}
	if got.AddedDir != found {
		t.Errorf("AddedDir = %q, want %q", got.AddedDir, found)
	}
	want := original + string(os.PathListSeparator) + found
	if os.Getenv("PATH") != want {
		t.Errorf("PATH = %q, want %q", os.Getenv("PATH"), want)
	}
}

// Acceptance 3: nothing anywhere is reported, not repaired — and PATH is not
// damaged on the way to finding that out.
func TestPrepare_NowhereToBeFound_LeavesPathUntouched(t *testing.T) {
	isolate(t)
	original := emptyPathDir(t)
	t.Setenv("PATH", original)

	got := prepare([]string{t.TempDir(), t.TempDir()})

	if got.Action != NotFound {
		t.Errorf("Action = %v, want NotFound", got.Action)
	}
	if os.Getenv("PATH") != original {
		t.Errorf("PATH = %q, want it untouched (%q)", os.Getenv("PATH"), original)
	}
	if len(got.Probed) == 0 {
		t.Error("Probed is empty; the error log cannot say where it looked")
	}
}

// Acceptance 4: a non-executable file named "tmux" is not a tmux. Accepting it
// would prepend a directory that cannot satisfy a single call.
func TestPrepare_SkipsNonExecutableAndKeepsLooking(t *testing.T) {
	isolate(t)
	t.Setenv("PATH", emptyPathDir(t))
	decoy := writeTmux(t, t.TempDir(), false)
	real := writeTmux(t, t.TempDir(), true)

	got := prepare([]string{decoy, real})

	if got.Action != Appended {
		t.Fatalf("Action = %v, want Appended", got.Action)
	}
	if got.AddedDir != real {
		t.Errorf("AddedDir = %q, want the executable one (%q), not the decoy (%q)", got.AddedDir, real, decoy)
	}
}

// Acceptance 7: probe order is the order given, so the list expresses a
// preference rather than whatever the filesystem answers first.
func TestPrepare_HonoursProbeOrder(t *testing.T) {
	isolate(t)
	t.Setenv("PATH", emptyPathDir(t))
	first := writeTmux(t, t.TempDir(), true)
	second := writeTmux(t, t.TempDir(), true)

	got := prepare([]string{first, second})

	if got.AddedDir != first {
		t.Errorf("AddedDir = %q, want the earlier directory %q", got.AddedDir, first)
	}
}

// Acceptance 5: the socket is taken back in every outcome, including the one
// where tmux was never found — a stale $TMUX must not outlive Prepare.
func TestPrepare_AlwaysUnsetsTmuxVars(t *testing.T) {
	for _, name := range []string{"found", "prepended", "not found"} {
		t.Run(name, func(t *testing.T) {
			isolate(t)
			t.Setenv("TMUX", "/private/tmp/tmux-501/other,123,4")
			t.Setenv("TMUX_PANE", "%9")

			var probe []string
			switch name {
			case "found":
				t.Setenv("PATH", writeTmux(t, t.TempDir(), true))
			case "prepended":
				t.Setenv("PATH", emptyPathDir(t))
				probe = []string{writeTmux(t, t.TempDir(), true)}
			case "not found":
				t.Setenv("PATH", emptyPathDir(t))
				probe = []string{t.TempDir()}
			}

			got := prepare(probe)

			if v, ok := os.LookupEnv("TMUX"); ok {
				t.Errorf("TMUX still set to %q; the daemon would address the operator's server", v)
			}
			if v, ok := os.LookupEnv("TMUX_PANE"); ok {
				t.Errorf("TMUX_PANE still set to %q", v)
			}
			if !got.DroppedTMUX {
				t.Error("DroppedTMUX = false, want true: a daemon started from a pane must be able to say so once")
			}
		})
	}
}

// Acceptance 5 (negative): DroppedTMUX must describe what happened, not what
// Prepare always does — otherwise the log line cries wolf on every start.
func TestPrepare_ReportsWhenTmuxWasNotSet(t *testing.T) {
	isolate(t)
	t.Setenv("PATH", writeTmux(t, t.TempDir(), true))

	if got := prepare(nil); got.DroppedTMUX {
		t.Error("DroppedTMUX = true although TMUX was never set")
	}
}

// Acceptance 6: idempotent. runServe calls this once, but a restart-in-process
// or a second caller must not grow PATH a copy at a time.
func TestPrepare_Idempotent(t *testing.T) {
	isolate(t)
	t.Setenv("PATH", emptyPathDir(t))
	found := writeTmux(t, t.TempDir(), true)

	first := prepare([]string{found})
	afterFirst := os.Getenv("PATH")
	second := prepare([]string{found})

	if os.Getenv("PATH") != afterFirst {
		t.Errorf("PATH grew on the second call: %q then %q", afterFirst, os.Getenv("PATH"))
	}
	if strings.Count(os.Getenv("PATH"), found) != 1 {
		t.Errorf("PATH contains %q more than once: %q", found, os.Getenv("PATH"))
	}
	// The second call finds it on PATH now, because the first one put it there.
	if first.Action != Appended || second.Action != Found {
		t.Errorf("Actions = %v then %v, want Appended then Found", first.Action, second.Action)
	}
}

// defaultProbeDirs is what production passes. It is not asserted against this
// machine's filesystem — only that it names the places a Homebrew or local
// install actually lands, in a stable order.
func TestDefaultProbeDirs(t *testing.T) {
	t.Setenv("HOME", "/Users/probe")
	dirs := defaultProbeDirs()

	want := []string{"/opt/homebrew/bin", "/usr/local/bin", "/Users/probe/.local/bin"}
	if len(dirs) != len(want) {
		t.Fatalf("defaultProbeDirs() = %v, want %v", dirs, want)
	}
	for i := range want {
		if dirs[i] != want[i] {
			t.Errorf("defaultProbeDirs()[%d] = %q, want %q", i, dirs[i], want[i])
		}
	}
}
