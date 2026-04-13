package probe_test

import (
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/tmux"
)

func TestStartWatch_DetectsChange(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)

	fake.SetPaneContent("sess:", "initial content")

	var called sync.WaitGroup
	called.Add(1)
	var callbackTarget string

	p.StartWatch("sess:", func(target string) {
		callbackTarget = target
		called.Done()
	})

	time.Sleep(100 * time.Millisecond)
	fake.SetPaneContent("sess:", "new content after user responded")

	called.Wait()
	if callbackTarget != "sess:" {
		t.Fatalf("expected callback with target sess:, got %s", callbackTarget)
	}
}

func TestStartWatch_NoChangeNoCallback(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)

	fake.SetPaneContent("sess:", "static content")

	callbackCalled := false
	p.StartWatch("sess:", func(string) {
		callbackCalled = true
	})

	time.Sleep(600 * time.Millisecond)
	p.StopWatch("sess:")

	if callbackCalled {
		t.Fatal("callback should not be called when content is static")
	}
}

func TestStopWatch_Idempotent(t *testing.T) {
	p := probe.New(nil)
	p.StopWatch("nonexistent")
	p.StopWatch("nonexistent")
}

func TestStartWatch_ReplacesExisting(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)

	fake.SetPaneContent("sess:", "content-v1")

	firstCalled := false
	p.StartWatch("sess:", func(string) {
		firstCalled = true
	})

	var secondCalled sync.WaitGroup
	secondCalled.Add(1)
	p.StartWatch("sess:", func(string) {
		secondCalled.Done()
	})

	time.Sleep(100 * time.Millisecond)
	fake.SetPaneContent("sess:", "content-v2")
	secondCalled.Wait()

	if firstCalled {
		t.Fatal("first watcher should have been cancelled by replacement")
	}
}

func TestStopAllWatches(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)

	fake.SetPaneContent("a:", "content-a")
	fake.SetPaneContent("b:", "content-b")

	aCalled := false
	bCalled := false
	p.StartWatch("a:", func(string) { aCalled = true })
	p.StartWatch("b:", func(string) { bCalled = true })

	p.StopAllWatches()

	fake.SetPaneContent("a:", "changed-a")
	fake.SetPaneContent("b:", "changed-b")
	time.Sleep(600 * time.Millisecond)

	if aCalled || bCalled {
		t.Fatal("callbacks should not fire after StopAllWatches")
	}
}
