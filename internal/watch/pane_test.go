package watch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every tmux test runs on its own server (-L <label>) with TMUX unset, never the person's default server and never the real
// pdx-watch session; the server is killed with its label at the end. SHELL is /bin/sh so the pane is a shell without any rc file.

func newTestPane(t *testing.T) *PaneControl {
	t.Helper()
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("no tmux")
	}
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("TMUX", "")
	os.Unsetenv("TMUX")
	label := fmt.Sprintf("pdxw-test-%d-%d", os.Getpid(), time.Now().UnixNano()%1_000_000)
	p := NewPaneControl(bin)
	p.label = label
	t.Cleanup(func() { _ = exec.Command(bin, "-L", label, "kill-server").Run() })
	return p
}

// run is a tmux command on the test server.
func (p *PaneControl) testRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command(p.bin, append([]string{"-L", p.label}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v: %s", args, err, out)
	}
	return string(out)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestCommandLine_ThePromptReachesTheShellByteForByte(t *testing.T) {
	prompts := []string{
		"plain",
		"it's",
		`say "hi"`,
		"$(touch /nonexistent/x)",
		"`id`",
		"a; echo b",
		"a && echo b",
		"back\\slash",
		"'",
		"''",
		"mixed ' \" $ ` ; & | < > ( ) { } * ? ~ !",
		"中文 emoji 🙂",
	}
	for _, prompt := range prompts {
		line, err := CommandLine("printf %s", "", prompt)
		if err != nil {
			t.Fatalf("%q: %v", prompt, err)
		}
		out, err := exec.Command("sh", "-c", line).Output()
		if err != nil {
			t.Fatalf("%q: sh: %v", prompt, err)
		}
		if string(out) != prompt {
			t.Errorf("prompt %q came back as %q (line %q)", prompt, out, line)
		}
	}
}

func TestCommandLine_ResumeAndRefusals(t *testing.T) {
	const id = "123e4567-e89b-12d3-a456-426614174000"
	line, err := CommandLine("claude --x", id, "go")
	if err != nil {
		t.Fatal(err)
	}
	if line != "claude --x --resume "+id+" 'go'" {
		t.Errorf("line = %q", line)
	}
	if line, err = CommandLine("claude --x", "", ""); err != nil || line != "claude --x" {
		t.Errorf("no resume, no prompt: %q %v", line, err)
	}
	for name, c := range map[string][3]string{
		"resume not a uuid":     {"claude", "abc; rm -rf /", "p"},
		"resume uppercase uuid": {"claude", strings.ToUpper(id), "p"},
		"newline in prompt":     {"claude", "", "a\nb"},
		"NUL in prompt":         {"claude", "", "a\x00b"},
		"ESC in prompt":         {"claude", "", "a\x1bb"},
		"CR in prompt":          {"claude", "", "a\rb"},
		"tab in prompt":         {"claude", "", "a\tb"},
		"newline in command":    {"claude\nrm x", "", "p"},
		"empty command":         {"", "", "p"},
		"ESC in command":        {"claude \x1b", "", "p"},
	} {
		if line, err := CommandLine(c[0], c[1], c[2]); err == nil {
			t.Errorf("%s: want a refusal, got %q", name, line)
		}
	}
}

func TestEnsureSession_CreatesOnceAndRecordsTheIds(t *testing.T) {
	p := newTestPane(t)
	dir := t.TempDir()
	sid, pane, created, err := p.EnsureSession(context.Background(), dir)
	if err != nil || !created || !strings.HasPrefix(sid, "$") || !strings.HasPrefix(pane, "%") {
		t.Fatalf("first: %q %q %v %v", sid, pane, created, err)
	}
	if got := strings.TrimSpace(p.testRun(t, "display-message", "-p", "-t", pane, "#{pane_current_path}")); !samePath(got, dir) {
		t.Errorf("the session starts in %q, want %q", got, dir)
	}
	sid2, pane2, created2, err := p.EnsureSession(context.Background(), dir)
	if err != nil || created2 || sid2 != sid || pane2 != pane {
		t.Fatalf("second: %q %q %v %v, want the same ids and no new session", sid2, pane2, created2, err)
	}
	if n := strings.Count(p.testRun(t, "list-sessions", "-F", "#{session_name}"), PaneSession); n != 1 {
		t.Errorf("%d sessions named %s", n, PaneSession)
	}
}

func samePath(a, b string) bool {
	ra, _ := filepath.EvalSymlinks(a)
	rb, _ := filepath.EvalSymlinks(b)
	return ra == rb
}

// tmux -c does not expand ~ and falls back to $HOME for a bad path (it would start the watcher in the wrong folder): a workdir
// that is not a directory is an error, and ~/ is expanded here.
func TestEnsureSession_WorkdirIsCheckedAndTildeExpanded(t *testing.T) {
	p := newTestPane(t)
	if _, _, _, err := p.EnsureSession(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing workdir must be refused")
	}
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := p.EnsureSession(context.Background(), file); err == nil {
		t.Fatal("a file as workdir must be refused")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(home, "w"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, pane, _, err := p.EnsureSession(context.Background(), "~/w")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(p.testRun(t, "display-message", "-p", "-t", pane, "#{pane_current_path}")); !samePath(got, filepath.Join(home, "w")) {
		t.Errorf("~/w started in %q", got)
	}
}

func TestPaneAtShell_ShellBusyAndMode(t *testing.T) {
	p := newTestPane(t)
	ctx := context.Background()
	_, pane, _, err := p.EnsureSession(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the shell", func() bool { st, err := p.PaneAtShell(ctx); return err == nil && st.AtShell })

	p.testRun(t, "send-keys", "-t", pane, "sleep 30", "Enter")
	waitFor(t, "sleep in the foreground", func() bool { st, err := p.PaneAtShell(ctx); return err == nil && !st.AtShell && st.Command == "sleep" })
	p.testRun(t, "send-keys", "-t", pane, "C-c")
	waitFor(t, "the shell again", func() bool { st, err := p.PaneAtShell(ctx); return err == nil && st.AtShell })

	p.testRun(t, "copy-mode", "-t", pane)
	st, err := p.PaneAtShell(ctx)
	if err != nil || st.AtShell || !st.InMode {
		t.Fatalf("in copy mode: %+v %v", st, err)
	}
}

func TestTypeLine_TypesIntoAShellAndRefusesABusyPane(t *testing.T) {
	p := newTestPane(t)
	ctx := context.Background()
	_, pane, _, err := p.EnsureSession(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the shell", func() bool { st, err := p.PaneAtShell(ctx); return err == nil && st.AtShell })

	typed := filepath.Join(t.TempDir(), "typed")
	if err := p.TypeLine(ctx, "touch '"+typed+"'"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the typed command to run", func() bool { _, err := os.Stat(typed); return err == nil })
	if strings.Contains(p.testRun(t, "list-buffers", "-F", "#{buffer_name}"), pasteBuffer) {
		t.Error("the buffer must be gone after a paste")
	}

	// the pane turns busy AFTER PaneAtShell said shell: the guard is the if-shell of TypeLine, not the earlier answer
	waitFor(t, "the shell", func() bool { st, err := p.PaneAtShell(ctx); return err == nil && st.AtShell })
	st, _ := p.PaneAtShell(ctx)
	if !st.AtShell {
		t.Fatal("setup: not at a shell")
	}
	p.testRun(t, "send-keys", "-t", pane, "sleep 30", "Enter")
	waitFor(t, "sleep in the foreground", func() bool { s, err := p.PaneAtShell(ctx); return err == nil && s.Command == "sleep" })
	never := filepath.Join(t.TempDir(), "never")
	err = p.TypeLine(ctx, "touch '"+never+"'")
	if !errors.Is(err, ErrPaneBusy) {
		t.Fatalf("a busy pane: %v, want ErrPaneBusy", err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(never); err == nil {
		t.Error("nothing may be typed into a busy pane")
	}
	if strings.Contains(p.testRun(t, "list-buffers", "-F", "#{buffer_name}"), pasteBuffer) {
		t.Error("a refused line must leave no buffer behind")
	}
	p.testRun(t, "send-keys", "-t", pane, "C-c")

	// a pane in copy mode is not typed into either
	waitFor(t, "the shell", func() bool { s, err := p.PaneAtShell(ctx); return err == nil && s.AtShell })
	p.testRun(t, "copy-mode", "-t", pane)
	if err := p.TypeLine(ctx, "touch '"+never+"'"); !errors.Is(err, ErrPaneBusy) {
		t.Fatalf("a pane in a mode: %v, want ErrPaneBusy", err)
	}
}

func TestTypeLine_RefusesALineWithAControlCharacter(t *testing.T) {
	p := newTestPane(t)
	ctx := context.Background()
	if _, _, _, err := p.EnsureSession(ctx, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"a\nb", "a\x00b", "a\x1bb", ""} {
		if err := p.TypeLine(ctx, line); err == nil || errors.Is(err, ErrPaneBusy) {
			t.Errorf("%q: %v, want a refusal", line, err)
		}
	}
}

// A recreated pdx-watch has new ids: the old pane id is never typed into, the session is looked up again.
func TestPaneAtShell_ARecreatedSessionIsLookedUpAgain(t *testing.T) {
	p := newTestPane(t)
	ctx := context.Background()
	dir := t.TempDir()
	sid, pane, _, err := p.EnsureSession(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	// keep the server alive with another session so the recreated one gets fresh ids on the same server
	p.testRun(t, "new-session", "-d", "-s", "other")
	p.testRun(t, "kill-session", "-t", "="+PaneSession)
	p.testRun(t, "new-session", "-d", "-s", PaneSession, "-c", dir)
	waitFor(t, "the shell", func() bool { st, err := p.PaneAtShell(ctx); return err == nil && st.AtShell })
	sid2, pane2 := p.Recorded()
	if sid2 == sid || pane2 == pane {
		t.Fatalf("ids not refreshed: %s %s → %s %s", sid, pane, sid2, pane2)
	}
	typed := filepath.Join(t.TempDir(), "typed")
	if err := p.TypeLine(ctx, "touch '"+typed+"'"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the typed command in the new pane", func() bool { _, err := os.Stat(typed); return err == nil })
}

// With no pdx-watch session at all, nothing is typed and the answer is an error the caller can tell from busy.
func TestTypeLine_NoSessionIsNotBusy(t *testing.T) {
	p := newTestPane(t)
	p.testRun(t, "new-session", "-d", "-s", "other") // a server, but no pdx-watch
	err := p.TypeLine(context.Background(), "echo hi")
	if err == nil || errors.Is(err, ErrPaneBusy) {
		t.Fatalf("no session: %v, want an error that is not ErrPaneBusy", err)
	}
}

func TestSendExit_TypesSlashExitAndEnterIntoTheAgentsInput(t *testing.T) {
	p := newTestPane(t)
	ctx := context.Background()
	_, pane, _, err := p.EnsureSession(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the shell", func() bool { st, err := p.PaneAtShell(ctx); return err == nil && st.AtShell })
	// a program reading its input stands in for the agent: it writes what it reads to a file
	got := filepath.Join(t.TempDir(), "got")
	p.testRun(t, "send-keys", "-t", pane, "cat > '"+got+"'", "Enter")
	waitFor(t, "cat in the foreground", func() bool { s, err := p.PaneAtShell(ctx); return err == nil && s.Command == "cat" })
	if err := p.SendExit(ctx); err != nil {
		t.Fatal(err)
	}
	p.testRun(t, "send-keys", "-t", pane, "C-d")
	waitFor(t, "/exit to arrive", func() bool { b, _ := os.ReadFile(got); return string(b) == "/exit\n" })
}

// The condition of the typing carries the pane's identity: a pane id that is now another session's (a restarted server hands ids
// out again) is not typed into even though it is a shell. typeInto is called with an id the session does not have.
func TestTypeInto_ARecordedSessionIdThatIsNotThePanesIsNotTypedInto(t *testing.T) {
	p := newTestPane(t)
	ctx := context.Background()
	sid, pane, _, err := p.EnsureSession(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the shell", func() bool { st, err := p.PaneAtShell(ctx); return err == nil && st.AtShell })
	never := filepath.Join(t.TempDir(), "never")
	wrong := "$" + fmt.Sprint(len(sid)+900)
	if err := p.typeInto(ctx, "touch '"+never+"'", wrong, pane); !errors.Is(err, ErrPaneBusy) {
		t.Fatalf("a pane that is not the recorded session's: %v, want ErrPaneBusy", err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(never); err == nil {
		t.Error("typed into a pane whose session id is not the recorded one")
	}
	if strings.Contains(p.testRun(t, "list-buffers", "-F", "#{buffer_name}"), pasteBuffer) {
		t.Error("a refused line must leave no buffer behind")
	}
	typed := filepath.Join(t.TempDir(), "typed")
	if err := p.typeInto(ctx, "touch '"+typed+"'", sid, pane); err != nil {
		t.Fatalf("the right ids: %v", err)
	}
	waitFor(t, "the typed command to run", func() bool { _, err := os.Stat(typed); return err == nil })
}
