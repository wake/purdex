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
		{"SessionStart", `{}`, agent.StatusIdle, true},
		{"UserPromptSubmit", `{}`, agent.StatusRunning, true},
		{"Notification", `{"notification_type":"permission_prompt"}`, agent.StatusWaiting, true},
		{"Notification", `{"notification_type":"elicitation_dialog"}`, agent.StatusWaiting, true},
		{"Notification", `{"notification_type":"idle_prompt"}`, agent.StatusIdle, true},
		{"Notification", `{"notification_type":"auth_success"}`, agent.StatusIdle, true},
		{"PermissionRequest", `{"tool_name":"Bash"}`, agent.StatusWaiting, true},
		{"Stop", `{}`, agent.StatusIdle, true},
		{"StopFailure", `{"error":"x"}`, agent.StatusError, true},
		{"SessionEnd", `{}`, agent.StatusClear, true},
		{"SubagentStart", `{"agent_id":"a"}`, "", true}, // Valid=true, Status="" (filtered)
		{"SubagentStop", `{"agent_id":"a"}`, "", true},  // Valid=true, Status="" (filtered)
	},
	"codex": {
		{"SessionStart", `{}`, agent.StatusIdle, true},
		{"UserPromptSubmit", `{}`, agent.StatusRunning, true},
		{"Notification", `{"notification_type":"permission_prompt"}`, agent.StatusWaiting, true},
		{"Notification", `{"notification_type":"elicitation_dialog"}`, agent.StatusWaiting, true},
		{"Notification", `{"notification_type":"idle_prompt"}`, agent.StatusIdle, true},
		{"Notification", `{"notification_type":"auth_success"}`, agent.StatusIdle, true},
		{"PermissionRequest", `{"tool_name":"Bash"}`, agent.StatusWaiting, true},
		{"Stop", `{}`, agent.StatusIdle, true},
		{"StopFailure", `{"error":"x"}`, agent.StatusError, true},
		{"SessionEnd", `{}`, agent.StatusClear, true},
		{"SubagentStart", `{"agent_id":"a"}`, "", true},
		{"SubagentStop", `{"agent_id":"a"}`, "", true},
	},
	"opencode": {
		{"SessionStart", `{}`, agent.StatusIdle, true},
		{"UserPromptSubmit", `{}`, agent.StatusRunning, true},
		{"PermissionRequest", `{"request_type":"tool"}`, agent.StatusWaiting, true},
		{"Stop", `{}`, agent.StatusIdle, true},
		{"StopFailure", `{"error":"x"}`, agent.StatusError, true},
		{"SessionEnd", `{}`, agent.StatusClear, true},
		{"SubagentStart", `{"agent_id":"a"}`, "", true},
		{"SubagentStop", `{"agent_id":"a"}`, "", true},
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

// TestDriftDeclaredEqualsEmitted asserts that, for every registered provider,
// EVERY fixture independently produces its declared (wantValid, wantStatus)
// AND the union of emitted statuses exactly equals SupportedStatuses().
//
// Per-fixture assertion (not just set equality) is the load-bearing change
// from the codex review: deleting one polymorphic Notification sub-branch
// (e.g. "elicitation_dialog" → Waiting) would not change the set
// {Waiting,Idle,Running,Error,Clear} because Waiting is still produced by
// other branches. Per-fixture assertion catches that exact regression.
//
// Set-equality is kept as a complementary safeguard for the inverse
// direction — emitting a status that no fixture covers, or declaring one
// no fixture exercises.
func TestDriftDeclaredEqualsEmitted(t *testing.T) {
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

			declaredSet := make(map[agent.Status]bool, len(row.Declared))
			for _, s := range row.Declared {
				declaredSet[s] = true
			}

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

			if !setsEqual(declaredSet, emittedSet) {
				t.Fatalf("drift for provider %q: declared=%v, emitted=%v\n"+
					"  declared-not-emitted (fix: add fixture covering this status, or remove from declaration): %v\n"+
					"  emitted-not-declared (fix: add to SupportedStatuses, or remove from DeriveStatus): %v",
					row.AgentType,
					statusSetSorted(declaredSet),
					statusSetSorted(emittedSet),
					statusSetSorted(diffSet(declaredSet, emittedSet)),
					statusSetSorted(diffSet(emittedSet, declaredSet)),
				)
			}
		})
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
