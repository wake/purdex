package workbook

import (
	"context"
	"sync"
	"time"

	"github.com/wake/purdex/internal/workbooklines"
)

// sinceSlack: the push frame and the turn-end event are stamped by different code paths within the same Stop, so an
// entry counts for a push when its turn ended no earlier than this long before the push's own timestamp (plan WB-3).
const sinceSlackMS = 2000

// PushLines is the workbook's workbooklines.Lines: the push module's hold waits here for the entry of a Stop. It is told
// by the engine at every push line and final state (wake) and at the end of every turn-end event's intake (intakeDone);
// nothing polls.
type PushLines struct {
	store func() *Store // nil while the module is off

	mu       sync.Mutex
	changed  chan struct{}    // closed and replaced at every change
	intakeAt map[string]int64 // session -> the newest turn-end event whose intake has finished (its At, unix ms)
}

var _ workbooklines.Lines = (*PushLines)(nil)

// NewPushLines builds the waiter over the module's store (read at call time: the store may close).
func NewPushLines(store func() *Store) *PushLines {
	return &PushLines{store: store, changed: make(chan struct{}), intakeAt: map[string]int64{}}
}

// wake tells every waiter to look again.
func (p *PushLines) wake() {
	p.mu.Lock()
	close(p.changed)
	p.changed = make(chan struct{})
	p.mu.Unlock()
}

// intakeDone records that the event at `at` of a session has been looked at, so an Await that finds no entry for it can
// stop waiting; it wakes the waiters.
func (p *PushLines) intakeDone(sessionID string, at int64) {
	p.mu.Lock()
	if at > p.intakeAt[sessionID] {
		p.intakeAt[sessionID] = at
	}
	close(p.changed)
	p.changed = make(chan struct{})
	p.mu.Unlock()
}

// snapshot returns the channel the next change closes and whether an intake at or after min has finished. Taken before
// the store is read, so a change between the read and the wait still wakes the wait.
func (p *PushLines) snapshot(sessionID string, min int64) (<-chan struct{}, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	at, has := p.intakeAt[sessionID]
	return p.changed, has && at >= min
}

// Await implements workbooklines.Lines.
func (p *PushLines) Await(ctx context.Context, sessionID string, sinceMs int64, deadline time.Time) (workbooklines.Line, bool) {
	min := sinceMs - sinceSlackMS
	left := time.Until(deadline)
	if left <= 0 {
		left = 0
	}
	timer := time.NewTimer(left)
	defer timer.Stop()
	for {
		changed, looked := p.snapshot(sessionID, min)
		st := p.store()
		if st == nil {
			return workbooklines.Line{}, false
		}
		e, ok, err := st.NewestSince(sessionID, min)
		if err != nil {
			return workbooklines.Line{}, false
		}
		switch {
		case ok && e.PushReadyAt > 0:
			if e.Push == "" {
				return workbooklines.Line{}, false // the model's push line was dropped: today's body
			}
			return workbooklines.Line{Thing: e.Thing, Push: e.Push, ConvKey: e.ConvKey, EntryID: e.ID}, true
		case ok && (e.State == StateFailed || e.State == StateSkipped):
			return workbooklines.Line{}, false
		case !ok && looked:
			return workbooklines.Line{}, false // the turn was looked at and produced no entry
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
