package dev

import (
	"context"
	"sync"
	"testing"
	"time"
)

func collectEvents(t *testing.T, ch <-chan BuildEvent, maxWait time.Duration) []BuildEvent {
	t.Helper()
	var out []BuildEvent
	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-timer.C:
			t.Fatalf("collectEvents: timeout after %v, collected %d events so far", maxWait, len(out))
		}
	}
}

func TestBuildSession_FanoutToSubscribers(t *testing.T) {
	s := newBuildSession()
	chA, _, unsubA := s.subscribe()
	chB, _, unsubB := s.subscribe()
	defer unsubA()
	defer unsubB()

	events := []BuildEvent{
		{Type: BuildEventPhase, Phase: "install"},
		{Type: BuildEventStdout, Line: "hello"},
		{Type: BuildEventStdout, Line: "world"},
		{Type: BuildEventDone},
	}
	go func() {
		for _, ev := range events {
			s.append(ev)
		}
		s.finish()
	}()

	gotA := collectEvents(t, chA, 2*time.Second)
	gotB := collectEvents(t, chB, 2*time.Second)

	if len(gotA) != len(events) {
		t.Errorf("subscriber A: want %d events, got %d: %+v", len(events), len(gotA), gotA)
	}
	if len(gotB) != len(events) {
		t.Errorf("subscriber B: want %d events, got %d: %+v", len(events), len(gotB), gotB)
	}
	if len(gotA) > 0 && gotA[0].Phase != "install" {
		t.Errorf("subscriber A first event: want phase install, got %+v", gotA[0])
	}
}

func TestBuildSession_LateSubscriberReceivesReplay(t *testing.T) {
	s := newBuildSession()

	s.append(BuildEvent{Type: BuildEventPhase, Phase: "install"})
	s.append(BuildEvent{Type: BuildEventStdout, Line: "already-out"})

	ch, replay, unsub := s.subscribe()
	defer unsub()

	if len(replay) != 2 {
		t.Fatalf("replay: want 2 events, got %d", len(replay))
	}
	if replay[0].Phase != "install" {
		t.Errorf("replay[0]: want phase install, got %+v", replay[0])
	}
	if replay[1].Line != "already-out" {
		t.Errorf("replay[1]: want line already-out, got %+v", replay[1])
	}

	s.append(BuildEvent{Type: BuildEventStdout, Line: "fresh"})
	s.finish()

	got := collectEvents(t, ch, 2*time.Second)
	if len(got) != 1 {
		t.Fatalf("live events: want 1, got %d: %+v", len(got), got)
	}
	if got[0].Line != "fresh" {
		t.Errorf("live[0]: want line fresh, got %+v", got[0])
	}
}

func TestBuildSession_ClosedSessionReturnsClosedChannel(t *testing.T) {
	s := newBuildSession()
	s.append(BuildEvent{Type: BuildEventDone})
	s.finish()

	ch, replay, _ := s.subscribe()
	if len(replay) != 1 {
		t.Errorf("replay: want 1 event, got %d", len(replay))
	}
	if _, ok := <-ch; ok {
		t.Error("expected closed channel from already-finished session")
	}
}

func TestBuildSession_AppendAfterFinishNoOp(t *testing.T) {
	s := newBuildSession()
	s.finish()
	s.append(BuildEvent{Type: BuildEventStdout, Line: "x"})
	s.finish() // double finish no-op
}

func TestBuildSession_ConcurrentSubscribeUnsubscribe(t *testing.T) {
	s := newBuildSession()
	done := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, unsub := s.subscribe()
			unsub()
		}()
	}
	go func() {
		for i := 0; i < 100; i++ {
			s.append(BuildEvent{Type: BuildEventStdout, Line: "x"})
		}
		close(done)
	}()

	wg.Wait()
	<-done
	s.finish()
}

func TestStreamCmd_CapturesStdoutAndStderr(t *testing.T) {
	s := newBuildSession()
	ch, _, unsub := s.subscribe()
	defer unsub()

	go func() {
		err := streamCmd(context.Background(), s, "test", "", "sh", "-c", "echo out-line; echo err-line 1>&2")
		if err != nil {
			t.Errorf("streamCmd: %v", err)
		}
		s.finish()
	}()

	got := collectEvents(t, ch, 3*time.Second)

	seenPhase, seenStdout, seenStderr := false, false, false
	for _, ev := range got {
		switch ev.Type {
		case BuildEventPhase:
			if ev.Phase == "test" {
				seenPhase = true
			}
		case BuildEventStdout:
			if ev.Line == "out-line" {
				seenStdout = true
			}
		case BuildEventStderr:
			if ev.Line == "err-line" {
				seenStderr = true
			}
		}
	}
	if !(seenPhase && seenStdout && seenStderr) {
		t.Errorf("missing events: phase=%v stdout=%v stderr=%v; all=%+v", seenPhase, seenStdout, seenStderr, got)
	}
}

func TestStreamCmd_PropagatesExitError(t *testing.T) {
	s := newBuildSession()
	err := streamCmd(context.Background(), s, "test", "", "sh", "-c", "exit 7")
	s.finish()
	if err == nil {
		t.Fatal("want non-nil error from non-zero exit, got nil")
	}
}
