package teammod

import (
	"os"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// A stale flag (no open row behind it) is harmless and the sweep prunes it;
// a flag with an open row stays.
func TestObserve_PruneAskFlagsKeepsOnlyFlagsWithOpenRows(t *testing.T) {
	f := newFixture(t)
	f.m.observeHookEvent(preAsk("sid-1", "toolu_a"))
	f.m.setAskFlag("cc", "sid-gone", true)
	f.m.setAskFlag("codex", "sid-gone2", true)
	// A forwarded event of a session with a flag but no row: no error, the flag goes.
	f.m.observeHookEvent(team.HookDecideRequest{Agent: "cc", Event: "PostToolUse", SessionID: "sid-gone", ToolName: "Bash"})
	if f.flagExists("sid-gone") {
		t.Fatal("a stale flag must go with the first forwarded event")
	}
	if n := f.m.pruneAskFlags(); n != 1 {
		t.Fatalf("pruned %d, want 1 (codex/sid-gone2)", n)
	}
	if _, err := os.Stat(f.flagPath("codex", "sid-gone2")); err == nil || !f.flagExists("sid-1") {
		t.Fatal("prune kept the wrong flags")
	}
}
