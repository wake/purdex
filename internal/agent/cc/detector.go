// Package cc provides the Claude Code agent provider implementation.
package cc

import (
	"strings"
	"sync"

	"github.com/wake/tmux-box/internal/tmux"
)

// Status represents the detected state of a tmux session.
type Status string

const (
	StatusNormal    Status = "normal"
	StatusNotInCC   Status = "not-in-cc"
	StatusCCIdle    Status = "cc-idle"
	StatusCCRunning Status = "cc-running"
	StatusCCWaiting Status = "cc-waiting"
	StatusCCUnread  Status = "cc-unread"
)

var defaultShells = map[string]bool{
	"zsh": true, "bash": true, "sh": true, "fish": true, "dash": true,
}

type Detector struct {
	mu         sync.RWMutex
	tmux       tmux.Executor
	ccCommands map[string]bool
}

func NewDetector(executor tmux.Executor, ccCommands []string) *Detector {
	cmds := make(map[string]bool, len(ccCommands))
	for _, c := range ccCommands {
		cmds[c] = true
	}
	return &Detector{tmux: executor, ccCommands: cmds}
}

func (d *Detector) UpdateCommands(cmds []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ccCommands = make(map[string]bool, len(cmds))
	for _, c := range cmds {
		d.ccCommands[c] = true
	}
}

func (d *Detector) Detect(session string) Status {
	cmd, err := d.tmux.PaneCurrentCommand(session)
	if err != nil {
		return StatusNormal
	}
	cmd = strings.TrimSpace(cmd)
	if defaultShells[cmd] {
		return StatusNormal
	}
	d.mu.RLock()
	isCC := d.ccCommands[cmd]
	d.mu.RUnlock()
	if isCC {
		return d.detectCCSubState(session)
	}
	children, err := d.tmux.PaneChildCommands(session)
	if err == nil {
		for _, child := range children {
			base := child
			if idx := strings.LastIndex(child, "/"); idx >= 0 {
				base = child[idx+1:]
			}
			d.mu.RLock()
			childIsCC := d.ccCommands[base]
			d.mu.RUnlock()
			if childIsCC {
				return d.detectCCSubState(session)
			}
		}
	}
	content, err := d.tmux.CapturePaneContent(session, 5)
	if err != nil {
		return StatusNotInCC
	}
	if looksLikeCC(content) {
		return d.detectCCSubState(session)
	}
	return StatusNotInCC
}

func (d *Detector) detectCCSubState(session string) Status {
	content, err := d.tmux.CapturePaneContent(session, 5)
	if err != nil {
		return StatusCCRunning
	}
	if strings.Contains(content, "Allow") && strings.Contains(content, "Deny") {
		return StatusCCWaiting
	}
	lines := strings.Split(strings.TrimSpace(content), "\n")
	start := len(lines) - 5
	if start < 0 {
		start = 0
	}
	for _, line := range lines[start:] {
		if strings.HasPrefix(strings.TrimSpace(line), "❯") {
			return StatusCCIdle
		}
	}
	return StatusCCRunning
}

func looksLikeCC(content string) bool {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "❯") {
			return true
		}
		if strings.Contains(trimmed, "Opus") || strings.Contains(trimmed, "Sonnet") || strings.Contains(trimmed, "Haiku") {
			return true
		}
		if strings.Contains(trimmed, "Allow") && strings.Contains(trimmed, "Deny") {
			return true
		}
	}
	return false
}
