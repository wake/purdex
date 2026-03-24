package stream

import (
	"context"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/wake/tmux-box/internal/module/session"
	"github.com/wake/tmux-box/internal/relay"
)

// TestE2EPipelineSPAThroughRelay verifies the full message pipeline:
// SPA WS → daemon bridge → relay WS → subprocess stdin → stdout → relay WS → bridge → SPA WS
func TestE2EPipelineSPAThroughRelay(t *testing.T) {
	sessions := map[string]*session.SessionInfo{
		"e2e001": {Code: "e2e001", Name: "e2e-test", Mode: "stream"},
	}
	_, _, srv := setupStreamModule(t, sessions)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start relay with "cat" as subprocess (echoes stdin lines to stdout)
	r := &relay.Relay{
		SessionCode: "e2e001",
		DaemonURL:   wsURL(srv, "/ws/cli-bridge/e2e001"),
		Command:     []string{"cat"},
	}
	errCh := make(chan error, 1)
	go func() { errCh <- r.Run(ctx) }()

	time.Sleep(200 * time.Millisecond)

	// Connect subscriber (mock SPA)
	sub := dial(t, wsURL(srv, "/ws/cli-bridge-sub/e2e001"))
	defer sub.Close()

	// SPA sends message → relay subprocess stdin → stdout → relay → bridge → subscriber
	msg := `{"type":"user","message":{"role":"user","content":"ping"}}`
	if err := sub.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatalf("subscriber write: %v", err)
	}

	sub.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, got, err := sub.ReadMessage()
	if err != nil {
		t.Fatalf("subscriber read: %v — message did not flow through the pipeline", err)
	}
	if string(got) != msg {
		t.Fatalf("got %q, want %q", got, msg)
	}

	cancel()
	<-errCh
}

// TestE2EMultipleMessages verifies multiple sequential messages flow through.
func TestE2EMultipleMessages(t *testing.T) {
	sessions := map[string]*session.SessionInfo{
		"e2e002": {Code: "e2e002", Name: "e2e-multi", Mode: "stream"},
	}
	_, _, srv := setupStreamModule(t, sessions)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := &relay.Relay{
		SessionCode: "e2e002",
		DaemonURL:   wsURL(srv, "/ws/cli-bridge/e2e002"),
		Command:     []string{"cat"},
	}
	errCh := make(chan error, 1)
	go func() { errCh <- r.Run(ctx) }()

	time.Sleep(200 * time.Millisecond)

	sub := dial(t, wsURL(srv, "/ws/cli-bridge-sub/e2e002"))
	defer sub.Close()

	messages := []string{
		`{"type":"user","message":{"role":"user","content":"first"}}`,
		`{"type":"user","message":{"role":"user","content":"second"}}`,
		`{"type":"user","message":{"role":"user","content":"third"}}`,
	}

	for _, msg := range messages {
		if err := sub.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	for i, want := range messages {
		sub.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, got, err := sub.ReadMessage()
		if err != nil {
			t.Fatalf("message %d: read: %v", i, err)
		}
		if string(got) != want {
			t.Fatalf("message %d: got %q, want %q", i, got, want)
		}
	}

	cancel()
	<-errCh
}
