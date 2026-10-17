package watchmod

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/wake/purdex/internal/watch"
	"github.com/wake/purdex/internal/watchsettings"
)

// Starting and waking the watcher on this host (host-load-control spec §12.3–§12.4, plan W3b steps 1–2). Both type the agent's command
// into the pdx-watch pane, only when the pane is at a shell, and are done when a verified watcher registers. One at a time: a start
// or wake that is in progress is the only one, also across a restart of the daemon (session.json). A caller waits at most StartGrace
// for the end and is told "in progress" after that, because a call from another host is cut off long before the three minutes a
// start may take; the job goes on in the background, and its failure is a cooldown (StartCooldown) for the next one.

// paneTyper is the part of watch.PaneControl a start or wake uses (a test replaces it: nothing there touches tmux).
type paneTyper interface {
	EnsureSession(ctx context.Context, workdir string) (sessionID, paneID string, created bool, err error)
	PaneAtShell(ctx context.Context) (watch.PaneState, error)
	TypeLine(ctx context.Context, line string) error
}

// errStartFailed: a start or wake was typed and no verified watcher showed up in time.
var errStartFailed = errors.New("the watcher did not register in time")

// fileExists says whether path is there (the kept transcript of a wake).
func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// job is the start or wake that is running; its result is read after done is closed.
type job struct {
	done chan struct{}
	ans  watch.HolderAnswer
	err  error
}

// sessionCtl is the module's SessionControl.
type sessionCtl struct {
	m *Module
	// gate serializes the decision of a start or wake (it is held across Reconcile); a caller waiting for it leaves when its ctx ends.
	gate     chan struct{}
	gateOnce sync.Once
	mu       sync.Mutex // guards cur
	cur      *job
}

