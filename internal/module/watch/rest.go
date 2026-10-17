package watchmod

import (
	"bytes"
	"context"
	"os"
	"sync"
	"syscall"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/watch"
	"github.com/wake/purdex/internal/watchsettings"
)

// Rest (host-load-control spec §12.4, plan W3b step 3): in mode patrol a watcher that has been idle for patrol_idle_min is told it is
// time to rest; on its answer, or after restAnswerWait, /exit goes to the same frame; if that frame is still there restExitWait later,
// its process gets SIGTERM (after its start time is checked again), and SIGKILL restKillWait after that (same check). The holder turns
// "resting" by reconcile, which this loop runs every restBusyPoll while a step waits. Mode resident never rests.
//
// Idle is read from what the daemon can see: the time the frame registered, the last event delivered to it, and the last write to its
// transcript (a turn writes it).

const (
	restPoll       = time.Minute
	restBusyPoll   = 10 * time.Second
	restAnswerWait = 5 * time.Minute
	restQuiet      = 90 * time.Second // the answer is over when the transcript has been still this long (the notice itself also writes it)
	restExitWait   = 5 * time.Minute
	maxAnswerRead  = 4 << 20 // bytes of the transcript read after the notice
	restKillWait   = 10 * time.Second
)

// restMarker is the start of the notice, which tells its record in the transcript from the answer.
const restMarker = "[負載守門] 你已閒置"

// restText is the notice; fixed text.
const restText = "[負載守門] 你已閒置一段時間，該休息了。請把筆記寫好並推送（purdex-watch 分支），回報一聲，然後結束這個 session（/exit）。如果有新事件進來，我會取消休息。"

type restPhase int

const (
	restIdle restPhase = iota
	restAsked
	restExited
	restTermed
)

// restState is where the rest of the current frame is.
type restState struct {
	mu    sync.Mutex
	frame string
	phase restPhase
	at    time.Time // when the phase was entered
	// asked is when the notice went out, off the transcript's size then (what comes after it, past the notice's own line, is the
	// answer), and gen the count of events delivered to the watcher then: a later count means an event came in.
	asked time.Time
	off   int64
	gen   int64
}

// exiter sends /exit to the watcher's pane (watch.PaneControl.SendExit).
type exiter interface {
	SendExit(ctx context.Context) error
}

