package watch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/wake/purdex/internal/tmuxpane"
)

// The tmux session the load watcher lives in and the helpers that put a command line into its pane (host-load-control spec
// §12.4, plan W3a step 3). The daemon types into the pane only when it is at a shell, and the check and the typing are one tmux
// command; everything is addressed by the immutable $N session id and %N pane id recorded for the session, so a recreated
// pdx-watch is looked up again and the old pane id is never typed into. The session is in the person's own tmux server (the
// default socket); label is for tests only.

// PaneSession is the reserved name of the watcher's tmux session.
const PaneSession = "pdx-watch"

// pasteBuffer is the prefix of the tmux buffer a line travels in: loaded from stdin, so it never passes through a command
// parser. Buffers belong to the whole tmux server, so every typing uses a buffer of its own (the prefix and 16 random hex
// digits): another client's buffer of the same name is neither overwritten nor pasted, and two typings cannot trade lines.
const pasteBuffer = "pdx-watch-line"

var (
	// ErrPaneBusy: the pane is not at a shell (another program or agent is running, or it is in a mode); nothing was typed.
	ErrPaneBusy = tmuxpane.ErrBusy
	// ErrNoPaneSession: there is no pdx-watch session (and so no pane to type into).
	ErrNoPaneSession = errors.New("no pdx-watch tmux session")

	sessionIDRe = tmuxpane.SessionIDRe
	paneIDRe    = tmuxpane.PaneIDRe
	uuidRe      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// maxTypedLine bounds a line typed into the pane.
const maxTypedLine = tmuxpane.MaxTypedLine

// tmux runs one tmux command (counted under watch.tmux).
func (p *PaneControl) tmux(ctx context.Context, stdin string, args ...string) (string, error) {
	return (&tmuxpane.Tmux{Bin: p.bin, Label: p.label, Tag: "watch.tmux"}).Run(ctx, stdin, args...)
}

// typeInto is TypeLine's tmux half for the pane the caller names (the helpers of internal/tmuxpane, for the pdx-watch session).
func (p *PaneControl) typeInto(ctx context.Context, line, sessionID, pane string) error {
	return (&tmuxpane.Tmux{Bin: p.bin, Label: p.label, Tag: "watch.tmux"}).TypeInto(ctx, line, sessionID, pane, PaneSession, pasteBuffer)
}

// sendExitTo is SendExit's tmux half for the pane the caller names.
func (p *PaneControl) sendExitTo(ctx context.Context, sessionID, pane string) error {
	return (&tmuxpane.Tmux{Bin: p.bin, Label: p.label, Tag: "watch.tmux"}).SendExitTo(ctx, sessionID, pane, PaneSession)
}

// PaneState is one reading of the watcher's pane.
type PaneState struct {
	Command string // the foreground process
	AtShell bool   // a shell, and the pane is not in a mode
	InMode  bool   // copy mode and the like
}

// PaneControl is the handle on the pdx-watch session: the ids recorded for it and the tmux calls.
type PaneControl struct {
	bin   string
	label string // tests: the -L socket label; "" is the person's own server

	mu        sync.Mutex // the recorded ids, and the one named buffer
	sessionID string
	paneID    string
}

// NewPaneControl returns the handle; bin is the tmux executable ("" looks for it).
func NewPaneControl(bin string) *PaneControl {
	if bin == "" {
		bin = tmuxpane.Find()
	}
	return &PaneControl{bin: bin}
}

// Recorded is the session and pane ids recorded for pdx-watch ("" before the first lookup).
func (p *PaneControl) Recorded() (sessionID, paneID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessionID, p.paneID
}

// hasSession says whether pdx-watch exists. tmux's exit 1 means no (no server counts as no); any other failure is an error.
func (p *PaneControl) hasSession(ctx context.Context) (bool, error) {
	_, err := p.tmux(ctx, "", "has-session", "-t", "="+PaneSession)
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// lookup reads the session's ids afresh and records them: the $N id and the first pane (the lowest window, then pane index).
// found is false when there is no pdx-watch session. Callers hold p.mu.
func (p *PaneControl) lookup(ctx context.Context) (found bool, err error) {
	ok, err := p.hasSession(ctx)
	if err != nil || !ok {
		p.sessionID, p.paneID = "", ""
		return false, err
	}
	out, err := p.tmux(ctx, "", "list-panes", "-s", "-t", "="+PaneSession, "-F", "#{session_id} #{window_index} #{pane_index} #{pane_id}")
	if err != nil {
		return false, err
	}
	bestW, bestP := -1, -1
	var sid, pane string
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(ln)
		if len(f) != 4 || !sessionIDRe.MatchString(f[0]) || !paneIDRe.MatchString(f[3]) {
			continue
		}
		w, werr := strconv.Atoi(f[1])
		pi, perr := strconv.Atoi(f[2])
		if werr != nil || perr != nil {
			continue
		}
		if bestW < 0 || w < bestW || (w == bestW && pi < bestP) {
			bestW, bestP, sid, pane = w, pi, f[0], f[3]
		}
	}
	if pane == "" {
		return false, fmt.Errorf("list-panes of %s answered no pane: %q", PaneSession, out)
	}
	p.sessionID, p.paneID = sid, pane
	return true, nil
}

// Find reads the pdx-watch session's ids without creating anything: found is false when there is no such session (nor a tmux
// server), an error when the lookup itself failed (which is not "no session": a caller must not treat it as one).
func (p *PaneControl) Find(ctx context.Context) (sessionID, paneID string, found bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	found, err = p.lookup(ctx)
	if err != nil || !found {
		return "", "", false, err
	}
	return p.sessionID, p.paneID, true, nil
}

// EnsureSession makes sure pdx-watch exists (created detached in workdir when missing) and records its $N and the first pane's
// %N. workdir is an absolute path or ~/…; tmux -c would neither expand ~ nor refuse a bad path (it falls back to $HOME), so the
// directory is checked here.
func (p *PaneControl) EnsureSession(ctx context.Context, workdir string) (sessionID, paneID string, created bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if found, err := p.lookup(ctx); err != nil {
		return "", "", false, err
	} else if found {
		return p.sessionID, p.paneID, false, nil
	}
	dir, err := expandWorkdir(workdir)
	if err != nil {
		return "", "", false, err
	}
	out, err := p.tmux(ctx, "", "new-session", "-d", "-s", PaneSession, "-c", dir, "-P", "-F", "#{session_id} #{pane_id}")
	if err != nil {
		// two starts crossed: the session is there now
		if found, lerr := p.lookup(ctx); lerr == nil && found {
			return p.sessionID, p.paneID, false, nil
		}
		return "", "", false, err
	}
	f := strings.Fields(out)
	if len(f) != 2 || !sessionIDRe.MatchString(f[0]) || !paneIDRe.MatchString(f[1]) {
		return "", "", false, fmt.Errorf("new-session answered %q", out)
	}
	p.sessionID, p.paneID = f[0], f[1]
	return p.sessionID, p.paneID, true, nil
}

func expandWorkdir(workdir string) (string, error) {
	dir := workdir
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("workdir %q: %w", workdir, err)
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("workdir %q is not an absolute path", workdir)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("workdir %q: %w", workdir, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("workdir %q is not a directory", workdir)
	}
	return dir, nil
}

// read is one display-message of the recorded pane: whether it still belongs to the recorded pdx-watch session, and its
// foreground command and mode. Callers hold p.mu.
func (p *PaneControl) read(ctx context.Context) (st PaneState, mine bool, err error) {
	out, err := p.tmux(ctx, "", "display-message", "-p", "-t", p.paneID,
		"#{session_id} #{pane_id} #{pane_in_mode} #{==:#{session_name},"+PaneSession+"} #{pane_current_command}")
	if err != nil {
		return PaneState{}, false, err
	}
	f := strings.SplitN(strings.TrimRight(out, "\n"), " ", 5)
	if len(f) != 5 {
		return PaneState{}, false, fmt.Errorf("display-message answered %q", out)
	}
	mine = f[0] == p.sessionID && f[1] == p.paneID && f[3] == "1"
	st.InMode = f[2] != "0"
	st.Command = f[4]
	st.AtShell = !st.InMode && tmuxpane.ShellCommands[strings.TrimPrefix(st.Command, "-")]
	return st, mine, nil
}

// readLocked reads the pane, looking the session up again when nothing is recorded or the recorded ids no longer belong to
// pdx-watch (a recreated session, a restarted server): the old pane id is never trusted. Callers hold p.mu.
func (p *PaneControl) readLocked(ctx context.Context) (PaneState, error) {
	if p.paneID != "" {
		if st, mine, err := p.read(ctx); err == nil && mine {
			return st, nil
		}
	}
	found, err := p.lookup(ctx)
	if err != nil {
		return PaneState{}, err
	}
	if !found {
		return PaneState{}, ErrNoPaneSession
	}
	st, mine, err := p.read(ctx)
	if err != nil {
		return PaneState{}, err
	}
	if !mine {
		return PaneState{}, fmt.Errorf("pane %s is not the first pane of %s", p.paneID, PaneSession)
	}
	return st, nil
}

// PaneAtShell is the early "is it busy" answer: one display-message for the foreground command, the mode and the ids. It is not
// the guard of the typing (TypeLine's own condition is).
func (p *PaneControl) PaneAtShell(ctx context.Context) (PaneState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.readLocked(ctx)
}

// CommandLine builds the line to type (spec §12.4): agent_command, then --resume <id> for a wake (a UUID), then the prompt as
// one POSIX single-quoted word ('  written as '\”), so $(…), backticks, ; and quotes in a prompt reach the agent as text. A
// control character anywhere (a newline, NUL, ESC) is refused: it could end the line early.
func CommandLine(agentCommand, resumeID, prompt string) (string, error) {
	if err := tmuxpane.CheckLine("agent_command", agentCommand); err != nil {
		return "", err
	}
	if err := tmuxpane.CheckLine("prompt", prompt); prompt != "" && err != nil {
		return "", err
	}
	line := agentCommand
	if resumeID != "" {
		if !uuidRe.MatchString(resumeID) {
			return "", fmt.Errorf("resume id %q is not a session id (a UUID)", resumeID)
		}
		line += " --resume " + resumeID
	}
	if prompt != "" {
		line += " '" + strings.ReplaceAll(prompt, "'", `'\''`) + "'"
	}
	if len(line) > maxTypedLine {
		return "", fmt.Errorf("the command line is over %d bytes", maxTypedLine)
	}
	return line, nil
}

// TypeLine types line into the pane and presses Enter, only if the pane is at a shell at that moment. The line is loaded into a
// named buffer from stdin, then ONE if-shell -F on the pane's own format runs paste-buffer -d -p and Enter, so nothing can take
// the pane between the check and the typing on tmux's side. When the condition was false the buffer is still there: it is
// deleted and the answer is ErrPaneBusy (the pane is never typed into).
func (p *PaneControl) TypeLine(ctx context.Context, line string) error {
	if err := tmuxpane.CheckLine("line", line); err != nil {
		return err
	}
	if len(line) > maxTypedLine {
		return fmt.Errorf("the line is over %d bytes", maxTypedLine)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := p.readLocked(ctx); err != nil { // the ids are current (a recreated session is found here)
		return err
	}
	return p.typeInto(ctx, line, p.sessionID, p.paneID)
}

// SendExit types /exit and Enter into the pane: the agent's own input, so the shell rule does not apply. The caller reconciles
// first and checks that the frame it means is still the one running (tmux cannot see frames); this command checks, in tmux, that
// the pane is still pdx-watch's recorded one and not in a mode, and sends both keys in that one command. If the agent ended in
// between, the shell answers "command not found", which is harmless; ErrPaneBusy is a pane that is no longer the recorded one
// or is in a mode.
func (p *PaneControl) SendExit(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := p.readLocked(ctx); err != nil {
		return err
	}
	return p.sendExitTo(ctx, p.sessionID, p.paneID)
}
