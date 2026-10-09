package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/claudeenv"
)

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
	tm := func(env []string, args ...string) (string, error) {
		cmd := exec.Command("tmux", append([]string{"-L", label}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	leak := []string{"CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=leaked-session", "CLAUDE_CODE_MESSAGING_SOCKET=/tmp/leaked.sock", "CLAUDE_CONFIG_DIR=/keep/me"}
	// the server starts from a polluted client, as when it was started inside a Claude Code session
	if out, err := tm(leak, "new-session", "-d", "-s", "polluted", "-c", "/tmp", "sleep 60"); err != nil {
		t.Fatalf("start the polluted server: %v %s", err, out)
	}
	sock, _ := tm(nil, "display-message", "-p", "#{socket_path}")
	t.Cleanup(func() {
		tm(nil, "kill-session", "-t", "=polluted")
		tm(nil, "kill-session", "-t", "=clean")
		time.Sleep(200 * time.Millisecond) // the empty server exits by itself; then its socket file is only litter
		os.Remove(strings.TrimSpace(sock))
	})
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
	run := func(args ...string) (string, error) {
		out, err := exec.Command("tmux", append([]string{"-L", label}, args...)...).CombinedOutput()
		return string(out), err
	}
	t.Cleanup(func() {
		sock, _ := run("display-message", "-p", "#{socket_path}")
		run("kill-session", "-t", "=cold")
		time.Sleep(200 * time.Millisecond)
		os.Remove(strings.TrimSpace(sock))
	})
	if out, err := run(newSessionCommand("new-session", "-d", "-s", "cold", "-c", "/tmp", "sleep 60")...); err != nil {
		t.Fatalf("new-session with no server: %v %s", err, out)
	}
	if out, err := run("has-session", "-t", "=cold"); err != nil {
		t.Fatalf("the session was not created: %v %s", err, out)
	}
}
