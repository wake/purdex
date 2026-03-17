// Package detect provides session status detection via tmux pane inspection.
package detect

import (
	"strings"

	"github.com/wake/tmux-box/internal/tmux"
)

// Status represents the detected state of a tmux session.
type Status string

const (
	StatusNormal    Status = "normal"     // shell prompt, no foreground process
	StatusNotInCC   Status = "not-in-cc"  // non-CC foreground process (e.g. node, vim)
	StatusCCIdle    Status = "cc-idle"    // CC is running, showing input prompt
	StatusCCRunning Status = "cc-running" // CC is executing a tool / generating
	StatusCCWaiting Status = "cc-waiting" // CC is waiting for user permission
	StatusCCUnread  Status = "cc-unread"  // set externally by bridge, not detected here
)

// defaultShells lists common shell names for detecting "normal" (shell idle) state.
var defaultShells = map[string]bool{
	"zsh": true, "bash": true, "sh": true, "fish": true, "dash": true,
}

// Detector inspects tmux pane state to determine session status.
type Detector struct {
	tmux       tmux.Executor
	ccCommands map[string]bool
}

// New creates a Detector. ccCommands lists the binary names that indicate
// Claude Code is running (e.g. "claude", "cld").
func New(executor tmux.Executor, ccCommands []string) *Detector {
	cmds := make(map[string]bool, len(ccCommands))
	for _, c := range ccCommands {
		cmds[c] = true
	}
	return &Detector{tmux: executor, ccCommands: cmds}
}

// Detect returns the current Status for the given tmux session/target.
func (d *Detector) Detect(session string) Status {
	cmd, err := d.tmux.PaneCurrentCommand(session)
	if err != nil {
		return StatusNormal
	}
	cmd = strings.TrimSpace(cmd)

	// Check if it's a known CC command.
	if !d.ccCommands[cmd] {
		if defaultShells[cmd] {
			return StatusNormal
		}
		return StatusNotInCC
	}

	// It's a CC command — inspect pane content for sub-state.
	content, err := d.tmux.CapturePaneContent(session, 5)
	if err != nil {
		return StatusCCRunning // can't read pane, assume running
	}

	// Check for permission prompt (Allow / Deny buttons).
	if strings.Contains(content, "Allow") && strings.Contains(content, "Deny") {
		return StatusCCWaiting
	}

	// Check for idle prompt (❯).
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) > 0 {
		lastLine := strings.TrimSpace(lines[len(lines)-1])
		if strings.HasPrefix(lastLine, "❯") {
			return StatusCCIdle
		}
	}

	return StatusCCRunning
}
