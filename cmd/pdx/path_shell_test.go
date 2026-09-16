package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

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
