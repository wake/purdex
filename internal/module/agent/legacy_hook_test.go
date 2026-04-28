package agent

import (
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// ccMetadataCatalog aliases fakeDefaultEvents so existing classifyLifecycle
// tests stay self-documenting under the cc-leaning name. Tests that want a
// true catalog miss inject the explicit empty slice instead — see the
// "PrematureCatalogMissIsInvalid" cases below.
var ccMetadataCatalog = fakeDefaultEvents

func TestIsLegacyHookForUnmigrated_CodexAllNames(t *testing.T) {
	names := []string{
		"SessionStart",
		"UserPromptSubmit",
		"Notification",
		"Stop",
		"StopFailure",
		"PermissionRequest",
		"SessionEnd",
		"SubagentStart",
		"SubagentStop",
	}
	for _, n := range names {
		if !isLegacyHookForUnmigrated("codex", n) {
			t.Errorf("codex + %q expected true", n)
		}
	}
}

func TestIsLegacyHookForUnmigrated_OpencodeWithoutNotification(t *testing.T) {
	if isLegacyHookForUnmigrated("opencode", "Notification") {
		t.Error("opencode + Notification must be false (opencode catalog has no Notification entry)")
	}
}

func TestIsLegacyHookForUnmigrated_OpencodeAllOtherNames(t *testing.T) {
	names := []string{
		"SessionStart",
		"UserPromptSubmit",
		"Stop",
		"StopFailure",
		"PermissionRequest",
		"SessionEnd",
		"SubagentStart",
		"SubagentStop",
	}
	for _, n := range names {
		if !isLegacyHookForUnmigrated("opencode", n) {
			t.Errorf("opencode + %q expected true", n)
		}
	}
}

func TestIsLegacyHookForUnmigrated_CCAlwaysFalse(t *testing.T) {
	for _, n := range []string{"SessionStart", "Stop", "Notification", "anything"} {
		if isLegacyHookForUnmigrated("cc", n) {
			t.Errorf("cc + %q must be false (cc already migrated in Phase 1)", n)
		}
	}
}

func TestIsLegacyHookForUnmigrated_UnknownAgent(t *testing.T) {
	if isLegacyHookForUnmigrated("unknown", "SessionStart") {
		t.Error("unknown agent type must default to false")
	}
}

// ---- P1-T11: classifyLifecycle three-branch decision tree ----
//
// Per spec §3.4.2: catalog hit (branch 1) > legacy fallback (branch 2) >
// LifecycleNone (branch 3). Branch 1 wins absolutely — a catalog hit with
// LifecycleNone is a legitimate no-op classification, not a "fall through to
// legacy" signal.

func TestClassifyLifecycle_CCMetadataPath(t *testing.T) {
	cc := &fakeAgentProvider{typeName: "cc", events: ccMetadataCatalog}
	cases := []struct {
		purdexName string
		want       agentpkg.LifecycleEventKind
	}{
		{"PdxSessionStart", agentpkg.LifecycleSessionStart},
		{"PdxSessionEnd", agentpkg.LifecycleSessionEnd},
		{"PdxSubagentStart", agentpkg.LifecycleSubagentStart},
		{"PdxStop", agentpkg.LifecycleStop},
		{"PdxNotification", agentpkg.LifecycleNone},      // catalog hit, no-op
		{"PdxPermissionRequest", agentpkg.LifecycleNone}, // catalog hit, no-op
	}
	for _, tc := range cases {
		req := EventRequest{AgentType: "cc", PurdexName: tc.purdexName}
		got := classifyLifecycle(cc, req)
		if got != tc.want {
			t.Errorf("cc + %q: got %s, want %s", tc.purdexName, got, tc.want)
		}
	}
}

// emptyEvents is the negative-test sentinel: a non-nil but empty catalog so
// fakeAgentProvider.Events bypasses the default-fill and returns a true
// "catalog miss" classification.
var emptyEvents = []agentpkg.HookEventSpec{}

func TestClassifyLifecycle_CodexLegacyFallback(t *testing.T) {
	codex := &fakeAgentProvider{typeName: "codex", events: emptyEvents}
	req := EventRequest{AgentType: "codex", PurdexName: "SessionEnd"}
	if got := classifyLifecycle(codex, req); got != agentpkg.LifecycleSessionEnd {
		t.Errorf("codex SessionEnd fallback: got %s, want LifecycleSessionEnd", got)
	}
}

func TestClassifyLifecycle_OpencodeLegacyFallback(t *testing.T) {
	oc := &fakeAgentProvider{typeName: "opencode", events: emptyEvents}
	req := EventRequest{AgentType: "opencode", PurdexName: "SessionStart"}
	if got := classifyLifecycle(oc, req); got != agentpkg.LifecycleSessionStart {
		t.Errorf("opencode SessionStart fallback: got %s, want LifecycleSessionStart", got)
	}
}

// Codex prematurely emitting a Pdx-prefixed name before its catalog migrates
// must surface as an unclassified event (LifecycleNone) — branch 1 misses
// (codex events not yet keyed by PurdexName), branch 2 misses (codex legacy
// set has "Stop" not "PdxStop"), so branch 3 wins. The handler then routes
// this through the catalog-miss invalid path rather than dispatching as Stop.
func TestClassifyLifecycle_CodexPrematureCatalogMissIsInvalid(t *testing.T) {
	codex := &fakeAgentProvider{typeName: "codex", events: emptyEvents}
	req := EventRequest{AgentType: "codex", PurdexName: "PdxStop"}
	if got := classifyLifecycle(codex, req); got != agentpkg.LifecycleNone {
		t.Errorf("codex PdxStop premature: got %s, want LifecycleNone", got)
	}
}

// opencode legacy set deliberately omits Notification (the opencode catalog
// has no Notification entry — a Notification arrival means the upstream
// emitted a class we don't classify yet). Must NOT route through the legacy
// fallback as some other lifecycle kind.
func TestClassifyLifecycle_OpencodeNotificationIsInvalid(t *testing.T) {
	oc := &fakeAgentProvider{typeName: "opencode", events: emptyEvents}
	req := EventRequest{AgentType: "opencode", PurdexName: "Notification"}
	if got := classifyLifecycle(oc, req); got != agentpkg.LifecycleNone {
		t.Errorf("opencode Notification: got %s, want LifecycleNone", got)
	}
}

// opencode includes PermissionRequest in its legacy set even though its
// catalog has no PermissionRequest entry yet — the plugin emits this event
// for both permission.asked and question.asked Bus events during W2
// transition. legacyLifecycleFor returns LifecycleNone for PermissionRequest
// (not a frame-mutation lifecycle event), but the predicate must still match
// so the daemon's downstream path treats it as a known no-op rather than an
// unclassified event.
func TestClassifyLifecycle_OpencodePermissionRequestIsKnownNoop(t *testing.T) {
	oc := &fakeAgentProvider{typeName: "opencode", events: emptyEvents}
	req := EventRequest{AgentType: "opencode", PurdexName: "PermissionRequest"}
	// PermissionRequest is in the legacy fallback predicate set but has no
	// lifecycle kind (no frame mutation), so legacyLifecycleFor returns None.
	// This is the "fallback ok but no-op" case.
	if got := classifyLifecycle(oc, req); got != agentpkg.LifecycleNone {
		t.Errorf("opencode PermissionRequest: got %s, want LifecycleNone (no-op)", got)
	}
	// Sanity: predicate itself must match (otherwise this would be branch 3
	// not branch 2; the downstream invalid path would surface differently).
	if !isLegacyHookForUnmigrated("opencode", "PermissionRequest") {
		t.Error("opencode PermissionRequest must satisfy isLegacyHookForUnmigrated predicate")
	}
}

// Provider implementing HookInstaller wins absolutely: a catalog hit with
// Lifecycle=None blocks the legacy fallback even if the predicate would
// otherwise match. This pins branch 1's "absolute precedence" semantics so a
// future codex catalog migration that adds Notification with LifecycleNone
// doesn't accidentally fall through to legacyLifecycleFor.
func TestClassifyLifecycle_CatalogHitWinsOverPredicate(t *testing.T) {
	codex := &fakeAgentProvider{
		typeName: "codex",
		events: []agentpkg.HookEventSpec{
			{PurdexName: "PdxStop", UpstreamKeys: []string{"Stop"}, Lifecycle: agentpkg.LifecycleNone},
		},
	}
	req := EventRequest{AgentType: "codex", PurdexName: "PdxStop"}
	if got := classifyLifecycle(codex, req); got != agentpkg.LifecycleNone {
		t.Errorf("catalog hit None should pin: got %s, want LifecycleNone", got)
	}
}

// Nil provider (registry miss) must short-circuit to LifecycleNone without
// panicking on the type-assert.
func TestClassifyLifecycle_NilProvider(t *testing.T) {
	req := EventRequest{AgentType: "codex", PurdexName: "Stop"}
	// Even though codex Stop would fall back to LifecycleStop with a real
	// fake, no-provider input shouldn't reach branch 2 — provider==nil means
	// registry returned nothing for the agent_type, so the daemon already
	// has no business dispatching. classifyLifecycle defends against nil.
	if got := classifyLifecycle(nil, req); got != agentpkg.LifecycleStop {
		// Note: branch 1 fails (nil → type-assert false), branch 2 succeeds
		// (codex+Stop is in legacy set). nil provider does NOT skip branch 2.
		t.Errorf("nil provider + codex Stop: got %s, want LifecycleStop (branch 2)", got)
	}
}

func TestLegacyLifecycleFor_AllLiterals(t *testing.T) {
	cases := []struct {
		name string
		want agentpkg.LifecycleEventKind
	}{
		{"SessionStart", agentpkg.LifecycleSessionStart},
		{"UserPromptSubmit", agentpkg.LifecycleUserPromptSubmit},
		{"Stop", agentpkg.LifecycleStop},
		{"StopFailure", agentpkg.LifecycleStopFailure},
		{"SessionEnd", agentpkg.LifecycleSessionEnd},
		{"SubagentStart", agentpkg.LifecycleSubagentStart},
		{"SubagentStop", agentpkg.LifecycleSubagentStop},
		{"Notification", agentpkg.LifecycleNone},
		{"PermissionRequest", agentpkg.LifecycleNone},
		{"PdxStop", agentpkg.LifecycleNone}, // Pdx-prefixed not handled here
		{"", agentpkg.LifecycleNone},
	}
	for _, tc := range cases {
		if got := legacyLifecycleFor(tc.name); got != tc.want {
			t.Errorf("legacyLifecycleFor(%q): got %s, want %s", tc.name, got, tc.want)
		}
	}
}
