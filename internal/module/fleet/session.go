// The fleetmod package, measuring side: the daemon module that measures the fleet (spec rev 2 §5–§7,
// docs/specs/2026-10-14-fleet-status-spec.md on branch docs/workbook-journal-spec): one long-lived SSH session per remote
// host, the aggregator that holds the newest reading of every host, GET /api/fleet and the 1 s fleet.changed push.
//
// This file is the session: one `ssh -T … -- <alias> <loop>` per host, in its own process group, its stdout cut into frames
// and handed on through a newest-wins slot. Nothing it reads is written to a database or to the log; only the verdict (a
// status and a code) leaves it, stderr goes to the daemon log at most once an hour per host and code.
package fleetmod

import (
	"context"
	"github.com/wake/purdex/internal/execstat"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	leaf "github.com/wake/purdex/internal/fleet"
)

// Timings of a session (spec §5). Fields of sessionCfg override them in tests.
//
// offlineAfter and maxFrameAge are two separate things: a host with no frame for 5 s shows offline (a remote `pdx gate
// status` may stall the loop for up to ~3 s, which this tolerates), while maxFrameAge only drops a stale frame when it is
// taken and never counts toward the offline verdict.
const (
	offlineAfter  = 5 * time.Second  // no frame for this long: offline/timeout, the session is kept
	killAfter     = 30 * time.Second // no frame for this long: the session is killed and restarted
	maxFrameAge   = 2 * time.Second  // a frame older than this when taken is dropped
	backoffFirst  = 5 * time.Second  // wait before a restart; doubles up to backoffMax; a good frame resets it
	backoffMax    = 60 * time.Second
	termGrace     = 2 * time.Second // TERM to the group, KILL after this
	badFrameLimit = 5               // unparsable fast frames in a row: error/parse and a restart
	maxStderr     = 4 << 10
	logEvery      = time.Hour // stderr reaches the log at most once per host and code per this
)

// clock is the time the session lives by; tests drive it.
type timeSource interface {
	Now() time.Time
	NewTimer(d time.Duration) timer
}

type timer interface {
	C() <-chan time.Time
	Stop() bool
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) NewTimer(d time.Duration) timer {
	return realTimer{time.NewTimer(d)}
}

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time { return r.t.C }
func (r realTimer) Stop() bool          { return r.t.Stop() }

// sink receives what a session learns. Both calls come from the session's goroutine, in order.
type sink interface {
	// Reading is one good frame; slow is nil unless the frame carried the slow part.
	Reading(fast leaf.Fast, slow *leaf.Slow)
	// State is a change of the host's status. Its code is the only failure detail there is: stderr is never passed on.
	// A session that restarts reports a non-ok State first, so the receiver resets its zone latch and page window.
	State(st leaf.Status, code leaf.Code, at time.Time)
}

// sessionCfg is everything a session needs; the zero values of the tunables mean the constants above.
type sessionCfg struct {
	LookPath func(file string) (string, error) // finds ssh; nil = exec.LookPath
	Home     string                            // for the ValidAlias check made before every start
	Valid    func(home, alias string) bool     // nil = leaf.ValidAlias
	Clock    timeSource                        // nil = the real one
	Sink     sink
	Logf     func(format string, args ...any) // the daemon log; nil = discard
	Limit    *hourlyLimiter                   // shared by all sessions of a Measure; nil = a private one

	Offline, Kill, MaxAge, BackoffFirst, BackoffMax, TermGrace time.Duration
	BadLimit                                                   int
}

func (c *sessionCfg) defaults() {
	if c.LookPath == nil {
		c.LookPath = exec.LookPath
	}
	if c.Valid == nil {
		c.Valid = leaf.ValidAlias
	}
	if c.Clock == nil {
		c.Clock = realClock{}
	}
	if c.Logf == nil {
		c.Logf = func(string, ...any) {}
	}
	if c.Limit == nil {
		c.Limit = &hourlyLimiter{}
	}
	for p, d := range map[*time.Duration]time.Duration{
		&c.Offline: offlineAfter, &c.Kill: killAfter, &c.MaxAge: maxFrameAge,
		&c.BackoffFirst: backoffFirst, &c.BackoffMax: backoffMax, &c.TermGrace: termGrace,
	} {
		if *p <= 0 {
			*p = d
		}
	}
	if c.BadLimit <= 0 {
		c.BadLimit = badFrameLimit
	}
}

// hourlyLimiter lets one thing per key through per logEvery.
type hourlyLimiter struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (l *hourlyLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t, ok := l.last[key]; ok && now.Sub(t) < logEvery {
		return false
	}
	if l.last == nil {
		l.last = map[string]time.Time{}
	}
	l.last[key] = now
	return true
}

// frameItem is one parsed frame waiting in the slot.
type frameItem struct {
	fast leaf.Fast
	slow *leaf.Slow
	at   time.Time
}

// slot holds the newest frame only: put never blocks and replaces what is there, so a slow consumer sees the newest frame and
// the reader (hence the remote side) is never held up.
type slot struct {
	mu   sync.Mutex
	item *frameItem
	ch   chan struct{} // cap 1: something may be waiting
}

