package agent_test

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent/codex"
	"github.com/wake/purdex/internal/agent/opencode"
)

// runForwardInvariants applies invariants 1, 2, 3 (per spec §6.1) to a
// migrated agent's catalog snapshot. Invariants 4 (preserved metadata) and
// 5 (Lifecycle alignment) are per-agent fixture-driven and live in each
// provider package's own *_test.go (TestCcEventSpecs_PreservedLegacyMetadata
// / TestCcEventSpecs_LifecycleAlignment etc.).
//
// The opencode caveat: opencode 65-entry catalog includes entries where
// Name and PurdexName do not satisfy a mechanical Pdx prefix (auth.session
// vs PdxAuthSession etc.). Phase 3 ship removes invariant 2 entirely along
// with the Name field; during Phase 1/2 this helper skips invariant 2 for
// opencode.
func runForwardInvariants(t *testing.T, agentType string, specs []agent.HookEventSpec) {
	t.Helper()
	for _, e := range specs {
		// Invariant 1: PurdexName non-empty and Pdx-prefixed.
		if e.PurdexName == "" {
			t.Errorf("%s [Name=%q]: PurdexName empty (invariant 1)", agentType, e.Name)
			continue
		}
		if !strings.HasPrefix(e.PurdexName, "Pdx") {
			t.Errorf("%s %q: PurdexName lacks Pdx prefix (invariant 1)", agentType, e.PurdexName)
		}
		// Invariant 1: UpstreamKeys non-empty for every entry.
		if len(e.UpstreamKeys) == 0 {
			t.Errorf("%s %q: UpstreamKeys empty (invariant 1)", agentType, e.PurdexName)
		}
		// Invariant 2: dev-time Name backfill for cc / codex (mechanical rename).
		if agentType != "opencode" {
			if want := strings.TrimPrefix(e.PurdexName, "Pdx"); e.Name != want {
				t.Errorf("%s %q: Name=%q want TrimPrefix(PurdexName,%q)=%q (invariant 2)", agentType, e.PurdexName, e.Name, "Pdx", want)
			}
		}
		// Invariant 3: PurdexName not in own UpstreamKeys.
		for _, k := range e.UpstreamKeys {
			if k == e.PurdexName {
				t.Errorf("%s %q: PurdexName present in UpstreamKeys (invariant 3)", agentType, e.PurdexName)
			}
		}
	}
}

// runReverseInvariants asserts an agent's catalog has NOT migrated yet —
// PurdexName / UpstreamKeys / Lifecycle remain at zero values. Each phase
// ship flips the corresponding agent from reverse to forward in the same
// commit that lands its catalog migration.
func runReverseInvariants(t *testing.T, agentType string, specs []agent.HookEventSpec) {
	t.Helper()
	for _, e := range specs {
		if e.PurdexName != "" {
			t.Errorf("%s %q: PurdexName=%q expected empty (reverse invariant — agent not migrated yet)", agentType, e.Name, e.PurdexName)
		}
		if e.UpstreamKeys != nil {
			t.Errorf("%s %q: UpstreamKeys=%v expected nil (reverse invariant)", agentType, e.Name, e.UpstreamKeys)
		}
		if e.Lifecycle != agent.LifecycleNone {
			t.Errorf("%s %q: Lifecycle=%v expected LifecycleNone (reverse invariant)", agentType, e.Name, e.Lifecycle)
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

// TestCatalogInvariants_Codex_LegacyShape asserts codex has not yet been
// migrated in Phase 1. P2-T1 will flip this from reverse to forward.
func TestCatalogInvariants_Codex_LegacyShape(t *testing.T) {
	specs := codex.NewProvider().Events()
	if len(specs) == 0 {
		t.Fatal("codex catalog snapshot is empty")
	}
	runReverseInvariants(t, "codex", specs)
}

// TestCatalogInvariants_Opencode_LegacyShape mirrors the codex assertion
// for opencode. P3-T1 flips it to forward.
func TestCatalogInvariants_Opencode_LegacyShape(t *testing.T) {
	specs := opencode.NewProvider().Events()
	if len(specs) == 0 {
		t.Fatal("opencode catalog snapshot is empty")
	}
	runReverseInvariants(t, "opencode", specs)
}
