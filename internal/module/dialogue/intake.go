package dialogue

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convturns"
	agentmod "github.com/wake/purdex/internal/module/agent"
)

// Limits of intake (plan §1-7, §1-8; the numbers are the workbook engine's).
const (
	maxQueued     = 16 // events waiting per session
	busyRetries   = 3  // transcript cache busy: read again this many times
	settleRetries = 15 // a Stop ahead of its transcript: read again this many times, then write what the file shows
	readTimeout   = 10 * time.Second
)

var defaultTiming = timing{busyDelay: 2 * time.Second, settleDelay: 100 * time.Millisecond, idle: 30 * time.Second}

// turnsFromReader is the part of convturns.Reader intake needs.
type turnsFromReader interface {
	TurnsFrom(ctx context.Context, provider, sessionID, fromTurnID string) (convturns.TurnsFromResult, error)
}

// timing holds the waits intake does; tests shorten them.
type timing struct{ busyDelay, settleDelay, idle time.Duration }

type intakeDeps struct {
	turns  turnsFromReader
	writer *Writer
	store  *Store
	logf   func(format string, args ...any) // nil: silent
	// onWritten runs after a successful Write (even one that had nothing new to write). hookText is the Stop hook's own
	// text: PR 2's turn summary falls back on it when the transcript has not caught up. Runs before onSettled.
	onWritten func(ev agentmod.TurnEndEvent, res convturns.TurnsFromResult, wr WriteResult, targetID, hookText string)
	// onSettled runs once for every event, however it ended (written, nothing to write, an error, dropped, abandoned
	// by Stop). PR 2 tells the push hold "this session was looked at" there.
	onSettled func(sid string, at int64)
	timing    timing
}

// intake turns the agent module's turn-end events into dialogue writes. One worker goroutine per session handles that
// session's events one at a time in arrival order (never merged: PR 2 pairs each Stop with its own (At, Seq)); a worker
// that has been idle for timing.idle ends.
type intake struct {
	d intakeDeps

	mu      sync.Mutex
	stopped bool
	queues  map[string]*sessionQueue
	wg      sync.WaitGroup
	stopCh  chan struct{}
}

type sessionQueue struct {
	events []agentmod.TurnEndEvent
	wake   chan struct{} // capacity 1
}

func newIntake(d intakeDeps) *intake {
	return &intake{d: d, queues: map[string]*sessionQueue{}, stopCh: make(chan struct{})}
}

func (in *intake) log(format string, args ...any) {
	if in.d.logf != nil {
		in.d.logf(format, args...)
	}
}

func (in *intake) settled(sid string, at int64) {
	if in.d.onSettled != nil {
		in.d.onSettled(sid, at)
	}
}

// workers is the number of live session workers.
func (in *intake) workers() int {
	in.mu.Lock()
	defer in.mu.Unlock()
	return len(in.queues)
}

// OnTurnEnd is the subscriber: it only queues the event and makes sure a worker exists. It never waits.
func (in *intake) OnTurnEnd(ev agentmod.TurnEndEvent) {
	if ev.SessionID == "" {
		return
	}
	in.mu.Lock()
	if in.stopped {
		in.mu.Unlock()
		return
	}
	q := in.queues[ev.SessionID]
	start := q == nil
	if start {
		q = &sessionQueue{wake: make(chan struct{}, 1)}
		in.queues[ev.SessionID] = q
	}
	if len(q.events) >= maxQueued {
		in.mu.Unlock()
		in.log("[dialogue] queue full for session %s: a turn-end event (at %d) is dropped", ev.SessionID, ev.At)
		in.settled(ev.SessionID, ev.At) // it will never be handled; nothing should wait for it
		return
	}
	q.events = append(q.events, ev)
	select {
	case q.wake <- struct{}{}:
	default:
	}
	if start {
		in.wg.Add(1)
		go in.work(ev.SessionID, q)
	}
	in.mu.Unlock()
}

// Stop makes new events be ignored and waits for the workers: an event being handled finishes or is given up at its next
// wait, and nothing is written once Stop has returned. Idempotent.
func (in *intake) Stop() {
	in.mu.Lock()
	if !in.stopped {
		in.stopped = true
		close(in.stopCh)
	}
	in.mu.Unlock()
	in.wg.Wait()
}

func (in *intake) isStopped() bool {
	select {
	case <-in.stopCh:
		return true
	default:
		return false
	}
}

// sleep waits d; false when Stop came first.
func (in *intake) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-in.stopCh:
		return false
	}
}

func (in *intake) work(sid string, q *sessionQueue) {
	defer in.wg.Done()
	for {
		in.mu.Lock()
		if in.stopped {
			// events still queued will never be handled
			rest := q.events
			q.events = nil
			delete(in.queues, sid)
			in.mu.Unlock()
			for _, ev := range rest {
				in.settled(sid, ev.At)
			}
			return
		}
		if len(q.events) == 0 {
			in.mu.Unlock()
			idle := time.NewTimer(in.d.timing.idle)
			select {
			case <-q.wake:
				idle.Stop()
				continue
			case <-in.stopCh:
				idle.Stop()
				continue
			case <-idle.C:
			}
			// Idle: leave only if nothing came in meanwhile. The check and the removal are under the lock OnTurnEnd
			// queues under, so an event either lands before this (we carry on) or finds no worker and starts a new one.
			in.mu.Lock()
			if len(q.events) == 0 {
				delete(in.queues, sid)
				in.mu.Unlock()
				return
			}
			in.mu.Unlock()
			continue
		}
		ev := q.events[0]
		q.events = q.events[1:]
		in.mu.Unlock()
		in.handle(ev)
	}
}

