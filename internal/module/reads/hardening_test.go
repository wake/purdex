package reads

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/reads"
)

// Review findings of PR BG-4a (R1 P2, attack high x3 / medium x2): a forged future cursor, an unusable session list used to
// fill lastRead, a feed that lost reads, trailing JSON, and the feed's lifecycle.

// A cursor from the future would cover every later unread of the session until the epoch changes. A read is clamped to the
// newest cursor the daemon has emitted for that session (or, for a session it has no event of, to the epoch's high-water
// mark); a legitimate T (the cursor of an event that already happened) is never changed; another epoch's T is left alone
// (it can never cover a current-epoch unread). Mutation gate: no clamp → red.
func TestRead_AFutureCursorIsClamped(t *testing.T) {
	e := newEnv(t)
	c := e.dial(t, "?reads=v1")
	c.next(t, "session.reads")
	e.notify.emit(codeA, "e1", 10)
	e.notify.emit(codeB, "e1", 40)
	time.Sleep(50 * time.Millisecond)

	e.post(codeA, `{"through":{"epoch":"e1","seq":18446744073709551615}}`)
	if r := readValue(t, c.next(t, "session.read")); r.Through != (reads.Cursor{Epoch: "e1", Seq: 10}) {
		t.Fatalf("a future seq: read = %+v, want it clamped to the session's newest (10)", r)
	}
	e.post(codeB, `{"through":{"epoch":"e1","seq":30}}`) // a legitimate, older T
	if r := readValue(t, c.next(t, "session.read")); r.Through != (reads.Cursor{Epoch: "e1", Seq: 30}) {
		t.Fatalf("a legitimate T was changed: %+v", r)
	}
	// another session's cursor (40) does not reach past this session's newest (10)
	e.post(codeC, `{"through":{"epoch":"e1","seq":40}}`)
	if r := readValue(t, c.next(t, "session.read")); r.Through.Seq > 40 {
		t.Fatalf("read = %+v", r)
	}
	e.post(codeA, `{"through":{"epoch":"old","seq":999}}`) // an older epoch: harmless, kept as sent, but never a replacement
	if got, _ := e.m.store.Get(codeA); got != (reads.Cursor{Epoch: "e1", Seq: 10}) {
		t.Fatalf("lastRead = %+v", got)
	}
}

// A session the daemon has no event of is bounded by the epoch's high-water mark (no event was ever emitted past it).
func TestRead_ASessionWithNoEventIsBoundedByTheEpochHighWater(t *testing.T) {
	e := newEnv(t)
	c := e.dial(t, "?reads=v1")
	c.next(t, "session.reads")
	e.notify.emit(codeB, "e1", 25)
	time.Sleep(50 * time.Millisecond)
	e.post(codeC, `{"through":{"epoch":"e1","seq":9999}}`)
	if r := readValue(t, c.next(t, "session.read")); r.Through != (reads.Cursor{Epoch: "e1", Seq: 25}) {
		t.Fatalf("read = %+v, want the high-water mark 25", r)
	}
}

// With no usable session list only codes the daemon has seen (an event of it, or an earlier read) are accepted, so made-up
// codes cannot push the real reads out of the bounded store. Mutation gate: accept any well-formed code → red.
func TestRead_UnknownListAcceptsOnlyCodesTheDaemonHasSeen(t *testing.T) {
	e := newEnv(t)
	e.list.set = nil // the list is unknown
	e.notify.emit(codeA, "e1", 3)
	time.Sleep(50 * time.Millisecond)
	if rec := e.post(codeA, `{}`); rec.Code != http.StatusNoContent {
		t.Fatalf("a session with an event: %d", rec.Code)
	}
	if rec := e.post(codeA, `{"through":{"epoch":"e1","seq":3}}`); rec.Code != http.StatusNoContent {
		t.Fatalf("a session that was read before: %d", rec.Code)
	}
	if rec := e.post(codeB, `{}`); rec.Code != http.StatusNotFound {
		t.Fatalf("a code never seen, with the list unknown: %d", rec.Code)
	}
	if _, ok := e.m.store.Get(codeB); ok {
		t.Fatal("an unverified code entered the store")
	}
}

