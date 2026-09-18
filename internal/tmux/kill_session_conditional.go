// internal/tmux/kill_session_conditional.go — kill a session only if the tmux
// server is still the one the caller means.
//
// The companion of send_keys_conditional.go, for the same reason. A daemon
// that created a session under one generation and wants it gone again cannot
// `kill-session -t <name>`: the server may have restarted in between, the
// session it made died with the old server, and the name may now belong to a
// stranger on the new one. Nor can it check the generation first and kill
// second — two invocations are two connections, and a restart fits between
// them. `if-shell -F` evaluates the comparison and runs the kill in the one
// server that received the invocation, targeting the session by id (`$N`),
// which unlike a name cannot be re-pointed.
package tmux

import (
	"fmt"
	"os/exec"
	"strings"
)

// conditionalKillArgs builds the single tmux invocation: the condition,
// `kill-session -t '$N'`, and the refusal branch that prints the sentinel.
func conditionalKillArgs(sessionID, expectedInstance string) ([]string, error) {
	if !sessionIDPattern.MatchString(sessionID) {
		return nil, fmt.Errorf("tmux kill-session: %q is not a session id", sessionID)
	}
	if !instancePattern.MatchString(expectedInstance) {
		return nil, fmt.Errorf("%w: %q", ErrUnsafeInstance, expectedInstance)
	}
	condition := fmt.Sprintf("#{==:#{pid}:#{start_time},%s}", expectedInstance)
	return []string{
		"if-shell", "-F", condition,
		// Single quotes: tmux expands `$name` inside double quotes, and a
		// session id starts with `$`. Session ids contain no quote.
		fmt.Sprintf("kill-session -t '%s'", sessionID),
		"display-message -p " + generationRefusedSentinel,
	}, nil
}

// KillSessionIfInstance kills the session with the given id only if the tmux
// server's generation equals expectedInstance, with the comparison and the
// kill performed by one server connection.
//
// Returns (true, nil) when the session was killed, (false, nil) when the
// server evaluated the condition and declined — the generation has moved, the
// session this caller means no longer exists, and whatever now answers to
// its id or name is somebody else's — and (false, err) when the invocation
// could not be completed: no server, or the id is gone on the matching server
// (err wraps ErrNoSession). The caller must treat the last as "unknown,
// nothing killed", not as a refusal.
func (r *RealExecutor) KillSessionIfInstance(sessionID, expectedInstance string) (bool, error) {
	args, err := conditionalKillArgs(sessionID, expectedInstance)
	if err != nil {
		return false, err
	}
	cmd := exec.Command("tmux", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "can't find session") {
			return false, fmt.Errorf("tmux if-shell kill-session %s: %w: %s", sessionID, ErrNoSession, msg)
		}
		return false, fmt.Errorf("tmux if-shell kill-session %s: %w: %s", sessionID, err, msg)
	}
	if strings.Contains(string(out), generationRefusedSentinel) {
		return false, nil
	}
	return true, nil
}
