package agent

import "testing"

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
