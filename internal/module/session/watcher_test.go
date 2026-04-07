package session

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

func newWatcherTestModule(t *testing.T) (*SessionModule, *tmux.FakeExecutor, *core.EventsBroadcaster) {
	t.Helper()
	meta, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { meta.Close() })

	fake := tmux.NewFakeExecutor()
	mod := NewSessionModule(meta)
	c := core.New(core.CoreDeps{
		Tmux:     fake,
		Registry: core.NewServiceRegistry(),
	})
	require.NoError(t, mod.Init(c))
	return mod, fake, c.Events
}

func TestWatcherTmuxAliveInitialState(t *testing.T) {
	mod, _, _ := newWatcherTestModule(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))
	assert.True(t, mod.TmuxAlive(), "tmux should be alive when FakeExecutor default alive=true")
}

func TestWatcherTransitionsToTmuxDown(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))

	fake.SetAlive(false)
	mod.checkAndBroadcast()
	assert.False(t, mod.TmuxAlive())

	select {
	case msg := <-sub.SendCh():
		assert.Contains(t, string(msg), `"type":"tmux"`)
		assert.Contains(t, string(msg), `"value":"unavailable"`)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected tmux unavailable broadcast")
	}
}

func TestWatcherRecoverFromTmuxDown(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))

	fake.SetAlive(false)
	mod.checkAndBroadcast()
	assert.False(t, mod.TmuxAlive())
	<-sub.SendCh()

	fake.SetAlive(true)
	fake.AddSession("recovered", "/tmp")
	mod.checkAndBroadcast()
	assert.True(t, mod.TmuxAlive())

	select {
	case msg := <-sub.SendCh():
		assert.Contains(t, string(msg), `"type":"tmux"`)
		assert.Contains(t, string(msg), `"value":"ok"`)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected tmux ok broadcast")
	}
}

func TestWatcherNilSessionsWithTmuxAlive(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fake.SetAlive(true)
	require.NoError(t, mod.Start(ctx))

	mod.checkAndBroadcast()
	assert.True(t, mod.TmuxAlive())
}

// TestBroadcastSessionsDebounce verifies that rapid concurrent calls to
// broadcastSessions() within the 500ms window result in only one broadcast.
func TestBroadcastSessionsDebounce(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fake.AddSession("s1", "/tmp")
	require.NoError(t, mod.Start(ctx))

	// Call broadcastSessions twice back-to-back within the debounce window.
	mod.broadcastSessions()
	mod.broadcastSessions()

	// Only one broadcast should have been sent.
	count := 0
	timeout := time.After(100 * time.Millisecond)
drain:
	for {
		select {
		case msg := <-sub.SendCh():
			if len(msg) > 0 {
				count++
			}
		case <-timeout:
			break drain
		}
	}
	assert.Equal(t, 1, count, "debounce should suppress second broadcast within 500ms window")
}

// TestBroadcastSessionsDebounceExpiry verifies that a second call after the
// debounce window has passed DOES produce a broadcast.
func TestBroadcastSessionsDebounceExpiry(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fake.AddSession("s1", "/tmp")
	require.NoError(t, mod.Start(ctx))

	// First call sets the lastBroadcast timestamp.
	mod.broadcastSessions()

	// Drain first broadcast.
	select {
	case <-sub.SendCh():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected first broadcast")
	}

	// Manually expire the debounce window by backdating lastBroadcast.
	mod.wstate.mu.Lock()
	mod.wstate.lastBroadcast = mod.wstate.lastBroadcast.Add(-600 * time.Millisecond)
	mod.wstate.mu.Unlock()

	// Second call after window expiry should go through.
	mod.broadcastSessions()

	select {
	case msg := <-sub.SendCh():
		assert.Contains(t, string(msg), `"type":"sessions"`, "second broadcast should contain sessions event")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected second broadcast after debounce expiry")
	}
}

func TestWatcherNoRepeatBroadcastInTmuxDown(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))

	fake.SetAlive(false)
	mod.checkAndBroadcast()
	<-sub.SendCh()

	mod.checkAndBroadcast()

	select {
	case <-sub.SendCh():
		t.Fatal("should not broadcast tmux unavailable twice in a row")
	case <-time.After(50 * time.Millisecond):
	}
}
