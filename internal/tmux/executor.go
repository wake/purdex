// internal/tmux/executor.go
package tmux

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wake/purdex/internal/claudeenv"
	"github.com/wake/purdex/internal/execstat"
)

var ErrNoSession = errors.New("no such session")

type TmuxSession struct {
	ID      string // tmux session ID, e.g. "$0"
	Name    string
	Created int64 // #{session_created}, unix seconds; 0 = unknown
	Cwd     string
}

// PaneLocation is one pane as ListAllPanes reports it, every value as tmux
// prints it.
type PaneLocation struct {
	PaneID    string // e.g. "%5"
	SessionID string // e.g. "$0"
	PanePID   string // pid of the pane's process
}

type TmuxPaneMetadata struct {
	SessionID          string
	SessionName        string
	WindowID           string
	PaneID             string
	PaneTitle          string
	WindowName         string
	PaneCurrentCommand string
}

// Executor abstracts tmux CLI for testability.
type Executor interface {
	// ListSessions and ActivePaneMetadata are the reads a session list is
	// built from (#1293). They take a context and are bounded by it: when it
	// ends the tmux child is killed and the returned error wraps ctx.Err(),
	// so errors.Is(err, context.DeadlineExceeded|Canceled) holds.
	ListSessions(ctx context.Context) ([]TmuxSession, error)
	ActivePaneMetadata(ctx context.Context, sessionName string) (TmuxPaneMetadata, error)
	NewSession(name, cwd string) error
	// NewSessionContext is NewSession bounded by ctx (#1293): when ctx ends
	// the tmux client is killed and the error wraps ctx.Err(). A killed
	// client says nothing about the server: new-session may still have
	// created the session, so a caller whose ctx ended asks HasSessionContext
	// before concluding that nothing exists.
	NewSessionContext(ctx context.Context, name, cwd string) error
	// NewSessionTaggedContext is NewSessionContext that also sets the new
	// session's user option (`@name`) to value in the same invocation, and
	// returns the id and generation new-session printed (also with an
	// error); PaneIdentity reads a pane's server generation, session and
	// pane ids, that option and its directory in one invocation
	// (pane_identity.go).
	NewSessionTaggedContext(ctx context.Context, name, cwd, option, value string) (sessionID, instance string, err error)
	PaneIdentity(ctx context.Context, target, option string) (PaneIdentity, error)
	KillSession(name string) error
	RenameSession(oldName, newName string) error
	HasSession(name string) bool
	// HasSessionContext is HasSession bounded by ctx (#1293). (false, nil) is
	// tmux's own "no" (no such session, or no server); an error wraps
	// ctx.Err() and means the question went unanswered.
	HasSessionContext(ctx context.Context, name string) (bool, error)
	// HasPane reports whether the given pane id (e.g. "%5") still exists in
	// the global tmux pane list. Returns (false, nil) on empty paneID or
	// confirmed absence; (true, nil) on confirmed presence; (_, err) on a
	// transient tmux command failure (no server, exec error). Per round-4
	// audit: callers that derive "pane gone" semantics MUST distinguish
	// confirmed-absence from query-error — collapsing both into false
	// causes false-positive "pane gone" / clear emissions during tmux
	// hiccups while the pane is still alive.
	HasPane(paneID string) (exists bool, err error)
	SendKeys(target, keys string) error
	SendKeysRaw(target string, keys ...string) error
	// SendKeysIfInstance sends keys to the session id's active pane only when
	// the tmux server's generation ("<pid>:<start_time>") equals
	// expectedInstance, evaluated by the SAME server connection that performs
	// the send. See send_keys_conditional.go for why nothing weaker works.
	//
	// (true, nil) sent; (false, nil) the server declined; (false, err) the
	// invocation could not be completed and nothing was sent.
	SendKeysIfInstance(sessionID, expectedInstance string, keys ...string) (sent bool, err error)
	// SendKeysIfInstanceTarget is SendKeysIfInstance aimed at `$N:<window>`
	// — that window's active pane — so a caller whose liveness checks read
	// `<name>:0` sends to the pane it checked. window must be an index.
	SendKeysIfInstanceTarget(sessionID, window, expectedInstance string, keys ...string) (sent bool, err error)
	// KillSessionIfInstance kills the session id only when the tmux server's
	// generation equals expectedInstance, evaluated by the SAME server
	// connection that performs the kill — so a session created under one
	// generation is never "killed" by name on the server that replaced it.
	// See kill_session_conditional.go.
	//
	// (true, nil) killed; (false, nil) the server declined; (false, err) the
	// invocation could not be completed and nothing was killed (err wraps
	// ErrNoSession when the id is gone on the matching server).
	KillSessionIfInstance(sessionID, expectedInstance string) (killed bool, err error)
	PasteText(target, text string) error
	PaneCurrentPath(target string) (string, error)
	PaneSessionName(target string) (string, error)
	// PaneSessionID returns the tmux session ID ("$N") the pane belongs to.
	// Unlike the session name it is immutable for the life of the session, so
	// a caller that must not be confused by a rename asks for this instead.
	//
	// It takes a context for the same reason ShowGlobalOption does: it is on a
	// request path with a deadline (GET /api/sessions/{code}/provenance calls
	// it once per pane to enumerate, and once more per pane to re-confirm),
	// and a tmux server that has stopped answering must not hold that request
	// open past it. A caller with nothing to bound it passes context.Background().
	PaneSessionID(ctx context.Context, target string) (string, error)
	// ListAllPanes reads every pane of the server — its id, its session's id
	// and its process's pid — in ONE tmux round trip, where PaneSessionID and
	// PanePID cost one per pane. The peers inventory reads it twice per pass:
	// once to enumerate the panes it walks, and once after the walk to
	// re-confirm that those panes are still in the sessions they were found in.
	//
	// It takes a context for the reason PaneSessionID does: it is on a request
	// path with a deadline, and a tmux server that has stopped answering must
	// not hold that request open past it. When ctx ends the error wraps
	// ctx.Err().
	//
	// A successful listing is complete: a row that does not parse fails the
	// whole call, so a pane absent from the answer is a pane that is gone.
	ListAllPanes(ctx context.Context) ([]PaneLocation, error)
	// ListPanePlacements reads every pane's pid and owning session NAME in ONE
	// tmux round trip, keyed by pane id, where ActivePanePID and
	// PaneSessionName cost one each per pane. A pane absent from a successful
	// answer is a pane that is gone. A pane linked into several sessions whose
	// owner the listing cannot decide comes back Ambiguous (see
	// buildPanePlacements). Bounded by ctx like ListAllPanes; a row that does
	// not parse fails the whole call.
	ListPanePlacements(ctx context.Context) (map[string]PanePlacement, error)
	PanePID(target string) (string, error)
	ActivePanePID(target string) (string, error)
	PaneChildCommands(target string) ([]string, error)
	CapturePaneContent(target string, lastN int) (string, error)
	// CapturePaneRange returns lines [start, endInclusive] of the pane.
	// Indexing follows tmux capture-pane -S/-E semantics:
	//   - start/end are line indices; 0 = top of visible pane
	//   - negative values reference history above the visible pane
	// Empty output is legal (not treated as an error).
	CapturePaneRange(target string, start, endInclusive int) (string, error)
	// CapturePaneTopLines returns the top n lines (0..n-1) of the pane.
	// Equivalent to CapturePaneRange(target, 0, n-1).
	// n <= 0 returns "" without invoking tmux (caller-friendly disable).
	CapturePaneTopLines(target string, n int) (string, error)
	PaneSize(target string) (cols, rows int, err error)
	ResizeWindow(target string, cols, rows int) error
	ResizeWindowAuto(target string) error
	// WindowSize returns the actual size of the target's current window
	// (`#{window_width} #{window_height}`). ctx cancels a stuck query.
	WindowSize(ctx context.Context, target string) (cols, rows uint16, err error)
	// StatusRows returns how many rows the target's status bar takes (0 when
	// off). A client must be window height + this tall for tmux to keep the
	// window at its size when that client is the only one attached.
	StatusRows(ctx context.Context, target string) (uint16, error)
	SetWindowOption(target, option, value string) error
	SetWindowOptionGlobal(option, value string) error
	ShowWindowOption(option string) (string, error)
	// ShowGlobalOption reads a SERVER/global option (`show-options -g`).
	// ShowWindowOption passes `-w` and can only see window options, so it
	// returns "" for a server option such as `default-shell` — which reads as
	// "unset" and is indistinguishable from a real absence.
	//
	// It takes a context because it is on a request path with a deadline
	// (POST /api/shell/resolve-command) and a tmux server that has stopped
	// answering must not hold that request open past it. A caller with
	// nothing to bound it passes context.Background().
	ShowGlobalOption(ctx context.Context, option string) (string, error)
	SetHookGlobal(event, command string) error
	RemoveHookGlobal(event string) error
	ShowHooksGlobal() (string, error)
	TmuxAlive() bool
	// ServerState classifies one `tmux info` probe: Up, Absent (no server;
	// creating a session starts one) or Broken (tmux unusable). TmuxAlive
	// is ServerState() == ServerUp (#1108, #1474 spec D1).
	ServerState() ServerState
}

