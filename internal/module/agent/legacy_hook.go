package agent

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