func newSlot() *slot { return &slot{ch: make(chan struct{}, 1)} }

func (s *slot) put(it frameItem) {
	s.mu.Lock()
	s.item = &it
	s.mu.Unlock()
	select {
	case s.ch <- struct{}{}:
	default:
	}
}

// take returns the newest frame and empties the slot. A frame older than maxAge at now is dropped (ok false).
func (s *slot) take(now time.Time, maxAge time.Duration) (frameItem, bool) {
	s.mu.Lock()
	it := s.item
	s.item = nil
	s.mu.Unlock()
	if it == nil || now.Sub(it.at) > maxAge {
		return frameItem{}, false
	}
	return *it, true
}

// session is one remote host's SSH session while this daemon is primary.
type session struct {
	cfg    sessionCfg
	alias  string
	cancel context.CancelFunc
	done   chan struct{}
}

// startSession starts the goroutine that keeps alias measured until stop.
func startSession(parent context.Context, cfg sessionCfg, alias string) *session {
	cfg.defaults()
	ctx, cancel := context.WithCancel(parent)
	s := &session{cfg: cfg, alias: alias, cancel: cancel, done: make(chan struct{})}
	go s.run(ctx)
	return s
}

// stop ends the session and returns once its process group and readers are gone: cancel, TERM to the group, KILL after the
// grace, wait for the stdout reader, return. Safe to call twice.
func (s *session) stop() {
	s.cancel()
	<-s.done
}

func (s *session) run(ctx context.Context) {
	defer close(s.done)
	ck := s.cfg.Clock
	wait := s.cfg.BackoffFirst
	for ctx.Err() == nil {
		if good := s.once(ctx); good {
			wait = s.cfg.BackoffFirst
		}
		if ctx.Err() != nil {
			return
		}
		t := ck.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C():
		}
		wait = min(wait*2, s.cfg.BackoffMax)
	}
}

// once runs one SSH process until it ends or is stopped; it reports whether a good frame came through. Every run has its
// own page-rate window (its counters are not comparable with the last process's).
func (s *session) once(ctx context.Context) (sawGood bool) {
	cfg, ck := &s.cfg, s.cfg.Clock
	var lastSt leaf.Status
	var lastCode leaf.Code
	emit := func(st leaf.Status, code leaf.Code) {
		if st == lastSt && code == lastCode {
			return
		}
		lastSt, lastCode = st, code
		cfg.Sink.State(st, code, ck.Now())
	}

	if !cfg.Valid(cfg.Home, s.alias) {
		emit(leaf.StatusError, leaf.CodeAliasGone)
		return false
	}
	args, err := leaf.LoopArgs(s.alias)
	if err != nil {
		emit(leaf.StatusError, leaf.CodeAliasGone)
		return false
	}
	ssh, err := cfg.LookPath("ssh")
	if err != nil {
		emit(leaf.StatusOffline, leaf.CodeUnreachable)
		return false
	}

	outR, outW, err := os.Pipe()
	if err != nil {
		emit(leaf.StatusOffline, leaf.CodeUnreachable)
		return false
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		emit(leaf.StatusOffline, leaf.CodeUnreachable)
		return false
	}
	cmd := execstat.Command("fleet.ssh", ssh, args...) // counted at Start; the group is reaped through cmd.Process.Wait below, so no wall time is added
	cmd.Stdin = nil                                    // /dev/null
	cmd.Stdout, cmd.Stderr = outW, errW
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		outR.Close()
		outW.Close()
		errR.Close()
		errW.Close()
		emit(leaf.StatusOffline, leaf.CodeUnreachable)
		return false
	}
	// Only the child (and what it spawns) holds the write ends now: EOF on a read end means no process of the group is left.
	outW.Close()
	errW.Close()
	pid := cmd.Process.Pid

	exited := make(chan *os.ProcessState, 1)
	go func() {
		st, _ := cmd.Process.Wait()
		exited <- st
	}()
	sl := newSlot()
	failed := make(chan struct{}, 1)
	outDone := make(chan struct{})
	go func() {
		defer close(outDone)
		s.readFrames(outR, sl, failed)
	}()
	var errBuf cappedBuf
	errDone := make(chan struct{})
	go func() {
		defer close(errDone)
		io.Copy(&errBuf, errR)
	}()

	// cleanup kills the whole group and waits for both readers; the pipes are closed by force if a process that left the
	// group still holds them, so this always returns.
	cleanup := func() {
		killGroup(pid, outDone, cfg.TermGrace, func() { outR.Close() })
		select {
		case <-errDone:
		case <-time.After(cfg.TermGrace):
		}
		outR.Close()
		errR.Close()
		<-outDone
		<-errDone
	}

	lastFrame := ck.Now()
	offline := false
	var tm timer
	arm := func() {
		if tm != nil {
			tm.Stop()
		}
		d := cfg.Offline
		if offline {
			d = cfg.Kill
		}
		tm = ck.NewTimer(max(lastFrame.Add(d).Sub(ck.Now()), 0))
	}
	arm()
	defer func() { tm.Stop() }()

	consume := func() {
		it, ok := sl.take(ck.Now(), cfg.MaxAge)
		if !ok {
			return // a stale frame is dropped and proves nothing: lastFrame stays
		}
		lastFrame, offline, sawGood = ck.Now(), false, true
		arm()
		emit(leaf.StatusOK, leaf.CodeNone)
		cfg.Sink.Reading(it.fast, it.slow)
	}

	var ps *os.ProcessState
	for ps == nil {
		select {
		case <-ctx.Done():
			cleanup()
			return sawGood
		case <-sl.ch:
			consume()
		case <-failed:
			cleanup()
			emit(leaf.StatusError, leaf.CodeParse)
			return sawGood
		case <-tm.C():
			silence := ck.Now().Sub(lastFrame)
			switch {
			case silence >= cfg.Kill:
				cleanup()
				emit(leaf.StatusOffline, leaf.CodeTimeout)
				return sawGood
			case silence >= cfg.Offline && !offline:
				offline = true
				arm()
				emit(leaf.StatusOffline, leaf.CodeTimeout)
			default:
				arm()
			}
		case <-outDone:
			// stdout closed: give the process a moment to exit by itself, so its status can be read.
			select {
			case ps = <-exited:
			case <-time.After(cfg.TermGrace):
				cleanup()
				emit(leaf.StatusOffline, leaf.CodeUnreachable)
				return sawGood
			case <-ctx.Done():
				cleanup()
				return sawGood
			}
		case ps = <-exited:
		}
	}

	// The process ended by itself: sweep what it left, read the last frame and stderr, then say why.
	cleanup()
	consume()
	stderr := errBuf.String()
	var st leaf.Status
	var code leaf.Code
	switch {
	case ctx.Err() != nil:
		return sawGood
	case ps != nil && ps.ExitCode() == 255:
		st, code = leaf.Classify255(stderr)
	default:
		st, code = leaf.StatusError, leaf.CodeRemoteExit
	}
	emit(st, code)
	if stderr != "" && cfg.Limit.allow(s.alias+"/"+string(code), ck.Now()) {
		cfg.Logf("fleet: %s: ssh session ended (%s/%s): %s", s.alias, st, code, oneLine(stderr))
	}
	return sawGood
}

