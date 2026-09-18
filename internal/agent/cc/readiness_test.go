package cc_test

import (
	"testing"

	"github.com/wake/purdex/internal/agent"
	cc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/tmux"
)

func TestReadinessChecker(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	checker := cc.NewReadinessChecker(fake)

	tests := []struct {
		name     string
		content  string
		expected agent.Status
	}{
		{"idle prompt", "❯ ", agent.StatusIdle},
		{"running spinner", "⠋ Reading file...", agent.StatusRunning},
		{"waiting permission", "Allow  Deny", agent.StatusWaiting},
		{"idle with status bar", "❯ \n─────────\n  project [Opus 4.6] 100% left", agent.StatusIdle},
		// CC 2.1.276 emits the prompt with a colour reset in front of it; the
		// pane is captured with -e, so the escape reaches the matcher.
		{"idle prompt behind an ANSI colour code", "\x1b[39m❯ ", agent.StatusIdle},
		{"idle prompt with ANSI and status bar", "\x1b[39m❯ \x1b[0m\n\x1b[2m─────────\x1b[0m\n  project [Opus 4.6] 100% left", agent.StatusIdle},
		{"ANSI-coloured spinner is still running", "\x1b[36m⠋\x1b[0m Reading file...", agent.StatusRunning},
		{"empty content", "", agent.StatusRunning},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake.SetPaneContent("test:", tt.content)
			result := checker.CheckReadiness("test:")
			if result.Status != tt.expected {
				t.Fatalf("expected %s, got %s", tt.expected, result.Status)
			}
		})
	}
}
