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
//  2. Dispatcher routing — every declared Kind appears in the production
//     supportedKinds map (Module.New()-populated). Missing case = drift
//     signal at PR-review time; per audit F6 the lifecycle fail-closes
//     for unwired kinds, but this test catches the drift earlier.
//
//  3. ProbeIntent shape — for every provider that implements the
//     interface, ProbeIntents() returns a contract-shaped slice
//     (OnSignal non-nil + OnEntryStatus non-empty). Empty slice is
//     acceptable (a provider may declare zero intents) but a single
//     malformed entry fails.
//
// supportedKinds is read from a freshly-constructed Module via
// productionSupportedKinds(t); this exercises the production routing
// directly rather than maintaining a hand-written mirror in the test.
package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sync"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent/codex"
	"github.com/wake/purdex/internal/agent/opencode"
)

// productionSupportedKinds reads the actual supportedKinds map populated
// by Module.New() — the production routing source of truth. Per audit F6:
// the drift test must exercise production routing rather than a hand-
// written mirror, so a missing wire surfaces as a real fail-closed
// branch in lifecycle, not as a stale test fixture.
func productionSupportedKinds(t *testing.T) map[agentpkg.ProbeIntentKind]struct{} {
	t.Helper()
	m := newTestModule(t) // calls Module.New, populates probeIntentDisp.supportedKinds
	return m.probeIntentDisp.supportedKinds
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
//   - Kind appears in production supportedKinds (i.e. Module.New()'s
//     startDetector switch + supportedKinds map have it wired)
//
// At least one provider must declare ProbeIntentProvider so the suite
// proves it is exercising real surface, not a vacuous loop. Today only
// codex satisfies the interface; cc and opencode return false on the
// type assertion and are skipped.
func TestProbeIntentDriftCoverage(t *testing.T) {
	wired := productionSupportedKinds(t)
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
			if _, ok := wired[intent.Kind]; !ok {
				t.Errorf("provider %q declares Kind=%q but Module.New()'s supportedKinds map (production routing) has no entry for it — add a switch case in startDetector + add the kind to supportedKinds together (audit F6 fail-closed contract)", p.Type(), intent.Kind)
			}
		}
	}
	if implementers == 0 {
		t.Fatalf("no production provider implements ProbeIntentProvider — drift suite is vacuous; expected at least codex")
	}
}

