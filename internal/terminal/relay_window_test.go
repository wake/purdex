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

// The client must learn the window size before it sees any terminal output:
// even with output ready immediately and a slow first query, the first frame
// on the wire is the window text frame.
func TestRelayWindow_FirstFrameIsWindowBeforePTYOutput(t *testing.T) {
	relay := terminal.NewRelay("sh", []string{"-c", "printf hello; cat"}, "/tmp")
	relay.WindowSize = func(ctx context.Context) (uint16, uint16, error) {
		time.Sleep(200 * time.Millisecond)
		return 100, 30, nil
	}
	ws, closeAll := dialRelay(t, relay)
	defer closeAll()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	typ, msg, err := ws.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, typ, "first frame must be the window frame, got %q", msg)
	var f windowFrame
	require.NoError(t, json.Unmarshal(msg, &f))
	assert.Equal(t, windowFrame{"window", 100, 30}, f)
}

// One stuck query must not stall reporting: it is cut at its own deadline and
// the next tick still reports.
func TestRelayWindow_StuckQueryTimesOutAndRetries(t *testing.T) {
	var calls atomic.Int32
	relay := terminal.NewRelay("cat", nil, "/tmp")
	relay.WindowPollInterval = 10 * time.Millisecond
	relay.WindowQueryTimeout = 50 * time.Millisecond
	relay.WindowSize = func(ctx context.Context) (uint16, uint16, error) {
		if calls.Add(1) == 1 {
			<-ctx.Done() // a hung tmux: only its own deadline frees it
			return 0, 0, ctx.Err()
		}
		return 90, 25, nil
	}
	ws, closeAll := dialRelay(t, relay)
	defer closeAll()
	log := collect(ws)
	require.Eventually(t, func() bool { f, _ := log.snapshot(); return len(f) == 1 }, 2*time.Second, 5*time.Millisecond)
	f, _ := log.snapshot()
	assert.Equal(t, windowFrame{"window", 90, 25}, f[0])
}

// readUntil reads binary output until it contains want (or fails on timeout).
func readUntil(t *testing.T, ws *websocket.Conn, want string) string {
	t.Helper()
	var got strings.Builder
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for !strings.Contains(got.String(), want) {
		typ, msg, err := ws.ReadMessage()
		require.NoError(t, err, "waiting for %q, got %q", want, got.String())
		if typ == websocket.BinaryMessage {
			got.Write(msg)
		}
	}
	return got.String()
}

// A mirror's PTY is sized from the window up front (rows include the status
// bar) and the client's own resize is ignored: tmux lets even a lone
// ignore-size client decide the window, so any other size would shrink it.
func TestRelayPTYSize_InitialAndIgnoresClientResize(t *testing.T) {
	relay := terminal.NewRelay("sh", []string{"-c", "while read l; do stty size; done"}, "/tmp")
	relay.WindowPollInterval = 5 * time.Millisecond
	relay.WindowSize = func(ctx context.Context) (uint16, uint16, error) { return 100, 30, nil }
	relay.StatusRows = func(ctx context.Context) (uint16, error) { return 1, nil }
	ws, closeAll := dialRelay(t, relay)
	defer closeAll()

	require.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":40,"rows":20}`)))
	require.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte("x\n")))
	out := readUntil(t, ws, "31 100")
	assert.NotContains(t, out, "20 40")
}

