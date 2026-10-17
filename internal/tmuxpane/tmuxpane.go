// Package tmuxpane is the helpers that type into a tmux pane safely: run a tmux command, check that the target is still the pane
// and session that were read, and type a line (or /exit) only if the pane is at a shell, in one tmux command. The load watcher's
// pdx-watch session and a team member's pane both use them. Everything is addressed by the immutable $N session id and %N pane id;
// the session name is part of the identity check, so a pane id handed to another session by a restarted server is never typed into.
package tmuxpane

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/execstat"
)

// Tmux runs tmux commands: Bin is the executable, Label the -L socket label (tests only; "" is the person's own server) and Tag the
// name the commands are counted under.
type Tmux struct {
	Bin   string
	Label string
	Tag   string
}

var (
	// ErrBusy: the pane is not at a shell (another program or agent is running, or it is in a mode); nothing was typed.
	ErrBusy = errors.New("pane_busy")

	SessionIDRe = regexp.MustCompile(`^\$[0-9]+$`)
	PaneIDRe    = regexp.MustCompile(`^%[0-9]+$`)
)

// RefusedSentinel is what the refusal branch of a conditional command prints (display-message -p), so the outcome comes back from
// the very command that made the check, not from a later look at tmux's state.
const RefusedSentinel = "pdx-watch:refused"

// MaxTypedLine bounds a line typed into the pane (agent_command ≤ 512 B plus an id and a prompt ≤ 4 KB, quoted).
const MaxTypedLine = 16 << 10

// ShellCommands are the foreground commands that count as "the pane is at a shell" (a login shell shows a leading '-').
var ShellCommands = map[string]bool{"zsh": true, "bash": true, "sh": true, "fish": true}

// FallbackPaths are where tmux is installed when it is not on PATH (the daemon may run with a thin PATH).
var FallbackPaths = []string{"/opt/homebrew/bin/tmux", "/usr/local/bin/tmux", "/opt/local/bin/tmux", "/usr/bin/tmux"}

func Find() string {
	if p, err := exec.LookPath("tmux"); err == nil {
		return p
	}
	for _, p := range FallbackPaths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode().Perm()&0o111 != 0 {
			return p
		}
	}
	return "tmux"
}

// Run runs one tmux command (counted under the Tag) and answers its stdout. An ExitError carries tmux's stderr.
func (t *Tmux) Run(ctx context.Context, stdin string, args ...string) (string, error) {
	if t.Label != "" {
		args = append([]string{"-L", t.Label}, args...)
	}
	cmd := execstat.CommandContext(ctx, t.Tag, t.Bin, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return string(out), fmt.Errorf("tmux %s: %w: %s", args[0], err, strings.TrimSpace(string(ee.Stderr)))
		}
		return string(out), fmt.Errorf("tmux %s: %w", args[0], err)
	}
	return string(out), nil
}

// CheckLine: s is non-empty, valid UTF-8 and holds no control character.
func CheckLine(what, s string) error {
	if s == "" || !utf8.ValidString(s) {
		return fmt.Errorf("%s must be non-empty UTF-8 text", what)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s holds a control character", what)
		}
	}
	return nil
}

// IdentityCondition is the part of a conditional command that says the target is still pane paneID of session sessionID named
// sessionName (a restarted tmux server can hand a pane id to another session between our lookup and the command). Both ids were
// checked against the $N / %N patterns when they were read.
func IdentityCondition(sessionID, paneID, sessionName string) string {
	return "#{&&:#{==:#{session_id}," + sessionID + "},#{&&:#{==:#{pane_id}," + paneID + "},#{==:#{session_name}," + sessionName + "}}}"
}

// TypeCondition: the identity, a shell in the foreground, and the pane not in a mode.
func TypeCondition(sessionID, paneID, sessionName string) string {
	return "#{&&:" + IdentityCondition(sessionID, paneID, sessionName) + "," +
		"#{&&:#{m/r:^-?(zsh|bash|sh|fish)$,#{pane_current_command}},#{==:#{pane_in_mode},0}}}"
}

// ExitCondition: the identity and the pane not in a mode (typed into copy mode, "/exit" would be a search).
func ExitCondition(sessionID, paneID, sessionName string) string {
	return "#{&&:" + IdentityCondition(sessionID, paneID, sessionName) + ",#{==:#{pane_in_mode},0}}"
}

// TypeInto is the tmux half of typing a line into the pane the caller names: the line travels in a buffer of its own, and ONE if-shell checks
// the pane's identity and state and pastes (the refusal branch deletes the buffer and prints RefusedSentinel, which is how the
// outcome comes back). An error leaves no buffer behind.
func (t *Tmux) TypeInto(ctx context.Context, line, sessionID, pane, sessionName, bufferPrefix string) error {
	buf, err := NewBufferName(bufferPrefix)
	if err != nil {
		return err
	}
	if _, err := t.Run(ctx, line, "load-buffer", "-b", buf, "-"); err != nil {
		t.DropBuffer(buf)
		return err
	}
	typed := fmt.Sprintf("paste-buffer -d -p -b %s -t %s ; send-keys -t %s Enter", buf, pane, pane)
	refused := fmt.Sprintf("delete-buffer -b %s ; display-message -p %s", buf, RefusedSentinel)
	out, err := t.Run(ctx, "", "if-shell", "-F", "-t", pane, TypeCondition(sessionID, pane, sessionName), typed, refused)
	if err != nil {
		t.DropBuffer(buf) // the context may be the reason: the cleanup has its own
		return err
	}
	if strings.Contains(out, RefusedSentinel) {
		return ErrBusy
	}
	return nil
}

// NewBufferName is prefix plus 16 random hex digits.
func NewBufferName(prefix string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(b[:]), nil
}

// DropBuffer removes a line buffer, best effort, under a context of its own: the caller's may be the one that was cancelled.
func (t *Tmux) DropBuffer(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = t.Run(ctx, "", "delete-buffer", "-b", name)
}

// SendExitTo types /exit and Enter into the pane the caller names, if it is still that pane of that session and not in a mode.
func (t *Tmux) SendExitTo(ctx context.Context, sessionID, pane, sessionName string) error {
	sent := fmt.Sprintf("send-keys -t %s -l /exit ; send-keys -t %s Enter", pane, pane)
	out, err := t.Run(ctx, "", "if-shell", "-F", "-t", pane, ExitCondition(sessionID, pane, sessionName), sent, "display-message -p "+RefusedSentinel)
	if err != nil {
		return err
	}
	if strings.Contains(out, RefusedSentinel) {
		return ErrBusy
	}
	return nil
}