func (c *sessionCtl) acquire(ctx context.Context) error {
	c.gateOnce.Do(func() { c.gate = make(chan struct{}, 1) })
	select {
	case c.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *sessionCtl) release() { <-c.gate }

func (c *sessionCtl) current() *job {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur
}

func (m *Module) startGraceFor() time.Duration {
	if m.startGrace > 0 {
		return m.startGrace
	}
	return watch.StartGrace
}

func (m *Module) registerWithinFor() time.Duration {
	if m.registerWithin > 0 {
		return m.registerWithin
	}
	return watch.RegisterWithin
}

func (m *Module) pollEveryFor() time.Duration {
	if m.pollEvery > 0 {
		return m.pollEvery
	}
	return 10 * time.Second
}

func (m *Module) transcriptExists(path string) bool {
	if m.statFile != nil {
		return m.statFile(path)
	}
	return fileExists(path)
}

// answerStarting is this host's holder as it is, marked as a start or wake in progress.
func (m *Module) answerStarting() watch.HolderAnswer {
	a := answerOf(m.view())
	a.Starting = true
	return a
}

func (c *sessionCtl) Start(ctx context.Context) (watch.HolderAnswer, error) {
	return c.run(ctx, "start", "")
}

func (c *sessionCtl) Wake(ctx context.Context, prompt string) (watch.HolderAnswer, error) {
	return c.run(ctx, "wake", prompt)
}

// run is a start or a wake: refusals first, then the job in the background, then the wait for the grace.
func (c *sessionCtl) run(ctx context.Context, kind, extra string) (watch.HolderAnswer, error) {
	m := c.m
	if m.typer == nil {
		return watch.HolderAnswer{}, watch.ErrNoSession
	}
	s, err := m.readSettings()
	if err != nil {
		return watch.HolderAnswer{}, err
	}
	if kind == "wake" {
		if _, err := watch.JoinWakePrompt(wakePromptOf(s), extra); err != nil {
			return watch.HolderAnswer{}, err
		}
	}
	if j := c.current(); j != nil { // one at a time: this is the one that is running
		return c.await(ctx, j)
	}
	if err := c.acquire(ctx); err != nil {
		return watch.HolderAnswer{}, err
	}
	if j := c.current(); j != nil { // another call made it while this one waited for the gate
		c.release()
		return c.await(ctx, j)
	}
	now := m.now()
	st := m.sess.Get()
	if st.InProgress != nil {
		// a mark with no job behind it is from a run of the daemon that ended: if the watcher registered meanwhile, that start worked
		if h, _, err := m.Reconcile(ctx); err == nil && h.State == watch.HolderAwake {
			_ = m.updateSession(func(x *watch.SessionState) { x.InProgress = nil })
			c.release()
			return answerOf(m.view()), nil
		}
	}
	if st.InProgress != nil {
		if m.youngMark(st.InProgress.Since) { // a start of an earlier run of the daemon, still within its time
			c.release()
			return m.answerStarting(), nil
		}
		// it never ended: that start failed
		if err := m.failedStart(now); err != nil {
			m.logf("[watch] session.json: %v", err)
		}
		st = m.sess.Get()
	}
	if until := st.CooldownUntil; now.Before(until) {
		c.release()
		return watch.HolderAnswer{}, &watch.CooldownError{Until: until}
	}
	h, _, err := m.Reconcile(ctx)
	if err != nil {
		c.release()
		return watch.HolderAnswer{}, err
	}
	if h.State == watch.HolderAwake { // a watcher is there: nothing to type
		if st.InProgress != nil {
			_ = m.updateSession(func(x *watch.SessionState) { x.InProgress = nil })
		}
		c.release()
		return answerOf(m.view()), nil
	}
	j := &job{done: make(chan struct{})}
	c.mu.Lock()
	c.cur = j
	c.mu.Unlock()
	c.release()
	go c.work(j, kind, extra, h, s)
	return c.await(ctx, j)
}

// await waits for the job's end for at most the grace; a job that is still going is answered as in progress.
func (c *sessionCtl) await(ctx context.Context, j *job) (watch.HolderAnswer, error) {
	t := time.NewTimer(c.m.startGraceFor())
	defer t.Stop()
	select {
	case <-j.done:
		return j.ans, j.err
	case <-t.C:
		return c.m.answerStarting(), nil
	case <-ctx.Done():
		return watch.HolderAnswer{}, ctx.Err()
	}
}

// failedStart records a start or wake that did not come up: the mark is cleared and the cooldown begins.
func (m *Module) failedStart(now time.Time) error {
	return m.updateSession(func(x *watch.SessionState) {
		x.InProgress = nil
		x.CooldownUntil = now.Add(watch.StartCooldown)
	})
}

func wakePromptOf(s watchsettings.Settings) string {
	if s.WakePrompt != "" {
		return s.WakePrompt
	}
	return watch.DefaultWakePrompt
}

func startPromptOf(s watchsettings.Settings) string {
	if s.StartPrompt != "" {
		return s.StartPrompt
	}
	return watch.DefaultStartPrompt
}

// work does the job: the checks on the pane, the typing, and the wait for a verified watcher.
func (c *sessionCtl) work(j *job, kind, extra string, before watch.Holder, s watchsettings.Settings) {
	m := c.m
	defer func() {
		c.mu.Lock()
		c.cur = nil
		c.mu.Unlock()
		close(j.done)
	}()
	ctx, done, ok := m.enterFor(context.Background(), m.registerWithinFor()+time.Minute)
	if !ok {
		j.err = errors.New("the module is not running")
		return
	}
	defer done()

	// a wake of a session whose transcript is on disk resumes it; otherwise this is a start
	resume := ""
	if kind == "wake" && before.State == watch.HolderResting && before.SessionID != "" && m.transcriptExists(before.TranscriptPath) {
		resume = before.SessionID
	}
	prompt := startPromptOf(s)
	if resume != "" {
		p, err := watch.JoinWakePrompt(wakePromptOf(s), extra)
		if err != nil {
			j.err = err
			return
		}
		prompt = p
	}
	line, err := watch.CommandLine(s.AgentCommand, resume, prompt)
	if err != nil {
		j.err = err
		return
	}
	began := m.now()
	if err := m.updateSession(func(x *watch.SessionState) {
		x.InProgress = &watch.InProgress{Kind: kind, Since: began, SessionID: resume, Frame: before.FrameID}
	}); err != nil { // the mark is what keeps a restart from starting twice: no mark, no start
		j.err = fmt.Errorf("session.json: %w", err)
		return
	}
	if _, _, _, err := m.typer.EnsureSession(ctx, s.Workdir); err != nil {
		j.err = err
		m.endAttempt(began, false)
		return
	}
	st, err := m.typer.PaneAtShell(ctx)
	if err == nil && !st.AtShell {
		err = watch.ErrPaneBusy
	}
	if err == nil {
		err = m.typer.TypeLine(ctx, line)
	}
	if err != nil {
		j.err = err
		// a pane that is busy is not a failed start (nothing was typed); anything else is
		m.endAttempt(began, !errors.Is(err, watch.ErrPaneBusy))
		return
	}
	// the line is typed: wait for the verified watcher (a start: a frame other than the one before; a wake: the same session)
	deadline := time.NewTimer(m.registerWithinFor())
	defer deadline.Stop()
	tick := time.NewTicker(m.pollEveryFor())
	defer tick.Stop()
	for {
		h, _, rerr := m.Reconcile(ctx)
		if rerr == nil && h.State == watch.HolderAwake && ((resume == "" && h.FrameID != before.FrameID) || (resume != "" && h.SessionID == resume)) {
			m.endAttempt(began, false) // the mark first: the view says "starting" while it is there
			j.ans = answerOf(m.view())
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			j.err = errStartFailed
			m.endAttempt(began, true)
			return
		case <-ctx.Done():
			j.err = ctx.Err()
			m.endAttempt(began, true)
			return
		}
	}
}

// endAttempt clears the in-progress mark; failed also starts the cooldown.
func (m *Module) endAttempt(began time.Time, failed bool) {
	var err error
	if failed {
		err = m.failedStart(m.now())
	} else {
		err = m.updateSession(func(x *watch.SessionState) { x.InProgress = nil })
	}
	if err != nil {
		m.logf("[watch] session.json: %v", err)
	}
	_ = began
}

// updateSession changes session.json and tells the watch.changed subscribers when the start attempt in it (starting, cooldown) moved.
func (m *Module) updateSession(fn func(*watch.SessionState)) error {
	err := m.sess.Update(fn)
	m.notifyChanged()
	return err
}

// startingNow says a start or wake is going on: its mark is in session.json and young enough to still be waited for. A mark left by a run
// of the daemon that ended is not "starting" for ever (recoverSession ends it).
func (m *Module) startingNow(st watch.SessionState) bool {
	return st.InProgress != nil && m.youngMark(st.InProgress.Since)
}

// youngMark says a mark begun at since is still within the time a start has; one stamped ahead of the clock (it went back) is not young.
func (m *Module) youngMark(since time.Time) bool {
	age := m.now().Sub(since)
	return age >= 0 && age < m.registerWithinFor()+time.Minute
}

// recoverSession ends a mark that has no job behind it (the daemon that began the start ended): the watcher is registered → the start
// worked; the mark is past the time a start has → it failed and the cooldown begins. A mark still within its time is left (it is
// "starting" until then). It runs on the patrol's tick, so a restart needs no new start or wake to settle it.
func (m *Module) recoverSession(ctx context.Context) {
	c, ok := m.session.(*sessionCtl)
	if !ok || m.sess == nil || c.current() != nil {
		return
	}
	st := m.sess.Get()
	if st.InProgress == nil {
		return
	}
	if err := c.acquire(ctx); err != nil {
		return
	}
	defer c.release()
	st = m.sess.Get()
	if st.InProgress == nil || c.current() != nil {
		return
	}
	if h, _, err := m.Reconcile(ctx); err == nil && h.State == watch.HolderAwake {
		_ = m.updateSession(func(x *watch.SessionState) { x.InProgress = nil })
		return
	}
	if !m.startingNow(st) {
		if err := m.failedStart(m.now()); err != nil {
			m.logf("[watch] session.json: %v", err)
		}
	}
}
