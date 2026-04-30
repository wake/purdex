package agent_test

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent/codex"
	"github.com/wake/purdex/internal/agent/opencode"
)

// driftFixture is one event input case used by the drift test.
//
// wantStatus is the Status this fixture is designed to provoke (or "" if the
// event is detail-only such as SubagentStart/Stop). wantValid encodes the
// expected Valid bit; non-mappable cases (e.g. cc compact SessionStart) live
// in cc-specific tests, not here.
//
// Per-fixture assertion is required (Phase 1 review finding): set-equality
// alone cannot detect the deletion of a polymorphic sub-branch like
// Notification(elicitation_dialog), because Waiting is still emitted via
// other branches. Each fixture must independently match its wantStatus.
type driftFixture struct {
	eventName  string
	rawJSON    string
	wantStatus agent.Status // empty for detail-only events
	wantValid  bool         // currently always true; placeholder for future negative fixtures
}

// providerFixtures enumerates representative event payloads for each provider
// such that the union of `DeriveStatus(...)` results covers EVERY Status the
// provider declares via SupportedStatuses. Per Phase 1 plan §1.5 + the codex
// review finding: every emit path AND sub-branch is asserted individually.
//
// SubagentStart/Stop are intentionally included for cc/codex/opencode —
// they Valid=true with Status="" and are filtered from the emittedSet so they
// document the catalog without polluting the set-equality comparison.
var providerFixtures = map[string][]driftFixture{
	"cc": {
		{"PdxSessionStart", `{}`, agent.StatusIdle, true},
		{"PdxUserPromptSubmit", `{}`, agent.StatusRunning, true},
		{"PdxNotification", `{"notification_type":"permission_prompt"}`, agent.StatusWaiting, true},
		{"PdxNotification", `{"notification_type":"elicitation_dialog"}`, agent.StatusWaiting, true},
		{"PdxNotification", `{"notification_type":"idle_prompt"}`, agent.StatusIdle, true},
		{"PdxNotification", `{"notification_type":"auth_success"}`, agent.StatusIdle, true},
		{"PdxPermissionRequest", `{"tool_name":"Bash"}`, agent.StatusWaiting, true},
		{"PdxPostToolUse", `{"tool_name":"Bash"}`, agent.StatusRunning, true},
		{"PdxStop", `{}`, agent.StatusIdle, true},
		{"PdxStopFailure", `{"error":"x"}`, agent.StatusError, true},
		{"PdxSessionEnd", `{}`, agent.StatusClear, true},
		{"PdxSubagentStart", `{"agent_id":"a"}`, "", true}, // Valid=true, Status="" (filtered)
		{"PdxSubagentStop", `{"agent_id":"a"}`, "", true},  // Valid=true, Status="" (filtered)
	},
	"codex": {
		{"PdxSessionStart", `{}`, agent.StatusIdle, true},
		{"PdxUserPromptSubmit", `{}`, agent.StatusRunning, true},
		{"PdxNotification", `{"notification_type":"permission_prompt"}`, agent.StatusWaiting, true},
		{"PdxNotification", `{"notification_type":"elicitation_dialog"}`, agent.StatusWaiting, true},
		{"PdxNotification", `{"notification_type":"idle_prompt"}`, agent.StatusIdle, true},
		{"PdxNotification", `{"notification_type":"auth_success"}`, agent.StatusIdle, true},
		{"PdxPermissionRequest", `{"tool_name":"Bash"}`, agent.StatusWaiting, true},
		{"PdxStop", `{}`, agent.StatusIdle, true},
		{"PdxStopFailure", `{"error":"x"}`, agent.StatusError, true},
		{"PdxSessionEnd", `{}`, agent.StatusClear, true},
		{"PdxSubagentStart", `{"agent_id":"a"}`, "", true},
		{"PdxSubagentStop", `{"agent_id":"a"}`, "", true},
	},
	"opencode": {
		{"PdxSessionStart", `{}`, agent.StatusIdle, true},
		{"PdxUserPromptSubmit", `{}`, agent.StatusRunning, true},
		{"PdxPermissionRequest", `{"request_type":"tool"}`, agent.StatusWaiting, true},
		{"PdxStop", `{}`, agent.StatusIdle, true},
		{"PdxStopFailure", `{"error":"x"}`, agent.StatusError, true},
		{"PdxSessionEnd", `{}`, agent.StatusClear, true},
		{"PdxSubagentStart", `{"agent_id":"a"}`, "", true},
		{"PdxSubagentStop", `{"agent_id":"a"}`, "", true},
	},
}

// driftRegistry builds a fresh registry with all three real providers in the
// production registration order.
func driftRegistry() *agent.Registry {
	r := agent.NewRegistry()
	r.Register(cc.NewProvider(nil, nil, nil, nil))
	r.Register(codex.NewProvider())
	r.Register(opencode.NewProvider())
	return r
}

func statusSetSorted(set map[agent.Status]bool) []string {
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, string(s))
	}
	sort.Strings(out)
	return out
}

