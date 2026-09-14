// cmd/pdx/msg_selftest.go
//
// `pdx msg selftest` — the upgrade gate for Claude Code's undocumented
// peer protocol (spec §3). It starts a throwaway Claude Code session in
// tmux, spawns a REAL `pdx peer-proxy` helper through the same client the
// daemon uses, delivers a nonce message into the session's inbox socket,
// waits for the native reply to reach the helper, prints PASS/FAIL, and
// always tears everything down: the target process, its registry files,
// the helper and its files.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/ccuds"
	"github.com/wake/purdex/internal/peers/proxyhelper"
)

const (
	// selftestDefaultTimeout is the reply wait when --timeout is absent.
	selftestDefaultTimeout = 60 * time.Second
	// selftestRegisterWait bounds the wait for the throwaway session's
	// registry entry; selftestPollInterval is its poll period.
	selftestRegisterWait = 15 * time.Second
	selftestPollInterval = 250 * time.Millisecond
	// selftestCleanupTimeout bounds the whole cleanup (its own context —
	// never the possibly cancelled run context, R2-M8).
	selftestCleanupTimeout = 30 * time.Second
	// selftestHelperReadyTimeout is handed to proxyhelper.Spawn.
	selftestHelperReadyTimeout = 3 * time.Second
	// selftestHelperStopGrace is handed to Handle.Stop during cleanup.
	selftestHelperStopGrace = 2 * time.Second
	// selftestWriteTimeout bounds the probe frame's socket write.
	selftestWriteTimeout = 5 * time.Second
	// selftestExitWait is how long cleanup waits for the target to exit
	// after kill-session before escalating; selftestSignalWait is the wait
	// after each of SIGTERM and SIGKILL.
	selftestExitWait   = 5 * time.Second
	selftestSignalWait = 2 * time.Second

	selftestProbeName = "pdx-selftest-probe"
)

// selftestPeer is what the selftest needs from a spawned helper; satisfied
// by proxyhelper.Handle.
type selftestPeer interface {
	PID() int
	Sock() string
	Files() []string
	Frames() <-chan string
	Stop(time.Duration) error
}

// selftestDeps is every process/filesystem seam of runMsgSelftest, so
// tests run it against fakes and an isolated temp dir (R2-M9).
// newSelftestDeps supplies the production values.
type selftestDeps struct {
	tmux func(ctx context.Context, args ...string) ([]byte, error)

	registryDir string
	sockDir     string
	cwd         string // the probe helper's registry cwd

	readRegistry      func(dir string) ([]ipeers.Entry, error)
	spawn             func(ctx context.Context, cfg proxyhelper.Config) (selftestPeer, error)
	writeFrame        func(ctx context.Context, sock string, line []byte, timeout time.Duration) error
	pidAlive          func(pid int) bool
	procStart         func(pid int) (string, error)
	signal            func(pid int, sig os.Signal) error
	readPeerFeatures  func(dir string, pid int) ([]string, bool)
	glob              func(pattern string) ([]string, error)
	registryProcStart func(path string) string
	remove            func(path string) error
	dialRefused       func(sock string) bool
	sleep             func(ctx context.Context, d time.Duration) error
	now               func() time.Time
}

