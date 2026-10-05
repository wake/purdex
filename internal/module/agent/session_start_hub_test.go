package agent

import (
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

func TestSessionStartHub_QueueFullDropsWithoutBlocking(t *testing.T) {
	var h sessionStartHub
	release := make(chan struct{})
	var delivered atomic.Int64
	unsub := h.subscribe(func(SessionStartEvent) {
		<-release
		delivered.Add(1)
	})
	defer unsub()

	start := time.Now()
	for i := 0; i < sessionStartQueueSize+5; i++ {
		h.publish(hubEvent("x"))
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("publish blocked for %v", d)
	}
	close(release)
	time.Sleep(200 * time.Millisecond)
	n := delivered.Load()
	if n < int64(sessionStartQueueSize) || n > int64(sessionStartQueueSize)+1 {
		t.Fatalf("delivered %d, want %d..%d", n, sessionStartQueueSize, sessionStartQueueSize+1)
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
