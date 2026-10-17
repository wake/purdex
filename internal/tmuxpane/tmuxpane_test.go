package tmuxpane

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

// Every tmux test here runs on its own server (-L <label>) with TMUX unset, never the person's own server or a real team member's
// pane; the server is killed with its label at the end. SHELL is /bin/sh so the pane is a shell without any rc file.
func newServer(t *testing.T) *Tmux {
	t.Helper()
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("no tmux")
	}
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("TMUX", "")
	os.Unsetenv("TMUX")
	label := fmt.Sprintf("pdxp-test-%d-%d", os.Getpid(), time.Now().UnixNano()%1_000_000)
	t.Cleanup(func() { _ = exec.Command(bin, "-L", label, "kill-server").Run() })
	return &Tmux{Bin: bin, Label: label, Tag: "tmuxpane.test"}
}

// session makes a detached session called name and answers its $N id and its first pane's %N id.
func session(t *testing.T, tm *Tmux, name string) (sid, pane string) {
	t.Helper()
	out, err := tm.Run(context.Background(), "", "new-session", "-d", "-s", name, "-c", t.TempDir(), "-P", "-F", "#{session_id} #{pane_id}")
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(out)
	if len(f) != 2 || !SessionIDRe.MatchString(f[0]) || !PaneIDRe.MatchString(f[1]) {
		t.Fatalf("new-session answered %q", out)
	}
	return f[0], f[1]
}

func waitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s was not made", path)
}

func buffers(t *testing.T, tm *Tmux) string {
	t.Helper()
	out, err := tm.Run(context.Background(), "", "list-buffers", "-F", "#{buffer_name}")
	if err != nil && !strings.Contains(err.Error(), "exit status 1") {
		t.Fatal(err)
	}
	return out
}

// A line is typed into a pane of any session name, when the name given is the pane's own.
func TestTypeInto_TypesIntoTheNamedSessionsPaneAndOnlyThere(t *testing.T) {
	tm := newServer(t)
	sid, pane := session(t, tm, "team-member-1")
	dir := t.TempDir()
	ok, never := filepath.Join(dir, "ok"), filepath.Join(dir, "never")

	if err := tm.TypeInto(context.Background(), "touch '"+never+"'", sid, pane, "pdx-watch", "tmuxpane-line"); !errors.Is(err, ErrBusy) {
		t.Fatalf("a name that is not the session's: err = %v; want ErrBusy", err)
	}
	if _, err := os.Stat(never); err == nil {
		t.Fatal("the line was typed into a pane of another session name")
	}
	if strings.Contains(buffers(t, tm), "tmuxpane-line") {
		t.Fatal("a refused typing left its buffer behind")
	}

	if err := tm.TypeInto(context.Background(), "touch '"+ok+"'", sid, pane, "team-member-1", "tmuxpane-line"); err != nil {
		t.Fatal(err)
	}
	waitFile(t, ok)
	if strings.Contains(buffers(t, tm), "tmuxpane-line") {
		t.Fatal("the buffer was not deleted after the paste")
	}
}

// Another client's buffer that carries the same prefix is neither overwritten nor pasted: every typing uses a buffer of its own.
func TestTypeInto_TheBufferNameCarriesThePrefixGivenAndIsUnique(t *testing.T) {
	a, err := NewBufferName("member-line")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewBufferName("member-line")
	if !strings.HasPrefix(a, "member-line-") || len(a) != len("member-line-")+16 || a == b {
		t.Fatalf("names = %q, %q", a, b)
	}
}

func TestSendExitTo_OnlyToThePaneOfTheNamedSession(t *testing.T) {
	tm := newServer(t)
	sid, pane := session(t, tm, "team-member-2")
	if err := tm.SendExitTo(context.Background(), sid, pane, "pdx-watch"); !errors.Is(err, ErrBusy) {
		t.Fatalf("a name that is not the session's: err = %v; want ErrBusy", err)
	}
	if err := tm.SendExitTo(context.Background(), "$"+fmt.Sprint(len(sid)+900), pane, "team-member-2"); !errors.Is(err, ErrBusy) {
		t.Fatalf("a session id that is not the pane's: err = %v; want ErrBusy", err)
	}
	if err := tm.SendExitTo(context.Background(), sid, pane, "team-member-2"); err != nil {
		t.Fatal(err)
	}
}

func TestConditions_NameTheSessionTheyAreGiven(t *testing.T) {
	for _, c := range []string{IdentityCondition("$1", "%2", "team-x"), TypeCondition("$1", "%2", "team-x"), ExitCondition("$1", "%2", "team-x")} {
		if !strings.Contains(c, "#{==:#{session_name},team-x}") || !strings.Contains(c, "#{==:#{session_id},$1}") || !strings.Contains(c, "#{==:#{pane_id},%2}") {
			t.Fatalf("condition = %s", c)
		}
	}
}

func TestCheckLine(t *testing.T) {
	for _, bad := range []string{"", "a\nb", "x\x1b[0m", "\xff"} {
		if CheckLine("line", bad) == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}
	if err := CheckLine("line", "echo 中文 ok"); err != nil {
		t.Fatal(err)
	}
}
