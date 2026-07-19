// internal/relay/terminal_test.go
package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// wsFrame captures a single WebSocket frame (type + payload) received by the
// test daemon stand-in.
type wsFrame struct {
	Type int
	Data []byte
}

// collectRelayFrames runs a relay against a stand-in daemon WS server that
// records every frame it receives. It returns the frames observed and the
// error returned by Run.
func collectRelayFrames(t *testing.T, command []string, cancelAfter time.Duration) []wsFrame {
	t.Helper()

	var mu sync.Mutex
	var frames []wsFrame

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			cp := make([]byte, len(msg))
			copy(cp, msg)
			mu.Lock()
			frames = append(frames, wsFrame{Type: mt, Data: cp})
			mu.Unlock()
		}
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[4:]

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	r := &Relay{DaemonURL: wsURL, Command: command}
	errCh := make(chan error, 1)
	go func() { errCh <- r.Run(ctx) }()

	if cancelAfter > 0 {
		time.Sleep(cancelAfter)
		cancel()
	}

	select {
	case <-errCh:
	case <-time.After(8 * time.Second):
		t.Fatal("relay.Run did not return")
	}

	// Give the stand-in server a moment to drain any trailing frame.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	out := make([]wsFrame, len(frames))
	copy(out, frames)
	return out
}

// findTerminalFrame returns the parsed terminal event from the first binary
// out-of-band frame, and whether one was found.
func findTerminalFrame(frames []wsFrame) (TerminalEvent, bool) {
	for _, f := range frames {
		if ev, ok := ParseTerminalEvent(f.Type, f.Data); ok {
			return ev, true
		}
	}
	return TerminalEvent{}, false
}

func TestRelayEmitsTerminalEventOnCleanExit(t *testing.T) {
	frames := collectRelayFrames(t, []string{"sh", "-c", "exit 0"}, 0)
	ev, ok := findTerminalFrame(frames)
	if !ok {
		t.Fatalf("expected a terminal event frame, got frames=%+v", frames)
	}
	if ev.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", ev.ExitCode)
	}
	if ev.Signaled {
		t.Fatalf("expected not signaled, got %+v", ev)
	}
}

func TestRelayEmitsTerminalEventOnNonZeroExit(t *testing.T) {
	frames := collectRelayFrames(t, []string{"sh", "-c", "exit 7"}, 0)
	ev, ok := findTerminalFrame(frames)
	if !ok {
		t.Fatalf("expected a terminal event frame, got frames=%+v", frames)
	}
	if ev.ExitCode != 7 {
		t.Fatalf("expected exit code 7, got %d", ev.ExitCode)
	}
	if ev.Signaled {
		t.Fatalf("expected not signaled, got %+v", ev)
	}
}

func TestRelayEmitsTerminalEventOnSignal(t *testing.T) {
	// A subprocess that ignores nothing and simply sleeps; ctx cancel triggers
	// SIGTERM via the relay's graceful-shutdown path.
	frames := collectRelayFrames(t, []string{"sleep", "30"}, 300*time.Millisecond)
	ev, ok := findTerminalFrame(frames)
	if !ok {
		t.Fatalf("expected a terminal event frame, got frames=%+v", frames)
	}
	if !ev.Signaled {
		t.Fatalf("expected signaled terminal event, got %+v", ev)
	}
	if ev.Signal != int(syscall.SIGTERM) {
		t.Fatalf("expected SIGTERM (%d), got signal %d", int(syscall.SIGTERM), ev.Signal)
	}
}

func TestParseTerminalEventRejectsTextAndForeignFrames(t *testing.T) {
	// Text frame carrying an identical payload must NOT be treated as terminal.
	payload, err := MarshalTerminalFrame(TerminalEvent{ExitCode: 0})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, ok := ParseTerminalEvent(websocket.TextMessage, payload); ok {
		t.Fatal("text frame must not be classified as terminal")
	}
	// Binary frame without the discriminator must be rejected.
	if _, ok := ParseTerminalEvent(websocket.BinaryMessage, []byte(`{"type":"assistant"}`)); ok {
		t.Fatal("non-terminal binary frame must not be classified as terminal")
	}
	// Well-formed binary terminal frame is accepted and round-trips.
	ev, ok := ParseTerminalEvent(websocket.BinaryMessage, payload)
	if !ok {
		t.Fatal("expected binary terminal frame to parse")
	}
	if ev.Type != TerminalEventType || ev.ExitCode != 0 {
		t.Fatalf("round-trip mismatch: %+v", ev)
	}
}