// handle records one event.
func (in *intake) handle(ev agentmod.TurnEndEvent) {
	defer in.settled(ev.SessionID, ev.At)
	sid := ev.SessionID
	cursor := ""
	if st, has, err := in.d.store.Session(sid); err != nil {
		in.log("[dialogue] read the cursor of session %s: %v", sid, err)
		return
	} else if has {
		cursor = st.CursorTurn
	}
	res, ok := in.read(sid, cursor)
	if !ok {
		return
	}
	tries := 0 // waits for the transcript and for running tools share one allowance
	force := false
	reread := false
	for {
		if reread {
			if res, ok = in.read(sid, cursor); !ok {
				return
			}
		}
		reread = true
		target, caughtUp := findTarget(res.Turns, res.Found, ev)
		if !caughtUp {
			if tries < settleRetries {
				tries++
				if !in.sleep(in.d.timing.settleDelay) {
					return
				}
				continue
			}
			// still behind after the waits: write the file as it is; whatever comes later is picked up from the cursor
			force = true
			target = len(res.Turns) - 1
			in.log("[dialogue] session %s: the transcript had not caught up with a Stop after %d waits; written as it is", sid, settleRetries)
		}
		targetID := ""
		if target >= 0 && target < len(res.Turns) {
			targetID = res.Turns[target].ID
		}
		if in.isStopped() {
			return
		}
		wr, err := in.d.writer.Write(sid, res, targetID, force)
		if errors.Is(err, errNeedAll) {
			// the files must be rebuilt: Write needs the whole conversation, not the part from the cursor
			var all convturns.TurnsFromResult
			if all, ok = in.read(sid, ""); !ok {
				return
			}
			res = all
			if in.isStopped() {
				return
			}
			wr, err = in.d.writer.Write(sid, res, targetID, force)
		}
		if err != nil {
			in.log("[dialogue] write session %s: %v", sid, err)
			return
		}
		if wr.WaitingTool && !force {
			if tries < settleRetries {
				tries++
				if !in.sleep(in.d.timing.settleDelay) {
					return
				}
				continue
			}
			force = true
			reread = false // the same turns, written as they are
			continue
		}
		if in.d.onWritten != nil {
			in.d.onWritten(ev, res, wr, targetID, ev.Text)
		}
		return
	}
}

// read is TurnsFrom with the busy retries and the read timeout. ok is false when the event is to be given up (the
// reason is logged).
func (in *intake) read(sid, from string) (convturns.TurnsFromResult, bool) {
	for busy := 0; ; busy++ {
		ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
		res, err := in.d.turns.TurnsFrom(ctx, "claude", sid, from)
		cancel()
		switch {
		case err == nil:
			return res, true
		case errors.Is(err, convfeed.ErrBusy) && busy < busyRetries:
			if !in.sleep(in.d.timing.busyDelay) {
				return convturns.TurnsFromResult{}, false
			}
		default:
			in.log("[dialogue] read the turns of session %s: %v", sid, err)
			return convturns.TurnsFromResult{}, false
		}
	}
}

// findTarget says which turn a Stop event is about (its index in turns, -1 for none) and whether the transcript has
// caught up with the Stop. It follows the workbook's adoptEvent but never changes a turn: the hook's text is not put
// into the transcript (plan §1-8). turns[0] may be the cursor turn, which late items can still join.
//
// The hook's text names the turn: the newest turn whose last words and the hook's text are a prefix of one another.
// With no text (a failed turn, a tool-only one) it is the newest turn, if that has ended or is running, has done something
// and began before the Stop. When nothing matches and the newest turn is running with no words at all, the file has not
// caught up (false).
func findTarget(turns []convmodel.Turn, cursorFound bool, ev agentmod.TurnEndEvent) (target int, caughtUp bool) {
	last := len(turns) - 1
	if last < 0 {
		return -1, true
	}
	want := strings.TrimSpace(ev.Text)
	if want == "" {
		t := turns[last]
		if t.Outcome != convmodel.OutcomeRunning {
			return last, true
		}
		// a running turn that began after this Stop is a newer turn's
		if summarisable(t) && !(ev.At > 0 && t.StartedAt >= ev.At) {
			return last, true
		}
		return -1, true
	}
	for i := last; i >= 0; i-- {
		if said := lastWords(turns[i]); said != "" && (strings.HasPrefix(said, want) || strings.HasPrefix(want, said)) {
			return i, true
		}
	}
	t := turns[last]
	if t.Outcome != convmodel.OutcomeRunning {
		return -1, true // an ended turn: written as the file shows it
	}
	if ev.At > 0 && t.StartedAt >= ev.At {
		return -1, true // it began after this Stop: a newer turn's, left running (its own Stop will come)
	}
	// With several turns newer than the cursor (turns[0] is the cursor turn when found) a running newest one with no known
	// start time cannot be told from a later prompt's, a background turn or a lagging transcript: left alone.
	newer := len(turns)
	if cursorFound {
		newer--
	}
	if newer > 1 && (lastWords(t) != "" || t.StartedAt <= 0 || ev.At <= 0) {
		return -1, true
	}
	if lastWords(t) != "" { // the file has words of its own that differ from the hook's (normalised, cut): still this turn
		return last, true
	}
	return -1, false
}

// lastWords is the turn's last non-blank assistant text, trimmed.
func lastWords(t convmodel.Turn) string {
	var last string
	for _, it := range t.Items {
		if it.AgentText != nil {
			if m := strings.TrimSpace(it.AgentText.Markdown); m != "" {
				last = m
			}
		}
	}
	return last
}

// summarisable: a turn with no assistant text and no tool step has nothing to say.
func summarisable(t convmodel.Turn) bool {
	for _, it := range t.Items {
		if it.Step != nil || (it.AgentText != nil && strings.TrimSpace(it.AgentText.Markdown) != "") {
			return true
		}
	}
	return false
}
