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
type driftFixture struct {
	eventName string
	rawJSON   string
}

// providerFixtures enumerates representative event payloads for each provider
// such that the union of `DeriveStatus(...)` results covers EVERY Status the
// provider declares via SupportedStatuses. Per Phase 1 plan §1.5: fixtures
// MUST cover each declared Status's emit path (including sub-branches like
// Notification(idle_prompt) → Idle), or D1 produces a false positive.
//
// SubagentStart/Stop are intentionally included for cc/opencode (and codex
// post-Phase-1) — they Valid=true with Status="" and are filtered from the
// emittedSet, so they document the catalog without polluting the comparison.
var providerFixtures = map[string][]driftFixture{
	"cc": {
		{"SessionStart", `{}`},                                  // → Idle
		{"UserPromptSubmit", `{}`},                              // → Running
		{"Notification", `{"notification_type":"permission_prompt"}`}, // → Waiting
		{"Notification", `{"notification_type":"elicitation_dialog"}`}, // → Waiting (sub-branch)
		{"Notification", `{"notification_type":"idle_prompt"}`},  // → Idle (sub-branch)
		{"Notification", `{"notification_type":"auth_success"}`}, // → Idle (sub-branch)
		{"PermissionRequest", `{"tool_name":"Bash"}`},           // → Waiting
		{"Stop", `{}`},                                          // → Idle
		{"StopFailure", `{"error":"x"}`},                        // → Error
		{"SessionEnd", `{}`},                                    // → Clear
		{"SubagentStart", `{"agent_id":"a"}`},                   // Valid=true, Status="" (filtered)
		{"SubagentStop", `{"agent_id":"a"}`},                    // Valid=true, Status="" (filtered)
	},
	"codex": {
		{"SessionStart", `{}`},                                  // → Idle
		{"UserPromptSubmit", `{}`},                              // → Running
		{"Notification", `{"notification_type":"permission_prompt"}`}, // → Waiting
		{"Notification", `{"notification_type":"elicitation_dialog"}`}, // → Waiting
		{"Notification", `{"notification_type":"idle_prompt"}`},  // → Idle
		{"Notification", `{"notification_type":"auth_success"}`}, // → Idle
		{"PermissionRequest", `{"tool_name":"Bash"}`},           // → Waiting
		{"Stop", `{}`},                                          // → Idle
		{"StopFailure", `{"error":"x"}`},                        // → Error
		{"SessionEnd", `{}`},                                    // → Clear
		{"SubagentStart", `{"agent_id":"a"}`},                   // Valid=true, Status=""
		{"SubagentStop", `{"agent_id":"a"}`},                    // Valid=true, Status=""
	},
	"opencode": {
		{"SessionStart", `{}`},                                  // → Idle
		{"UserPromptSubmit", `{}`},                              // → Running
		{"PermissionRequest", `{"request_type":"tool"}`},        // → Waiting
		{"Stop", `{}`},                                          // → Idle
		{"StopFailure", `{"error":"x"}`},                        // → Error
		{"SessionEnd", `{}`},                                    // → Clear
		{"SubagentStart", `{"agent_id":"a"}`},                   // Valid=true, Status=""
		{"SubagentStop", `{"agent_id":"a"}`},                    // Valid=true, Status=""
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
// the set of statuses returned by SupportedStatuses() is exactly equal to the
// set of statuses actually emitted (Valid=true, Status!="") when DeriveStatus
// is called against the per-provider fixture catalog. Bidirectional check:
// drift in either direction (declared-but-never-emitted, emitted-but-not-
// declared) fails the test.
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
			for _, fx := range fixtures {
				result := provider.DeriveStatus(fx.eventName, json.RawMessage(fx.rawJSON))
				if !result.Valid {
					continue
				}
				if result.Status == "" {
					continue
				}
				emittedSet[result.Status] = true
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
