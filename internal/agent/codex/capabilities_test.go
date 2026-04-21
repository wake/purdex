package codex_test

import (
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/codex"
)

// TestCodex_HasReadiness_IsFalse guards the capability baseline; lights §3.6.1.
func TestCodex_HasReadiness_IsFalse(t *testing.T) {
	if codex.HasReadiness {
		t.Fatalf("codex.HasReadiness = true; want false")
	}
}

// TestCodex_ReadinessChecker_StubReturnsRunning is a regression guard that
// declaring HasReadiness=false doesn't perturb the stub checker.
func TestCodex_ReadinessChecker_StubReturnsRunning(t *testing.T) {
	checker := codex.NewReadinessChecker(nil)
	result := checker.CheckReadiness("")
	if result.Status != agent.StatusRunning {
		t.Fatalf("CheckReadiness stub status = %q; want %q", result.Status, agent.StatusRunning)
	}
}