// --- Real Executor ---

// observedCmd is an *exec.Cmd whose Output/Run record the fork in an execstat
// counter (count and wall time, success or failure; never arguments or output,
// #1767). Everything else (Stdin, Stderr, Env, WaitDelay) is the embedded
// Cmd's, so call sites keep their own setup.
type observedCmd struct {
	*exec.Cmd
	counter *execstat.Counter
}

func tmuxCmd(args ...string) *observedCmd {
	return &observedCmd{exec.Command("tmux", args...), &execstat.Tmux}
}

func tmuxCmdContext(ctx context.Context, args ...string) *observedCmd {
	return &observedCmd{exec.CommandContext(ctx, "tmux", args...), &execstat.Tmux}
}

func psCmd(args ...string) *observedCmd {
	return &observedCmd{exec.Command("ps", args...), &execstat.PS}
}

func (c *observedCmd) Output() ([]byte, error) {
	t0 := time.Now()
	out, err := c.Cmd.Output()
	c.counter.Observe(time.Since(t0))
	return out, err
}

func (c *observedCmd) Run() error {
	t0 := time.Now()
	err := c.Cmd.Run()
	c.counter.Observe(time.Since(t0))
	return err
}

type RealExecutor struct{}

func NewRealExecutor() *RealExecutor { return &RealExecutor{} }

