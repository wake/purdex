package cc

import (
	"path/filepath"
	"testing"
)

// Lead-team spec §6.6 "leaves Claude Code's entries alone": the CC installer
// writes no timeout and no matcher on any entry — PreToolUse and
// PermissionRequest included — so Claude Code's 600 s default applies and
// the P2c lock path (5 s) never comes near it. Pinned so a later "fix"
// does not quietly cap a hook that may wait.
func TestCCInstallHooks_EntriesCarryNoTimeoutOrMatcher(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := mergeClaudeHooks(path, "/usr/local/bin/pdx", false); err != nil {
		t.Fatalf("mergeClaudeHooks: %v", err)
	}
	hooks := hooksMap(t, readSettings(t, path))
	for _, key := range []string{"PreToolUse", "PermissionRequest"} {
		if _, ok := hooks[key]; !ok {
			t.Fatalf("%s must be installed", key)
		}
	}
	n := 0
	for event, entries := range hooks {
		for _, entry := range toEntrySlice(entries) {
			em, _ := entry.(map[string]any)
			if _, has := em["matcher"]; has {
				t.Errorf("%s: entry carries a matcher: %v", event, em)
			}
			for _, inner := range toEntrySlice(em["hooks"]) {
				im, _ := inner.(map[string]any)
				if _, has := im["timeout"]; has {
					t.Errorf("%s: hook carries a timeout: %v", event, im)
				}
				n++
			}
		}
	}
	if n != len(hooks) {
		t.Fatalf("inner hooks = %d, want one per event (%d)", n, len(hooks))
	}
}
