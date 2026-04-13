package cc

import (
	"strings"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/tmux"
)

type ccReadinessChecker struct {
	tmux tmux.Executor
}

// NewReadinessChecker creates a CC readiness checker.
func NewReadinessChecker(tmux tmux.Executor) probe.ReadinessChecker {
	return &ccReadinessChecker{tmux: tmux}
}

func (c *ccReadinessChecker) CheckReadiness(target string) probe.ReadinessResult {
	content, err := c.tmux.CapturePaneContent(target, 5)
	if err != nil {
		return probe.ReadinessResult{Status: agent.StatusRunning}
	}

	if strings.Contains(content, "Allow") && strings.Contains(content, "Deny") {
		return probe.ReadinessResult{Status: agent.StatusWaiting, Raw: content}
	}

	lines := strings.Split(strings.TrimSpace(content), "\n")
	start := len(lines) - 5
	if start < 0 {
		start = 0
	}
	for _, line := range lines[start:] {
		if strings.HasPrefix(strings.TrimSpace(line), "❯") {
			return probe.ReadinessResult{Status: agent.StatusIdle, Raw: content}
		}
	}

	return probe.ReadinessResult{Status: agent.StatusRunning, Raw: content}
}
