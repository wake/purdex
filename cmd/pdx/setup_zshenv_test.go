package main

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/gate/shim"
)

// WWD-9 S0-b: `pdx setup --agent cc` no longer installs the gate's shims and takes the pdx gate block out of ~/.zshenv. The tests run the real
// entry (runSetup, in a process of its own: it exits), on both of its paths: the daemon answers, and it does not.

// TestSetupHelperProcess is runSetup in a child process; it does nothing in the test run itself.
func TestSetupHelperProcess(t *testing.T) {
	if os.Getenv("PDX_SETUP_HELPER") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	// the test main of this package puts a home of its own in HOME (gatetest): the home of this run is the one the test made
	if err := os.Setenv("HOME", os.Getenv("PDX_SETUP_HOME")); err != nil {
		panic(err)
	}
	runSetup(args)
	os.Exit(0)
}

// setupEnv is a home whose config names the daemon at host:port and no other (the real daemon of the machine this runs on is never reached).
func setupEnv(t *testing.T, port int) (home string) {
	t.Helper()
	home = t.TempDir()
	cfgDir := filepath.Join(home, ".config", "pdx")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf("bind = \"127.0.0.1\"\nport = %d\n", port)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// daemonThatAnswers is a daemon: no relay under way, and the setup of the hooks done.
func daemonThatAnswers(t *testing.T) (port int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/team/inflight":
			_, _ = w.Write([]byte(`{"approvals_open":0,"relays_active":0,"relays":[]}`))
		case "/api/hooks/cc/setup":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			t.Errorf("the setup asked %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().(*net.TCPAddr).Port
}

// runPdxSetup runs `pdx setup <args>` with HOME = home and returns its exit code and streams.
func runPdxSetup(t *testing.T, home string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestSetupHelperProcess$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "HOME="+home, "PDX_SETUP_HOME="+home, "PDX_SETUP_HELPER=1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return code, out.String(), errb.String()
}

func writeZshenv(t *testing.T, home, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, ".zshenv"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readZshenv(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".zshenv"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// shimFiles are the shims in the home as a name → content map (an old setup wrote them).
func shimFiles(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, _ := os.ReadDir(shim.DirIn(home))
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(shim.DirIn(home), e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(b)
	}
	return out
}

// Both paths: the block goes, the rest of the file is as it was, the shims already there are not touched and no shim is written; a second run
// changes nothing and says nothing.
func TestSetupCC_TakesTheGateBlockOutOfZshenvOnBothPaths(t *testing.T) {
	for name, port := range map[string]func(*testing.T) int{"the daemon answers": daemonThatAnswers, "the daemon does not answer": closedPort} {
		home := setupEnv(t, port(t))
		before := "export KEEP=1\n" + shim.Block(shim.DirIn(home)) + "alias ll='ls -l'\n"
		writeZshenv(t, home, before)
		if err := shim.Install(home, "/p/pdx"); err != nil { // an old setup: shims in place (and a block, which Install keeps as it is)
			t.Fatal(err)
		}
		writeZshenv(t, home, before)
		shims := shimFiles(t, home)
		if len(shims) != len(shim.Tools) {
			t.Fatalf("%s: %d shims to start with", name, len(shims))
		}

		code, _, errb := runPdxSetup(t, home, "--agent", "cc", "--force")
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", name, code, errb)
		}
		if got, want := readZshenv(t, home), "export KEEP=1\nalias ll='ls -l'\n"; got != want {
			t.Errorf("%s: .zshenv %q, want %q", name, got, want)
		}
		if !strings.Contains(errb, "block removed") {
			t.Errorf("%s: no word that the block was removed: %q", name, errb)
		}
		if strings.Contains(errb, "gate shims installed") {
			t.Errorf("%s: the setup still says it installs shims: %q", name, errb)
		}
		after := shimFiles(t, home)
		if len(after) != len(shims) {
			t.Errorf("%s: the shims changed: %d → %d files", name, len(shims), len(after))
		}
		for tool, content := range shims {
			if after[tool] != content {
				t.Errorf("%s: the shim %s was rewritten", name, tool)
			}
		}

		// again: nothing changes, nothing is said about the block
		code, _, errb = runPdxSetup(t, home, "--agent", "cc", "--force")
		if code != 0 || readZshenv(t, home) != "export KEEP=1\nalias ll='ls -l'\n" || strings.Contains(errb, "block") {
			t.Errorf("%s: a second setup: exit %d, .zshenv %q, stderr %q", name, code, readZshenv(t, home), errb)
		}
	}
}

// A home with no shims and no block: setup installs the hooks and makes no shim, no .zshenv and no shim directory.
func TestSetupCC_InstallsNoShimsOnAFreshHome(t *testing.T) {
	for name, port := range map[string]func(*testing.T) int{"the daemon answers": daemonThatAnswers, "the daemon does not answer": closedPort} {
		home := setupEnv(t, port(t))
		code, _, errb := runPdxSetup(t, home, "--agent", "cc", "--force")
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", name, code, errb)
		}
		if _, err := os.Stat(shim.DirIn(home)); !os.IsNotExist(err) {
			t.Errorf("%s: a shim directory was made: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(home, ".zshenv")); !os.IsNotExist(err) {
			t.Errorf("%s: a .zshenv was made: %v", name, err)
		}
		if strings.Contains(errb, "gate shims") {
			t.Errorf("%s: %q", name, errb)
		}
	}
}

// A file of just the block is removed; a symbolic link (a dotfiles repo's) is emptied and stays a link.
func TestSetupCC_AZshenvOfJustTheBlock(t *testing.T) {
	home := setupEnv(t, closedPort(t))
	writeZshenv(t, home, shim.Block(shim.DirIn(home)))
	if code, _, errb := runPdxSetup(t, home, "--agent", "cc", "--force"); code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if _, err := os.Stat(filepath.Join(home, ".zshenv")); !os.IsNotExist(err) {
		t.Errorf("a file of just the block is removed: %v", err)
	}

	home = setupEnv(t, closedPort(t))
	target := filepath.Join(home, "dotfiles", "zshenv")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(shim.Block(shim.DirIn(home))), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".zshenv")); err != nil {
		t.Fatal(err)
	}
	if code, _, errb := runPdxSetup(t, home, "--agent", "cc", "--force"); code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if fi, err := os.Lstat(filepath.Join(home, ".zshenv")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link is gone: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "" {
		t.Errorf("the target is emptied, not removed: %q", b)
	}
}

// A block with no end marker is left alone, with a word about it, and the setup goes on.
func TestSetupCC_AnUnclosedBlockIsLeftAloneWithAWarning(t *testing.T) {
	for name, port := range map[string]func(*testing.T) int{"the daemon answers": daemonThatAnswers, "the daemon does not answer": closedPort} {
		home := setupEnv(t, port(t))
		in := "a=1\n# >>> pdx gate >>>\npath=(x)\nb=2\n"
		writeZshenv(t, home, in)
		code, _, errb := runPdxSetup(t, home, "--agent", "cc", "--force")
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", name, code, errb)
		}
		if got := readZshenv(t, home); got != in {
			t.Errorf("%s: the file changed: %q", name, got)
		}
		if !strings.Contains(errb, "no end marker") || !strings.Contains(errb, ".zshenv") {
			t.Errorf("%s: no warning: %q", name, errb)
		}
	}
}

// The markers around text pdx never wrote (a here-document of the user's): the file is left alone, with a word about it.
func TestSetupCC_MarkersAroundTheUsersOwnTextAreLeftAlone(t *testing.T) {
	for name, port := range map[string]func(*testing.T) int{"the daemon answers": daemonThatAnswers, "the daemon does not answer": closedPort} {
		home := setupEnv(t, port(t))
		in := "cat <<'EOF' > /tmp/x\n# >>> pdx gate >>>\nmy data\n# <<< pdx gate <<<\nEOF\n"
		writeZshenv(t, home, in)
		code, _, errb := runPdxSetup(t, home, "--agent", "cc", "--force")
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", name, code, errb)
		}
		if got := readZshenv(t, home); got != in {
			t.Errorf("%s: the file changed: %q", name, got)
		}
		if !strings.Contains(errb, "did not write") {
			t.Errorf("%s: no warning: %q", name, errb)
		}
	}
}

