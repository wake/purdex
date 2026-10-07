package codex

import (
	"path/filepath"
	"testing"
)

// codexTimeoutOf reads the timeout the installer wrote for one upstream key.
func codexTimeoutOf(t *testing.T, hooks map[string]any, key string) float64 {
	t.Helper()
	groups := codexMatcherGroups(hooks[key])
	if len(groups) != 1 {
		t.Fatalf("%s: %d matcher groups, want 1", key, len(groups))
	}
	inner := toCodexEntrySlice(groups[0].(map[string]any)["hooks"])
	m, _ := inner[0].(map[string]any)
	v, _ := m["timeout"].(float64)
	return v
}

// Lead-team spec §6.6 "the installer raises Codex's PreToolUse timeout from
// 5 to 10 s and leaves Claude Code's entries alone": PreToolUse is 10,
// PermissionRequest (answers {} at once) and every other default event stay
// 5, SessionEnd / Interrupt stay clamped at 3.
func TestCodexHookTimeoutSeconds_PreToolUseIsTen(t *testing.T) {
	if got := codexHookTimeoutSeconds("PreToolUse"); got != 10 {
		t.Fatalf("PreToolUse timeout = %d, want 10", got)
	}
	for _, key := range []string{"PermissionRequest", "PostToolUse", "SessionStart", "Stop", "UserPromptSubmit"} {
		if got := codexHookTimeoutSeconds(key); got != 5 {
			t.Errorf("%s timeout = %d, want 5 (only PreToolUse changes)", key, got)
		}
	}
	for _, key := range []string{"SessionEnd", "Interrupt"} {
		if got := codexHookTimeoutSeconds(key); got != 3 {
			t.Errorf("%s timeout = %d, want 3", key, got)
		}
	}
}

// The written hooks.json carries it, and a re-install over a file whose
// PreToolUse entry still says 5 rewrites that one entry (the installer
// strips and re-appends every pdx-owned entry) without touching the others
// or a third-party group under the same key.
func TestCodexInstallHooks_WritesPreToolUseTimeoutTenAndUpgradesFive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	third := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/notify pre", "timeout": 7}}}
	writeHooksFile(t, home, map[string]any{
		"PreToolUse":        []any{pdxGroupEntry("PdxPreToolUse"), third}, // the pre-P2c install: 5
		"PermissionRequest": []any{pdxGroupEntry("PdxPermissionRequest")},
	})
	if err := (&Provider{}).InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	hooks := hooksSection(t, readHooksFile(t, filepath.Join(home, ".codex", "hooks.json")))
	groups := codexMatcherGroups(hooks["PreToolUse"])
	if len(groups) != 2 {
		t.Fatalf("PreToolUse groups = %d, want the third-party one and ours", len(groups))
	}
	var ours, theirs float64
	for _, g := range groups {
		inner := toCodexEntrySlice(g.(map[string]any)["hooks"])
		m, _ := inner[0].(map[string]any)
		cmd, _ := m["command"].(string)
		v, _ := m["timeout"].(float64)
		if isPdxCommandCodex(cmd) {
			ours = v
		} else {
			theirs = v
		}
	}
	if ours != 10 {
		t.Errorf("our PreToolUse timeout = %v, want 10 (re-install must upgrade a 5)", ours)
	}
	if theirs != 7 {
		t.Errorf("third-party PreToolUse timeout = %v, want 7 untouched", theirs)
	}
	if got := codexTimeoutOf(t, hooks, "PermissionRequest"); got != 5 {
		t.Errorf("PermissionRequest timeout = %v, want 5", got)
	}
	if got := codexTimeoutOf(t, hooks, "PostToolUse"); got != 5 {
		t.Errorf("PostToolUse timeout = %v, want 5", got)
	}
	if got := codexTimeoutOf(t, hooks, "SessionEnd"); got != 3 {
		t.Errorf("SessionEnd timeout = %v, want 3", got)
	}
}