// The window frame also carries the PTY's actual size, so a client can lay its
// grid out exactly as tmux draws it; a PTY-only change (status bar toggled)
// is reported too.
func TestRelayPTYSize_FrameCarriesPTYSizeAndReportsPTYOnlyChange(t *testing.T) {
	var status atomic.Uint32
	status.Store(1)
	relay := terminal.NewRelay("cat", nil, "/tmp")
	relay.WindowPollInterval = 5 * time.Millisecond
	relay.WindowSize = func(ctx context.Context) (uint16, uint16, error) { return 100, 30, nil }
	relay.StatusRows = func(ctx context.Context) (uint16, error) { return uint16(status.Load()), nil }
	ws, closeAll := dialRelay(t, relay)
	defer closeAll()

	type frame struct {
		Type    string `json:"type"`
		Cols    int    `json:"cols"`
		Rows    int    `json:"rows"`
		PTYCols int    `json:"pty_cols"`
		PTYRows int    `json:"pty_rows"`
	}
	next := func() frame {
		_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
		for {
			typ, msg, err := ws.ReadMessage()
			require.NoError(t, err)
			if typ != websocket.TextMessage {
				continue
			}
			var f frame
			require.NoError(t, json.Unmarshal(msg, &f))
			return f
		}
	}
	assert.Equal(t, frame{"window", 100, 30, 100, 31}, next())

	status.Store(2) // window unchanged, status bar grew
	assert.Equal(t, frame{"window", 100, 30, 100, 32}, next())
}

func TestRelayPTYSize_FollowsWindowChanges(t *testing.T) {
	var rows atomic.Uint32
	rows.Store(30)
	relay := terminal.NewRelay("sh", []string{"-c", "while read l; do stty size; done"}, "/tmp")
	relay.WindowPollInterval = 5 * time.Millisecond
	relay.WindowSize = func(ctx context.Context) (uint16, uint16, error) { return 100, uint16(rows.Load()), nil }
	relay.StatusRows = func(ctx context.Context) (uint16, error) { return 1, nil }
	ws, closeAll := dialRelay(t, relay)
	defer closeAll()

	rows.Store(50)
	time.Sleep(100 * time.Millisecond) // several ticks
	require.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte("x\n")))
	readUntil(t, ws, "51 100")
}

// If the window size cannot be learned the mirror must not start a PTY at a
// guessed size (it could shrink the desktop window): the connection is refused.
func TestRelayPTYSize_FailureRefusesConnection(t *testing.T) {
	relay := terminal.NewRelay("cat", nil, "/tmp")
	relay.WindowSize = func(ctx context.Context) (uint16, uint16, error) { return 100, 30, nil }
	relay.StatusRows = func(ctx context.Context) (uint16, error) { return 0, errors.New("tmux gone") }
	ws, closeAll := dialRelay(t, relay)
	defer closeAll()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err := ws.ReadMessage()
	require.Error(t, err)
	assert.True(t, websocket.IsCloseError(err, websocket.CloseInternalServerErr), "got %v", err)
}

// Each tmux query has its own deadline and a tick reads the window once: two
// queries that are each under the timeout but together over it must still
// report (they used to share one context, and PTYSize re-read the window).
func TestRelayPTYSize_EachQueryHasItsOwnDeadlineAndWindowIsReadOncePerTick(t *testing.T) {
	var windowCalls, statusCalls atomic.Int32
	relay := terminal.NewRelay("cat", nil, "/tmp")
	relay.WindowPollInterval = 10 * time.Millisecond
	relay.WindowQueryTimeout = 100 * time.Millisecond
	relay.WindowSize = func(ctx context.Context) (uint16, uint16, error) {
		windowCalls.Add(1)
		time.Sleep(60 * time.Millisecond)
		return 100, 30, nil
	}
	relay.StatusRows = func(ctx context.Context) (uint16, error) {
		statusCalls.Add(1)
		time.Sleep(60 * time.Millisecond) // 60+60 > 100: only separate deadlines pass
		return 1, nil
	}
	ws, closeAll := dialRelay(t, relay)
	defer closeAll()
	log := collect(ws)
	require.Eventually(t, func() bool { f, _ := log.snapshot(); return len(f) == 1 }, 3*time.Second, 5*time.Millisecond)
	time.Sleep(300 * time.Millisecond) // a few more ticks
	w, s := windowCalls.Load(), statusCalls.Load()
	// start-up reads the window once for the PTY size, then once per tick.
	assert.InDelta(t, float64(w), float64(s), 1.0, "window=%d status=%d: one status read per window read", w, s)
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
