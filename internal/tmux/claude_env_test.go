package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/internal/claudeenv"
)

// privateServer returns a runner for the private tmux server `-L label` and ends that server with the test. Two things keep
// it from outliving the test (#2760): -f /dev/null, so the user's ~/.tmux.conf (which may say `exit-empty off`) is not
// loaded, and a cleanup that kill-servers it by label before the socket file is removed (a server whose socket is gone can
// no longer be reached, and with exit-empty off it never exits). Only the labelled server is ever killed.
func privateServer(t *testing.T, label string) func(env []string, args ...string) (string, error) {
	t.Helper()
	os.Unsetenv("TMUX")
	tm := func(env []string, args ...string) (string, error) {
		cmd := exec.Command("tmux", append([]string{"-L", label, "-f", "/dev/null"}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	t.Cleanup(func() {
		tm(nil, "kill-server")
		os.Remove(privateSocket(label)) // a server that already exited by itself leaves its socket file behind
	})
	return tm
}

// privateSocket is where tmux puts the socket of `-L label`: $TMUX_TMPDIR (or /tmp) / tmux-<uid> / label.
func privateSocket(label string) string {
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real // /tmp is a symlink on macOS; tmux reports the resolved path
	}
	return filepath.Join(dir, "tmux-"+strconv.Itoa(os.Getuid()), label)
}

// The server a test started is gone when the test is over: the label answers "no server", and the socket file is removed.
func TestPrivateServer_IsGoneWhenTheTestEnds(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	label := "pdx-es-gone-" + strconv.Itoa(os.Getpid())
	sock := privateSocket(label)
	var pid int
	t.Run("own", func(t *testing.T) {
		tm := privateServer(t, label)
		if out, err := tm(nil, "new-session", "-d", "-s", "x", "-c", "/tmp", "sleep 60"); err != nil {
			t.Fatalf("start: %v %s", err, out)
		}
		if out, _ := tm(nil, "display-message", "-p", "#{socket_path}"); strings.TrimSpace(out) != sock {
			t.Fatalf("socket path %q, derived %q", strings.TrimSpace(out), sock)
		}
		out, _ := tm(nil, "display-message", "-p", "#{pid}")
		pid, _ = strconv.Atoi(strings.TrimSpace(out))
		// the last session goes; a server that kept the user's config would stay (exit-empty off)
		tm(nil, "kill-session", "-t", "=x")
	})
	if out, err := exec.Command("tmux", "-L", label, "list-sessions").CombinedOutput(); err == nil || !strings.Contains(string(out), "no server running") && !strings.Contains(string(out), "error connecting") {
		t.Errorf("the test's server is still there: %v %s", err, out)
	}
	// the socket is removed either way, so only the process shows a leaked server
	if pid <= 0 {
		t.Fatal("never learned the server's pid")
	}
	for i := 0; i < 30 && syscall.Kill(pid, 0) == nil; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Errorf("the tmux server (pid %d) of the test is still running", pid)
		syscall.Kill(pid, syscall.SIGTERM)
	}
	if _, err := os.Stat(sock); err == nil {
		t.Errorf("the socket file %s is still there", sock)
	}
}

// #2122: a tmux server started from inside a Claude Code session holds that session's identity in its
// global environment, and every session created on it inherits it. The new-session the daemon runs is
// behind claudeenv's global unset, so a session it creates is clean. Run on a PRIVATE socket (-L), never
// the default server; the only kill is kill-session of the sessions this test made.
func TestNewSessionCommand_ACleanSessionOnAServerThatHoldsASessionIdentity(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	os.Unsetenv("TMUX")
	label := "pdx-es-" + strconv.Itoa(os.Getpid())
	tm := privateServer(t, label)
	leak := []string{"CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=leaked-session", "CLAUDE_CODE_MESSAGING_SOCKET=/tmp/leaked.sock", "CLAUDE_CONFIG_DIR=/keep/me"}
	// the server starts from a polluted client, as when it was started inside a Claude Code session
	if out, err := tm(leak, "new-session", "-d", "-s", "polluted", "-c", "/tmp", "sleep 60"); err != nil {
		t.Fatalf("start the polluted server: %v %s", err, out)
	}
	if out, _ := tm(nil, "show-environment", "-g"); !strings.Contains(out, "CLAUDE_CODE_SESSION_ID=leaked-session") {
		t.Fatalf("precondition: the server's global environment should hold the leaked identity:\n%s", out)
	}

	file := filepath.Join(t.TempDir(), "env.txt")
	args := newSessionCommand("new-session", "-d", "-s", "clean", "-c", "/tmp", "env > "+file+"; sleep 60")
	if out, err := tm(nil, args...); err != nil {
		t.Fatalf("new-session behind the unset: %v %s", err, out)
	}
	var env string
	for i := 0; i < 50; i++ {
		if b, err := os.ReadFile(file); err == nil && len(b) > 0 {
			env = string(b)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if env == "" {
		t.Fatal("the new session's shell never wrote its environment")
	}
	for _, name := range claudeenv.SessionVars {
		if strings.Contains(env, name+"=") {
			t.Errorf("the new session inherited %s", name)
		}
	}
	if !strings.Contains(env, "CLAUDE_CONFIG_DIR=/keep/me") {
		t.Error("configuration (CLAUDE_CONFIG_DIR) was removed from the new session")
	}
	// the old session is untouched, and the unset lasted: later sessions are clean too
	if out, _ := tm(nil, "show-environment", "-g"); strings.Contains(out, "CLAUDE_CODE_SESSION_ID") {
		t.Errorf("the server's global environment still holds the identity:\n%s", out)
	}
}

// With no server running, the same invocation starts one and creates the session (the list is
// not ended by "no server running").
func TestNewSessionCommand_WorksWhenNoServerIsRunning(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	os.Unsetenv("TMUX")
	label := "pdx-es-cold-" + strconv.Itoa(os.Getpid())
	tm := privateServer(t, label)
	run := func(args ...string) (string, error) { return tm(nil, args...) }
	if out, err := run(newSessionCommand("new-session", "-d", "-s", "cold", "-c", "/tmp", "sleep 60")...); err != nil {
		t.Fatalf("new-session with no server: %v %s", err, out)
	}
	if out, err := run("has-session", "-t", "=cold"); err != nil {
		t.Fatalf("the session was not created: %v %s", err, out)
	}
}
