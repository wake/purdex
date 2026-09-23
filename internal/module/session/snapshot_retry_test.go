package session

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/tmux"
)

// waitRetriesDone waits for every background snapshot retry to have exited.
func waitRetriesDone(t *testing.T, mod *SessionModule) {
	t.Helper()
	require.Eventually(t, func() bool { return mod.snapshotRetriesLive.Load() == 0 },
		3*time.Second, 5*time.Millisecond, "a snapshot retry goroutine did not exit")
}

// #1293 R2: the list is stable (no push will ever come), the subscribe
// snapshot's read times out, then tmux recovers. The connection must still
// get its first sessions frame — the SPA keeps its attach gate shut until it
// does — and the retry goroutine must then exit.
func TestSubscribeSnapshot_RetriedAfterTimeoutWithNoSessionChange(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	fake.AddSession("s1", "/tmp")
	mod.listTimeout = 100 * time.Millisecond
	mod.snapshotRetryDelays = []time.Duration{20 * time.Millisecond, 20 * time.Millisecond, 20 * time.Millisecond}
	fake.SetReadHook(tmux.BlockReadsUntil(make(chan struct{}), nil))

	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)
	mod.sendSessionsSnapshot(sub)
	assert.Empty(t, drainSessionFrames(t, sub), "the first snapshot read timed out")

	fake.SetReadHook(nil) // tmux recovers; nothing about the list changes

	deadline := time.After(3 * time.Second)
	for {
		frames := drainSessionFrames(t, sub)
		if len(frames) > 0 {
			require.Len(t, frames, 1)
			assert.Equal(t, mod.epoch, frames[0].Epoch)
			assert.GreaterOrEqual(t, frames[0].Seq, uint64(1))
			assert.Contains(t, frames[0].Value, `"name":"s1"`)
			break
		}
		select {
		case <-deadline:
			t.Fatal("the connection never got a sessions frame")
		default:
		}
	}
	waitRetriesDone(t, mod)
	select {
	case <-sub.Done():
		t.Fatal("a recovered snapshot must not close the connection")
	default:
	}
}

// Every retry fails too: the connection is closed so the client reconnects
// (and gets a fresh snapshot on the new connection), and the goroutine exits.
func TestSubscribeSnapshot_AllRetriesFailClosesTheConnection(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	fake.AddSession("s1", "/tmp")
	mod.listTimeout = 50 * time.Millisecond
	mod.snapshotRetryDelays = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}
	fake.SetReadHook(tmux.BlockReadsUntil(make(chan struct{}), nil))

	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)
	mod.sendSessionsSnapshot(sub)

	select {
	case <-sub.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the connection was left open without a snapshot")
	}
	assert.False(t, events.HasSubscribers())
	waitRetriesDone(t, mod)
}

// The connection ends while a retry is waiting: the goroutine exits at once
// and reads nothing more.
func TestSubscribeSnapshot_RetryStopsWhenConnectionEndsWhileWaiting(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	fake.AddSession("s1", "/tmp")
	mod.listTimeout = 50 * time.Millisecond
	mod.snapshotRetryDelays = []time.Duration{time.Hour}
	var reads atomic.Int32
	fake.SetReadHook(func(ctx context.Context, op tmux.ReadOp, target string) error {
		reads.Add(1)
		<-ctx.Done()
		return ctx.Err()
	})

	sub := events.AddTestSubscriber()
	mod.sendSessionsSnapshot(sub)
	require.Equal(t, int32(1), mod.snapshotRetriesLive.Load(), "a retry is pending")
	before := reads.Load()

	events.RemoveTestSubscriber(sub)
	waitRetriesDone(t, mod)
	assert.Equal(t, before, reads.Load(), "no read after the connection ended")
}

// The connection ends while a retry's read is in flight: the read is
// abandoned right away, not left to run out its budget.
func TestSubscribeSnapshot_RetryReadAbandonedWhenConnectionEnds(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	fake.AddSession("s1", "/tmp")
	mod.listTimeout = 10 * time.Second
	mod.snapshotRetryDelays = []time.Duration{time.Millisecond}
	var reads atomic.Int32
	inRetry := make(chan struct{})
	fake.SetReadHook(func(ctx context.Context, op tmux.ReadOp, target string) error {
		if op != tmux.ReadListSessions {
			return nil
		}
		if reads.Add(1) == 1 {
			return errors.New("first read: tmux timed out")
		}
		close(inRetry)
		<-ctx.Done()
		return ctx.Err()
	})

	sub := events.AddTestSubscriber()
	mod.sendSessionsSnapshot(sub)
	select {
	case <-inRetry:
	case <-time.After(3 * time.Second):
		t.Fatal("the retry read never started")
	}
	start := time.Now()
	events.RemoveTestSubscriber(sub)
	waitRetriesDone(t, mod)
	assert.Less(t, time.Since(start), 2*time.Second, "the in-flight read ran on after the connection ended")
}
