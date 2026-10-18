// internal/tmux/send_keys_mode.go — the typed submit's Enter guard (K-1a-2): ONE if-shell that checks the tmux generation, that
// the target pane still belongs to the session, and that the pane is not in a mode (copy-mode would eat the keys).
package tmux

import (
	"errors"
	"fmt"
	"strings"
)

// KeyEnter is the byte Claude Code reads as Enter.
const KeyEnter = "\r"

// KeySendNow is Ctrl+X Ctrl+S, Claude Code's own "send now" (default chat:sendNow): the queued messages go out at once and the turn
// that is running is interrupted. Both bytes travel in one send-keys; a lone Ctrl+S does nothing (measured, spec §4.1, MS2-0).
const KeySendNow = "\x18\x13"

// KeyUp is the Up arrow (ESC [ A) as Claude Code's input reads it: on an empty input it pops the queued messages (K-1b).
const KeyUp = "\x1b[A"

var (
	// ErrPaneBusy: the pane is in a mode (copy-mode, view-mode…); nothing was sent.
	ErrPaneBusy = errors.New("tmux pane is in a mode")
	// ErrGenerationMismatch: the tmux server is not the one the caller means; nothing was sent.
	ErrGenerationMismatch = errors.New("tmux generation differs")
	// ErrPaneMismatch: the pane is no longer in the session the caller means; nothing was sent.
	ErrPaneMismatch = errors.New("tmux pane is not in the session")
)

// modeRefusedSentinel starts the else branch's report; the rest are the three facts the condition looked at.
const modeRefusedSentinel = "pdx-send-refused"

// conditionalModeSendArgs builds the single invocation; the keys travel as hex like conditionalSendArgs.
func conditionalModeSendArgs(sessionID, expectedInstance, paneID string, keys ...string) ([]string, error) {
	if !sessionIDPattern.MatchString(sessionID) {
		return nil, fmt.Errorf("tmux send-keys: %q is not a session id", sessionID)
	}
	if !paneIDPattern.MatchString(paneID) {
		return nil, fmt.Errorf("tmux send-keys: %q is not a pane id", paneID)
	}
	if !instancePattern.MatchString(expectedInstance) {
		return nil, fmt.Errorf("%w: %q", ErrUnsafeInstance, expectedInstance)
	}
	var send strings.Builder
	fmt.Fprintf(&send, "send-keys -t '%s' -H", paneID)
	for _, k := range keys {
		for i := 0; i < len(k); i++ {
			fmt.Fprintf(&send, " %02x", k[i])
		}
	}
	gen := fmt.Sprintf("#{==:#{pid}:#{start_time},%s}", expectedInstance)
	cond := fmt.Sprintf("#{&&:%s,#{&&:#{==:#{pane_in_mode},0},#{==:#{session_id},%s}}}", gen, sessionID)
	report := fmt.Sprintf("display-message -p -t '%s' '%s gen=%s mode=#{pane_in_mode} sess=#{session_id}'", paneID, modeRefusedSentinel, gen)
	return []string{"if-shell", "-F", "-t", paneID, cond, send.String(), report}, nil
}

// SendKeysIfInstanceAndMode sends keys to one explicit pane only if the server generation equals expectedInstance, the pane
// is in sessionID and `#{pane_in_mode}` is 0, all decided by the one server connection that sends. nil: sent. A declined
// condition is ErrGenerationMismatch / ErrPaneMismatch / ErrPaneBusy (in that order); any other error means the invocation
// could not be completed (nothing was sent).
func (r *RealExecutor) SendKeysIfInstanceAndMode(sessionID, expectedInstance, paneID string, keys ...string) error {
	args, err := conditionalModeSendArgs(sessionID, expectedInstance, paneID, keys...)
	if err != nil {
		return err
	}
	cmd := tmuxCmd(args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("tmux if-shell send-keys: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	s := strings.TrimSpace(string(out))
	if !strings.HasPrefix(s, modeRefusedSentinel) {
		return nil
	}
	switch {
	case strings.Contains(s, " gen=0 "):
		return ErrGenerationMismatch
	case !strings.HasSuffix(s, " sess="+sessionID):
		return ErrPaneMismatch
	default:
		return ErrPaneBusy
	}
}
