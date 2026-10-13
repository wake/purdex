package push

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/reads"
)

// BG-4b (badge spec §7.2 "Pushes"): per push registration, the reads since the last push; carried by the next push; flushed
// after n minutes of silence.

type rsClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*rsTimer
}

type rsTimer struct {
	at        time.Time
	fn        func()
	cancelled bool
	fired     bool
}

func newRSClock() *rsClock { return &rsClock{now: time.Unix(1_700_000_000, 0)} }

func (f *rsClock) Now() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now }

func (f *rsClock) after(d time.Duration, fn func()) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &rsTimer{at: f.now.Add(d), fn: fn}
	f.timers = append(f.timers, t)
	return func() { f.mu.Lock(); t.cancelled = true; f.mu.Unlock() }
}

// advance moves the clock and fires, in order, every live timer that is due.
func (f *rsClock) advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	var due []*rsTimer
	for _, t := range f.timers {
		if !t.cancelled && !t.fired && !t.at.After(f.now) {
			t.fired = true
			due = append(due, t)
		}
	}
	f.mu.Unlock()
	for _, t := range due {
		t.fn()
	}
}

// live is the number of timers that are neither fired nor cancelled.
func (f *rsClock) live() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, t := range f.timers {
		if !t.cancelled && !t.fired {
			n++
		}
	}
	return n
}

func (f *rsClock) nextAt() (time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var best time.Time
	found := false
	for _, t := range f.timers {
		if !t.cancelled && !t.fired && (!found || t.at.Before(best)) {
			best, found = t.at, true
		}
	}
	return best, found
}

type rsEnv struct {
	*readSync
	clock *rsClock
	fired []string
	mu    sync.Mutex
}

func newRSEnv() *rsEnv {
	e := &rsEnv{clock: newRSClock()}
	e.readSync = newReadSync(e.clock.Now, 15*time.Minute, func(id string) { e.mu.Lock(); e.fired = append(e.fired, id); e.mu.Unlock() })
	e.readSync.after = e.clock.after
	return e
}

func (e *rsEnv) firedFor() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.fired...)
}

func r(code string, seq uint64) reads.Read {
	return reads.Read{Code: code, Through: reads.Cursor{Epoch: "e", Seq: seq}}
}

// Pending reads are kept per registration in the order each code first became pending; a newer T for a pending code updates
// it in place (and does not move it).
func TestReadSync_OrderAndInPlaceUpdate(t *testing.T) {
	e := newRSEnv()
	e.Add("d1", r("a", 1), time.Time{})
	e.Add("d1", r("b", 1), time.Time{})
	e.Add("d1", r("a", 5), time.Time{})
	e.Add("d2", r("z", 9), time.Time{})
	got := e.Reserve("d1")
	if len(got) != 2 || got[0] != r("a", 5) || got[1] != r("b", 1) {
		t.Fatalf("d1 = %v, want a(5) then b(1)", got)
	}
	if got := e.Reserve("d2"); len(got) != 1 || got[0] != r("z", 9) {
		t.Fatalf("d2 = %v", got)
	}
	if got := e.Reserve("nobody"); len(got) != 0 {
		t.Fatalf("an unknown device has %v", got)
	}
}

// 20 pending → the 16 oldest are carried; Reserve removes nothing.
func TestReadSync_ReserveTakesTheSixteenOldestAndRemovesNothing(t *testing.T) {
	e := newRSEnv()
	for i := 1; i <= 20; i++ {
		e.Add("d1", r(fmt.Sprintf("c%02d", i), uint64(i)), time.Time{})
	}
	got := e.Reserve("d1")
	if len(got) != 16 || got[0].Code != "c01" || got[15].Code != "c16" {
		t.Fatalf("reserved %d: %v", len(got), got)
	}
	if n := e.Pending("d1"); n != 20 {
		t.Fatalf("Reserve removed reads: %d pending", n)
	}
	e.Sent("d1", got)
	if rest := e.Reserve("d1"); len(rest) != 4 || rest[0].Code != "c17" {
		t.Fatalf("after the ack: %v, want the 4 that did not go", rest)
	}
}

// Acknowledge by version: a read that arrived while the push was in flight (a newer T for a reserved code) survives its ack.
// Mutation gate: delete by code instead of by (code, T) → red.
func TestReadSync_AckByVersion(t *testing.T) {
	e := newRSEnv()
	e.Add("d1", r("a", 1), time.Time{})
	e.Add("d1", r("b", 1), time.Time{})
	reserved := e.Reserve("d1")
	e.Add("d1", r("a", 2), time.Time{}) // arrives while the push is in flight
	e.Sent("d1", reserved)
	left := e.Reserve("d1")
	if len(left) != 1 || left[0] != r("a", 2) {
		t.Fatalf("left = %v, want only a(2): b was sent, a(1) was sent but a(2) is newer", left)
	}
}

// A failed send acknowledges nothing: the caller simply never calls Sent, so everything stays and is carried again.
func TestReadSync_NoAckKeepsEverything(t *testing.T) {
	e := newRSEnv()
	e.Add("d1", r("a", 1), time.Time{})
	first := e.Reserve("d1")
	second := e.Reserve("d1")
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("first %v second %v", first, second)
	}
}