// readWaitDelay bounds how long a bounded read waits for the child's pipes
// after its context ended and the child was killed. A tmux read is a single
// client process, so this is defensive: should anything ever inherit the
// child's stdout, Output() still returns within deadline + readWaitDelay.
const readWaitDelay = 500 * time.Millisecond

// boundedRead builds a tmux invocation that is killed when ctx ends — the
// reads above, and new-session, whose caller bounds it with a cap of its own.
func boundedRead(ctx context.Context, args ...string) *observedCmd {
	cmd := tmuxCmdContext(ctx, args...)
	cmd.WaitDelay = readWaitDelay
	return cmd
}

// readCtxErr reports a bounded read that failed because its context ended as
// an error wrapping ctx.Err() — not the bare "signal: killed" the killed
// child produces — so callers can errors.Is it against
// context.DeadlineExceeded / context.Canceled. Returns nil when the context
// is still live (the failure is tmux's own).
func readCtxErr(ctx context.Context, op string, err error) error {
	ctxErr := ctx.Err()
	if ctxErr == nil {
		return nil
	}
	return fmt.Errorf("%s: %w (%v)", op, ctxErr, err)
}

func (r *RealExecutor) ListSessions(ctx context.Context) ([]TmuxSession, error) {
	out, err := boundedRead(ctx, "list-sessions", "-F", "#{session_id}\t#{session_name}\t#{session_created}\t#{session_path}").Output()
	if err != nil {
		if cerr := readCtxErr(ctx, "tmux list-sessions", err); cerr != nil {
			return nil, cerr
		}
		// No server (stale or absent socket, #1473) or "no sessions" is an
		// empty list, not a failure. Only stderr can carry either phrase:
		// Output() captures it into ExitError.Stderr.
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if IsNoServer(stderr) || strings.Contains(stderr, "no sessions") {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("tmux list-sessions: %w", err)
	}
	return parseListSessionsOutput(string(out)), nil
}

// parseListSessionsOutput parses the TAB-separated
// "#{session_id}\t#{session_name}\t#{session_created}\t#{session_path}" lines from list-sessions.
//
// A line with fewer than 4 fields is malformed and skipped — never filled
// with empty Name/Cwd — so a bad line cannot take down the sessions API nor
// hand a bogus ID to orphan cleanup. The usual cause is a non-UTF-8 client
// locale, under which tmux sanitises the TAB separators to "_". Malformed
// lines are reported once per call (this runs on every watcher tick).
func parseListSessionsOutput(out string) []TmuxSession {
	var sessions []TmuxSession
	malformed := 0
	firstBad := ""
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		// SplitN(…, 4) on purpose: session_id, session_name and
		// session_created can never contain a TAB (tmux's session_check_name
		// vis-encodes it), but session_path is a raw filesystem path and may
		// — so the last field must absorb the rest of the line; that is also
		// why the path comes last. Do not "fix" this into
		// Split + len != 3.
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 4 {
			if malformed == 0 {
				firstBad = line
			}
			malformed++
			continue
		}
		created, _ := strconv.ParseInt(parts[2], 10, 64) // unreadable = 0 = unknown
		sessions = append(sessions, TmuxSession{ID: parts[0], Name: parts[1], Created: created, Cwd: parts[3]})
	}
	if malformed > 0 {
		log.Printf("tmux list-sessions: %d malformed line(s), e.g. %q (expected 4 tab-separated fields; is a UTF-8 locale exported?)", malformed, firstBad)
	}
	return sessions
}

