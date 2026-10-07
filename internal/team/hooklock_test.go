package team

import (
	"path/filepath"
	"testing"
)

func TestHookLockPath_RejectsEscapesAndUnknownAgents(t *testing.T) {
	for _, sid := range []string{"", ".", "..", "a/b", `a\b`, "../x", "/abs"} {
		if p := HookLockPath("/d", HookAgentCC, sid); p != "" {
			t.Errorf("sid %q → %q, want \"\"", sid, p)
		}
	}
	if p := HookLockPath("/d", "opencode", "sid"); p != "" {
		t.Errorf("unknown agent → %q", p)
	}
	if p := HookLockPath("", HookAgentCC, "sid"); p != "" {
		t.Errorf("no data dir → %q", p)
	}
	want := filepath.Join("/d", "hooklocks", "cc", "11111111-2222-4333-8444-555555555555")
	if p := HookLockPath("/d", HookAgentCC, "11111111-2222-4333-8444-555555555555"); p != want {
		t.Errorf("path = %q, want %q", p, want)
	}
	if p := HookLockPath("/d", HookAgentCodex, "01a0"); p != filepath.Join("/d", "hooklocks", "codex", "01a0") {
		t.Errorf("codex path = %q", p)
	}
}
