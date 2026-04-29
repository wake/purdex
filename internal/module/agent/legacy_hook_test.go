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

// Post-P2-T5: codex catalog has migrated, so the predicate's codex case is
// gone. Any (agentType=codex, name) → false. This is the inverse of the
// pre-P2 TestIsLegacyHookForUnmigrated_CodexAllNames assertion; renamed so
// the intent is unambiguous when reading test output.
func TestIsLegacyHookForUnmigrated_CodexAllFalse(t *testing.T) {
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
		"PdxStop",
		"PdxSessionEnd",
		"anything",
	}
	for _, n := range names {
		if isLegacyHookForUnmigrated("codex", n) {
			t.Errorf("codex + %q must be false (codex catalog migrated in Phase 2)", n)
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

// Post-P2-T5: codex no longer participates in the legacy fallback predicate.
// A genuine catalog miss (empty events) on a legacy literal must surface as
// branch 3 (LifecycleNone) — handler then routes through catalog-miss invalid
// path. Renamed from CodexLegacyFallback to flip the documented expectation.
func TestClassifyLifecycle_CodexNoLegacyFallback(t *testing.T) {
	codex := &fakeAgentProvider{typeName: "codex", events: emptyEvents}
	req := EventRequest{AgentType: "codex", PurdexName: "SessionEnd"}
	if got := classifyLifecycle(codex, req); got != agentpkg.LifecycleNone {
		t.Errorf("codex SessionEnd post-P2: got %s, want LifecycleNone (no legacy fallback)", got)
	}
}

func TestClassifyLifecycle_OpencodeLegacyFallback(t *testing.T) {
	oc := &fakeAgentProvider{typeName: "opencode", events: emptyEvents}
	req := EventRequest{AgentType: "opencode", PurdexName: "SessionStart"}
	if got := classifyLifecycle(oc, req); got != agentpkg.LifecycleSessionStart {
		t.Errorf("opencode SessionStart fallback: got %s, want LifecycleSessionStart", got)
	}
}

// codex catalog miss (empty events) plus codex absent from the legacy
// predicate (post-P2-T5) means branch 1 and branch 2 both fail; branch 3
// returns LifecycleNone, which the handler routes through the catalog-miss
// invalid path. Used to also guard the pre-P2 "premature Pdx prefix" case;
// post-P2 the same outcome arises from a different cause (predicate gone).
func TestClassifyLifecycle_CodexEmptyCatalogIsInvalid(t *testing.T) {
	codex := &fakeAgentProvider{typeName: "codex", events: emptyEvents}
	req := EventRequest{AgentType: "codex", PurdexName: "PdxStop"}
	if got := classifyLifecycle(codex, req); got != agentpkg.LifecycleNone {
		t.Errorf("codex PdxStop with empty catalog: got %s, want LifecycleNone", got)
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

// Nil provider must not panic on the branch-1 type-assert and must still
// reach the branch-2 legacy fallback when (agentType, name) is in the
// per-agent legacy set. Post-P2-T5 codex is no longer in the predicate, so
// the test uses opencode (still pre-Phase-3) to keep exercising the nil-
// provider → branch 2 fallback path.
func TestClassifyLifecycle_NilProvider(t *testing.T) {
	req := EventRequest{AgentType: "opencode", PurdexName: "Stop"}
	// Branch 1 fails (nil → type-assert false), branch 2 succeeds
	// (opencode+Stop is in legacy set), so the result is LifecycleStop.
	if got := classifyLifecycle(nil, req); got != agentpkg.LifecycleStop {
		t.Errorf("nil provider + opencode Stop: got %s, want LifecycleStop (branch 2)", got)
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