// The flush: with pending reads and nothing pushed to the phone for 15 minutes (from its last push, of any kind), the daemon
// is told to send one. It does not fire while pushes keep going. Mutation gate: count from the first pending read instead of
// the last push → red.
func TestReadSync_FlushCountsFromTheLastPush(t *testing.T) {
	e := newRSEnv()
	last := e.clock.Now()
	e.Add("d1", r("a", 1), last) // the phone was last pushed to just now
	at, ok := e.clock.nextAt()
	if !ok || at.Sub(e.clock.Now()) != 15*time.Minute {
		t.Fatalf("flush scheduled at +%v (ok=%v), want +15m", at.Sub(e.clock.Now()), ok)
	}
	e.clock.advance(10 * time.Minute)
	e.Add("d1", r("b", 1), last)            // a second read does not move the deadline
	e.Sent("d1", []reads.Read{r("zzz", 0)}) // a push to the phone at +10m (carrying nothing of ours): the count starts over
	at, _ = e.clock.nextAt()
	if want := e.clock.Now().Add(15 * time.Minute); !at.Equal(want) {
		t.Fatalf("after a push the flush is at %v, want %v", at, want)
	}
	e.clock.advance(14 * time.Minute)
	if len(e.firedFor()) != 0 {
		t.Fatal("flushed while the phone had been pushed to 14 minutes ago")
	}
	e.clock.advance(time.Minute)
	if got := e.firedFor(); len(got) != 1 || got[0] != "d1" {
		t.Fatalf("fired = %v, want one flush for d1", got)
	}
}

// With no push known the count starts at the first pending read, not at once.
func TestReadSync_NoKnownPushCountsFromTheFirstRead(t *testing.T) {
	e := newRSEnv()
	e.Add("d1", r("a", 1), time.Time{})
	at, _ := e.clock.nextAt()
	if at.Sub(e.clock.Now()) != 15*time.Minute {
		t.Fatalf("flush at +%v", at.Sub(e.clock.Now()))
	}
}

// A phone quiet for longer than the interval gets its flush at once (the condition already holds); the one after waits again.
func TestReadSync_AQuietPhoneIsFlushedAtOnce(t *testing.T) {
	e := newRSEnv()
	e.Add("d1", r("a", 1), e.clock.Now().Add(-time.Hour))
	e.clock.advance(0)
	if got := e.firedFor(); len(got) != 1 {
		t.Fatalf("fired = %v", got)
	}
}

// A flush that did not clear the list (the send failed) is tried again one interval later; one that did cancels the timer.
func TestReadSync_AFlushThatFailedIsRetriedAndOneThatWorkedStops(t *testing.T) {
	e := newRSEnv()
	e.Add("d1", r("a", 1), time.Time{})
	e.clock.advance(15 * time.Minute)
	if len(e.firedFor()) != 1 {
		t.Fatal("no flush")
	}
	if e.clock.live() != 1 {
		t.Fatalf("%d timers live after a flush with reads still pending, want the retry", e.clock.live())
	}
	e.clock.advance(15 * time.Minute)
	if len(e.firedFor()) != 2 {
		t.Fatalf("fired = %v, want a second attempt", e.firedFor())
	}
	e.Sent("d1", e.Reserve("d1")) // it worked this time
	if e.clock.live() != 0 {
		t.Fatalf("%d timers live with nothing pending", e.clock.live())
	}
	e.clock.advance(time.Hour)
	if len(e.firedFor()) != 2 {
		t.Fatal("flushed with nothing pending")
	}
}

// Every removal path drops the pending reads and the timer, and a callback that was already on its way does nothing.
// Mutation gate: Forget leaves the timer → red.
func TestReadSync_ForgetDropsPendingAndTimer(t *testing.T) {
	e := newRSEnv()
	e.Add("d1", r("a", 1), time.Time{})
	e.Add("d2", r("b", 1), time.Time{})
	e.Forget("d1")
	if n := e.Pending("d1"); n != 0 {
		t.Fatalf("%d pending after Forget", n)
	}
	if e.clock.live() != 1 {
		t.Fatalf("%d timers live, want only d2's", e.clock.live())
	}
	e.clock.advance(15 * time.Minute)
	if got := e.firedFor(); len(got) != 1 || got[0] != "d2" {
		t.Fatalf("fired = %v", got)
	}
	// a late reads for the forgotten device starts afresh
	e.Add("d1", r("c", 1), time.Time{})
	if n := e.Pending("d1"); n != 1 {
		t.Fatalf("pending %d", n)
	}
}

func TestReadSync_AStaleCallbackAfterForgetDoesNothing(t *testing.T) {
	e := newRSEnv()
	e.Add("d1", r("a", 1), time.Time{})
	stale := e.clock.timers[0].fn // the callback already on its way
	e.Forget("d1")
	e.Add("d1", r("b", 1), time.Time{}) // the same id registers again: a new timer
	stale()
	if len(e.firedFor()) != 0 {
		t.Fatal("a stale callback flushed the new registration early")
	}
}

// Stop leaves no timer and takes no more reads.
func TestReadSync_StopLeavesNoTimer(t *testing.T) {
	e := newRSEnv()
	e.Add("d1", r("a", 1), time.Time{})
	e.Add("d2", r("a", 1), time.Time{})
	e.Stop()
	if e.clock.live() != 0 {
		t.Fatalf("%d timers live after Stop", e.clock.live())
	}
	e.Add("d3", r("a", 1), time.Time{})
	if e.clock.live() != 0 || e.Pending("d3") != 0 {
		t.Fatal("a stopped readSync took a read")
	}
	e.clock.advance(time.Hour)
	if len(e.firedFor()) != 0 {
		t.Fatal("flushed after Stop")
	}
	e.Start() // a restart of the module starts clean
	e.Add("d4", r("a", 1), time.Time{})
	if e.Pending("d4") != 1 {
		t.Fatal("did not take a read after Start")
	}
}
