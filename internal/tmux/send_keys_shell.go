// internal/tmux/send_keys_shell.go — the launch panel's typing guard (no-agent launch panel spec §2.3): ONE if-shell that checks
// the tmux generation AND that the pane's foreground command is a shell, so a launch line is typed only into a pane that is
// at a prompt — nothing that starts between the plan and the send receives it, and no second tmux call decides the reason.
package tmux

import (
	"fmt"
	"strings"
)

// ErrPaneBusy (send_keys_mode.go) is this guard's "the foreground command is not a shell" too; ErrGenerationMismatch is
// checked first when both fail.

// ShellCommands are the foreground commands that mean "the pane is at a prompt": what `#{pane_current_command}` reads for an
// idle interactive shell. A login shell may carry a leading `-` (IsShellCommand strips it; the tmux condition lists both).
var ShellCommands = []string{"zsh", "bash", "fish", "sh", "dash", "ksh", "tcsh", "nu", "pwsh"}

// IsShellCommand reports whether a foreground command name is a shell (a leading `-` stripped, case-insensitive). The
// daemon's launch plan uses the same rule to report `busy`.
func IsShellCommand(cmd string) bool {
	c := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(cmd), "-"))
	for _, s := range ShellCommands {
		if c == s {
			return true
		}
	}
	return false
}

// shellRefusedSentinel starts the else branch's report: the generation verdict, then the pane's command (the rest of the line).
const shellRefusedSentinel = "pdx-launch-refused"

// shellCondition is a nested `#{||:…}` of case-insensitive glob matches (`#{m/i:name,…}`, no regex) over every shell name and its
// login `-name`, the same rule as IsShellCommand. The names carry no glob metacharacter.
func shellCondition() string {
	names := make([]string, 0, 2*len(ShellCommands))
	for _, s := range ShellCommands {
		names = append(names, s, "-"+s)
	}
	cond := fmt.Sprintf("#{m/i:%s,#{pane_current_command}}", names[len(names)-1])
	for i := len(names) - 2; i >= 0; i-- {
		cond = fmt.Sprintf("#{||:#{m/i:%s,#{pane_current_command}},%s}", names[i], cond)
	}
	return cond
}

// conditionalShellSendArgs builds the single invocation, aimed at the session's active pane (`$N:`); the keys travel as hex like
// conditionalSendArgs, so a line with quotes, `$` or `#` is inert inside the nested command string.
func conditionalShellSendArgs(sessionID, expectedInstance string, keys ...string) ([]string, error) {
	if !sessionIDPattern.MatchString(sessionID) {
		return nil, fmt.Errorf("tmux send-keys: %q is not a session id", sessionID)
	}
	if !instancePattern.MatchString(expectedInstance) {
		return nil, fmt.Errorf("%w: %q", ErrUnsafeInstance, expectedInstance)
	}
	target := sessionID + ":"
	var send strings.Builder
	fmt.Fprintf(&send, "send-keys -t '%s' -H", target)
	for _, k := range keys {
		for i := 0; i < len(k); i++ {
			fmt.Fprintf(&send, " %02x", k[i])
		}
	}
	gen := fmt.Sprintf("#{==:#{pid}:#{start_time},%s}", expectedInstance)
	cond := fmt.Sprintf("#{&&:%s,%s}", gen, shellCondition())
	report := fmt.Sprintf("display-message -p -t '%s' '%s gen=%s cmd=#{pane_current_command}'", target, shellRefusedSentinel, gen)
	return []string{"if-shell", "-F", "-t", target, cond, send.String(), report}, nil
}

// SendKeysIfInstanceAndShell sends keys to the session's active pane only if the server generation equals expectedInstance and
// the pane's foreground command is a shell, all decided by the one server connection that sends. nil: sent. A declined
// condition is ErrGenerationMismatch (checked first) or ErrPaneBusy; any other error means the invocation could not be
// completed (nothing was sent).
func (r *RealExecutor) SendKeysIfInstanceAndShell(sessionID, expectedInstance string, keys ...string) error {
	args, err := conditionalShellSendArgs(sessionID, expectedInstance, keys...)
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
	if !strings.HasPrefix(s, shellRefusedSentinel) {
		return nil
	}
	if strings.Contains(s, " gen=0 ") {
		return ErrGenerationMismatch
	}
	return ErrPaneBusy
}