// A .zshenv that cannot be rewritten (a link into a directory that is read-only): a word on stderr, the exit code is 0 and the hooks are in
// (the setup of the hooks is what the command is for).
func TestSetupCC_AnUnwritableZshenvOnlyWarns(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	home := setupEnv(t, closedPort(t))
	dotfiles := filepath.Join(home, "dotfiles")
	if err := os.MkdirAll(dotfiles, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dotfiles, "zshenv")
	in := "a=1\n" + shim.Block(shim.DirIn(home))
	if err := os.WriteFile(target, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".zshenv")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dotfiles, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dotfiles, 0o700) })
	code, _, errb := runPdxSetup(t, home, "--agent", "cc", "--force")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !strings.Contains(errb, "could not remove the pdx gate block") {
		t.Errorf("no warning for a block that stays: %q", errb)
	}
	if b, _ := os.ReadFile(target); string(b) != in {
		t.Errorf("the file changed: %q", b)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err != nil {
		t.Errorf("the hooks were not installed: %v", err)
	}
}

// --remove is the removal of the hooks: it does not look at .zshenv.
func TestSetupCC_RemoveDoesNotTouchZshenv(t *testing.T) {
	home := setupEnv(t, closedPort(t))
	in := "a=1\n" + shim.Block(shim.DirIn(home))
	writeZshenv(t, home, in)
	if code, _, errb := runPdxSetup(t, home, "--agent", "cc", "--remove", "--force"); code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got := readZshenv(t, home); got != in {
		t.Errorf(".zshenv changed by --remove: %q", got)
	}
}