// newSelftestDeps returns the production seams. stderr receives the
// helper's forwarded stderr lines. An unresolvable home directory is an
// error (the registry lives under it) — never a cwd-relative guess.
func newSelftestDeps(stderr io.Writer) (selftestDeps, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return selftestDeps{}, fmt.Errorf("cannot resolve home directory: %w", err)
	}
	registryDir := filepath.Join(home, ".claude", "sessions")
	cwd, _ := os.Getwd()
	return selftestDeps{
		tmux: func(ctx context.Context, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, "tmux", args...).Output()
		},
		registryDir: registryDir,
		sockDir:     ccuds.DefaultSockDir,
		cwd:         cwd,
		readRegistry: func(dir string) ([]ipeers.Entry, error) {
			entries, _, err := ipeers.ReadRegistry(dir, ipeers.DefaultLiveness())
			return entries, err
		},
		spawn: func(ctx context.Context, cfg proxyhelper.Config) (selftestPeer, error) {
			exe, err := os.Executable()
			if err != nil {
				return nil, fmt.Errorf("locate pdx binary: %w", err)
			}
			h, err := proxyhelper.Spawn(ctx, proxyhelper.ExecStarter(exe, stderr), cfg, selftestHelperReadyTimeout)
			if err != nil {
				return nil, err
			}
			return h, nil
		},
		writeFrame:        ccuds.WriteFrame,
		pidAlive:          func(pid int) bool { return syscall.Kill(pid, 0) == nil },
		procStart:         ccuds.DefaultProcStart,
		signal:            func(pid int, sig os.Signal) error { return syscall.Kill(pid, sig.(syscall.Signal)) },
		readPeerFeatures:  ccuds.ReadPeerFeatures,
		glob:              filepath.Glob,
		registryProcStart: ccuds.RegistryProcStart,
		remove:            os.Remove,
		dialRefused:       proxyhelper.DialRefused,
		sleep: func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		now: time.Now,
	}, nil
}

// selftestTimeout parses --timeout: "" ⇒ selftestDefaultTimeout; anything
// else must be a positive time.ParseDuration string.
func selftestTimeout(raw string) (time.Duration, error) {
	if raw == "" {
		return selftestDefaultTimeout, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid --timeout %s", raw)
	}
	return d, nil
}