// readFrames cuts r into frames and parses each exactly once as it arrives (ParseFast moves the page window, so a second
// parse of the same instant would reset it); the receive time stamps it. A burst of bad frames is counted here, in order,
// so the newest-wins slot cannot hide it. Frame-level faults (too big, no @@end) and parse errors all count. Only the newest
// good frame is kept in sl.
func (s *session) readFrames(r io.Reader, sl *slot, failed chan<- struct{}) {
	fr := leaf.NewFrameReader(r)
	var win leaf.PageWindow
	bad := 0
	noteBad := func() {
		bad++
		if bad == s.cfg.BadLimit {
			select {
			case failed <- struct{}{}:
			default:
			}
		}
	}
	for {
		text, err := fr.Next()
		if err != nil {
			if leaf.IsBadFrame(err) {
				noteBad()
				continue
			}
			return
		}
		at := s.cfg.Clock.Now()
		fast, perr := leaf.ParseFast(text, at, &win)
		if perr != nil {
			noteBad()
			continue
		}
		bad = 0
		it := frameItem{fast: fast, at: at}
		if slow, ok := leaf.ParseSlow(text, at); ok {
			it.slow = &slow
		}
		sl.put(it)
	}
}

// killGroup ends the process group led by pid: TERM, KILL after grace, then wait for done (the stdout reader) for another
// grace and force it with closeRead. Signalling the group (not only ssh) is what frees the pipe when a ProxyCommand child
// holds it.
func killGroup(pid int, done <-chan struct{}, grace time.Duration, closeRead func()) {
	// Always signal the group, even when stdout is closed already: a grandchild may hold only stderr. ESRCH (nobody left) is
	// harmless.
	// A pid reuse by another group within the grace is practically impossible (the leader is not reaped before cleanup ends).
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	// The stdout reader finishing says nothing about the group (a holder of stderr only, ignoring TERM): wait out the grace
	// for the group to vanish, then probe it and send KILL if anything is left.
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) && syscall.Kill(-pid, 0) == nil {
		time.Sleep(10 * time.Millisecond)
	}
	if syscall.Kill(-pid, 0) != nil { // ESRCH: nobody left
		select {
		case <-done:
		case <-time.After(grace):
			closeRead()
		}
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	select {
	case <-done:
	case <-time.After(grace):
		closeRead()
	}
}

// cappedBuf keeps the first maxStderr bytes and swallows the rest, so a chatty ssh never blocks on a full pipe.
type cappedBuf struct {
	mu  sync.Mutex
	buf []byte
}

func (c *cappedBuf) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := maxStderr - len(c.buf); room > 0 {
		c.buf = append(c.buf, p[:min(len(p), room)]...)
	}
	return len(p), nil
}

func (c *cappedBuf) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.buf)
}

// oneLine is stderr as one printable log line of at most 200 bytes.
func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, strings.TrimSpace(s))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
