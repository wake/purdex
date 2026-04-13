package cc_test

import (
	"testing"

	cc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent"
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