// runMsgSelftestCmd is the `pdx msg selftest` verb: it validates
// --timeout, wires the production seams and runs the body under a
// SIGINT/SIGTERM-cancellable context. --config is accepted by the
// grammar but unused — the selftest never talks to the daemon.
func runMsgSelftestCmd(inv msgInvocation, stdout, stderr io.Writer) int {
	timeout, err := selftestTimeout(inv.timeout)
	if err != nil {
		fmt.Fprintf(stderr, "pdx msg: %v\n", err)
		return 2
	}
	deps, err := newSelftestDeps(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "pdx msg: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runMsgSelftest(ctx, deps, timeout, stdout, stderr)
}

// selftestState is what cleanup needs to know about what the run created.
//
// Two processes matter: the tmux pane pid is the `sh -c` wrapper that
// pipes into claude (captured right after new-session, so cleanup can
// always reach it), and the claude process itself, whose pid is only
// known once it registers (the registry entry's PID).
type selftestState struct {
	name            string
	sessionStarted  bool   // tmux new-session was attempted ⇒ kill-session
	panePID         int    // the wrapper shell; 0 ⇒ unknown
	paneProcStart   string // proves panePID is still the same process
	targetPID       int    // the claude process; 0 ⇒ never identified
	targetProcStart string // proves targetPID is still the same process
	targetInbox     string // the registered inbox; "" ⇒ never registered
	h               selftestPeer
}

// runMsgSelftest is the selftest body. Every exit path runs the cleanup
// (deferred) under its own context; cleanup failure forces exit 1.
func runMsgSelftest(ctx context.Context, deps selftestDeps, timeout time.Duration, stdout, stderr io.Writer) (code int) {
	start := deps.now()
	suffix, err := selftestHex(3)
	if err != nil {
		fmt.Fprintf(stdout, "FAIL: %v\n", err)
		return 1
	}
	st := &selftestState{name: "pdx-selftest-" + suffix}

	// Step 7 is registered first so it runs on every exit path — including
	// a cancelled run ctx — under a context of its own (R2-M8).
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), selftestCleanupTimeout)
		defer cancel()
		if !selftestCleanup(cctx, deps, st, stdout) {
			code = 1
		}
	}()

	// Step 2: the throwaway session. `claude -p --input-format stream-json`
	// exits at once when its stdin is a tty, so it runs behind a pipe
	// inside an `sh -c` wrapper; the claude arguments are positional to
	// sh (no re-quoting — the settings JSON stays one argv element).
	// sessionStarted is set BEFORE the call: a cancel or failure that
	// lands after tmux created the session must still be followed by
	// kill-session (a session that never existed is tolerated there by
	// selftestTmuxNoSession).
	st.sessionStarted = true
	if _, err := deps.tmux(ctx, "new-session", "-d", "-s", st.name, "--",
		"sh", "-c", `sleep 2147483647 | exec claude "$@"`, "pdx-selftest",
		"-p", "--verbose",
		"--input-format", "stream-json", "--output-format", "stream-json",
		"--name", st.name,
		"--settings", `{"crossSessionInbound":"accept"}`); err != nil {
		fmt.Fprintf(stdout, "FAIL: tmux new-session: %v\n", err)
		return 1
	}

	// The pane pid is the wrapper shell — captured immediately so cleanup
	// can reach the session's process tree even if claude never registers
	// (R2-M8).
	panePID, paneProcStart, err := selftestIdentifyPane(ctx, deps, st.name)
	if err != nil {
		fmt.Fprintf(stderr, "pdx msg: %v\n", err)
		fmt.Fprintln(stdout, "FAIL: cannot identify the throwaway session's process")
		return 1
	}
	st.panePID, st.paneProcStart = panePID, paneProcStart

	// Step 3: wait for the registry entry of this tmux session; its PID is
	// the claude process, whose identity is captured the moment it is seen.
	target, status := selftestAwaitRegistration(ctx, deps, st.name)
	switch status {
	case selftestInterrupted:
		fmt.Fprintln(stdout, "FAIL: interrupted")
		return 1
	case selftestNotRegistered:
		fmt.Fprintln(stdout, "FAIL: session did not register (Claude Code ≥ 2.1.224 with peer messaging required)")
		return 1
	}
	targetProcStart, err := deps.procStart(target.PID)
	if err != nil {
		fmt.Fprintf(stderr, "pdx msg: registered pid %d: %v\n", target.PID, err)
		fmt.Fprintln(stdout, "FAIL: cannot identify the throwaway session's process")
		return 1
	}
	pid := target.PID
	st.targetPID, st.targetProcStart, st.targetInbox = pid, targetProcStart, target.Inbox

	// Step 4: a real helper, impersonating one peer with the target's own
	// feature list.
	features, ok := deps.readPeerFeatures(deps.registryDir, pid)
	if !ok {
		features = ccuds.DefaultPeerFeatures
	}
	sessionID, err := selftestUUID()
	if err != nil {
		fmt.Fprintf(stdout, "FAIL: %v\n", err)
		return 1
	}
	h, err := deps.spawn(ctx, proxyhelper.Config{
		Name:         selftestProbeName,
		RegistryDir:  deps.registryDir,
		SockDir:      deps.sockDir,
		Version:      ccuds.VerifiedCCVersion,
		Cwd:          deps.cwd,
		SessionID:    sessionID,
		PeerFeatures: features,
	})
	if err != nil {
		fmt.Fprintf(stdout, "FAIL: helper did not start: %v\n", err)
		return 1
	}
	st.h = h

	// Step 5: the probe frame, addressed for reply to the helper's socket.
	nonce, err := selftestHex(4)
	if err != nil {
		fmt.Fprintf(stdout, "FAIL: %v\n", err)
		return 1
	}
	msgID, err := selftestUUID()
	if err != nil {
		fmt.Fprintf(stdout, "FAIL: %v\n", err)
		return 1
	}
	line, err := ccuds.BuildFrame(msgID, h.Sock(), ccuds.Wrapper{
		From:     "uds:" + h.Sock(),
		FromName: selftestProbeName,
		FromMode: ipeers.ModePrompting,
		Text:     "PDX_SELFTEST " + nonce + ": reply with exactly: PONG " + nonce,
	})
	if err != nil {
		fmt.Fprintf(stdout, "FAIL: build frame: %v\n", err)
		return 1
	}
	if err := deps.writeFrame(ctx, target.Inbox, line, selftestWriteTimeout); err != nil {
		fmt.Fprintf(stdout, "FAIL: write to %s: %v\n", target.Inbox, err)
		return 1
	}

	// Step 6: the native reply must reach the helper.
	return selftestAwaitReply(ctx, deps, h, target.Inbox, nonce, timeout, st.name, start, stdout)
}

