package codex_test

import (
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/codex"
)

// TestCodex_ReadinessContract_StubMatchesCapability ensures the advertised
// capability bit and the stub checker behaviour stay aligned; lights §3.6.1.
// Colocating the assertion keeps the capability constant and the checker stub
// as a single contract instead of two drift-prone facts.
func TestCodex_ReadinessContract_StubMatchesCapability(t *testing.T) {
	if codex.HasReadiness {
		t.Fatalf("codex.HasReadiness = true; want false while checker is a stub")
	}
	result := codex.NewReadinessChecker(nil).CheckReadiness("")
	if result.Status != agent.StatusRunning {
		t.Fatalf("stub CheckReadiness status = %q; want %q", result.Status, agent.StatusRunning)
	}
}
