package stream

import (
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/relay"
)

// terminalSignal pairs a session code with the terminal event delivered to the
// upper-layer seam, for assertion in tests.
type terminalSignal struct {
	code string
	ev   relay.TerminalEvent
}

func TestCliBridge_TerminalEventReachesSeam(t *testing.T) {
	sessions := map[string]*session.SessionInfo{
		"trm123": {Code: "trm123", Name: "term-sess", Mode: "stream"},
	}
	m, _, srv := setupStreamModule(t, sessions)

	got := make(chan terminalSignal, 4)
	m.SetTerminalHandler(func(code string, ev relay.TerminalEvent) {
		got <- terminalSignal{code: code, ev: ev}
	})

	relayConn := dial(t, wsURL(srv, "/ws/cli-bridge/trm123"))
	defer relayConn.Close()
	time.Sleep(50 * time.Millisecond)

	// Emit a structured terminal frame the way the relay client does.
	payload, err := relay.MarshalTerminalFrame(relay.TerminalEvent{ExitCode: 7})
	require.NoError(t, err)
	require.NoError(t, relayConn.WriteMessage(websocket.BinaryMessage, payload))

	select {
	case sig := <-got:
		assert.Equal(t, "trm123", sig.code)
		assert.Equal(t, relay.TerminalEventType, sig.ev.Type)
		assert.Equal(t, 7, sig.ev.ExitCode)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for terminal signal on seam")
	}
}

func TestCliBridge_TerminalFrameNotFannedOutToSubscribers(t *testing.T) {
	sessions := map[string]*session.SessionInfo{
		"trm456": {Code: "trm456", Name: "term-sess2", Mode: "stream"},
	}
	m, _, srv := setupStreamModule(t, sessions)
	m.SetTerminalHandler(func(string, relay.TerminalEvent) {})

	relayConn := dial(t, wsURL(srv, "/ws/cli-bridge/trm456"))
	defer relayConn.Close()
	time.Sleep(50 * time.Millisecond)

	sub := dial(t, wsURL(srv, "/ws/cli-bridge-sub/trm456"))
	defer sub.Close()

	// Send a terminal frame (must be intercepted, NOT fanned out), then a normal
	// stream-json text line (must reach the subscriber).
	payload, err := relay.MarshalTerminalFrame(relay.TerminalEvent{ExitCode: 0})
	require.NoError(t, err)
	require.NoError(t, relayConn.WriteMessage(websocket.BinaryMessage, payload))
	require.NoError(t, relayConn.WriteMessage(websocket.TextMessage, []byte(`{"type":"assistant","content":"hi"}`)))

	// The first (and only) message the subscriber sees must be the text line,
	// proving the terminal frame was not fanned out.
	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	mt, msg, err := sub.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, websocket.TextMessage, mt)
	assert.Equal(t, `{"type":"assistant","content":"hi"}`, string(msg))
}

func TestCliBridge_NoTerminalHandler_DoesNotPanic(t *testing.T) {
	sessions := map[string]*session.SessionInfo{
		"trm789": {Code: "trm789", Name: "term-sess3", Mode: "stream"},
	}
	_, _, srv := setupStreamModule(t, sessions)

	relayConn := dial(t, wsURL(srv, "/ws/cli-bridge/trm789"))
	defer relayConn.Close()
	time.Sleep(50 * time.Millisecond)

	payload, err := relay.MarshalTerminalFrame(relay.TerminalEvent{ExitCode: 0})
	require.NoError(t, err)
	require.NoError(t, relayConn.WriteMessage(websocket.BinaryMessage, payload))

	// Give the read loop time to process; absence of a registered handler must
	// be a no-op, not a panic.
	time.Sleep(100 * time.Millisecond)
}