// selftestIdentifyPane returns the throwaway session's pane pid (the
// `sh -c` wrapper, not claude) and its procStart string.
func selftestIdentifyPane(ctx context.Context, deps selftestDeps, name string) (int, string, error) {
	out, err := deps.tmux(ctx, "list-panes", "-t", name, "-F", "#{pane_pid}")
	if err != nil {
		return 0, "", fmt.Errorf("tmux list-panes: %w", err)
	}
	first, _, _ := bytes.Cut(bytes.TrimSpace(out), []byte("\n"))
	pid, err := strconv.Atoi(strings.TrimSpace(string(first)))
	if err != nil || pid <= 0 {
		return 0, "", fmt.Errorf("tmux list-panes: unexpected pane pid %q", string(first))
	}
	procStart, err := deps.procStart(pid)
	if err != nil {
		return 0, "", err
	}
	return pid, procStart, nil
}

type selftestRegStatus int

const (
	selftestRegistered selftestRegStatus = iota
	selftestNotRegistered
	selftestInterrupted
)

// selftestAwaitRegistration polls the registry for ≤ selftestRegisterWait
// for the non-proxy entry whose tmux session is name. The session name
// is random per run, so it identifies the entry on its own; the entry's
// PID is the claude process (the pane pid is only its wrapper shell).
func selftestAwaitRegistration(ctx context.Context, deps selftestDeps, name string) (ipeers.Entry, selftestRegStatus) {
	deadline := deps.now().Add(selftestRegisterWait)
	for {
		entries, err := deps.readRegistry(deps.registryDir)
		if err == nil {
			for _, e := range entries {
				if !e.IsProxy && e.TmuxSessionName() == name {
					return e, selftestRegistered
				}
			}
		}
		if ctx.Err() != nil {
			return ipeers.Entry{}, selftestInterrupted
		}
		if !deps.now().Before(deadline) {
			return ipeers.Entry{}, selftestNotRegistered
		}
		if err := deps.sleep(ctx, selftestPollInterval); err != nil {
			return ipeers.Entry{}, selftestInterrupted
		}
	}
}

// selftestAwaitReply waits ≤ timeout on the helper's Frames for a "user"
// frame from the target inbox whose content carries nonce. Returns the
// exit code, having printed the PASS/FAIL line.
func selftestAwaitReply(ctx context.Context, deps selftestDeps, h selftestPeer, inbox, nonce string,
	timeout time.Duration, name string, start time.Time, stdout io.Writer) int {
	waitCtx, cancelWait := context.WithCancel(ctx)
	defer cancelWait()
	timedOut := make(chan struct{})
	go func() {
		if deps.sleep(waitCtx, timeout) == nil {
			close(timedOut)
		}
	}()

	for {
		select {
		case raw, ok := <-h.Frames():
			if !ok {
				// The run ctx also owns the helper process (ExecStarter
				// uses CommandContext): a Ctrl-C closes Frames too, and
				// must read as an interrupt, never as a helper crash.
				if ctx.Err() != nil {
					fmt.Fprintln(stdout, "FAIL: interrupted")
					return 1
				}
				fmt.Fprintln(stdout, "FAIL: helper exited")
				return 1
			}
			f, err := ccuds.ParseFrame([]byte(raw))
			if err != nil {
				fmt.Fprintf(stdout, "note: ignoring undecodable frame at the helper: %v\n", err)
				continue
			}
			from, _ := ccuds.FromSocket(f.From)
			if f.Type != "user" || from != inbox {
				continue // another peer's frame, not the target's reply
			}
			if !strings.Contains(f.Message.Content, nonce) {
				fmt.Fprintf(stdout, "note: frame from %s without the nonce; still waiting\n", name)
				continue
			}
			elapsed := deps.now().Sub(start).Round(time.Millisecond)
			fmt.Fprintf(stdout, "PASS: reply from %s via helper pid %d in %v\n", name, h.PID(), elapsed)
			return 0
		case <-timedOut:
			fmt.Fprintf(stdout, "FAIL: no reply within %s\n", selftestFormatTimeout(timeout))
			return 1
		case <-ctx.Done():
			fmt.Fprintln(stdout, "FAIL: interrupted")
			return 1
		}
	}
}

