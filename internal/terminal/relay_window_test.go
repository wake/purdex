package terminal_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/terminal"
)

type windowFrame struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func dialRelay(t *testing.T, relay *terminal.Relay) (*websocket.Conn, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(relay.HandleWebSocket))
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	return ws, func() { ws.Close(); srv.Close() }
}

// readWindowFrames collects text window frames in the background.
type frameLog struct {
	mu     sync.Mutex
	frames []windowFrame
	binary int
}

func collect(ws *websocket.Conn) *frameLog {
	l := &frameLog{}
	go func() {
		for {
			typ, msg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			l.mu.Lock()
			if typ == websocket.TextMessage {
				var f windowFrame
				if json.Unmarshal(msg, &f) == nil {
					l.frames = append(l.frames, f)
				}
			} else {
				l.binary++
			}
			l.mu.Unlock()
		}
	}()
	return l
}

func (l *frameLog) snapshot() ([]windowFrame, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]windowFrame(nil), l.frames...), l.binary
}

func TestRelayWindow_SendsInitialThenOnlyOnChange(t *testing.T) {
	var cols atomic.Uint32
	cols.Store(100)
	relay := terminal.NewRelay("cat", nil, "/tmp")
	relay.WindowPollInterval = 5 * time.Millisecond
	relay.WindowSize = func(ctx context.Context) (uint16, uint16, error) {
		return uint16(cols.Load()), 30, nil
	}
	ws, closeAll := dialRelay(t, relay)
	defer closeAll()
	log := collect(ws)

	require.Eventually(t, func() bool { f, _ := log.snapshot(); return len(f) == 1 }, 2*time.Second, 5*time.Millisecond)
	time.Sleep(100 * time.Millisecond) // many poll ticks, value unchanged
	f, _ := log.snapshot()
	require.Len(t, f, 1)
	assert.Equal(t, windowFrame{"window", 100, 30}, f[0])

	cols.Store(120)
	require.Eventually(t, func() bool { f, _ := log.snapshot(); return len(f) == 2 }, 2*time.Second, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	f, _ = log.snapshot()
	require.Len(t, f, 2)
	assert.Equal(t, windowFrame{"window", 120, 30}, f[1])
}

func TestRelayWindow_QueryErrorSendsNothingKeepsConnection(t *testing.T) {
	var calls atomic.Int32
	relay := terminal.NewRelay("cat", nil, "/tmp")
	relay.WindowPollInterval = 5 * time.Millisecond
	relay.WindowSize = func(ctx context.Context) (uint16, uint16, error) {
		calls.Add(1)
		return 0, 0, errors.New("tmux gone")
	}
	ws, closeAll := dialRelay(t, relay)
	defer closeAll()
	log := collect(ws)

	require.Eventually(t, func() bool { return calls.Load() >= 3 }, 2*time.Second, 5*time.Millisecond)
	require.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte("hi\n")))
	require.Eventually(t, func() bool { _, b := log.snapshot(); return b > 0 }, 2*time.Second, 5*time.Millisecond)
	f, _ := log.snapshot()
	assert.Empty(t, f)
}

func TestRelayWindow_NotConfiguredSendsNoText(t *testing.T) {
	relay := terminal.NewRelay("cat", nil, "/tmp")
	ws, closeAll := dialRelay(t, relay)
	defer closeAll()
	log := collect(ws)
	require.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte("hi\n")))
	require.Eventually(t, func() bool { _, b := log.snapshot(); return b > 0 }, 2*time.Second, 5*time.Millisecond)
	f, _ := log.snapshot()
	assert.Empty(t, f)
}

func TestRelayWindow_BlockedQueryStopsOnClose(t *testing.T) {
	entered := make(chan struct{}, 1)
	returned := make(chan struct{})
	relay := terminal.NewRelay("cat", nil, "/tmp")
	relay.WindowPollInterval = 5 * time.Millisecond
	relay.WindowSize = func(ctx context.Context) (uint16, uint16, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		select {
		case <-returned:
		default:
			close(returned)
		}
		return 0, 0, ctx.Err()
	}
	ws, closeAll := dialRelay(t, relay)
	defer closeAll()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("query never started")
	}
	ws.Close()
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("blocked query not cancelled after websocket close")
	}
}

// Text window frames and binary PTY output share one writer; run with -race.
func TestRelayWindow_ConcurrentTextAndBinaryWrites(t *testing.T) {
	var n atomic.Uint32
	relay := terminal.NewRelay("cat", nil, "/tmp")
	relay.WindowPollInterval = time.Millisecond
	relay.WindowSize = func(ctx context.Context) (uint16, uint16, error) {
		return uint16(n.Add(1)%500) + 1, 24, nil // changes every poll
	}
	ws, closeAll := dialRelay(t, relay)
	defer closeAll()
	log := collect(ws)

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		require.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte("xxxxxxxxxxxxxxxx\n")))
		time.Sleep(time.Millisecond)
	}
	f, b := log.snapshot()
	assert.Greater(t, len(f), 10)
	assert.Greater(t, b, 0)
}