// TestProbeIntentDrift_AllDeclaredKindsHaveDispatcherCase is the focused
// dispatcher-routing guard. It collects every declared Kind across the
// production registry and asserts the set is a subset of the production
// supportedKinds map (Module.New()-populated). Failure here means a
// provider added a Kind constant + ProbeIntent slot without extending
// both the startDetector switch AND supportedKinds in lockstep.
//
// Per audit F6: this exercises real production routing rather than a
// hand-written wiredKinds mirror — a missing wire surfaces as
// fail-closed in the production lifecycle (TestApplyIntentLifecycle_
// UnsupportedKind_FailsClosed pins that branch directly).
func TestProbeIntentDrift_AllDeclaredKindsHaveDispatcherCase(t *testing.T) {
	wired := productionSupportedKinds(t)
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
		if _, ok := wired[kind]; !ok {
			t.Errorf("Kind=%q declared by providers=%v but missing from production supportedKinds (Module.New() switch + supportedKinds map); add a case + add to supportedKinds together — audit F6 fail-closed", kind, providers)
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
// nukes a production wiring at the same time as a malformed intent surfaces
// both signals to the reviewer rather than just the routing one.
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

// TestProbeIntentDrift_DeclaredKindsEqualSupportedKinds is the
// strict bidirectional drift gate (per W6-6 spec §6.2 / plan §2 P2-T2).
//
// The two preceding tests (TestProbeIntentDriftCoverage +
// TestProbeIntentDrift_AllDeclaredKindsHaveDispatcherCase) verify the
// ⊆ direction: every declared Kind has a dispatcher case. This one
// verifies the ⊇ direction too: every Kind in the production
// supportedKinds map has at least one provider declaring it.
//
// Failure modes caught:
//
//   - Declared by some provider but missing from supportedKinds:
//     audit F6 fail-closed branch (lifecycle skip-arm) caught earlier
//     by the ⊆ test, but this test re-asserts the same invariant
//     under set equality so a single failure message lists the
//     symmetric difference rather than two unrelated assertions.
//   - Wired in supportedKinds but no provider declares it: dead
//     dispatcher case — perhaps a Kind constant was renamed without
//     updating the provider, or the supportedKinds map was extended
//     speculatively for a Kind that never made it to ProbeIntents().
//     The fail-closed branch never trips for it, but the surface area
//     is still inflated.
//
// Together with the W6-6 ScreenChange landing this set is exactly
// {ProcessDead, ScreenChange}. Future Kinds extend both sides
// simultaneously per audit F6.
func TestProbeIntentDrift_DeclaredKindsEqualSupportedKinds(t *testing.T) {
	wired := productionSupportedKinds(t)
	r := productionRegistry(t)

	declared := make(map[agentpkg.ProbeIntentKind]struct{})
	for _, p := range r.All() {
		pip, ok := p.(agentpkg.ProbeIntentProvider)
		if !ok {
			continue
		}
		for _, intent := range pip.ProbeIntents() {
			declared[intent.Kind] = struct{}{}
		}
	}

	// Symmetric difference reports both directions in one message.
	var declaredButNotWired []agentpkg.ProbeIntentKind
	for k := range declared {
		if _, ok := wired[k]; !ok {
			declaredButNotWired = append(declaredButNotWired, k)
		}
	}
	var wiredButNotDeclared []agentpkg.ProbeIntentKind
	for k := range wired {
		if _, ok := declared[k]; !ok {
			wiredButNotDeclared = append(wiredButNotDeclared, k)
		}
	}
	if len(declaredButNotWired) > 0 || len(wiredButNotDeclared) > 0 {
		t.Errorf("declared/wired drift: declared-but-not-wired=%v wired-but-not-declared=%v (declared=%v wired=%v)",
			declaredButNotWired, wiredButNotDeclared,
			keysOf(declared), keysOf(wired))
	}
}

// keysOf returns a deterministic list of map keys for diagnostic
// messages (Go map iteration order is unspecified). Sort by string
// repr so failure messages stay stable across runs.
func keysOf(m map[agentpkg.ProbeIntentKind]struct{}) []agentpkg.ProbeIntentKind {
	out := make([]agentpkg.ProbeIntentKind, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// crude sort by string conversion — adequate for diagnostics
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if string(out[i]) > string(out[j]) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// TestProbeIntentDrift_StartDetectorSwitchCoversAllSupportedKinds is
// the AST-level drift gate (W6-6 R2 F3 finding).
//
// The runtime drift tests above all read from supportedKinds (a map
// constructed in Module.New) and from the ProbeIntent slice declared
// by each provider. None of them inspect the body of the
// `m.probeIntentDisp.startDetector = func(ctx, mod, kind, ...) {
// switch kind { case ... } }` closure that ACTUALLY ROUTES production
// signals — the wire test (TestDispatcher_CodexScreenChangeWire_*) +
// the integration tests both swap startDetector to a fake closure to
// inject a fake screenWatcher, so the production switch case body is
// untested in this test layer.
//
// Failure mode that this guards: a refactor or merge accident removes
// `case agentpkg.ProbeIntentKindScreenChange: codex.StartScreenChange
// Detector(...)` from the production switch but leaves the entry in
// supportedKinds. Lifecycle's audit-F6 gate would still consider the
// kind "supported" and arm an active intent; the dispatcher would
// then fall through to the default branch (waits on ctx, never
// emits) and ScreenChange permission-approval recovery would silently
// stop working.
//
// The check parses internal/module/agent/module.go, walks Module.New
// to find the AssignStmt assigning the closure to
// `m.probeIntentDisp.startDetector`, descends into the closure body,
// finds the `switch kind { ... }` SwitchStmt, and collects the
// identifier names of every case expression of the form
// `agentpkg.<KindConst>`. The default case is excluded.
//
// Then collects supportedKinds entries from the same file (the map
// literal assigned to `m.probeIntentDisp.supportedKinds`).
//
// The two sets must be exactly equal. Asymmetric difference fails
// the test with a precise diagnostic.
func TestProbeIntentDrift_StartDetectorSwitchCoversAllSupportedKinds(t *testing.T) {
	const modulePath = "module.go"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, modulePath, nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse %s: %v", modulePath, err)
	}

	switchKinds, switchOK := extractStartDetectorSwitchKinds(t, file)
	if !switchOK {
		t.Fatalf("could not locate `m.probeIntentDisp.startDetector = func(...)` switch in %s — has Module.New been restructured?", modulePath)
	}
	mapKinds, mapOK := extractSupportedKindsMapKeys(t, file)
	if !mapOK {
		t.Fatalf("could not locate `m.probeIntentDisp.supportedKinds = map[...]struct{}{...}` literal in %s", modulePath)
	}
	if len(switchKinds) == 0 {
		t.Fatalf("startDetector switch had zero non-default cases — suite is vacuous")
	}
	if len(mapKinds) == 0 {
		t.Fatalf("supportedKinds map literal had zero entries — suite is vacuous")
	}

	// Symmetric difference between the two name sets.
	switchOnly := setDifference(switchKinds, mapKinds)
	mapOnly := setDifference(mapKinds, switchKinds)
	if len(switchOnly) > 0 || len(mapOnly) > 0 {
		t.Errorf("startDetector switch ↔ supportedKinds drift:\n"+
			"  switch-cases-not-in-supportedKinds = %v\n"+
			"  supportedKinds-keys-not-in-switch  = %v\n"+
			"  switch  = %v\n"+
			"  support = %v\n"+
			"Add a case in startDetector AND an entry in supportedKinds together (audit F6 fail-closed contract).",
			switchOnly, mapOnly, sortedNames(switchKinds), sortedNames(mapKinds))
	}
}

// extractStartDetectorSwitchKinds walks the AST of module.go and
// returns the set of case-clause expression names of form
// `agentpkg.<Selector>` inside the switch on `kind` in the closure
// assigned to `m.probeIntentDisp.startDetector`. Only the selector
// portion (e.g. `ProbeIntentKindProcessDead`) is returned — sufficient
// because the spec restricts case expressions to that import-qualified
// shape.
func extractStartDetectorSwitchKinds(t *testing.T, file *ast.File) (map[string]struct{}, bool) {
	t.Helper()
	out := make(map[string]struct{})
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		if len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		// LHS shape: m.probeIntentDisp.startDetector
		if !isSelector(assign.Lhs[0], "m", "probeIntentDisp", "startDetector") {
			return true
		}
		funcLit, ok := assign.Rhs[0].(*ast.FuncLit)
		if !ok {
			return true
		}
		// Find the `switch kind { ... }` inside the func body.
		ast.Inspect(funcLit.Body, func(inner ast.Node) bool {
			sw, ok := inner.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			tagIdent, ok := sw.Tag.(*ast.Ident)
			if !ok || tagIdent.Name != "kind" {
				return true
			}
			found = true
			for _, stmt := range sw.Body.List {
				cc, ok := stmt.(*ast.CaseClause)
				if !ok || len(cc.List) == 0 {
					// default case has empty List.
					continue
				}
				for _, expr := range cc.List {
					sel, ok := expr.(*ast.SelectorExpr)
					if !ok {
						continue
					}
					ident, ok := sel.X.(*ast.Ident)
					if !ok || ident.Name != "agentpkg" {
						continue
					}
					out[sel.Sel.Name] = struct{}{}
				}
			}
			return false
		})
		return false
	})
	return out, found
}

// extractSupportedKindsMapKeys finds the map literal assigned to
// `m.probeIntentDisp.supportedKinds` and returns the set of selector
// names used as keys (e.g. `ProbeIntentKindProcessDead`).
func extractSupportedKindsMapKeys(t *testing.T, file *ast.File) (map[string]struct{}, bool) {
	t.Helper()
	out := make(map[string]struct{})
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		if len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		if !isSelector(assign.Lhs[0], "m", "probeIntentDisp", "supportedKinds") {
			return true
		}
		composite, ok := assign.Rhs[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		found = true
		for _, elt := range composite.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			sel, ok := kv.Key.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok || ident.Name != "agentpkg" {
				continue
			}
			out[sel.Sel.Name] = struct{}{}
		}
		return false
	})
	return out, found
}

// isSelector reports whether the expression matches a chained
// SelectorExpr `head.middle.tail` (e.g. `m.probeIntentDisp.startDetector`).
// Returns false on any shape mismatch — keeps the AST walk strict so
// stale matches don't suppress drift.
func isSelector(expr ast.Expr, head, middle, tail string) bool {
	outer, ok := expr.(*ast.SelectorExpr)
	if !ok || outer.Sel.Name != tail {
		return false
	}
	innerSel, ok := outer.X.(*ast.SelectorExpr)
	if !ok || innerSel.Sel.Name != middle {
		return false
	}
	rootIdent, ok := innerSel.X.(*ast.Ident)
	if !ok || rootIdent.Name != head {
		return false
	}
	return true
}

// setDifference returns elements in a not in b.
func setDifference(a, b map[string]struct{}) []string {
	var out []string
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	// stable order for diagnostics
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[i] > out[j] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// sortedNames returns the keys of m in lexical order.
func sortedNames(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[i] > out[j] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
