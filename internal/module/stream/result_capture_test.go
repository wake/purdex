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

// TestCaptureResult_UnitStoresIsErrorAndSubtype exercises the one-shot capture
// directly (no WS), pinning the parse of result.is_error / result.subtype.
func TestCaptureResult_UnitStoresIsErrorAndSubtype(t *testing.T) {
	m := &StreamModule{}
	captured := false

	// non-result line: ignored, LastResult stays empty.
	m.captureResult("x", []byte(`{"type":"assistant","content":"hi"}`), &captured)
	_, ok := m.LastResult("x")
	assert.False(t, ok)
	assert.False(t, captured)

	// result line: captured.
	m.captureResult("x", []byte(`{"type":"result","subtype":"error_max_turns","is_error":true}`), &captured)
	got, ok := m.LastResult("x")
	require.True(t, ok)
	assert.True(t, got.IsError)
	assert.Equal(t, "error_max_turns", got.Subtype)
	assert.True(t, captured)
}

// TestCaptureResult_OneShot: once captured, a later result line does not overwrite.
func TestCaptureResult_OneShot(t *testing.T) {
	m := &StreamModule{}
	captured := false
	m.captureResult("y", []byte(`{"type":"result","subtype":"success","is_error":false}`), &captured)
	m.captureResult("y", []byte(`{"type":"result","subtype":"error","is_error":true}`), &captured)
	got, ok := m.LastResult("y")
	require.True(t, ok)
	assert.False(t, got.IsError)
	assert.Equal(t, "success", got.Subtype)
}

// TestResultLine_FannedOutAndCaptured_NotPrematureTerminal proves the additive
// result capture (a) still fans the result line out to subscribers, (b) records
// it for LastResult, and (c) does NOT fire the terminal seam — the timepoint is
// process exit only (spec §5.3), so a result before exit must not be terminal.
func TestResultLine_FannedOutAndCaptured_NotPrematureTerminal(t *testing.T) {
	sessions := map[string]*session.SessionInfo{
		"res123": {Code: "res123", Name: "res-sess", Mode: "stream"},
	}
	m, _, srv := setupStreamModule(t, sessions)

	terminalFired := make(chan relay.TerminalEvent, 1)
	m.SetTerminalHandler(func(_ string, ev relay.TerminalEvent) { terminalFired <- ev })

	relayConn := dial(t, wsURL(srv, "/ws/cli-bridge/res123"))
	defer relayConn.Close()
	time.Sleep(50 * time.Millisecond)

	sub := dial(t, wsURL(srv, "/ws/cli-bridge-sub/res123"))
	defer sub.Close()

	resultLine := `{"type":"result","subtype":"success","is_error":false}`
	require.NoError(t, relayConn.WriteMessage(websocket.TextMessage, []byte(resultLine)))

	// (a) the result line still reaches the subscriber (fan-out unchanged).
	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := sub.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, resultLine, string(msg))

	// (b) captured for LastResult.
	require.Eventually(t, func() bool {
		got, ok := m.LastResult("res123")
		return ok && got.Subtype == "success" && !got.IsError
	}, 2*time.Second, 20*time.Millisecond)

	// (c) no terminal fired from a result alone.
	select {
	case <-terminalFired:
		t.Fatal("terminal seam fired on a result line before process exit")
	case <-time.After(200 * time.Millisecond):
	}

	// Now the real process-exit frame → terminal fires, result still readable.
	payload, err := relay.MarshalTerminalFrame(relay.TerminalEvent{ExitCode: 0})
	require.NoError(t, err)
	require.NoError(t, relayConn.WriteMessage(websocket.BinaryMessage, payload))
	select {
	case ev := <-terminalFired:
		assert.Equal(t, 0, ev.ExitCode)
	case <-time.After(2 * time.Second):
		t.Fatal("terminal seam did not fire on process-exit frame")
	}
	got, ok := m.LastResult("res123")
	require.True(t, ok)
	assert.Equal(t, "success", got.Subtype)
}
