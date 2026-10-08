package agent

import (
	"fmt"
	"log"
	"sync/atomic"
	"time"
)

// Hold timing of the emit slot. Every hook frame serialises on
// hookEmitter.mu, and what runs inside it reads the frame store and tmux, so
// the time the mutex is held (store waits included) is the cost this design
// puts on every hook emit. It is measured from taking the mutex to releasing
// it, in four buckets; a hold that reaches holdWarn is logged with the
// counts so far, and under PDX_DEV_MODE a summary line goes out about every
// holdSummaryEvery (written by whichever emit finishes after the interval,
// so no ticker or goroutine is needed).

// slotKind labels who held the slot, for the slow-hold log line.
type slotKind string

const (
	kindHook     slotKind = "hook"     // a hook event from the agent
	kindProbe    slotKind = "probe"    // a screen-probe status transition
	kindSweep    slotKind = "sweep"    // the pid sweep
	kindWorker   slotKind = "worker"   // the mod re-emit worker
	kindNonTmux  slotKind = "non_tmux" // a hook from a session outside tmux
	kindSnapshot slotKind = "snapshot" // the subscribe-time replay
)

const (
	holdWarn         = 250 * time.Millisecond
	holdSummaryEvery = 5 * time.Minute
)

// holdBounds are the upper bounds of the first three buckets; the fourth is
// everything from holdWarn up.
var holdBounds = [3]time.Duration{10 * time.Millisecond, 50 * time.Millisecond, holdWarn}

// holdStats is the slot's hold distribution. The counters are atomic so the
// summary and the tests can read them without the slot's mutex.
type holdStats struct {
	// clock is a test seam; nil means time.Now.
	clock func() time.Time

	buckets     [4]atomic.Uint64 // <10ms, <50ms, <250ms, >=250ms
	total       atomic.Uint64
	maxNs       atomic.Int64
	lastSummary atomic.Int64 // unix nanos of the last summary, 0 before the first hold
}

func (h *holdStats) now() time.Time {
	if h.clock != nil {
		return h.clock()
	}
	return time.Now()
}

// begin is called right after the slot's mutex is taken.
func (e *hookEmitter) begin() time.Time { return e.hold.now() }

// end records the hold that began at start. It runs before the mutex is
// released (defer it after the Unlock defer), so the hold is the mutex's.
func (e *hookEmitter) end(start time.Time, session string, kind slotKind) {
	h := &e.hold
	now := h.now()
	d := now.Sub(start)
	i := 0
	for i < len(holdBounds) && d >= holdBounds[i] {
		i++
	}
	h.buckets[i].Add(1)
	h.total.Add(1)
	for {
		cur := h.maxNs.Load()
		if int64(d) <= cur || h.maxNs.CompareAndSwap(cur, int64(d)) {
			break
		}
	}
	if i == len(holdBounds) {
		log.Printf("[agent] emit slot held %s (session=%q kind=%s) %s", d, session, kind, h.describe())
	}
	if isDevMode() {
		last := h.lastSummary.Load()
		switch {
		case last == 0:
			h.lastSummary.CompareAndSwap(0, now.UnixNano())
		case now.UnixNano()-last >= int64(holdSummaryEvery) && h.lastSummary.CompareAndSwap(last, now.UnixNano()):
			log.Printf("[agent] emit slot summary %s", h.describe())
		}
	}
}

// describe is the bucket counts as they stand.
func (h *holdStats) describe() string {
	return fmt.Sprintf("holds{<10ms=%d <50ms=%d <250ms=%d >=250ms=%d} total=%d max=%s",
		h.buckets[0].Load(), h.buckets[1].Load(), h.buckets[2].Load(), h.buckets[3].Load(),
		h.total.Load(), time.Duration(h.maxNs.Load()))
}