func (r *RealExecutor) ActivePaneMetadata(ctx context.Context, sessionName string) (TmuxPaneMetadata, error) {
	out, err := boundedRead(ctx, "display-message", "-p", "-t", activePaneTarget(sessionName), activePaneMetadataFormat).Output()
	if err != nil {
		if cerr := readCtxErr(ctx, "tmux display-message", err); cerr != nil {
			return TmuxPaneMetadata{}, cerr
		}
		return TmuxPaneMetadata{}, fmt.Errorf("tmux display-message: %w", err)
	}
	if md, ok := parseActivePaneMetadata(string(out)); ok {
		return md, nil
	}
	// The field boundaries of the combined answer cannot be trusted (a TAB
	// the substitution did not remove, or a short answer): ask field by
	// field, as before #1293, rather than return misaligned values.
	return r.activePaneMetadataPerField(ctx, sessionName)
}

// activePaneMetadataFormat reads every ActivePaneMetadata field with ONE
// display-message (#1293 §3.1/§3.4: seven execs per session made a large
// host's list read approach its deadline), TAB-joined.
//
// pane_title and window_name are text a user or a program in the pane
// controls, and session_name / pane_current_command are free text too, so
// each goes through tmux's substitution modifier #{s/<TAB>/ /:…}: tmux
// replaces every TAB in the value with a space before printing, and the six
// separators are the only TABs in the answer. That is the same value the
// per-field read yields, since sanitizeTmuxMetadata maps TAB to a space
// anyway. The ids ($N, @N, %N) cannot contain a TAB and need no modifier.
//
// parseActivePaneMetadata is the second line for a tmux that does not apply
// the modifier: it demands exactly seven fields and each id in its slot
// ($N, @N, %N fence the free-text fields), else the read falls back.
const activePaneMetadataFormat = "#{session_id}\t" + tabsToSpace + "pane_title}" +
	"\t#{window_id}\t" + tabsToSpace + "window_name}" +
	"\t#{pane_id}\t" + tabsToSpace + "session_name}" +
	"\t" + tabsToSpace + "pane_current_command}"

// tabsToSpace opens a format whose value has every TAB replaced by a space:
// tabsToSpace + "field}" is #{s/<TAB>/ /:field} (the pattern is a literal TAB).
const tabsToSpace = "#{s/\t/ /:"

const activePaneMetadataFieldCount = 7

var (
	tmuxSessionIDRe = regexp.MustCompile(`^\$[0-9]+$`)
	tmuxWindowIDRe  = regexp.MustCompile(`^@[0-9]+$`)
	tmuxPaneIDRe    = regexp.MustCompile(`^%[0-9]+$`)
)

// parseActivePaneMetadata splits the combined display-message answer on TAB
// FIRST and only then sanitises each field — sanitizeTmuxMetadata maps TAB to
// a space, so the separator cannot survive into a field value.
//
// ok is false — and the caller must fall back to per-field reads — when the
// answer does not have exactly seven fields or any fenced id is not in its
// slot. With the substitution modifier applied neither can happen; a tmux
// that ignored it and printed a raw TAB inside a field yields extra fields,
// and even a field that forges an id in every slot it shifts (codex critic
// on dfafdf1a: pane_title "x<TAB>@9<TAB>y<TAB>%9") is caught by the count.
// Never a half-filled struct.
func parseActivePaneMetadata(out string) (md TmuxPaneMetadata, ok bool) {
	parts := strings.Split(strings.TrimSuffix(out, "\n"), "\t")
	if len(parts) != activePaneMetadataFieldCount ||
		!tmuxSessionIDRe.MatchString(parts[0]) ||
		!tmuxWindowIDRe.MatchString(parts[2]) ||
		!tmuxPaneIDRe.MatchString(parts[4]) {
		return TmuxPaneMetadata{}, false
	}
	for i := range parts {
		parts[i] = sanitizeTmuxMetadata(parts[i])
	}
	return TmuxPaneMetadata{
		SessionID:          parts[0],
		PaneTitle:          parts[1],
		WindowID:           parts[2],
		WindowName:         parts[3],
		PaneID:             parts[4],
		SessionName:        parts[5],
		PaneCurrentCommand: parts[6],
	}, true
}