func TestRead_TrailingJSONIsRefused(t *testing.T) {
	e := newEnv(t)
	for name, body := range map[string]string{
		"a second object":  `{"through":{"epoch":"e1","seq":7}}{"through":{"epoch":"e1","seq":999}}`,
		"a trailing token": `{} x`,
	} {
		if rec := e.post(codeA, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", name, rec.Code)
		}
	}
	if rec := e.post(codeA, "{}\n  "); rec.Code != http.StatusNoContent {
		t.Fatalf("trailing whitespace is fine: %d", rec.Code)
	}
}

// The feed never loses an accepted read while it can hold the codes: a slow subscriber gets, per code, the latest cursor (an
// older one pending for the same code is replaced in place), in the order each code first became pending. R2 attack, high.
// Mutation gate: drop when the queue is full → red.
func TestFeed_ASlowSubscriberGetsTheLatestPerCodeAndLosesNone(t *testing.T) {
	f := newReadFeed()
	defer f.stop(nil)
	release := make(chan struct{})
	var mu sync.Mutex
	got := map[string]reads.Cursor{}
	var order []string
	var calls atomic.Int32
	f.subscribe(func(r reads.Read) {
		if calls.Add(1) == 1 {
			<-release // the first delivery blocks
		}
		mu.Lock()
		if _, seen := got[r.Code]; !seen {
			order = append(order, r.Code)
		}
		got[r.Code] = r.Through
		mu.Unlock()
	})
	f.publish(reads.Read{Code: "first", Through: reads.Cursor{Epoch: "e", Seq: 1}})
	time.Sleep(30 * time.Millisecond) // "first" is now inside the blocked callback
	for i := 2; i <= 1000; i++ {      // far more than any queue: three codes, rising cursors
		code := []string{"x", "y", "z"}[i%3]
		f.publish(reads.Read{Code: code, Through: reads.Cursor{Epoch: "e", Seq: uint64(i)}})
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		done := len(got) == 4 && got["x"].Seq == 999 && got["y"].Seq == 1000 && got["z"].Seq == 998
		mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			mu.Lock()
			t.Fatalf("the subscriber ended with %v", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := calls.Load(); n > 8 {
		t.Fatalf("%d deliveries for 1000 reads of 4 codes: they were not coalesced", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(order, ",") != "first,z,x,y" {
		t.Fatalf("order = %v, want the order each code first became pending", order)
	}
}

// R1 P2: after unsubscribe no queued read is delivered (at most the one callback already running finishes).
// Mutation gate: deliver the backlog after unsubscribe → red.
func TestFeed_UnsubscribeDropsTheBacklog(t *testing.T) {
	f := newReadFeed()
	defer f.stop(nil)
	release, entered := make(chan struct{}), make(chan struct{}, 1)
	var calls atomic.Int32
	unsub := f.subscribe(func(reads.Read) {
		if calls.Add(1) == 1 {
			entered <- struct{}{}
			<-release
		}
	})
	f.publish(reads.Read{Code: "a", Through: reads.Cursor{Epoch: "e", Seq: 1}})
	<-entered
	for _, code := range []string{"b", "c", "d"} {
		f.publish(reads.Read{Code: code, Through: reads.Cursor{Epoch: "e", Seq: 1}})
	}
	unsub()
	close(release)
	time.Sleep(100 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d callbacks ran, want only the one that was already in flight", n)
	}
}

// stop returns once its workers have (they are waited for), and unsubscribing afterwards is harmless.
func TestFeed_StopWaitsForTheWorkersAndIsRepeatable(t *testing.T) {
	f := newReadFeed()
	var running atomic.Int32
	unsub := f.subscribe(func(reads.Read) {
		running.Add(1)
		time.Sleep(50 * time.Millisecond)
		running.Add(-1)
	})
	f.publish(reads.Read{Code: "a", Through: reads.Cursor{Epoch: "e", Seq: 1}})
	time.Sleep(10 * time.Millisecond)
	f.stop(nil)
	if running.Load() != 0 {
		t.Fatal("stop returned while a delivery was still running")
	}
	f.stop(nil)
	unsub()
}

// Module.Stop may be called twice and from two goroutines.
func TestModule_StopIsReentrant(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = e.m.Stop(nil) }()
	}
	wg.Wait()
}
