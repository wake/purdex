package session

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/tmux"
)

// Against a real tmux: a mirror that is the ONLY attached client, and that
// sends a phone-sized resize, must leave the window exactly as it was. (tmux
// sizes a window from even a lone ignore-size client, so this only holds
// because the PTY is slaved to window size + status rows.)
func TestMirrorSoleClient_DoesNotResizeWindow(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// A private tmux server: never touch the developer's own sessions.
	// (Short path: a unix socket path is capped at ~100 bytes.)
	tmpDir, err := os.MkdirTemp("/tmp", "pdxmt")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	t.Setenv("TMUX_TMPDIR", tmpDir)
	t.Setenv("TMUX", "")
	os.Unsetenv("TMUX")
	tm := func(args ...string) string {
		out, err := exec.Command("tmux", args...).CombinedOutput()
		require.NoError(t, err, "tmux %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	tm("new-session", "-d", "-s", "mt", "-x", "150", "-y", "44")
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-server").Run() })
	tm("set-option", "-w", "-t", "mt", "window-size", "latest")
	size := func() string { return tm("display-message", "-p", "-t", "mt", "#{window_width}x#{window_height}") }
	require.Equal(t, "150x44", size())

	relay := newTerminalRelay(tmux.NewRealExecutor(), "mt", "auto", true)
	srv := httptest.NewServer(http.HandlerFunc(relay.HandleWebSocket))
	defer srv.Close()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	defer ws.Close()

	// The first frame is the window report: 150x44.
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	typ, msg, err := ws.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, typ)
	var f struct{ Cols, Rows int }
	require.NoError(t, json.Unmarshal(msg, &f))
	require.Equal(t, [2]int{150, 44}, [2]int{f.Cols, f.Rows})

	// The phone's size, then input so the client counts as active.
	require.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":83,"rows":55}`)))
	require.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte("\r")))
	time.Sleep(1500 * time.Millisecond)
	require.Equal(t, "150x44", size(), "a sole mirror client must not change the window")
	require.Equal(t, "ignore-size", flagsContain(tm("list-clients", "-t", "mt", "-F", "#{client_flags}")))
}

func flagsContain(flags string) string {
	if strings.Contains(flags, "ignore-size") {
		return "ignore-size"
	}
	return flags
}