// activePaneMetadataPerField is the pre-#1293 read: one display-message per
// field, each answer sanitised on its own. It is the fallback for a combined
// answer whose field boundaries cannot be trusted (see
// parseActivePaneMetadata). It runs under the same ctx, so a hung tmux still
// ends at the caller's deadline.
func (r *RealExecutor) activePaneMetadataPerField(ctx context.Context, sessionName string) (TmuxPaneMetadata, error) {
	target := activePaneTarget(sessionName)
	query := func(format string) (string, error) {
		out, err := boundedRead(ctx, "display-message", "-p", "-t", target, format).Output()
		if err != nil {
			if cerr := readCtxErr(ctx, "tmux display-message "+format, err); cerr != nil {
				return "", cerr
			}
			return "", fmt.Errorf("tmux display-message %s: %w", format, err)
		}
		return sanitizeTmuxMetadata(strings.TrimSuffix(string(out), "\n")), nil
	}

	sessionID, err := query("#{session_id}")
	if err != nil {
		return TmuxPaneMetadata{}, err
	}
	resolvedSessionName, err := query("#{session_name}")
	if err != nil {
		return TmuxPaneMetadata{}, err
	}
	windowID, err := query("#{window_id}")
	if err != nil {
		return TmuxPaneMetadata{}, err
	}
	paneID, err := query("#{pane_id}")
	if err != nil {
		return TmuxPaneMetadata{}, err
	}
	paneTitle, err := query("#{pane_title}")
	if err != nil {
		return TmuxPaneMetadata{}, err
	}
	windowName, err := query("#{window_name}")
	if err != nil {
		return TmuxPaneMetadata{}, err
	}
	paneCurrentCommand, err := query("#{pane_current_command}")
	if err != nil {
		return TmuxPaneMetadata{}, err
	}

	return TmuxPaneMetadata{
		SessionID:          sessionID,
		SessionName:        resolvedSessionName,
		WindowID:           windowID,
		PaneID:             paneID,
		PaneTitle:          paneTitle,
		WindowName:         windowName,
		PaneCurrentCommand: paneCurrentCommand,
	}, nil
}

func activePaneTarget(sessionName string) string {
	return "=" + sessionName + ":"
}

func sanitizeTmuxMetadata(s string) string {
	mapped := strings.Map(func(r rune) rune {
		switch r {
		case '\t', '\n', '\r':
			return ' '
		}
		if (r >= 0 && r < 0x20) || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(mapped), " ")
}

// newSessionCommand is new-session behind claudeenv's global unset, in the one tmux invocation: a server that
// was started from inside a Claude Code session holds that session's identity in its global environment, and
// every session created on it inherits it (#2122). Nothing else about the call changes.
func newSessionCommand(args ...string) []string {
	return append(claudeenv.TmuxGlobalUnsetArgs(), args...)
}

func (r *RealExecutor) NewSession(name, cwd string) error {
	return tmuxCmd(newSessionCommand("new-session", "-d", "-s", name, "-c", cwd)...).Run()
}

func (r *RealExecutor) NewSessionContext(ctx context.Context, name, cwd string) error {
	err := boundedRead(ctx, newSessionCommand("new-session", "-d", "-s", name, "-c", cwd)...).Run()
	if err == nil {
		return nil
	}
	if cerr := readCtxErr(ctx, "tmux new-session", err); cerr != nil {
		return cerr
	}
	return err
}

func (r *RealExecutor) KillSession(name string) error {
	err := tmuxCmd("kill-session", "-t", "="+name).Run()
	if err != nil {
		return ErrNoSession
	}
	return nil
}

func (r *RealExecutor) RenameSession(oldName, newName string) error {
	err := tmuxCmd("rename-session", "-t", "="+oldName, newName).Run()
	if err != nil {
		return fmt.Errorf("tmux rename-session: %w", err)
	}
	return nil
}

func (r *RealExecutor) HasSession(name string) bool {
	// Use "=" prefix for exact name matching (tmux 3.2+).
	// Without it, "has-session -t foo" matches "foobar" via prefix.
	return tmuxCmd("has-session", "-t", "="+name).Run() == nil
}

func (r *RealExecutor) HasSessionContext(ctx context.Context, name string) (bool, error) {
	err := boundedRead(ctx, "has-session", "-t", "="+name).Run()
	if err == nil {
		return true, nil
	}
	if cerr := readCtxErr(ctx, "tmux has-session", err); cerr != nil {
		return false, cerr
	}
	return false, nil
}

// HasPane reports whether the given pane id is currently listed by
// `tmux list-panes -a -F '#{pane_id}'`. Conservative on any error: a
// non-zero exit (no server running, etc) returns false. Empty paneID
// short-circuits to false without invoking tmux.
//
// Used by codex ProbeIntent ProcessDead detector — see
// internal/agent/codex/probe_intent_process_dead.go.
func (r *RealExecutor) HasPane(paneID string) (bool, error) {
	if paneID == "" {
		return false, nil
	}
	cmd := tmuxCmd("list-panes", "-a", "-F", "#{pane_id}")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// Round-5 audit F10: classify "no server running" as confirmed
		// global absence (false, nil) rather than a transient query
		// failure. Without this branch the detector would poll forever
		// when the user tears down the last tmux session — codex pane
		// is definitively gone but the lights stay armed. IsNoServer also
		// covers the absent-socket form after a reboot (#1473).
		if IsNoServer(stderr.String()) {
			return false, nil
		}
		return false, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == paneID {
			return true, nil
		}
	}
	return false, nil
}

func (r *RealExecutor) SendKeys(target, keys string) error {
	return tmuxCmd("send-keys", "-t", target, keys, "Enter").Run()
}

