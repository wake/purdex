// internal/tmux/send_keys_shell_test.go — the launch panel's typing guard: generation AND "the pane is at a shell prompt" in ONE
// if-shell. Real tmux on a private server (ownTmux: -L label, TMUX cleared), on the deployed versions.
package tmux_test

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/tmux"
)

// shPane starts a session whose pane runs `sh`, an idle interactive shell.
func shPane(t *testing.T) modePane {
	t.Helper()
	run := ownTmux(t)
	run("new-session", "-d", "-s", "s", "-c", os.TempDir(), "sh")
	out := run("display-message", "-p", "-t", "=s:", "#{session_id} #{pane_id} #{pid}:#{start_time}")
	f := strings.Fields(out)
	if len(f) != 3 {
		t.Fatalf("identity %q", out)
	}
	p := modePane{run: run, sessionID: f[0], paneID: f[1], inst: f[2]}
	p.waitShell(t)
	return p
}

// waitShell waits for the pane to read as a shell at a prompt (`sh` is bash on macOS, dash on Debian: either is a shell).
func (p modePane) waitShell(t *testing.T) {
	t.Helper()
	for i := 0; i < 60; i++ {
		if tmux.IsShellCommand(p.run("display-message", "-p", "-t", p.paneID, "#{pane_current_command}")) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the pane never read as a shell")
}

func (p modePane) waitCommand(t *testing.T, want string) {
	t.Helper()
	for i := 0; i < 60; i++ {
		if p.run("display-message", "-p", "-t", p.paneID, "#{pane_current_command}") == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pane never ran %q", want)
}

func TestSendKeysIfInstanceAndShell_ShellPaneReceivesTheLine(t *testing.T) {
	p := shPane(t)
	if err := (&tmux.RealExecutor{}).SendKeysIfInstanceAndShell(p.sessionID, p.inst, "echo $((40+2))", tmux.KeyEnter); err != nil {
		t.Fatalf("send: %v", err)
	}
	// "42" is the OUTPUT of the line; the typed text spells it differently.
	if !p.screen(t, "\n42") {
		t.Fatalf("the line did not run: %q", p.run("capture-pane", "-p", "-t", p.paneID))
	}
}

// What the launch types is a command line with quotes, `$` and `#`: it must arrive byte for byte (hex keys), not re-read by tmux.
func TestSendKeysIfInstanceAndShell_LineArrivesVerbatim(t *testing.T) {
	p := shPane(t)
	line := `cd '/tmp/it'\''s here' && echo "$HOME" # #{pid} ;`
	if err := (&tmux.RealExecutor{}).SendKeysIfInstanceAndShell(p.sessionID, p.inst, line); err != nil {
		t.Fatalf("send: %v", err)
	}
	if !p.screen(t, line) {
		t.Fatalf("typed text altered: %q", p.run("capture-pane", "-p", "-t", p.paneID))
	}
}

func TestSendKeysIfInstanceAndShell_BusyPaneIsRefusedAndNothingTyped(t *testing.T) {
	p := catPane(t) // the foreground command is `cat`
	err := (&tmux.RealExecutor{}).SendKeysIfInstanceAndShell(p.sessionID, p.inst, "typed-into-cat-9z", tmux.KeyEnter)
	if !errors.Is(err, tmux.ErrPaneBusy) {
		t.Fatalf("err = %v, want ErrPaneBusy", err)
	}
	if p.screen(t, "typed-into-cat-9z") {
		t.Fatal("a refused launch typed into the busy pane")
	}
}

// The race the plan cannot close: the pane was at a prompt when the plan was read, a command starts, THEN the launch is sent.
func TestSendKeysIfInstanceAndShell_PaneTurnsBusyAfterThePlan(t *testing.T) {
	p := shPane(t)
	// "the plan": the pane is a shell right now.
	if got := p.run("display-message", "-p", "-t", p.paneID, "#{pane_current_command}"); !tmux.IsShellCommand(got) {
		t.Fatalf("setup: %q", got)
	}
	p.run("send-keys", "-t", p.paneID, "sleep 30", "Enter")
	p.waitCommand(t, "sleep")
	err := (&tmux.RealExecutor{}).SendKeysIfInstanceAndShell(p.sessionID, p.inst, "echo launched-after-busy-7q", tmux.KeyEnter)
	if !errors.Is(err, tmux.ErrPaneBusy) {
		t.Fatalf("err = %v, want ErrPaneBusy", err)
	}
	if p.screen(t, "launched-after-busy-7q") {
		t.Fatal("a launch was typed into a pane that had started a command")
	}
}

func TestSendKeysIfInstanceAndShell_WrongGenerationWinsOverBusy(t *testing.T) {
	for name, p := range map[string]modePane{"shell": shPane(t)} {
		err := (&tmux.RealExecutor{}).SendKeysIfInstanceAndShell(p.sessionID, "1:2", "wrong-gen-3k")
		if !errors.Is(err, tmux.ErrGenerationMismatch) {
			t.Fatalf("%s: err = %v, want ErrGenerationMismatch", name, err)
		}
		if p.screen(t, "wrong-gen-3k") {
			t.Fatalf("%s: typed despite the generation", name)
		}
	}
}

func TestSendKeysIfInstanceAndShell_GenerationAndBusyBothFail_GenerationIsReported(t *testing.T) {
	p := catPane(t)
	err := (&tmux.RealExecutor{}).SendKeysIfInstanceAndShell(p.sessionID, "1:2", "both-fail-5w")
	if !errors.Is(err, tmux.ErrGenerationMismatch) || errors.Is(err, tmux.ErrPaneBusy) {
		t.Fatalf("err = %v, want ErrGenerationMismatch (precedence)", err)
	}
}

func TestSendKeysIfInstanceAndShell_RejectsUnsafeArguments(t *testing.T) {
	x := &tmux.RealExecutor{}
	for _, c := range [][2]string{{"$0;kill-server", "1:2"}, {"work", "1:2"}, {"$0", "1:2,#{x}"}, {"$0", ""}} {
		if err := x.SendKeysIfInstanceAndShell(c[0], c[1], "x"); err == nil || errors.Is(err, tmux.ErrPaneBusy) || errors.Is(err, tmux.ErrGenerationMismatch) {
			t.Fatalf("%v: err = %v, want a refusal before tmux is asked", c, err)
		}
	}
}

func TestIsShellCommand(t *testing.T) {
	for _, c := range []string{"zsh", "bash", "fish", "sh", "dash", "ksh", "tcsh", "nu", "pwsh", "-zsh", "-bash", "ZSH", " zsh "} {
		if !tmux.IsShellCommand(c) {
			t.Errorf("%q should be a shell", c)
		}
	}
	for _, c := range []string{"", "npm", "node", "claude", "vim", "python3", "zshrc", "--zsh", "sleep"} {
		if tmux.IsShellCommand(c) {
			t.Errorf("%q should not be a shell", c)
		}
	}
}

func TestFakeSendKeysIfInstanceAndShell(t *testing.T) {
	f := tmux.NewFakeExecutor()
	f.SetInstance("9:9")
	if err := f.SendKeysIfInstanceAndShell("$0", "1:1", "x"); !errors.Is(err, tmux.ErrGenerationMismatch) {
		t.Fatalf("gen: %v", err)
	}
	f.SetPaneCommand("$0:", "npm")
	if err := f.SendKeysIfInstanceAndShell("$0", "9:9", "x"); !errors.Is(err, tmux.ErrPaneBusy) {
		t.Fatalf("busy: %v", err)
	}
	if len(f.RawKeysSent()) != 0 {
		t.Fatal("a refusal recorded keys")
	}
	f.SetPaneCommand("$0:", "-zsh")
	if err := f.SendKeysIfInstanceAndShell("$0", "9:9", "line", "\r"); err != nil {
		t.Fatalf("shell: %v", err)
	}
	if got := f.RawKeysSent(); len(got) != 1 || got[0].Target != "$0:" || strings.Join(got[0].Keys, "") != "line\r" {
		t.Fatalf("recorded %+v", got)
	}
}
