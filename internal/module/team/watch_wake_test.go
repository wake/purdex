package teammod

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/watch"
)

type fakeWaker struct {
	mu      sync.Mutex
	calls   [][2]string
	block   chan struct{}
	started chan struct{}
}

func (f *fakeWaker) WakeWatcher(ctx context.Context, reason, detail string) error {
	f.mu.Lock()
	f.calls = append(f.calls, [2]string{reason, detail})
	n := len(f.calls)
	f.mu.Unlock()
	if n == 1 && f.block != nil {
		close(f.started)
		select {
		case <-f.block:
		case <-ctx.Done():
		}
	}
	return nil
}

func (f *fakeWaker) got() [][2]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]string(nil), f.calls...)
}

// Switching the unattended mode wakes the load watcher with the new state; switches that come while a wake is still going on are
// folded into the last one, so a flapping switch never piles up wakes.
func TestUnattended_TheSwitchWakesTheWatcherAndFastSwitchesAreFolded(t *testing.T) {
	f := newFixture(t)
	f.realUnattended()
	w := &fakeWaker{block: make(chan struct{}), started: make(chan struct{})}
	f.core.Registry.Register(watch.WakerKey, watch.Waker(w))

	f.switchTo(true)
	<-w.started // the first wake is under way
	f.switchTo(false)
	f.switchTo(true)
	f.switchTo(false)
	close(w.block)
	deadline := time.Now().Add(5 * time.Second)
	for len(w.got()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	calls := w.got()
	if len(calls) != 2 {
		t.Fatalf("calls = %v; want the first and one folded", calls)
	}
	if calls[0] != [2]string{"unattended_changed", "無人值守模式 → 開（by Purdex.app @ air26）"} {
		t.Fatalf("first = %v", calls[0])
	}
	if calls[1] != [2]string{"unattended_changed", "無人值守模式 → 關（by Purdex.app @ air26）"} {
		t.Fatalf("folded = %v: the watcher is told the state it is in now", calls[1])
	}
}

func TestUnattendedWakeDetail_OneLineWithABoundedLabel(t *testing.T) {
	d := unattendedWakeDetail(true, "a\nb")
	if d != "無人值守模式 → 開（by a b）" {
		t.Fatalf("detail = %q", d)
	}
	long := unattendedWakeDetail(false, "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	if err := watch.ValidateWake("unattended_changed", long); err != nil {
		t.Fatal(err)
	}
}
