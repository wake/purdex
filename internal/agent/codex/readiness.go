package codex

import (
	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/tmux"
)

// HasReadiness reports whether this agent can derive detailed readiness
// (waiting / error / clear) from tmux pane content. Stub checker returns
// StatusRunning, so this stays false until a real implementation lands.
// Lights spec: §3.6.1, §6.4.
const HasReadiness = false

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
