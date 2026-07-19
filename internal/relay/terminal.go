// internal/relay/terminal.go
package relay

import (
	"encoding/json"

	"github.com/gorilla/websocket"
)

// TerminalEventType is the discriminator value carried in a terminal frame's
// "type" field. It is the authoritative process-exit signal (spec §5.3): the
// timing is decided by process exit, never by an in-band `result` event.
const TerminalEventType = "terminal"

// TerminalEvent is the daemon-internal, out-of-band frame the relay emits once
// its subprocess has exited. It carries the process exit code (and, when the
// process was terminated by a signal, the signal number) so the daemon's
// execution layer can decide the terminal timepoint and, later, classify the
// outcome (P.8). It is deliberately NOT part of the SPA stream: it travels as a
// WebSocket BinaryMessage so it can never be confused with the NDJSON text
// lines that make up the stream-json output.
type TerminalEvent struct {
	Type string `json:"type"`
	// ExitCode is the subprocess exit code, or -1 when it did not exit normally
	// (e.g. terminated by a signal or the state is unknown).
	ExitCode int `json:"exit_code"`
	// Signaled is true when the subprocess was terminated by a signal.
	Signaled bool `json:"signaled,omitempty"`
	// Signal is the terminating signal number, set only when Signaled is true.
	Signal int `json:"signal,omitempty"`
}

// MarshalTerminalFrame encodes ev as the wire payload for the out-of-band
// terminal frame. It forces the discriminator so callers cannot forget it.
func MarshalTerminalFrame(ev TerminalEvent) ([]byte, error) {
	ev.Type = TerminalEventType
	return json.Marshal(ev)
}

// ParseTerminalEvent decodes a daemon-internal out-of-band terminal frame.
// It returns ok=true only for WebSocket BinaryMessage frames carrying a
// {"type":"terminal",...} envelope, so ordinary stream-json text lines are
// never misclassified as terminal signals.
func ParseTerminalEvent(msgType int, data []byte) (TerminalEvent, bool) {
	if msgType != websocket.BinaryMessage {
		return TerminalEvent{}, false
	}
	var ev TerminalEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return TerminalEvent{}, false
	}
	if ev.Type != TerminalEventType {
		return TerminalEvent{}, false
	}
	return ev, true
}
