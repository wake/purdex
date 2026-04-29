// internal/module/agent/probe_intent_dispatcher_drift_test.go
//
// W6-3 P2-T5 drift coverage for ProbeIntent providers.
//
// Three guards make sure the ProbeIntent surface stays internally
// consistent as future agent providers add intents (W6-4 / W6-1 / W6-2 /
// W6-6 will all extend this set):
//
//  1. Drift coverage — every registered provider that implements
//     ProbeIntentProvider declares at least one well-formed intent
//     (OnEntryStatus non-empty + OnSignal non-nil) and every declared
//     Kind has a dispatcher case wired.
//
//  2. Dispatcher routing — every declared Kind is in the wiredKinds set
//     this test maintains as a mirror of Module.New()'s startDetector
//     switch. Missing case = drift signal at PR-review time; reviewers
//     bump wiredKinds in lockstep with the production switch.
//
//  3. ProbeIntent shape — for every provider that implements the
//     interface, ProbeIntents() returns a contract-shaped slice
//     (OnSignal non-nil + OnEntryStatus non-empty). Empty slice is
//     acceptable (a provider may declare zero intents) but a single
//     malformed entry fails.
//
// The wiredKinds set MUST stay in sync with internal/module/agent/module.go
// New()'s startDetector closure. Adding a new Kind without updating both is
// the canonical drift bug this test guards against.
package agent

import (
	"sync"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent/codex"
	"github.com/wake/purdex/internal/agent/opencode"
)

// wiredKinds is the set of ProbeIntentKind values for which Module.New()
// installs a dispatch case in its startDetector closure. Bump this set in
// lockstep with the production switch in module.go — drift on either side
// trips TestProbeIntentDrift_AllDeclaredKindsHaveDispatcherCase.
//
// W6-3 first PR scope: only ProcessDead. W6-4 / W6-1 / W6-2 / W6-6 add
// more entries here as they land.
var wiredKinds = map[agentpkg.ProbeIntentKind]struct{}{
	agentpkg.ProbeIntentKindProcessDead: {},
}

// productionRegistry constructs a registry mirroring what Module.Init()
// would register on a real daemon, without depending on core.Core wiring.
// Used by the drift tests so they walk the production surface (cc + codex
// + opencode) rather than a tailored fixture.
func productionRegistry(t *testing.T) *agentpkg.Registry {
	t.Helper()
	r := agentpkg.NewRegistry()
	// cc.NewProvider tolerates nil prober/tmux/cfg/cfgMu for tests where the
	// hook installer / readiness paths are not exercised — see
	// cc/provider_test.go for prior art. Same is true for codex.NewProvider
	// (no args) and opencode.NewProvider (no args).
	r.Register(cc.NewProvider(nil, nil, nil, &sync.RWMutex{}))
	r.Register(codex.NewProvider())
	r.Register(opencode.NewProvider())
	return r
}

