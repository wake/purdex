package agent_test

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent/codex"
	"github.com/wake/purdex/internal/agent/opencode"
)

// runForwardInvariants applies invariants 1 and 3 (per spec §6.1) to a
// migrated agent's catalog snapshot. Invariants 4 (preserved metadata) and
// 5 (Lifecycle alignment) are per-agent fixture-driven and live in each
// provider package's own *_test.go (TestCcEventSpecs_PreservedLegacyMetadata
// / TestCcEventSpecs_LifecycleAlignment etc.).
//
// Invariant 2 (dev-time `Name == TrimPrefix(PurdexName, "Pdx")`) was removed
// in P3-T1 along with reverse-shape mode: with all three agents migrated
// forward, opencode's non-mechanical legacy names (auth.session vs
// PdxAuthSession etc.) make the constraint vacuous, and the Name field
// itself was removed in P3-T4.
func runForwardInvariants(t *testing.T, agentType string, specs []agent.HookEventSpec) {
	t.Helper()
	for i, e := range specs {
		// Invariant 1: PurdexName non-empty and Pdx-prefixed.
		if e.PurdexName == "" {
			t.Errorf("%s [index=%d UpstreamKeys=%v]: PurdexName empty (invariant 1)", agentType, i, e.UpstreamKeys)
			continue
		}
		if !strings.HasPrefix(e.PurdexName, "Pdx") {
			t.Errorf("%s %q: PurdexName lacks Pdx prefix (invariant 1)", agentType, e.PurdexName)
		}
		// Invariant 1: UpstreamKeys non-empty for every entry.
		if len(e.UpstreamKeys) == 0 {
			t.Errorf("%s %q: UpstreamKeys empty (invariant 1)", agentType, e.PurdexName)
		}
		// Invariant 3: PurdexName not in own UpstreamKeys.
		for _, k := range e.UpstreamKeys {
			if k == e.PurdexName {
				t.Errorf("%s %q: PurdexName present in UpstreamKeys (invariant 3)", agentType, e.PurdexName)
			}
		}
	}
}

// TestCatalogInvariants_CC_AllForwardInvariants runs forward invariants
// against cc's Phase 1 migrated catalog.
func TestCatalogInvariants_CC_AllForwardInvariants(t *testing.T) {
	specs := cc.NewProvider(nil, nil, nil, nil).Events()
	if len(specs) == 0 {
		t.Fatal("cc catalog snapshot is empty")
	}
	runForwardInvariants(t, "cc", specs)
}

// TestCatalogInvariants_Codex_AllForwardInvariants runs forward invariants
// against codex's Phase 2 migrated catalog.
func TestCatalogInvariants_Codex_AllForwardInvariants(t *testing.T) {
	specs := codex.NewProvider().Events()
	if len(specs) == 0 {
		t.Fatal("codex catalog snapshot is empty")
	}
	runForwardInvariants(t, "codex", specs)
}

// TestCatalogInvariants_Opencode_AllForwardInvariants runs forward invariants
// against opencode's Phase 3 migrated catalog. P3-T1 flipped this from
// reverse to forward in the same commit that landed the catalog migration.
func TestCatalogInvariants_Opencode_AllForwardInvariants(t *testing.T) {
	specs := opencode.NewProvider().Events()
	if len(specs) == 0 {
		t.Fatal("opencode catalog snapshot is empty")
	}
	runForwardInvariants(t, "opencode", specs)
}
