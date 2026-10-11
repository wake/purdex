// internal/tmux/send_keys_mode_test.go — the Enter guard of the typed submit (K-1a-2): generation AND copy-mode in ONE
// if-shell, aimed at an explicit pane. Real tmux on a private server (ownTmux: -L label, TMUX cleared).
package tmux_test

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/tmux"
)

type modePane struct {
	run                     func(args ...string) string
	sessionID, paneID, inst string
}

// catPane starts a session whose pane runs `cat`, so keys sent to it are echoed and can be read back with capture-pane.
func catPane(t *testing.T) modePane {
	t.Helper()
	run := ownTmux(t)
	run("new-session", "-d", "-s", "m", "-c", os.TempDir(), "cat")
	out := run("display-message", "-p", "-t", "=m:", "#{session_id} #{pane_id} #{pid}:#{start_time}")
	f := strings.Fields(out)
	if len(f) != 3 {
		t.Fatalf("identity %q", out)
	}
	return modePane{run: run, sessionID: f[0], paneID: f[1], inst: f[2]}
}

func (p modePane) screen(t *testing.T, want string) bool {
	t.Helper()
	for i := 0; i < 40; i++ {
		if strings.Contains(p.run("capture-pane", "-p", "-t", p.paneID), want) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func TestSendKeysIfInstanceAndMode_NormalPaneReceivesKeys(t *testing.T) {
	p := catPane(t)
	if err := (&tmux.RealExecutor{}).SendKeysIfInstanceAndMode(p.sessionID, p.inst, p.paneID, "hello-k1a2", tmux.KeyEnter); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !p.screen(t, "hello-k1a2") {
		t.Fatal("the keys never arrived")
	}
}

func TestSendKeysIfInstanceAndMode_CopyModeIsRefusedAsPaneBusy(t *testing.T) {
	p := catPane(t)
	p.run("copy-mode", "-t", p.paneID)
	err := (&tmux.RealExecutor{}).SendKeysIfInstanceAndMode(p.sessionID, p.inst, p.paneID, "typed-in-copy-mode")
	if !errors.Is(err, tmux.ErrPaneBusy) {
		t.Fatalf("err = %v, want ErrPaneBusy", err)
	}
	p.run("send-keys", "-t", p.paneID, "-X", "cancel")
	if strings.Contains(p.run("capture-pane", "-p", "-t", p.paneID), "typed-in-copy-mode") {
		t.Fatal("a refused send must type nothing")
	}
}

func TestSendKeysIfInstanceAndMode_WrongGenerationIsRefused(t *testing.T) {
	p := catPane(t)
	err := (&tmux.RealExecutor{}).SendKeysIfInstanceAndMode(p.sessionID, "1:2", p.paneID, "wrong-gen")
	if !errors.Is(err, tmux.ErrGenerationMismatch) || errors.Is(err, tmux.ErrPaneBusy) {
		t.Fatalf("err = %v, want ErrGenerationMismatch", err)
	}
	if strings.Contains(p.run("capture-pane", "-p", "-t", p.paneID), "wrong-gen") {
		t.Fatal("a refused send must type nothing")
	}
}

func TestSendKeysIfInstanceAndMode_PaneOfAnotherSessionIsRefused(t *testing.T) {
	p := catPane(t)
	p.run("new-session", "-d", "-s", "other", "-c", os.TempDir(), "cat")
	other := p.run("display-message", "-p", "-t", "=other:", "#{pane_id}")
	err := (&tmux.RealExecutor{}).SendKeysIfInstanceAndMode(p.sessionID, p.inst, other, "stranger")
	if !errors.Is(err, tmux.ErrPaneMismatch) {
		t.Fatalf("err = %v, want ErrPaneMismatch", err)
	}
}

func TestSendKeysIfInstanceAndMode_RejectsUnsafeArguments(t *testing.T) {
	x := &tmux.RealExecutor{}
	for _, c := range [][3]string{{"$1", "1:2", "%1'"}, {"$1", "1:2", "1"}, {"$1", "1:2", ""}, {"$1", "1}", "%1"}, {"dev", "1:2", "%1"}} {
		if err := x.SendKeysIfInstanceAndMode(c[0], c[1], c[2], "x"); err == nil || errors.Is(err, tmux.ErrPaneBusy) {
			t.Fatalf("%v must be refused as a bad argument, got %v", c, err)
		}
	}
}

// Text and Enter travel in ONE guarded call: a refusal types neither, and an accepted call lands both (the Enter ends the line).
func TestSendKeysIfInstanceAndMode_TextAndEnterAreAllOrNothing(t *testing.T) {
	p := catPane(t)
	ex := &tmux.RealExecutor{}
	if err := ex.SendKeysIfInstanceAndMode(p.sessionID, "1:2", p.paneID, "refused-text", tmux.KeyEnter); !errors.Is(err, tmux.ErrGenerationMismatch) {
		t.Fatalf("err = %v, want ErrGenerationMismatch", err)
	}
	if err := ex.SendKeysIfInstanceAndMode(p.sessionID, p.inst, p.paneID, "landed-text", tmux.KeyEnter); err != nil {
		t.Fatal(err)
	}
	if !p.screen(t, "landed-text") {
		t.Fatal("the accepted call never arrived")
	}
	if strings.Contains(p.run("capture-pane", "-p", "-t", p.paneID), "refused-text") {
		t.Fatal("a refused call typed its text")
	}
}
