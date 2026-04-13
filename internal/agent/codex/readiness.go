package codex

import (
	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/tmux"
)

type codexReadinessChecker struct {
	tmux tmux.Executor
}

// NewReadinessChecker creates a Codex readiness checker.
func NewReadinessChecker(tmux tmux.Executor) probe.ReadinessChecker {
	return &codexReadinessChecker{tmux: tmux}
}

func (c *codexReadinessChecker) CheckReadiness(target string) probe.ReadinessResult {
	return probe.ReadinessResult{Status: agent.StatusRunning}
}