// selftestCleanup is step 7 (M12/R2-M8): always runs, under its own ctx,
// every sub-step reported on stdout. Returns false — after printing
// `cleanup incomplete: …` — when anything remains; true after
// `cleanup: ok`.
func selftestCleanup(ctx context.Context, deps selftestDeps, st *selftestState, stdout io.Writer) bool {
	var remaining []string
	problem := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		fmt.Fprintln(stdout, msg)
		remaining = append(remaining, msg)
	}

	// a. the helper and its files. Its socket still accepting after Stop
	// means the helper (or a successor holding its path) is still there:
	// a cleanup failure, not a note (R2-F) — the path is left alone.
	if st.h != nil {
		if err := st.h.Stop(selftestHelperStopGrace); err != nil {
			fmt.Fprintf(stdout, "helper pid %d stopped: %v\n", st.h.PID(), err)
		}
		for _, p := range st.h.Files() {
			switch err := deps.remove(p); {
			case err == nil:
				fmt.Fprintf(stdout, "removed helper leftover: %s\n", p)
			case errors.Is(err, fs.ErrNotExist):
			default:
				problem("helper file not removed: %s: %v", p, err)
			}
		}
		if sock := st.h.Sock(); !deps.dialRefused(sock) {
			problem("probe socket %s still listening", sock)
		} else {
			selftestRemoveSock(deps, sock, "helper", problem, stdout)
		}
	}

	// b. the tmux session, by name.
	if st.sessionStarted {
		if _, err := deps.tmux(ctx, "kill-session", "-t", st.name); err != nil && !selftestTmuxNoSession(err) {
			problem("tmux kill-session %s: %v", st.name, selftestTmuxErr(err))
		}
	}

	// c. the processes, by identity: the claude process first (when it was
	// identified), then the pane's wrapper shell.
	targetID := ipeers.ProcDifferent // no target ⇒ nothing to keep for
	if st.targetPID != 0 {
		targetID = selftestReap(ctx, deps, "target", st.targetPID, st.targetProcStart, problem, stdout)
	}
	if st.panePID != 0 {
		selftestReap(ctx, deps, "pane", st.panePID, st.paneProcStart, problem, stdout)
	}

	// d. the claude process's files — only those that carry its procStart.
	// Without a registry entry its pid is unknown and nothing on disk can
	// be proven ours. A live pid whose identity could not be read (c) is
	// left with its files: it may still be the running claude.
	if st.targetPID != 0 && targetID != ipeers.ProcUnknown {
		pid := strconv.Itoa(st.targetPID)
		var candidates []string
		for _, pattern := range []string{pid + ".json", pid + ".*.key"} {
			matches, err := deps.glob(filepath.Join(deps.registryDir, pattern))
			if err != nil {
				problem("list %s: %v", pattern, err)
				continue
			}
			candidates = append(candidates, matches...)
		}
		for _, p := range candidates {
			if deps.registryProcStart(p) != st.targetProcStart {
				fmt.Fprintf(stdout, "foreign file kept: %s\n", p)
				continue
			}
			if err := deps.remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				problem("target file not removed: %s: %v", p, err)
				continue
			}
			fmt.Fprintf(stdout, "removed target file: %s\n", p)
		}
		// The registered inbox is authoritative; the constructed path is
		// only a fallback for a session that never registered.
		sock := st.targetInbox
		if sock == "" {
			sock = filepath.Join(deps.sockDir, pid+".sock")
		}
		selftestRemoveSock(deps, sock, "target", problem, stdout)
	}

	// e. verdict.
	if len(remaining) == 0 {
		fmt.Fprintln(stdout, "cleanup: ok")
		return true
	}
	fmt.Fprintf(stdout, "cleanup incomplete: %s\n", strings.Join(remaining, "; "))
	return false
}

// selftestIdentify classifies pid against procStart: the same shared
// tri-state as the daemon's sweep (ipeers.ClassifyProc), never folded —
// unknown means the process must not be signalled and its files must not
// be touched; a dead pid is different.
func selftestIdentify(deps selftestDeps, pid int, procStart string) ipeers.ProcIdentity {
	_, id := ipeers.ClassifyProc(pid, procStart, deps.pidAlive, deps.procStart)
	return id
}

