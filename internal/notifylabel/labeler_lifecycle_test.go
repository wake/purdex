package notifylabel

import (
	"context"
	"testing"
	"time"
)

// The sweep runs every ttl/2 and rereads what is older than ttl/2, so an entry is never more than ttl old when the next
// sweep looks at it, whatever the phase of the ticker against the read (a ticker of ttl with an age test of ttl could leave
// an entry nearly twice as old).
func TestLabeler_SweepRereadsAtHalfTheTTL(t *testing.T) {
	f := baseSources()
	l, clk := newTest(t, f)
	l.Label("s1", "dev")
	settle(t, l)
	clk.advance(14 * time.Second)
	l.sweep()
	settle(t, l)
	if got := f.reads.Load(); got != 1 {
		t.Fatalf("a sweep before ttl/2 read: %d", got)
	}
	clk.advance(2 * time.Second) // 16 s
	l.sweep()
	settle(t, l)
	if got := f.reads.Load(); got != 2 {
		t.Fatalf("a sweep after ttl/2 did not read: %d", got)
	}
}

// After Stop returns, a read that was still parked on a source throws its answer away: it neither writes the cache nor
// asks the sources again, so nothing outlives the module's teardown.
func TestLabeler_AReadThatEndsAfterStopWritesNothing(t *testing.T) {
	f := baseSources()
	f.gate = make(chan struct{})
	started := make(chan struct{}, 1)
	src := f.sources()
	inner := src.Code
	src.Code = func(_ context.Context, sid string) (string, bool, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		return inner(context.Background(), sid) // ignores the labeler's context, waits for the gate
	}
	l := New(src, WithStopWait(20*time.Millisecond))
	l.Start()
	l.Label("s1", "dev")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the read did not start")
	}
	l.Stop()
	reads := f.reads.Load()
	close(f.gate)
	time.Sleep(100 * time.Millisecond)
	if l.size() != 0 {
		t.Fatalf("a read that ended after Stop wrote %d entries", l.size())
	}
	if f.reads.Load() != reads {
		t.Fatal("the sources were asked again after Stop")
	}
}
