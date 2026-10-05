package agent

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func hubEvent(id string) SessionStartEvent { return SessionStartEvent{SessionID: id, Source: "test"} }

func TestSessionStartHub_OrderedDelivery(t *testing.T) {
	var h sessionStartHub
	got := make(chan string, 4)
	unsub := h.subscribe(func(ev SessionStartEvent) { got <- ev.SessionID })
	defer unsub()

	h.publish(hubEvent("A"))
	h.publish(hubEvent("B"))
	for _, want := range []string{"A", "B"} {
		select {
		case id := <-got:
			if id != want {
				t.Fatalf("got %s, want %s", id, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for %s", want)
		}
	}
}

func TestSessionStartHub_BlockedSubscriberDoesNotGrowGoroutines(t *testing.T) {
	var h sessionStartHub
	release := make(chan struct{})
	unsub := h.subscribe(func(SessionStartEvent) { <-release })
	defer unsub()
	defer close(release)

	before := runtime.NumGoroutine()
	for i := 0; i < 100; i++ {
		h.publish(hubEvent("x"))
	}
	time.Sleep(50 * time.Millisecond)
	if grew := runtime.NumGoroutine() - before; grew > 5 {
		t.Fatalf("goroutines grew by %d for one blocked subscriber", grew)
	}
}

// blockedHub returns a hub whose subscriber is parked inside the callback on
// a "gate" event, so later publishes pile up behind it. release() lets it go;
// got receives every non-gate event in delivery order.
func blockedHub(t *testing.T) (h *sessionStartHub, got chan SessionStartEvent, release func()) {
	t.Helper()
	h = &sessionStartHub{}
	got = make(chan SessionStartEvent, 1024)
	entered := make(chan struct{})
	gate := make(chan struct{})
	unsub := h.subscribe(func(ev SessionStartEvent) {
		if ev.SessionID == "gate" {
			close(entered)
			<-gate
			return
		}
		got <- ev
	})
	t.Cleanup(unsub)
	h.publish(hubEvent("gate"))
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer never entered the callback")
	}
	return h, got, func() { close(gate) }
}

func TestSessionStartHub_CoalescesPerSessionID(t *testing.T) {
	h, got, release := blockedHub(t)
	h.publish(SessionStartEvent{SessionID: "S1", Source: "resume"})
	h.publish(SessionStartEvent{SessionID: "S2", Source: "startup"})
	h.publish(SessionStartEvent{SessionID: "S1", Source: "startup"})
	release()

	var evs []SessionStartEvent
	for len(evs) < 2 {
		select {
		case ev := <-got:
			evs = append(evs, ev)
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout; got %+v", evs)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if len(got) != 0 {
		t.Fatalf("extra deliveries: %d", len(got))
	}
	if evs[0].SessionID != "S1" || evs[0].Source != "startup" || evs[1].SessionID != "S2" {
		t.Fatalf("got %+v, want S1(startup) then S2", evs)
	}
}

func TestSessionStartHub_NoLossUnderLoad(t *testing.T) {
	h, got, release := blockedHub(t)
	for i := 0; i < 500; i++ {
		id := fmt.Sprintf("s%d", i%200)
		h.publish(SessionStartEvent{SessionID: id, Source: fmt.Sprintf("n%d", i)})
	}
	release()

	seen := map[string]string{}
	deadline := time.After(3 * time.Second)
	for len(seen) < 200 {
		select {
		case ev := <-got:
			if _, dup := seen[ev.SessionID]; dup {
				t.Fatalf("session %s delivered twice", ev.SessionID)
			}
			seen[ev.SessionID] = ev.Source
		case <-deadline:
			t.Fatalf("only %d/200 sessions delivered", len(seen))
		}
	}
	for i := 0; i < 200; i++ {
		// latest publish for session i%200 is the largest n with n%200==i
		want := fmt.Sprintf("n%d", 400+i)
		if i+400 >= 500 {
			want = fmt.Sprintf("n%d", 200+i)
		}
		if got := seen[fmt.Sprintf("s%d", i)]; got != want {
			t.Fatalf("s%d: got %s, want %s", i, got, want)
		}
	}
}

func TestSessionStartHub_PublishNeverBlocks(t *testing.T) {
	h, _, release := blockedHub(t)
	defer release()
	start := time.Now()
	for i := 0; i < 1000; i++ {
		h.publish(hubEvent(fmt.Sprintf("p%d", i)))
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("1000 publishes took %v", d)
	}
}

func TestSessionStartHub_UnsubscribeStopsDelivery(t *testing.T) {
	var h sessionStartHub
	var n atomic.Int64
	unsub := h.subscribe(func(SessionStartEvent) { n.Add(1) })
	unsub()
	unsub() // idempotent
	h.publish(hubEvent("late"))
	time.Sleep(100 * time.Millisecond)
	if n.Load() != 0 {
		t.Fatalf("delivered %d after unsubscribe", n.Load())
	}
}

func TestSessionStartHub_PublishRacingUnsubscribe(t *testing.T) {
	var h sessionStartHub
	for i := 0; i < 50; i++ {
		unsub := h.subscribe(func(SessionStartEvent) {})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				h.publish(hubEvent("r"))
			}
		}()
		go func() {
			defer wg.Done()
			unsub()
		}()
		wg.Wait()
	}
}

