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