// TestDriftThreeWayEquality asserts that, for every registered provider,
// three sets of Status values are pairwise equal:
//
//  1. declaredSet — union of Events().EmitsStatus
//  2. emittedSet  — actually produced by DeriveStatus on the provider's
//     fixtures (Valid=true, Status != "")
//  3. supportedSet — returned by SupportedStatuses()
//
// This is the post-`Events()-SSoT` upgrade of the prior two-way drift test
// (TestDriftDeclaredEqualsEmitted). SupportedStatuses is now derived from
// Events() via DeriveSupportedStatuses, so in steady state 1 == 3 by
// construction; the assertion still fires if anyone re-introduces a
// hard-coded SupportedStatuses literal that disagrees with Events().
//
// Per-fixture assertion is retained (Phase 1 review finding): set-equality
// alone cannot catch the deletion of a polymorphic Notification sub-branch
// because other branches keep the aggregate Status set intact.
func TestDriftThreeWayEquality(t *testing.T) {
	r := driftRegistry()
	rows := agent.Coverage(r)
	if len(rows) == 0 {
		t.Fatal("no coverage rows — registry empty")
	}

	for _, row := range rows {
		row := row
		t.Run(row.AgentType, func(t *testing.T) {
			provider, ok := r.Get(row.AgentType)
			if !ok {
				t.Fatalf("registry.Get(%q) failed", row.AgentType)
			}
			fixtures, ok := providerFixtures[row.AgentType]
			if !ok {
				t.Fatalf("no driftFixture entry for provider %q — add fixtures or remove provider", row.AgentType)
			}

			// supportedSet comes from StatusSupporter (via Coverage row).
			supportedSet := make(map[agent.Status]bool, len(row.Declared))
			for _, s := range row.Declared {
				supportedSet[s] = true
			}

			// declaredSet comes from the Events() EmitsStatus union.
			declaredSet := make(map[agent.Status]bool)
			installer, ok := provider.(agent.HookInstaller)
			if !ok {
				t.Fatalf("provider %q does not implement HookInstaller — Events() unavailable", row.AgentType)
			}
			for _, spec := range installer.Events() {
				for _, s := range spec.EmitsStatus {
					declaredSet[s] = true
				}
			}

			// emittedSet comes from DeriveStatus driven by fixtures.
			emittedSet := make(map[agent.Status]bool)
			for i, fx := range fixtures {
				result := provider.DeriveStatus(fx.eventName, json.RawMessage(fx.rawJSON))
				// Per-fixture assertion — each branch independently verified.
				if result.Valid != fx.wantValid {
					t.Errorf("provider %q fixture[%d] %s %s: Valid=%v, want %v",
						row.AgentType, i, fx.eventName, fx.rawJSON, result.Valid, fx.wantValid)
					continue
				}
				if result.Status != fx.wantStatus {
					t.Errorf("provider %q fixture[%d] %s %s: Status=%q, want %q",
						row.AgentType, i, fx.eventName, fx.rawJSON, result.Status, fx.wantStatus)
					continue
				}
				if result.Valid && result.Status != "" {
					emittedSet[result.Status] = true
				}
			}

			// Report any pair-wise mismatch with three-way diagnostic context.
			if !setsEqual(declaredSet, emittedSet) ||
				!setsEqual(declaredSet, supportedSet) ||
				!setsEqual(emittedSet, supportedSet) {
				t.Fatalf("drift for provider %q three-way mismatch:\n"+
					"  declared (from Events().EmitsStatus union): %v\n"+
					"  emitted (from DeriveStatus fixtures):       %v\n"+
					"  supported (from SupportedStatuses()):       %v\n"+
					"  declared-not-emitted (fix: add fixture, or remove from Events().EmitsStatus): %v\n"+
					"  emitted-not-declared (fix: add to Events().EmitsStatus, or remove from DeriveStatus): %v\n"+
					"  declared-not-supported (fix: SupportedStatuses no longer derives from Events()): %v\n"+
					"  supported-not-declared (fix: reverse of the previous row): %v",
					row.AgentType,
					statusSetSorted(declaredSet),
					statusSetSorted(emittedSet),
					statusSetSorted(supportedSet),
					statusSetSorted(diffSet(declaredSet, emittedSet)),
					statusSetSorted(diffSet(emittedSet, declaredSet)),
					statusSetSorted(diffSet(declaredSet, supportedSet)),
					statusSetSorted(diffSet(supportedSet, declaredSet)),
				)
			}
		})
	}
}

// TestDriftFixtureCoversAllEvents asserts every Events() event for each
// provider appears in providerFixtures at least once. Guards the SSoT
// invariant that declared hook events all have executable coverage, so
// "declared but untested" events cannot silently accumulate.
func TestDriftFixtureCoversAllEvents(t *testing.T) {
	r := driftRegistry()
	for _, row := range agent.Coverage(r) {
		provider, ok := r.Get(row.AgentType)
		if !ok {
			t.Errorf("registry.Get(%q) failed", row.AgentType)
			continue
		}
		installer, ok := provider.(agent.HookInstaller)
		if !ok {
			continue
		}
		fixtures := providerFixtures[row.AgentType]
		covered := make(map[string]bool, len(fixtures))
		for _, fx := range fixtures {
			covered[fx.eventName] = true
		}
		for _, spec := range installer.Events() {
			if !agent.IsInstallableHookSpec(spec) {
				continue
			}
			// Post-W2: every catalog entry is keyed on PurdexName.
			key := spec.PurdexName
			if !covered[key] {
				t.Errorf("provider %q: installable Events() declares %q but providerFixtures has no entry (add a fixture or make the spec non-installable)",
					row.AgentType, key)
			}
		}
	}
}

