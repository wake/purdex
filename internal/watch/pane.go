package watch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/execstat"
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

// refusedSentinel is what the refusal branch of a conditional command prints (display-message -p), so the outcome comes back from
// the very command that made the check, not from a later look at tmux's state.
const refusedSentinel = "pdx-watch:refused"

// maxTypedLine bounds a line typed into the pane (agent_command ≤ 512 B plus an id and a prompt ≤ 4 KB, quoted).
const maxTypedLine = 16 << 10

var (
	// ErrPaneBusy: the pane is not at a shell (another program or agent is running, or it is in a mode); nothing was typed.
	ErrPaneBusy = errors.New("pane_busy")
	// ErrNoPaneSession: there is no pdx-watch session (and so no pane to type into).
	ErrNoPaneSession = errors.New("no pdx-watch tmux session")

	sessionIDRe = regexp.MustCompile(`^\$[0-9]+$`)
	paneIDRe    = regexp.MustCompile(`^%[0-9]+$`)
	uuidRe      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// shellCommands are the foreground commands that count as "the pane is at a shell" (a login shell shows a leading '-').
var shellCommands = map[string]bool{"zsh": true, "bash": true, "sh": true, "fish": true}

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
		bin = findTmux()
	}
	return &PaneControl{bin: bin}
}

// tmuxFallbackPaths are where tmux is installed when it is not on PATH (the daemon may run with a thin PATH).
var tmuxFallbackPaths = []string{"/opt/homebrew/bin/tmux", "/usr/local/bin/tmux", "/opt/local/bin/tmux", "/usr/bin/tmux"}

func findTmux() string {
	if p, err := exec.LookPath("tmux"); err == nil {
		return p
	}
	for _, p := range tmuxFallbackPaths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode().Perm()&0o111 != 0 {
			return p
		}
	}
	return "tmux"
}

// Recorded is the session and pane ids recorded for pdx-watch ("" before the first lookup).
func (p *PaneControl) Recorded() (sessionID, paneID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessionID, p.paneID
}

// tmux runs one tmux command (counted under watch.tmux) and answers its stdout. An ExitError carries tmux's stderr.
func (p *PaneControl) tmux(ctx context.Context, stdin string, args ...string) (string, error) {
	if p.label != "" {
		args = append([]string{"-L", p.label}, args...)
	}
	cmd := execstat.CommandContext(ctx, "watch.tmux", p.bin, args...)
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
	st.AtShell = !st.InMode && shellCommands[strings.TrimPrefix(st.Command, "-")]
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
	if err := checkLine("agent_command", agentCommand); err != nil {
		return "", err
	}
	if err := checkLine("prompt", prompt); prompt != "" && err != nil {
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

// checkLine: s is non-empty, valid UTF-8 and holds no control character.
func checkLine(what, s string) error {
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

// identityCondition is the part of a conditional command that says the target is still pane paneID of session sessionID named
// pdx-watch (a restarted tmux server can hand a pane id to another session between our lookup and the command). Both ids were
// checked against the $N / %N patterns when they were read.
func identityCondition(sessionID, paneID string) string {
	return "#{&&:#{==:#{session_id}," + sessionID + "},#{&&:#{==:#{pane_id}," + paneID + "},#{==:#{session_name}," + PaneSession + "}}}"
}

// typeCondition: the identity, a shell in the foreground, and the pane not in a mode.
func typeCondition(sessionID, paneID string) string {
	return "#{&&:" + identityCondition(sessionID, paneID) + "," +
		"#{&&:#{m/r:^-?(zsh|bash|sh|fish)$,#{pane_current_command}},#{==:#{pane_in_mode},0}}}"
}

// exitCondition: the identity and the pane not in a mode (typed into copy mode, "/exit" would be a search).
func exitCondition(sessionID, paneID string) string {
	return "#{&&:" + identityCondition(sessionID, paneID) + ",#{==:#{pane_in_mode},0}}"
}

// TypeLine types line into the pane and presses Enter, only if the pane is at a shell at that moment. The line is loaded into a
// named buffer from stdin, then ONE if-shell -F on the pane's own format runs paste-buffer -d -p and Enter, so nothing can take
// the pane between the check and the typing on tmux's side. When the condition was false the buffer is still there: it is
// deleted and the answer is ErrPaneBusy (the pane is never typed into).
func (p *PaneControl) TypeLine(ctx context.Context, line string) error {
	if err := checkLine("line", line); err != nil {
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

// typeInto is TypeLine's tmux half for the pane the caller names: the line travels in a buffer of its own, and ONE if-shell checks
// the pane's identity and state and pastes (the refusal branch deletes the buffer and prints refusedSentinel, which is how the
// outcome comes back). An error leaves no buffer behind.
func (p *PaneControl) typeInto(ctx context.Context, line, sessionID, pane string) error {
	buf, err := newBufferName()
	if err != nil {
		return err
	}
	if _, err := p.tmux(ctx, line, "load-buffer", "-b", buf, "-"); err != nil {
		p.dropBuffer(buf)
		return err
	}
	typed := fmt.Sprintf("paste-buffer -d -p -b %s -t %s ; send-keys -t %s Enter", buf, pane, pane)
	refused := fmt.Sprintf("delete-buffer -b %s ; display-message -p %s", buf, refusedSentinel)
	out, err := p.tmux(ctx, "", "if-shell", "-F", "-t", pane, typeCondition(sessionID, pane), typed, refused)
	if err != nil {
		p.dropBuffer(buf) // the context may be the reason: the cleanup has its own
		return err
	}
	if strings.Contains(out, refusedSentinel) {
		return ErrPaneBusy
	}
	return nil
}

// newBufferName is pasteBuffer plus 16 random hex digits.
func newBufferName() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return pasteBuffer + "-" + hex.EncodeToString(b[:]), nil
}

// dropBuffer removes a line buffer, best effort, under a context of its own: the caller's may be the one that was cancelled.
func (p *PaneControl) dropBuffer(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = p.tmux(ctx, "", "delete-buffer", "-b", name)
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

func (p *PaneControl) sendExitTo(ctx context.Context, sessionID, pane string) error {
	sent := fmt.Sprintf("send-keys -t %s -l /exit ; send-keys -t %s Enter", pane, pane)
	out, err := p.tmux(ctx, "", "if-shell", "-F", "-t", pane, exitCondition(sessionID, pane), sent, "display-message -p "+refusedSentinel)
	if err != nil {
		return err
	}
	if strings.Contains(out, refusedSentinel) {
		return ErrPaneBusy
	}
	return nil
}