func (r *RealExecutor) SendKeysRaw(target string, keys ...string) error {
	args := []string{"send-keys", "-t", target}
	args = append(args, keys...)
	return tmuxCmd(args...).Run()
}

func (r *RealExecutor) PasteText(target, text string) error {
	// Use a named buffer to avoid races when multiple goroutines paste
	// concurrently (the default anonymous buffer is shared globally).
	bufName := fmt.Sprintf("pdx-%d", time.Now().UnixNano())

	cmd := tmuxCmd("load-buffer", "-b", bufName, "-")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tmux load-buffer: %w", err)
	}
	// -d  deletes the named buffer after pasting.
	// -p  wraps content in bracketed paste markers (\e[200~ … \e[201~)
	//     so the receiving application (e.g. Claude Code) recognises it as
	//     a paste event rather than typed input.
	// -r  preserves LF as-is (default converts LF → CR).
	return tmuxCmd("paste-buffer", "-b", bufName, "-t", target, "-d", "-p", "-r").Run()
}

func (r *RealExecutor) PaneCurrentPath(target string) (string, error) {
	out, err := tmuxCmd("display-message", "-p", "-t", target, "#{pane_current_path}").Output()
	if err != nil {
		return "", fmt.Errorf("tmux display-message pane_current_path: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *RealExecutor) PaneSessionName(target string) (string, error) {
	out, err := tmuxCmd("display-message", "-p", "-t", target, "#{session_name}").Output()
	if err != nil {
		return "", fmt.Errorf("tmux display-message session_name: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *RealExecutor) PaneSessionID(ctx context.Context, target string) (string, error) {
	out, err := tmuxCmdContext(ctx, "display-message", "-p", "-t", target, "#{session_id}").Output()
	if err != nil {
		return "", fmt.Errorf("tmux display-message session_id: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// listAllPanesFormat separates its fields with a space, not the TAB the other
// listings use: tmux without a UTF-8 client locale rewrites a TAB in -F output
// to "_" (alpha.340), which would leave every row one unparseable field. None
// of the three values can contain a space, so a space is unambiguous.
const listAllPanesFormat = "#{pane_id} #{session_id} #{pane_pid}"

func (r *RealExecutor) ListAllPanes(ctx context.Context) ([]PaneLocation, error) {
	out, err := boundedRead(ctx, "list-panes", "-a", "-F", listAllPanesFormat).Output()
	if err != nil {
		if cerr := readCtxErr(ctx, "tmux list-panes -a", err); cerr != nil {
			return nil, cerr
		}
		return nil, fmt.Errorf("tmux list-panes -a: %w", err)
	}
	panes, err := parsePaneLocations(out)
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes -a: %w", err)
	}
	return panes, nil
}

func (r *RealExecutor) ListPanePlacements(ctx context.Context) (map[string]PanePlacement, error) {
	out, err := boundedRead(ctx, "list-panes", "-a", "-F", listPanePlacementsFormat).Output()
	if err != nil {
		if cerr := readCtxErr(ctx, "tmux list-panes -a", err); cerr != nil {
			return nil, cerr
		}
		return nil, fmt.Errorf("tmux list-panes -a: %w", err)
	}
	rows, err := parsePaneRows(out)
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes -a: %w", err)
	}
	return buildPanePlacements(rows), nil
}

var panePIDRe = regexp.MustCompile(`^[0-9]+$`)

// maxQuotedRow bounds how much of a bad row an error repeats: enough to
// recognise it, never a screenful of garbage in a log line.
const maxQuotedRow = 64

// parsePaneLocations parses list-panes output in listAllPanesFormat. Blank
// lines are skipped; every other line must be exactly "%N $N N", or the whole
// listing fails (spec D5). Skipping the line instead would return a listing
// without that pane, which a caller reads as "the pane is gone" and drops its
// owner, where the truth is that the listing cannot be trusted.
func parsePaneLocations(out []byte) ([]PaneLocation, error) {
	var panes []PaneLocation
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 3 || !tmuxPaneIDRe.MatchString(fields[0]) ||
			!tmuxSessionIDRe.MatchString(fields[1]) || !panePIDRe.MatchString(fields[2]) {
			return nil, fmt.Errorf(`malformed row %s, want "%%N $N N"`, quoteRow(line))
		}
		panes = append(panes, PaneLocation{PaneID: fields[0], SessionID: fields[1], PanePID: fields[2]})
	}
	return panes, nil
}

func quoteRow(line string) string {
	if len(line) <= maxQuotedRow {
		return strconv.Quote(line)
	}
	return strconv.Quote(line[:maxQuotedRow]) + "..."
}

func (r *RealExecutor) PanePID(target string) (string, error) {
	out, err := tmuxCmd("list-panes", "-t", target, "-F", "#{pane_pid}").Output()
	if err != nil {
		return "", fmt.Errorf("tmux list-panes pid: %w", err)
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	return strings.TrimSpace(line), nil
}

// ActivePanePID returns the PID of the currently active pane of the target
// session/window, unlike PanePID which returns the first listed pane. Used
// when a value must come from the pane the user is looking at (e.g. shell
// HOME for tilde-path expansion).
func (r *RealExecutor) ActivePanePID(target string) (string, error) {
	out, err := tmuxCmd("display-message", "-p", "-t", target, "#{pane_pid}").Output()
	if err != nil {
		return "", fmt.Errorf("tmux display-message pane_pid: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *RealExecutor) PaneChildCommands(target string) ([]string, error) {
	return r.paneProcessCommands(target, false)
}

func (r *RealExecutor) PaneDescendantCommands(target string) ([]string, error) {
	return r.paneProcessCommands(target, true)
}

func (r *RealExecutor) paneProcessCommands(target string, recursive bool) ([]string, error) {
	panePID, err := r.PanePID(target)
	if err != nil {
		return nil, err
	}
	// Build a process tree from ps output, then walk the pane shell's descendants.
	out, err := psCmd("-ax", "-o", "pid=,ppid=,comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	childrenByPPID := make(map[string][]processEntry)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		entry := processEntry{
			pid:     fields[0],
			ppid:    fields[1],
			command: fields[2],
		}
		childrenByPPID[entry.ppid] = append(childrenByPPID[entry.ppid], entry)
	}

	queue := append([]string(nil), panePID)
	var cmds []string
	for len(queue) > 0 {
		parentPID := queue[0]
		queue = queue[1:]
		for _, child := range childrenByPPID[parentPID] {
			cmds = append(cmds, child.command)
			if recursive {
				queue = append(queue, child.pid)
			}
		}
		if !recursive {
			break
		}
	}

	return cmds, nil
}

func (r *RealExecutor) CapturePaneContent(target string, lastN int) (string, error) {
	arg := fmt.Sprintf("-%d", lastN)
	out, err := tmuxCmd("capture-pane", "-e", "-t", target, "-p", "-S", arg).Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w", err)
	}
	return string(out), nil
}

func (r *RealExecutor) CapturePaneRange(target string, start, endInclusive int) (string, error) {
	startArg := fmt.Sprintf("%d", start)
	endArg := fmt.Sprintf("%d", endInclusive)
	// -e preserves ANSI escape sequences. Without it, tmux normalizes the
	// pane to plain text and ANSI-only changes (spinner color, status
	// highlights) hash to the same content as the previous tick — so a
	// TopLines watcher would miss "running" signals on agents that animate
	// purely via color (cc's spinner is one). Mirrors CapturePaneContent.
	out, err := tmuxCmd("capture-pane", "-e", "-p", "-t", target, "-S", startArg, "-E", endArg).Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane range: %w", err)
	}
	return string(out), nil
}

func (r *RealExecutor) CapturePaneTopLines(target string, n int) (string, error) {
	if n <= 0 {
		return "", nil
	}
	return r.CapturePaneRange(target, 0, n-1)
}

func (r *RealExecutor) PaneSize(target string) (cols, rows int, err error) {
	out, err := tmuxCmd("list-panes", "-t", target, "-F", "#{pane_width} #{pane_height}").Output()
	if err != nil {
		return 0, 0, fmt.Errorf("tmux list-panes size: %w", err)
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	var c, r2 int
	if _, err := fmt.Sscanf(line, "%d %d", &c, &r2); err != nil {
		return 0, 0, fmt.Errorf("parse pane size: %w", err)
	}
	return c, r2, nil
}

func (r *RealExecutor) ResizeWindow(target string, cols, rows int) error {
	return tmuxCmd("resize-window", "-t", target,
		"-x", fmt.Sprintf("%d", cols), "-y", fmt.Sprintf("%d", rows)).Run()
}

func (r *RealExecutor) ResizeWindowAuto(target string) error {
	return tmuxCmd("resize-window", "-A", "-t", target).Run()
}

func (r *RealExecutor) WindowSize(ctx context.Context, target string) (uint16, uint16, error) {
	out, err := boundedRead(ctx, "display-message", "-p", "-t", target,
		"#{window_width} #{window_height}").Output()
	if err != nil {
		if cerr := readCtxErr(ctx, "display-message window size", err); cerr != nil {
			return 0, 0, cerr
		}
		return 0, 0, fmt.Errorf("tmux display-message window size: %w", err)
	}
	return parseWindowSize(string(out))
}

func (r *RealExecutor) StatusRows(ctx context.Context, target string) (uint16, error) {
	out, err := boundedRead(ctx, "display-message", "-p", "-t", target, "#{status}").Output()
	if err != nil {
		if cerr := readCtxErr(ctx, "display-message status rows", err); cerr != nil {
			return 0, cerr
		}
		return 0, fmt.Errorf("tmux display-message status rows: %w", err)
	}
	return parseStatusRows(string(out))
}

// parseStatusRows parses tmux's `status` option: off, on (one row) or 2..5.
func parseStatusRows(s string) (uint16, error) {
	switch v := strings.TrimSpace(s); v {
	case "off":
		return 0, nil
	case "on":
		return 1, nil
	default:
		n, err := strconv.ParseUint(v, 10, 8)
		if err != nil || n < 2 || n > 5 {
			return 0, fmt.Errorf("parse status rows: %q", s)
		}
		return uint16(n), nil
	}
}

// parseWindowSize parses "<cols> <rows>"; both must be in 1..65535.
func parseWindowSize(s string) (uint16, uint16, error) {
	f := strings.Fields(s)
	if len(f) != 2 {
		return 0, 0, fmt.Errorf("parse window size: %q", s)
	}
	c, err1 := strconv.ParseUint(f[0], 10, 16)
	r, err2 := strconv.ParseUint(f[1], 10, 16)
	if err1 != nil || err2 != nil || c == 0 || r == 0 {
		return 0, 0, fmt.Errorf("parse window size: %q", s)
	}
	return uint16(c), uint16(r), nil
}

func (r *RealExecutor) SetWindowOption(target, option, value string) error {
	return tmuxCmd("set-window-option", "-t", target, option, value).Run()
}

func (r *RealExecutor) SetWindowOptionGlobal(option, value string) error {
	return tmuxCmd("set-window-option", "-g", option, value).Run()
}

func (r *RealExecutor) ShowWindowOption(option string) (string, error) {
	out, err := tmuxCmd("show-options", "-w", "-g", "-q", "-v", option).Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if IsNoServer(stderr) {
				return "", nil
			}
		}
		return "", fmt.Errorf("tmux show-options -w %s: %w", option, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *RealExecutor) ShowGlobalOption(ctx context.Context, option string) (string, error) {
	out, err := tmuxCmdContext(ctx, "show-options", "-g", "-q", "-v", option).Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if IsNoServer(stderr) {
				return "", nil
			}
		}
		return "", fmt.Errorf("tmux show-options -g %s: %w", option, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *RealExecutor) SetHookGlobal(event, command string) error {
	return tmuxCmd("set-hook", "-g", event, command).Run()
}

func (r *RealExecutor) RemoveHookGlobal(event string) error {
	return tmuxCmd("set-hook", "-gu", event).Run()
}

func (r *RealExecutor) ShowHooksGlobal() (string, error) {
	out, err := tmuxCmd("show-hooks", "-g").Output()
	if err != nil {
		// "no hooks" is a normal condition — return empty string
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if strings.Contains(stderr, "no hooks") || IsNoServer(stderr) {
				return "", nil
			}
		}
		return "", fmt.Errorf("tmux show-hooks: %w", err)
	}
	return string(out), nil
}

// ServerState is what one `tmux info` probe says about the server (#1108,
// #1474 spec D1).
type ServerState int

const (
	// ServerUp: a server answered.
	ServerUp ServerState = iota
	// ServerAbsent: no server exists (stale or missing socket). A usable
	// host — creating a session starts one — and the normal state after a
	// reboot.
	ServerAbsent
	// ServerBroken: tmux cannot be used — binary missing or not
	// executable, `tmux info` timing out, or any other failure.
	ServerBroken
)

func (s ServerState) String() string {
	switch s {
	case ServerUp:
		return "up"
	case ServerAbsent:
		return "absent"
	case ServerBroken:
		return "broken"
	}
	return fmt.Sprintf("ServerState(%d)", int(s))
}

// serverStateTimeout bounds the `tmux info` probe. A var so a test can
// shorten it.
var serverStateTimeout = 5 * time.Second

func (r *RealExecutor) ServerState() ServerState {
	ctx, cancel := context.WithTimeout(context.Background(), serverStateTimeout)
	defer cancel()
	// Output() (not Run) so a failure's stderr lands in ExitError.Stderr.
	_, err := boundedRead(ctx, "info").Output()
	if err == nil {
		return ServerUp
	}
	// A probe killed at the deadline is a hung server, never an absent
	// one, whatever it printed first.
	if ctx.Err() != nil {
		return ServerBroken
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && IsNoServer(string(exitErr.Stderr)) {
		return ServerAbsent
	}
	return ServerBroken
}

// TmuxAlive reports whether a server answers. "No server" is false here even
// though it is a usable host: the watcher's internal up/down state machine
// (wait-for gate, hooks) needs a running server (spec D1).
func (r *RealExecutor) TmuxAlive() bool {
	return r.ServerState() == ServerUp
}

type processEntry struct {
	pid     string
	ppid    string
	command string
}