func TestSessionStartHub_PanicDoesNotStopLaterEventsOrOthers(t *testing.T) {
	var h sessionStartHub
	got := make(chan string, 4)
	var first atomic.Bool
	unsubP := h.subscribe(func(ev SessionStartEvent) {
		if first.CompareAndSwap(false, true) {
			panic("boom")
		}
		got <- "p:" + ev.SessionID
	})
	defer unsubP()
	other := make(chan string, 4)
	unsubO := h.subscribe(func(ev SessionStartEvent) { other <- ev.SessionID })
	defer unsubO()

	h.publish(hubEvent("A"))
	h.publish(hubEvent("B"))
	for _, ch := range []chan string{other, other} {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatal("other subscriber starved")
		}
	}
	select {
	case id := <-got:
		if id != "p:B" {
			t.Fatalf("got %s, want p:B", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("panicking subscriber's consumer died after one panic")
	}
}

func collectHubEvents(t *testing.T, got chan SessionStartEvent, n int) []SessionStartEvent {
	t.Helper()
	var evs []SessionStartEvent
	deadline := time.After(3 * time.Second)
	for len(evs) < n {
		select {
		case ev := <-got:
			evs = append(evs, ev)
		case <-deadline:
			t.Fatalf("timeout: got %d/%d", len(evs), n)
		}
	}
	return evs
}

func TestSessionStartHub_OverflowBecomesFullRecheck(t *testing.T) {
	h, got, release := blockedHub(t)
	for i := 0; i < sessionStartPendingCap+10; i++ {
		h.publish(hubEvent(fmt.Sprintf("o%d", i)))
	}
	release()
	evs := collectHubEvents(t, got, sessionStartPendingCap+1)
	for i := 0; i < sessionStartPendingCap; i++ {
		if evs[i].Overflow || evs[i].SessionID != fmt.Sprintf("o%d", i) {
			t.Fatalf("event %d = %+v", i, evs[i])
		}
	}
	last := evs[sessionStartPendingCap]
	if !last.Overflow || last.SessionID != "" {
		t.Fatalf("last = %+v, want Overflow with empty SessionID", last)
	}
	time.Sleep(50 * time.Millisecond)
	if len(got) != 0 {
		t.Fatalf("extra deliveries: %d", len(got))
	}

	// the overflow flag resets
	h.publish(hubEvent("after"))
	evs = collectHubEvents(t, got, 1)
	if evs[0].Overflow || evs[0].SessionID != "after" {
		t.Fatalf("after = %+v", evs[0])
	}
	time.Sleep(50 * time.Millisecond)
	if len(got) != 0 {
		t.Fatalf("second overflow event delivered")
	}
}

func TestSessionStartHub_PendingMemoryIsBounded(t *testing.T) {
	h, _, release := blockedHub(t)
	defer release()
	for i := 0; i < sessionStartPendingCap+5000; i++ {
		h.publish(hubEvent(fmt.Sprintf("m%d", i)))
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sub := range h.subs {
		sub.mu.Lock()
		n, p := len(sub.order), len(sub.pending)
		sub.mu.Unlock()
		if n > sessionStartPendingCap || p > sessionStartPendingCap {
			t.Fatalf("order=%d pending=%d, cap %d", n, p, sessionStartPendingCap)
		}
	}
}
