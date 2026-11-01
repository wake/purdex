package dialogue

import (
	"context"
	"sync"
	"time"

	"github.com/wake/purdex/internal/workbooklines"
)

// The push frame and the turn-end event of one Stop are stamped by different code paths (the hook's arrival, the frame's
// broadcast after it was applied), so the summary of a Stop is the one whose turn_at is nearest to the push's stamp
// within these bounds. The intake window is tighter: an event that was looked at and made no summary releases the hold
// only if it is that close to the push's stamp, so an older event a moment before does not.
const (
	matchBeforeMS     = 2000
	matchAfterMS      = 500
	intakeBeforeMS    = 1500
	maxIntakeKept     = 8  // intake times kept per session
	maxIntakeSessions = 64 // sessions kept before the old ones are pruned
)

// summaryLine is what the push wait reads of a turn summary.
type summaryLine struct {
	ID          int64
	ConvKey     string
	State       string
	Reason      string
	Title       string
	Push        string
	PushReadyAt int64
}

// summarySource is the store as the push wait sees it.
type summarySource interface {
	closest(sid string, at, beforeMS, afterMS int64) (summaryLine, bool, error)
}

type storeSource struct{ st *Store }

func (s storeSource) closest(sid string, at, beforeMS, afterMS int64) (summaryLine, bool, error) {
	r, ok, err := s.st.ClosestSummary(sid, at, beforeMS, afterMS)
	if err != nil || !ok {
		return summaryLine{}, false, err
	}
	return summaryLine{ID: r.ID, ConvKey: r.ConvKey, State: r.State, Reason: r.Reason, Title: r.Title, Push: r.Push, PushReadyAt: r.PushReadyAt}, true, nil
}

// PushLines is the dialogue module's workbooklines.Lines: the push module's hold waits here for the summary of a Stop.
// It is told at every summary's final state (wake) and at the end of every turn-end event's intake (intakeDone);
// nothing polls.
type PushLines struct {
	source func() summarySource // nil result while the module is off

	mu       sync.Mutex
	changed  chan struct{}      // closed and replaced at every change
	intakeAt map[string][]int64 // session -> the At (unix ms) of its newest turn-end events whose intake has finished
}

var _ workbooklines.Lines = (*PushLines)(nil)

// NewPushLines builds the waiter over a source (read at call time: the store may close).
func NewPushLines(source func() summarySource) *PushLines {
	return &PushLines{source: source, changed: make(chan struct{}), intakeAt: map[string][]int64{}}
}

// StorePushLines builds the waiter over the module's store (nil while the module is off).
func StorePushLines(store func() *Store) *PushLines {
	return NewPushLines(func() summarySource {
		if st := store(); st != nil {
			return storeSource{st}
		}
		return nil
	})
}

// wake tells every waiter to look again.
func (p *PushLines) wake() {
	p.mu.Lock()
	close(p.changed)
	p.changed = make(chan struct{})
	p.mu.Unlock()
}

// intakeDone records that the event at `at` of a session has been looked at, so an Await that finds no summary for it
// can stop waiting; it wakes the waiters.
func (p *PushLines) intakeDone(sessionID string, at int64) {
	p.mu.Lock()
	kept := append(p.intakeAt[sessionID], at)
	if len(kept) > maxIntakeKept {
		kept = kept[len(kept)-maxIntakeKept:]
	}
	p.intakeAt[sessionID] = kept
	for len(p.intakeAt) > maxIntakeSessions { // forget the least recently seen session
		oldest, oldestAt := "", int64(0)
		for sid, ats := range p.intakeAt {
			if sid != sessionID && (oldest == "" || ats[len(ats)-1] < oldestAt) {
				oldest, oldestAt = sid, ats[len(ats)-1]
			}
		}
		delete(p.intakeAt, oldest)
	}
	close(p.changed)
	p.changed = make(chan struct{})
	p.mu.Unlock()
}

// snapshot returns the channel the next change closes and whether an intake for this stamp has finished: one whose event
// time is within the intake window of it. Taken before the store is read, so a change between the read and the wait still
// wakes the wait.
func (p *PushLines) snapshot(sessionID string, since int64) (<-chan struct{}, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, at := range p.intakeAt[sessionID] {
		if at >= since-intakeBeforeMS && at <= since+matchAfterMS {
			return p.changed, true
		}
	}
	return p.changed, false
}

// Await implements workbooklines.Lines.
func (p *PushLines) Await(ctx context.Context, sessionID string, sinceMs int64, deadline time.Time) (workbooklines.Line, bool) {
	left := time.Until(deadline)
	if left <= 0 {
		left = 0
	}
	timer := time.NewTimer(left)
	defer timer.Stop()
	for {
		changed, looked := p.snapshot(sessionID, sinceMs)
		src := p.source()
		if src == nil {
			return workbooklines.Line{}, false
		}
		e, ok, err := src.closest(sessionID, sinceMs, matchBeforeMS, matchAfterMS)
		if err != nil {
			return workbooklines.Line{}, false
		}
		switch {
		case ok && e.PushReadyAt > 0:
			if e.Push == "" {
				return workbooklines.Line{}, false // the model's push line was dropped: today's body
			}
			return workbooklines.Line{Thing: e.Title, Push: e.Push, ConvKey: e.ConvKey, EntryID: e.ID}, true
		case ok && (e.State == stateFailed || e.State == stateSkipped):
			return workbooklines.Line{}, false
		case !ok && looked:
			return workbooklines.Line{}, false // the turn was looked at and produced no summary
		}
		select {
		case <-changed:
		case <-timer.C:
			return workbooklines.Line{}, false
		case <-ctx.Done():
			return workbooklines.Line{}, false
		}
	}
}