func (m *Module) transcriptMtime(path string) time.Time {
	if path == "" {
		return time.Time{}
	}
	if m.mtimeOf != nil {
		return m.mtimeOf(path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

func (m *Module) procStartOf(pid int) (string, error) {
	if m.procStart != nil {
		return m.procStart(pid)
	}
	return agentpkg.ProcessStartText(pid)
}

func (m *Module) signalPID(pid int, sig syscall.Signal) error {
	if m.signalFn != nil {
		return m.signalFn(pid, sig)
	}
	return syscall.Kill(pid, sig)
}

func latest(ts ...time.Time) time.Time {
	var out time.Time
	for _, t := range ts {
		if t.After(out) {
			out = t
		}
	}
	return out
}

func (m *Module) transcriptSize(path string) int64 {
	if path == "" {
		return 0
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// answeredAfterNotice says whether the transcript holds, after the line of the notice itself (found by its fixed text), a line of the
// assistant: the watcher's answer. The notice's own record does not count, however late it is written.
func (m *Module) answeredAfterNotice(path string, off int64) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, maxAnswerRead)
	n, _ := f.ReadAt(buf, off)
	data := buf[:n]
	i := bytes.Index(data, []byte(restMarker))
	if i < 0 {
		return false
	}
	j := bytes.IndexByte(data[i:], '\n')
	if j < 0 {
		return false
	}
	return bytes.Contains(data[i+j:], []byte(`"type":"assistant"`))
}

// restNext is how long the loop waits before the next step.
func (m *Module) restNext() time.Duration {
	m.rst.mu.Lock()
	defer m.rst.mu.Unlock()
	if m.rst.phase != restIdle {
		return restBusyPoll
	}
	return restPoll
}

// restStep is one look at the watcher: it moves the rest one step when the time has come. A failure is logged and tried again at the
// next step.
func (m *Module) restStep(ctx context.Context) {
	s, err := m.readSettings()
	if err != nil || s.Mode != watchsettings.ModePatrol {
		m.restReset("")
		return
	}
	h, _, err := m.Reconcile(ctx)
	if err != nil {
		m.logf("[watch] rest: reconcile: %v", err)
		return
	}
	if h.State != watch.HolderAwake {
		m.restReset("")
		return
	}
	now := m.now()
	m.rst.mu.Lock()
	defer m.rst.mu.Unlock()
	if m.rst.frame != h.FrameID { // another watcher than the one counted
		m.rst.frame, m.rst.phase, m.rst.at = h.FrameID, restIdle, time.Time{}
	}
	mtime := m.transcriptMtime(h.TranscriptPath)
	if m.rst.phase != restIdle && (h.LastDelivery.After(m.rst.asked) || m.activity.Load() != m.rst.gen) { // an event came in after the notice: there is work, no rest
		m.rst.phase, m.rst.at = restIdle, time.Time{}
		return
	}
	switch m.rst.phase {
	case restIdle:
		idleFor := time.Duration(s.PatrolIdleMin) * time.Minute
		if now.Sub(latest(h.Since, h.LastDelivery, h.LastTurn, mtime)) < idleFor {
			return
		}
		off, gen := m.transcriptSize(h.TranscriptPath), m.activity.Load()
		if err := m.notifyWatcher(ctx, h, restText); err != nil {
			m.logf("[watch] rest: the notice to %s: %v", h.SessionID, err)
			return
		}
		m.rst.phase, m.rst.at, m.rst.asked, m.rst.off, m.rst.gen = restAsked, now, now, off, gen
	case restAsked:
		answered := now.Sub(mtime) >= restQuiet && m.answeredAfterNotice(h.TranscriptPath, m.rst.off)
		if !answered && now.Sub(m.rst.at) < restAnswerWait {
			return
		}
		if m.exit == nil {
			m.logf("[watch] rest: no way to send /exit")
			return
		}
		if m.restBusy() { // checked again at the last moment
			return
		}
		if err := m.exit.SendExit(ctx); err != nil {
			m.logf("[watch] rest: /exit: %v", err)
			return
		}
		m.rst.phase, m.rst.at = restExited, now
	case restExited:
		if now.Sub(m.rst.at) < restExitWait {
			return
		}
		if m.restBusy() {
			return
		}
		switch m.signalIfSame(h, syscall.SIGTERM) {
		case sigSent:
			m.rst.phase, m.rst.at = restTermed, now
		case sigGone:
			m.rst.phase, m.rst.at = restIdle, time.Time{}
		}
	case restTermed:
		if now.Sub(m.rst.at) < restKillWait {
			return
		}
		if m.restBusy() {
			return
		}
		if m.signalIfSame(h, syscall.SIGKILL) == sigFailed {
			return // tried again at the next step
		}
		m.rst.phase, m.rst.at = restIdle, time.Time{} // gone: the next reconcile turns the holder resting; still there: counted again
	}
}

// restBusy says an event has begun to be delivered since the notice, and cancels the rest (rst.mu held).
func (m *Module) restBusy() bool {
	if m.activity.Load() == m.rst.gen {
		return false
	}
	m.rst.phase, m.rst.at = restIdle, time.Time{}
	return true
}

type sigResult int

const (
	sigSent   sigResult = iota
	sigGone             // the pid is not the watcher any more (reused, or gone): nothing to signal
	sigFailed           // the check or the signal failed: try again
)

// signalIfSame signals the watcher's process after its start time is read again and is the one the holder recorded (a pid that was
// reused is another process).
func (m *Module) signalIfSame(h watch.Holder, sig syscall.Signal) sigResult {
	if h.PID <= 1 || h.ProcessStartTime == "" {
		return sigGone
	}
	got, err := m.procStartOf(h.PID)
	if err != nil {
		m.logf("[watch] rest: start time of %d: %v", h.PID, err)
		return sigFailed
	}
	if got != h.ProcessStartTime {
		m.logf("[watch] rest: pid %d is not the watcher any more (start time %q, want %q): no %s", h.PID, got, h.ProcessStartTime, sig)
		return sigGone
	}
	if err := m.signalPID(h.PID, sig); err != nil {
		m.logf("[watch] rest: %s to %d: %v", sig, h.PID, err)
		return sigFailed
	}
	return sigSent
}

func (m *Module) restReset(frame string) {
	m.rst.mu.Lock()
	m.rst.frame, m.rst.phase, m.rst.at = frame, restIdle, time.Time{}
	m.rst.mu.Unlock()
}

// notifyWatcher sends text to this host's watcher through the notifier of the events.
func (m *Module) notifyWatcher(ctx context.Context, h watch.Holder, text string) error {
	n := m.dlv.notifier
	if n == nil {
		n = peerNotifier{m}
	}
	return n.Notify(ctx, h, text)
}

// restLoop runs restStep, quickly while a step waits and slowly otherwise.
func (m *Module) restLoop(ctx context.Context) {
	for {
		step, done, ok := m.enterFor(ctx, 30*time.Second)
		if ok {
			m.restStep(step)
			done()
		}
		wait := m.restNext()
		if m.restEvery > 0 {
			wait = m.restEvery
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
