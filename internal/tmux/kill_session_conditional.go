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
	"regexp"
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

// spawnOpValuePattern is what a tag value may be where it is put into a tmux format: a spawn op id, a lower-case UUID. A format
// is parsed by tmux — a `}`, `,`, `#` or quote in a value could rewrite the condition — so the value is not escaped but
// refused unless it is of this one shape, which has none of them.
var spawnOpValuePattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// conditionalKillTaggedArgs is conditionalKillArgs with the session's user option in the same condition: the kill happens only
// if the server is still the expected generation AND the session still carries option=value. `-t '$N:'` is what lets the format
// read THAT session's option (without a target it would read whatever session the server calls current).
func conditionalKillTaggedArgs(sessionID, expectedInstance, option, value string) ([]string, error) {
	if !sessionIDPattern.MatchString(sessionID) {
		return nil, fmt.Errorf("tmux kill-session: %q is not a session id", sessionID)
	}
	if !instancePattern.MatchString(expectedInstance) {
		return nil, fmt.Errorf("%w: %q", ErrUnsafeInstance, expectedInstance)
	}
	if !userOptionPattern.MatchString(option) {
		return nil, fmt.Errorf("tmux kill-session: %q is not a user option name", option)
	}
	if !spawnOpValuePattern.MatchString(value) {
		return nil, fmt.Errorf("tmux kill-session: %q is not a spawn op id", value)
	}
	condition := fmt.Sprintf("#{&&:#{==:#{pid}:#{start_time},%s},#{==:#{%s},%s}}", expectedInstance, option, value)
	return []string{
		"if-shell", "-F", "-t", sessionID + ":", condition,
		fmt.Sprintf("kill-session -t '%s'", sessionID),
		"display-message -p " + generationRefusedSentinel,
	}, nil
}

// KillSessionIfTagged kills the session with the given id only if the tmux server is still the expected generation AND the
// session still carries the user option set to value, the comparison and the kill being ONE tmux invocation. It is for a
// caller that decided to kill from a read of the owner (the boot sweep of orphan spawn sessions): between that read and a plain
// KillSessionIfInstance the owner could change — the user clears the tag, taking the session over — and the kill would still
// land. Here the server evaluates the tag where it kills.
//
// Returns as KillSessionIfInstance does: (true, nil) killed; (false, nil) the server declined (another generation, the tag is
// not value any more, or the session is gone — the kill names its target by id, so it can land on that session alone);
// (false, err) nothing was killed — a value that is not a spawn op id (a lower-case UUID) or a name that is not a user option
// is refused before tmux runs.
func (r *RealExecutor) KillSessionIfTagged(sessionID, expectedInstance, option, value string) (bool, error) {
	args, err := conditionalKillTaggedArgs(sessionID, expectedInstance, option, value)
	if err != nil {
		return false, err
	}
	return r.runConditionalKill(sessionID, args)
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
	return r.runConditionalKill(sessionID, args)
}

// runConditionalKill runs the one invocation and reads its answer: the refusal sentinel is a decline, a missing session is
// ErrNoSession, any other failure an error; nothing was killed in either of the last two.
func (r *RealExecutor) runConditionalKill(sessionID string, args []string) (bool, error) {
	cmd := tmuxCmd(args...)
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