// selftestReap waits ≤ selftestExitWait for the process (pid, procStart)
// to be gone — dead, or the pid held by another process — then escalates
// SIGTERM, wait, SIGKILL, wait, re-checking identity right before each
// signal so a reused pid is never signalled. A live pid whose identity
// cannot be read is never signalled either: `<label> pid <p>: identity
// unknown, left running` is recorded as a problem and unknown returned,
// so the caller keeps its files. Still the same process at the end ⇒
// `<label> pid <p> still alive` is recorded. Returns the final identity.
func selftestReap(ctx context.Context, deps selftestDeps, label string, pid int, procStart string,
	problem func(string, ...any), stdout io.Writer) ipeers.ProcIdentity {
	ident := func() ipeers.ProcIdentity { return selftestIdentify(deps, pid, procStart) }
	unknown := func() ipeers.ProcIdentity {
		problem("%s pid %d: identity unknown, left running", label, pid)
		return ipeers.ProcUnknown
	}
	id := selftestWaitGone(ctx, deps, ident, selftestExitWait)
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		switch id {
		case ipeers.ProcDifferent:
			return id
		case ipeers.ProcUnknown:
			return unknown()
		}
		if err := deps.signal(pid, sig); err != nil {
			fmt.Fprintf(stdout, "%s pid %d: %v: %v\n", label, pid, sig, err)
		}
		id = selftestWaitGone(ctx, deps, ident, selftestSignalWait)
	}
	switch id {
	case ipeers.ProcDifferent:
		return id
	case ipeers.ProcUnknown:
		return unknown()
	}
	problem("%s pid %d still alive", label, pid)
	return ipeers.ProcSame
}

// selftestRemoveSock unlinks sock only when nobody listens on it; a live
// listener is left alone and reported.
func selftestRemoveSock(deps selftestDeps, sock, owner string, problem func(string, ...any), stdout io.Writer) {
	if !deps.dialRefused(sock) {
		fmt.Fprintf(stdout, "%s socket kept (something listens): %s\n", owner, sock)
		return
	}
	switch err := deps.remove(sock); {
	case err == nil:
		fmt.Fprintf(stdout, "removed %s socket: %s\n", owner, sock)
	case errors.Is(err, fs.ErrNotExist):
	default:
		problem("%s socket not removed: %s: %v", owner, sock, err)
	}
}

// selftestWaitGone polls ident() at selftestPollInterval for ≤ d and
// returns different as soon as the process is gone (or another process
// holds the pid); otherwise the identity observed last (same, or unknown
// for a transient read failure that did not clear before the deadline).
func selftestWaitGone(ctx context.Context, deps selftestDeps, ident func() ipeers.ProcIdentity, d time.Duration) ipeers.ProcIdentity {
	deadline := deps.now().Add(d)
	for {
		id := ident()
		if id == ipeers.ProcDifferent {
			return id
		}
		if !deps.now().Before(deadline) {
			return id
		}
		if deps.sleep(ctx, selftestPollInterval) != nil {
			return ident()
		}
	}
}

// selftestTmuxNoSession reports whether a kill-session error means the
// session (or the whole server) was already gone — success for cleanup.
func selftestTmuxNoSession(err error) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	msg := string(ee.Stderr)
	return strings.Contains(msg, "can't find session") ||
		strings.Contains(msg, "no server running") ||
		strings.Contains(msg, "no current session") ||
		strings.Contains(msg, "session not found")
}

// selftestTmuxErr renders a tmux failure with its stderr when available.
func selftestTmuxErr(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if msg := strings.TrimSpace(string(ee.Stderr)); msg != "" {
			return msg
		}
	}
	return err.Error()
}

// selftestFormatTimeout renders a whole-second timeout as "<n>s" (the
// default reads "60s", not "1m0s"); a sub-second remainder falls back to
// time.Duration's own format.
func selftestFormatTimeout(d time.Duration) string {
	if d%time.Second == 0 {
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	}
	return d.String()
}

// selftestHex returns n random bytes as 2n hex characters.
func selftestHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// selftestUUID returns a v4 UUID in canonical 8-4-4-4-12 form.
func selftestUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}
