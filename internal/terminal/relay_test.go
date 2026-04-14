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
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/terminal"
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

func TestPingIsSentByRelay(t *testing.T) {
	relay := terminal.NewRelay("cat", []string{}, "/tmp")
	relay.PingInterval = 100 * time.Millisecond
	relay.PongTimeout = 50 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relay.HandleWebSocket(w, r)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer ws.Close()

	pingReceived := make(chan struct{}, 1)
	ws.SetPingHandler(func(msg string) error {
		select {
		case pingReceived <- struct{}{}:
		default:
		}
		// Reply with pong to keep connection alive
		return ws.WriteControl(websocket.PongMessage, []byte(msg), time.Now().Add(time.Second))
	})

	// Must drain read loop for control frames to be processed
	go func() {
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}()

	select {
	case <-pingReceived:
		// OK — relay sent a ping
	case <-time.After(time.Second):
		t.Fatal("expected ping from relay within 1s")
	}
}

func TestPongTimeoutClosesRelayConnection(t *testing.T) {
	relay := terminal.NewRelay("cat", []string{}, "/tmp")
	relay.PingInterval = 100 * time.Millisecond
	relay.PongTimeout = 50 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relay.HandleWebSocket(w, r)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer ws.Close()

	// Swallow pings without sending pong — server should close us
	ws.SetPingHandler(func(string) error {
		return nil // no pong reply
	})

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}()

	select {
	case <-closed:
		// OK — connection was closed by server after pong timeout
	case <-time.After(2 * time.Second):
		t.Fatal("expected server to close connection after pong timeout")
	}
}
