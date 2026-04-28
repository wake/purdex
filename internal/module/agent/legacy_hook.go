package agent

import agentpkg "github.com/wake/purdex/internal/agent"

// codexLegacyEventNames is the set of pre-W2 raw event-name literals codex
// upstream may emit while the codex catalog has not yet been migrated to
// W2's PurdexName / Lifecycle population. Phase 2 ship removes the codex
// case from isLegacyHookForUnmigrated; this set goes with it once the
// fallback path is no longer reachable.
var codexLegacyEventNames = map[string]bool{
	"SessionStart":      true,
	"UserPromptSubmit":  true,
	"Notification":      true,
	"Stop":              true,
	"StopFailure":       true,
	"PermissionRequest": true,
	"SessionEnd":        true,
	"SubagentStart":     true,
	"SubagentStop":      true,
}

// opencodeLegacyEventNames is the equivalent set for opencode (8 entries).
// Notably absent: Notification — the opencode catalog has no Notification
// entry, so accepting "Notification" here would route an unknown event into
// the legacy fallback path with a stale Lifecycle effect. PermissionRequest
// is the inverse case: required because opencode's plugin emits it for both
// permission.asked and question.asked Bus events during Phase 1/2.
var opencodeLegacyEventNames = map[string]bool{
	"SessionStart":      true,
	"UserPromptSubmit":  true,
	"Stop":              true,
	"StopFailure":       true,
	"PermissionRequest": true,
	"SessionEnd":        true,
	"SubagentStart":     true,
	"SubagentStop":      true,
}

// legacyLifecycleFor maps a pre-W2 raw upstream event-name literal back to
// its LifecycleEventKind so the W2 fallback dispatch path can route
// codex/opencode events whose catalogs have not yet migrated to PurdexName +
// Lifecycle. Phase 3 ship removes this alongside isLegacyHookForUnmigrated.
func legacyLifecycleFor(name string) agentpkg.LifecycleEventKind {
	switch name {
	case "SessionStart":
		return agentpkg.LifecycleSessionStart
	case "UserPromptSubmit":
		return agentpkg.LifecycleUserPromptSubmit
	case "Stop":
		return agentpkg.LifecycleStop
	case "StopFailure":
		return agentpkg.LifecycleStopFailure
	case "SessionEnd":
		return agentpkg.LifecycleSessionEnd
	case "SubagentStart":
		return agentpkg.LifecycleSubagentStart
	case "SubagentStop":
		return agentpkg.LifecycleSubagentStop
	}
	return agentpkg.LifecycleNone
}

// isLegacyHookForUnmigrated reports whether (agentType, name) corresponds
// to a pre-W2 raw event-name literal whose catalog entry has not yet been
// migrated, so the daemon's lifecycle dispatch can fall back to legacy
// string-comparison handling. Phase 3 ship removes the predicate entirely
// once all three agents populate Lifecycle.
//
// The predicate is **per-agent**: the legacy event-name sets do not match
// across agents (opencode lacks Notification; cc is already migrated in
// Phase 1). A merged set would let unknown events leak into the fallback
// branch.
// classifyLifecycleForReq is the Module-bound counterpart to classifyLifecycle:
// it resolves the request's provider via m.registry and then runs the same
// three-branch decision tree. Returns LifecycleNone when registry is missing
// or the agent_type is unknown — same effect as a catalog miss with no legacy
// fallback. frame_ops.go's hot path uses this so callers don't replicate the
// registry / type-assert lookup at every dispatch site.
func (m *Module) classifyLifecycleForReq(req EventRequest) agentpkg.LifecycleEventKind {
	if m == nil || m.registry == nil {
		return agentpkg.LifecycleNone
	}
	provider, _ := m.registry.Get(req.AgentType)
	return classifyLifecycle(provider, req)
}

// classifyLifecycle resolves an EventRequest to its LifecycleEventKind via
// the spec §3.4.2 three-branch decision tree:
//
//  1. Catalog hit (provider implements HookInstaller and Events() lookup
//     by PurdexName succeeds) — use spec.Lifecycle. LifecycleNone is a
//     legitimate hit value for events with no frame-mutation effect
//     (PdxNotification, PdxPermissionRequest, etc.).
//  2. Catalog miss + isLegacyHookForUnmigrated(agentType, name) — fallback
//     to legacyLifecycleFor(name) so codex/opencode pre-migration traffic
//     routes correctly during the W2 transition.
//  3. Otherwise — return LifecycleNone (treated as no-op for lifecycle
//     dispatch). Includes "codex sender prematurely emitting PdxXxx before
//     codex catalog migrates" — branch 1 and 2 both miss, surfacing the
//     event as catalog-invalid.
//
// provider may be nil; branch 1 is then skipped. Phase 3 ship collapses
// branches 2 + the helper alongside isLegacyHookForUnmigrated.
func classifyLifecycle(provider agentpkg.AgentProvider, req EventRequest) agentpkg.LifecycleEventKind {
	if installer, ok := provider.(agentpkg.HookInstaller); ok {
		if spec, found := agentpkg.LookupByPurdexName(installer.Events(), req.PurdexName); found {
			return spec.Lifecycle
		}
	}
	if isLegacyHookForUnmigrated(req.AgentType, req.PurdexName) {
		return legacyLifecycleFor(req.PurdexName)
	}
	return agentpkg.LifecycleNone
}

func isLegacyHookForUnmigrated(agentType, name string) bool {
	switch agentType {
	case "codex":
		return codexLegacyEventNames[name]
	case "opencode":
		return opencodeLegacyEventNames[name]
	default:
		return false
	}
}
