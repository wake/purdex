package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/tmux"
)

// --- Reported tmux value (#1108, #1474 spec D2, D3) ---
//
// The `tmux` wire value means "can tmux be used": ok for a running server
// and for no server at all (creating a session starts one), unavailable
// only for a broken tmux. The internal up/down state machine is unchanged.

// newStatusTestModule seeds the watcher as if its last probe said from, with
// no real wait-for goroutine (newHookTestModule).
func newStatusTestModule(t *testing.T, from tmux.ServerState) (*SessionModule, *tmux.FakeExecutor, *core.EventsBroadcaster) {
	t.Helper()
	mod, fake, events := newHookTestModule(t, from == tmux.ServerUp)
	fake.SetServerState(from)
	mod.recordServerState(from)
	return mod, fake, events
}

// tmuxFrames returns the tmux values broadcast within the drain window,
// ignoring every other frame.
func tmuxFrames(t *testing.T, sub *core.EventSubscriber) []string {
	t.Helper()
	var out []string
	for _, v := range drainTypes(t, sub) {
		if s, ok := strings.CutPrefix(v, "tmux:"); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestReportedTmuxStatus_Transitions(t *testing.T) {
	const (
		up     = tmux.ServerUp
		absent = tmux.ServerAbsent
		broken = tmux.ServerBroken
	)
	cases := []struct {
		name     string
		from, to tmux.ServerState
		want     []string
	}{
		{"up to absent broadcasts nothing", up, absent, nil},
		{"up to broken", up, broken, []string{"unavailable"}},
		{"absent to broken", absent, broken, []string{"unavailable"}},
		{"broken to absent", broken, absent, []string{"ok"}},
		{"broken to up", broken, up, []string{"ok"}},
		{"absent to up broadcasts nothing", absent, up, nil},
		{"up stays up", up, up, nil},
		{"absent stays absent", absent, absent, nil},
		{"broken stays broken", broken, broken, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mod, fake, events := newStatusTestModule(t, tc.from)
			sub := events.AddTestSubscriber()
			defer events.RemoveTestSubscriber(sub)

			fake.SetServerState(tc.to)
			mod.checkAndBroadcast()
			mod.checkAndBroadcast() // a repeated same-state tick adds nothing
			assert.Equal(t, tc.want, tmuxFrames(t, sub))
			// Internal up/down still follows "a server answers" (spec D1).
			assert.Equal(t, tc.to == up, mod.TmuxAlive())
		})
	}
}

// dialEvents opens one /ws/host-events connection (the real path: only it
// runs the OnSubscribe callbacks).
func dialEvents(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return conn
}

// readTmuxFrames reads until the connection has been quiet for quiet and
// returns every tmux value seen, in order.
func readTmuxFrames(t *testing.T, conn *websocket.Conn, quiet time.Duration) []string {
	t.Helper()
	var out []string
	for {
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(quiet)))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return out
		}
		var ev core.HostEvent
		require.NoError(t, json.Unmarshal(msg, &ev))
		if ev.Type == "tmux" {
			out = append(out, ev.Value)
		}
	}
}

// A new subscriber learns the current value at once, even when no edge will
// ever come (#1474 §2: a daemon started with tmux down).
func TestOnSubscribe_SendsReportedTmuxStatus(t *testing.T) {
	for _, tc := range []struct {
		state tmux.ServerState
		want  string
	}{
		{tmux.ServerUp, "ok"},
		{tmux.ServerAbsent, "ok"},
		{tmux.ServerBroken, "unavailable"},
	} {
		t.Run(tc.state.String(), func(t *testing.T) {
			mod, fake, events := newWatcherTestModule(t)
			fake.SetServerState(tc.state)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			require.NoError(t, mod.Start(ctx))

			srv := httptest.NewServer(http.HandlerFunc(events.HandleHostEvents))
			defer srv.Close()
			frames := readTmuxFrames(t, dialEvents(t, srv), 300*time.Millisecond)
			require.NotEmpty(t, frames, "no tmux frame for a new subscriber")
			assert.Equal(t, tc.want, frames[0])
		})
	}
}

// A value change racing a subscribe never leaves the subscriber's last tmux
// frame stale (spec D3: both run under statusMu). Run with -race.
func TestOnSubscribe_ConcurrentChangeNeverStale(t *testing.T) {
	mod, _, events := newWatcherTestModule(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))
	srv := httptest.NewServer(http.HandlerFunc(events.HandleHostEvents))
	defer srv.Close()

	const subs = 8
	conns := make([]*websocket.Conn, subs)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			if i%2 == 0 {
				mod.recordServerState(tmux.ServerBroken)
			} else {
				mod.recordServerState(tmux.ServerAbsent)
			}
		}
		mod.recordServerState(tmux.ServerBroken)
	}()
	for i := range conns {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
			if err == nil {
				conns[i] = conn
			}
		}(i)
	}
	wg.Wait()

	for i, conn := range conns {
		require.NotNil(t, conn, "subscriber %d failed to dial", i)
		defer conn.Close()
		frames := readTmuxFrames(t, conn, 300*time.Millisecond)
		require.NotEmpty(t, frames, "subscriber %d got no tmux frame", i)
		assert.Equal(t, "unavailable", frames[len(frames)-1], "subscriber %d ended on a stale value", i)
	}
}
