// internal/terminal/relay_test.go
package terminal_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/wake/tmux-box/internal/terminal"
)

func TestRelayEcho(t *testing.T) {
	// "cat" echoes stdin to stdout via PTY
	relay := terminal.NewRelay("cat", []string{}, "/tmp")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relay.HandleWebSocket(w, r)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	// Send text
	ws.WriteMessage(websocket.TextMessage, []byte("hello\n"))

	// Read echo (PTY echoes input)
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	var received string
	for i := 0; i < 20; i++ {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			break
		}
		received += string(msg)
		if strings.Contains(received, "hello") {
			return // success
		}
	}
	t.Errorf("never received echo, got: %q", received)
}

func TestRelayResize(t *testing.T) {
	relay := terminal.NewRelay("cat", []string{}, "/tmp")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relay.HandleWebSocket(w, r)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	// Send resize message — should not crash
	resize, _ := json.Marshal(terminal.ResizeMsg{Type: "resize", Cols: 120, Rows: 40})
	err = ws.WriteMessage(websocket.TextMessage, resize)
	if err != nil {
		t.Fatal(err)
	}

	// Give it a moment, then verify connection still alive
	time.Sleep(50 * time.Millisecond)
	err = ws.WriteMessage(websocket.TextMessage, []byte("ok\n"))
	if err != nil {
		t.Errorf("connection died after resize: %v", err)
	}
}