// TestDriftFixtureCoverageNonEmpty guards against an empty fixture set silently
// producing emitted=empty == declared=empty (false positive in D1). Every
// registered provider must have a non-empty fixture entry.
func TestDriftFixtureCoverageNonEmpty(t *testing.T) {
	r := driftRegistry()
	for _, row := range agent.Coverage(r) {
		fixtures, ok := providerFixtures[row.AgentType]
		if !ok {
			t.Errorf("provider %q has no fixture entry in providerFixtures", row.AgentType)
			continue
		}
		if len(fixtures) == 0 {
			t.Errorf("provider %q has empty fixture slice", row.AgentType)
		}
	}
}

// TestDriftPerEventEmitsStatusMatch is the fix-plan §2.5 D5 assertion:
// for every provider × every declared event × every fixture of that event,
// the set of Status values DeriveStatus actually returns must equal the
// spec's EmitsStatus set (bidirectional strong equality).
//
// Why set-equality per-event and not aggregate: the aggregate test
// (TestDriftThreeWayEquality) unions across events and therefore cannot
// detect a single event that over-declares EmitsStatus while another event
// happens to cover the over-declared Status. Example failure mode this
// guards: marking Stop's EmitsStatus as [Idle, Clear] — aggregate still
// matches because SessionEnd emits Clear — but no Stop fixture ever emits
// Clear, so the declaration is a lie. D5 catches it because Stop's emitSet
// would be {Idle}, not equal to {Idle, Clear}.
//
// Detail-only events (EmitsStatus = []) are checked: no fixture of that
// event may emit a non-empty Status.
//
// FutureOnly events are covered naturally — their EmitsStatus is declared,
// providerFixtures has their fixtures, and the per-event set-equality fires
// either way. This is deliberate per plan §2.5 (no "D6 fixture-must-not-
// include-FutureOnly" guard): FutureOnly is an installer/checker-facet bit
// only, not a DeriveStatus-capability filter.
func TestDriftPerEventEmitsStatusMatch(t *testing.T) {
	r := driftRegistry()
	for _, row := range agent.Coverage(r) {
		row := row
		t.Run(row.AgentType, func(t *testing.T) {
			provider, ok := r.Get(row.AgentType)
			if !ok {
				t.Fatalf("registry.Get(%q) failed", row.AgentType)
			}
			installer, ok := provider.(agent.HookInstaller)
			if !ok {
				t.Fatalf("provider %q does not implement HookInstaller", row.AgentType)
			}
			fixturesByEvent := make(map[string][]driftFixture)
			for _, fx := range providerFixtures[row.AgentType] {
				fixturesByEvent[fx.eventName] = append(fixturesByEvent[fx.eventName], fx)
			}
			for _, spec := range installer.Events() {
				if !agent.IsInstallableHookSpec(spec) {
					continue
				}
				spec := spec
				key := spec.PurdexName
				t.Run(key, func(t *testing.T) {
					fxs, ok := fixturesByEvent[key]
					if !ok || len(fxs) == 0 {
						t.Fatalf("provider %q event %q has no fixture", row.AgentType, key)
					}
					// Declared set (from the spec).
					declared := make(map[agent.Status]bool, len(spec.EmitsStatus))
					for _, s := range spec.EmitsStatus {
						declared[s] = true
					}
					// Emitted set (from every fixture).
					emitted := make(map[agent.Status]bool)
					for _, fx := range fxs {
						result := provider.DeriveStatus(fx.eventName, json.RawMessage(fx.rawJSON))
						if result.Valid && result.Status != "" {
							emitted[result.Status] = true
						}
					}
					if !setsEqual(declared, emitted) {
						t.Fatalf("provider %q event %q emits-status mismatch:\n"+
							"  declared: %v\n"+
							"  emitted:  %v\n"+
							"  declared-not-emitted (remove from EmitsStatus or add a fixture): %v\n"+
							"  emitted-not-declared (add to EmitsStatus or fix DeriveStatus):   %v",
							row.AgentType, key,
							statusSetSorted(declared),
							statusSetSorted(emitted),
							statusSetSorted(diffSet(declared, emitted)),
							statusSetSorted(diffSet(emitted, declared)),
						)
					}
				})
			}
		})
	}
}

func setsEqual(a, b map[agent.Status]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func diffSet(a, b map[agent.Status]bool) map[agent.Status]bool {
	out := make(map[agent.Status]bool)
	for k := range a {
		if !b[k] {
			out[k] = true
		}
	}
	return out
}