// TestProbeIntentDriftCoverage walks every production provider and
// verifies any ProbeIntent it declares is fully wired:
//   - OnEntryStatus is non-empty (otherwise the lifecycle helper would
//     never gate active=true and the intent would be dead code)
//   - OnSignal is non-nil (dispatcher's applyProbeGuards invokes it as
//     mapping callback; nil would crash production)
//   - Kind appears in wiredKinds (i.e. Module.New()'s startDetector
//     switch has a case for it)
//
// At least one provider must declare ProbeIntentProvider so the suite
// proves it is exercising real surface, not a vacuous loop. Today only
// codex satisfies the interface; cc and opencode return false on the
// type assertion and are skipped.
func TestProbeIntentDriftCoverage(t *testing.T) {
	r := productionRegistry(t)
	providers := r.All()
	if len(providers) == 0 {
		t.Fatalf("productionRegistry returned no providers — registry surface broken")
	}
	implementers := 0
	for _, p := range providers {
		pip, ok := p.(agentpkg.ProbeIntentProvider)
		if !ok {
			continue
		}
		implementers++
		intents := pip.ProbeIntents()
		// A provider that implements the interface but returns no intents
		// is allowed — but the case is suspicious enough to flag as info
		// (no failure). Future PRs that add a new ProbeIntentProvider
		// stub will surface here until they wire intents.
		if len(intents) == 0 {
			t.Logf("provider %q implements ProbeIntentProvider but declares zero intents — verify intentional", p.Type())
			continue
		}
		for _, intent := range intents {
			if len(intent.OnEntryStatus) == 0 {
				t.Errorf("provider %q intent Kind=%q: OnEntryStatus is empty — lifecycle would never arm", p.Type(), intent.Kind)
			}
			if intent.OnSignal == nil {
				t.Errorf("provider %q intent Kind=%q: OnSignal is nil — dispatcher would crash on signal mapping", p.Type(), intent.Kind)
			}
			if _, ok := wiredKinds[intent.Kind]; !ok {
				t.Errorf("provider %q declares Kind=%q but Module.New()'s startDetector switch has no case for it (wiredKinds drift) — add the case + bump wiredKinds in this test in lockstep", p.Type(), intent.Kind)
			}
		}
	}
	if implementers == 0 {
		t.Fatalf("no production provider implements ProbeIntentProvider — drift suite is vacuous; expected at least codex")
	}
}

// TestProbeIntentDrift_AllDeclaredKindsHaveDispatcherCase is the focused
// dispatcher-routing guard. It collects every declared Kind across the
// production registry and asserts the set is a subset of wiredKinds.
// Failure here means a provider added a Kind constant + ProbeIntent slot
// without bumping Module.New()'s switch.
func TestProbeIntentDrift_AllDeclaredKindsHaveDispatcherCase(t *testing.T) {
	r := productionRegistry(t)
	declared := make(map[agentpkg.ProbeIntentKind][]string) // kind → providers declaring it
	for _, p := range r.All() {
		pip, ok := p.(agentpkg.ProbeIntentProvider)
		if !ok {
			continue
		}
		for _, intent := range pip.ProbeIntents() {
			declared[intent.Kind] = append(declared[intent.Kind], p.Type())
		}
	}
	if len(declared) == 0 {
		t.Fatalf("no provider declares any ProbeIntent — drift suite is vacuous")
	}
	for kind, providers := range declared {
		if _, ok := wiredKinds[kind]; !ok {
			t.Errorf("Kind=%q declared by providers=%v but missing from wiredKinds (Module.New() switch); add a case + bump wiredKinds together", kind, providers)
		}
	}
}

// TestProbeIntentDrift_OnSignalNonNil_OnEntryStatusNonEmpty isolates the
// minimum-shape contract: every declared intent must have a non-nil
// OnSignal mapping function and a non-empty OnEntryStatus gate. These
// are independent of dispatcher routing — even a Kind that's wired
// would still crash the dispatcher / never arm if either invariant
// breaks.
//
// Companion to TestProbeIntentDriftCoverage; this one fails fast on the
// minimal contract regardless of routing wiring, so a regression that
// nukes wiredKinds at the same time as a malformed intent surfaces both
// signals to the reviewer rather than just the routing one.
func TestProbeIntentDrift_OnSignalNonNil_OnEntryStatusNonEmpty(t *testing.T) {
	r := productionRegistry(t)
	any := false
	for _, p := range r.All() {
		pip, ok := p.(agentpkg.ProbeIntentProvider)
		if !ok {
			continue
		}
		for _, intent := range pip.ProbeIntents() {
			any = true
			if intent.OnSignal == nil {
				t.Errorf("provider %q kind=%q has nil OnSignal — dispatcher applyProbeGuards mapping callback would crash", p.Type(), intent.Kind)
			}
			if len(intent.OnEntryStatus) == 0 {
				t.Errorf("provider %q kind=%q has empty OnEntryStatus — lifecycle helper would never gate active=true so intent is dead code", p.Type(), intent.Kind)
			}
		}
	}
	if !any {
		t.Fatalf("zero ProbeIntent declarations across the production registry — suite is vacuous")
	}
}
