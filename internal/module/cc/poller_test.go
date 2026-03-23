package cc

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/detect"
	"github.com/wake/tmux-box/internal/module/session"
)

func TestPoller_BroadcastsOnChange(t *testing.T) {
	c, fake := newTestCoreWithSession(t)
	// Use a fast poll interval for tests
	c.Cfg.Detect.PollInterval = 1

	mod := New()
	require.NoError(t, mod.Init(c))

	// Override sessions with a fake provider containing one session
	mod.sessions = &fakeSessionProvider{
		sessions: map[string]*session.SessionInfo{
			"abc123": {
				Code: "abc123",
				Name: "test-sess",
			},
		},
	}

	// Set up fake tmux: "test-sess:0" has claude running and is idle
	fake.SetPaneCommand("test-sess:0", "claude")
	fake.SetPaneContent("test-sess:0", "some output\n❯ ")

	// Add a test subscriber so HasSubscribers() returns true
	sub := c.Events.AddTestSubscriber()
	defer c.Events.RemoveTestSubscriber(sub)

	// Start poller with a cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mod.startPoller(ctx)

	// Wait for at least one poll tick
	msg := readOne(t, sub, 3*time.Second)
	require.NotNil(t, msg, "should have received a broadcast")

	var evt core.SessionEvent
	require.NoError(t, json.Unmarshal(msg, &evt))
	assert.Equal(t, "status", evt.Type)
	assert.Equal(t, "abc123", evt.Session)
	assert.Equal(t, string(detect.StatusCCIdle), evt.Value)

	// Now change the status — CC starts running
	fake.SetPaneContent("test-sess:0", "Running tool...")

	// Wait for another poll tick
	msg = readOne(t, sub, 3*time.Second)
	require.NotNil(t, msg, "should have received a second broadcast on status change")

	require.NoError(t, json.Unmarshal(msg, &evt))
	assert.Equal(t, "status", evt.Type)
	assert.Equal(t, "abc123", evt.Session)
	assert.Equal(t, string(detect.StatusCCRunning), evt.Value)
}

func TestPoller_SkipsWhenNoSubscribers(t *testing.T) {
	c, fake := newTestCoreWithSession(t)
	c.Cfg.Detect.PollInterval = 1

	mod := New()
	require.NoError(t, mod.Init(c))

	mod.sessions = &fakeSessionProvider{
		sessions: map[string]*session.SessionInfo{
			"abc123": {
				Code: "abc123",
				Name: "test-sess",
			},
		},
	}

	fake.SetPaneCommand("test-sess:0", "claude")
	fake.SetPaneContent("test-sess:0", "some output\n❯ ")

	// Do NOT add any subscriber — HasSubscribers() returns false

	ctx, cancel := context.WithCancel(context.Background())
	mod.startPoller(ctx)

	// Wait for several ticks
	time.Sleep(2500 * time.Millisecond)
	cancel()

	// No subscribers → PaneCurrentCommand should never have been called
	assert.Zero(t, fake.PaneCommandCallCount("test-sess:0"),
		"detector should not have been called when no subscribers")
}

func TestPoller_NoDuplicateBroadcastForSameStatus(t *testing.T) {
	c, fake := newTestCoreWithSession(t)
	c.Cfg.Detect.PollInterval = 1

	mod := New()
	require.NoError(t, mod.Init(c))

	mod.sessions = &fakeSessionProvider{
		sessions: map[string]*session.SessionInfo{
			"abc123": {
				Code: "abc123",
				Name: "test-sess",
			},
		},
	}

	fake.SetPaneCommand("test-sess:0", "claude")
	fake.SetPaneContent("test-sess:0", "some output\n❯ ")

	sub := c.Events.AddTestSubscriber()
	defer c.Events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	mod.startPoller(ctx)

	// Wait for multiple ticks (at 1s interval, wait 3.5s for ~3 ticks)
	time.Sleep(3500 * time.Millisecond)
	cancel()

	// Drain all messages — should only have one (initial detection)
	msgs := drainAll(sub)
	assert.Len(t, msgs, 1,
		"should broadcast only once for unchanged status (got %d)", len(msgs))
}

func TestPoller_SendStatusSnapshot(t *testing.T) {
	c, fake := newTestCoreWithSession(t)

	mod := New()
	require.NoError(t, mod.Init(c))

	mod.sessions = &fakeSessionProvider{
		sessions: map[string]*session.SessionInfo{
			"abc123": {
				Code: "abc123",
				Name: "test-sess",
			},
			"def456": {
				Code: "def456",
				Name: "other-sess",
			},
		},
	}

	fake.SetPaneCommand("test-sess:0", "claude")
	fake.SetPaneContent("test-sess:0", "some output\n❯ ")
	fake.SetPaneCommand("other-sess:0", "zsh")

	// Create a test subscriber and call sendStatusSnapshot directly
	sub := c.Events.AddTestSubscriber()
	defer c.Events.RemoveTestSubscriber(sub)

	mod.sendStatusSnapshot(sub)

	// Should get status for both sessions
	msgs := drainAll(sub)
	require.Len(t, msgs, 2, "should send snapshot for each session")

	events := make(map[string]core.SessionEvent)
	for _, msg := range msgs {
		var evt core.SessionEvent
		require.NoError(t, json.Unmarshal(msg, &evt))
		events[evt.Session] = evt
	}

	assert.Equal(t, string(detect.StatusCCIdle), events["abc123"].Value)
	assert.Equal(t, string(detect.StatusNormal), events["def456"].Value)
}

// --- test helpers ---

// readOne reads one message from the subscriber's send channel with a timeout.
func readOne(t *testing.T, sub *core.EventSubscriber, timeout time.Duration) []byte {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case msg := <-sub.SendCh():
		return msg
	case <-timer.C:
		return nil
	}
}

// drainAll reads all pending messages from the subscriber's send channel.
func drainAll(sub *core.EventSubscriber) [][]byte {
	var msgs [][]byte
	for {
		select {
		case msg := <-sub.SendCh():
			msgs = append(msgs, msg)
		default:
			return msgs
		}
	}
}
