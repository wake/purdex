package agent

import (
	"encoding/json"
	"testing"
	"time"
)

func mkRaw(toolName, filePath string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"tool_name":  toolName,
		"tool_input": map[string]any{"file_path": filePath},
	})
	return b
}

func TestExtractPathHint_AbsoluteRead(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	h, basename, ok := ExtractPathHint(mkRaw("Read", "/a/b/c.go"), "PreToolUse", "claude-code", "abc123", now)
	if !ok {
		t.Fatal("expected hint, got drop")
	}
	if h.SchemaVersion != 1 || h.AgentID != "claude-code" || h.SessionCode != "abc123" ||
		h.Dir != "/a/b" || h.Kind != PathHintKindRead {
		t.Errorf("unexpected hint: %+v", h)
	}
	if basename != "c.go" {
		t.Errorf("basename = %q", basename)
	}
}

func TestExtractPathHint_WriteEditNotebookEdit(t *testing.T) {
	for _, tc := range []struct{ tool, kind string }{
		{"Write", PathHintKindWrite},
		{"Edit", PathHintKindEdit},
		{"NotebookEdit", PathHintKindEdit},
	} {
		h, _, ok := ExtractPathHint(mkRaw(tc.tool, "/a/b/c"), "PreToolUse", "claude-code", "s1", time.Unix(0, 0))
		if !ok || h.Kind != tc.kind {
			t.Errorf("%s expected kind=%s, got ok=%v kind=%s", tc.tool, tc.kind, ok, h.Kind)
		}
	}
}

func TestExtractPathHint_DropsRelative(t *testing.T) {
	if _, _, ok := ExtractPathHint(mkRaw("Read", "rel/path.go"), "PreToolUse", "claude-code", "s1", time.Unix(0, 0)); ok {
		t.Fatal("expected drop for non-absolute path")
	}
}

func TestExtractPathHint_DropsUnknownTool(t *testing.T) {
	if _, _, ok := ExtractPathHint(mkRaw("Bash", "/a/b"), "PreToolUse", "claude-code", "s1", time.Unix(0, 0)); ok {
		t.Fatal("expected drop for non-file tool")
	}
}

func TestExtractPathHint_DropsWrongEventName(t *testing.T) {
	if _, _, ok := ExtractPathHint(mkRaw("Read", "/a/b"), "SessionStart", "claude-code", "s1", time.Unix(0, 0)); ok {
		t.Fatal("expected drop for non-PreToolUse/PostToolUse event")
	}
}

func TestExtractPathHint_AcceptsPostToolUse(t *testing.T) {
	if _, _, ok := ExtractPathHint(mkRaw("Read", "/a/b/c.go"), "PostToolUse", "claude-code", "s1", time.Unix(0, 0)); !ok {
		t.Fatal("PostToolUse should also qualify")
	}
}

func TestExtractPathHint_DropsEmptyFilePath(t *testing.T) {
	if _, _, ok := ExtractPathHint(mkRaw("Read", ""), "PreToolUse", "claude-code", "s1", time.Unix(0, 0)); ok {
		t.Fatal("expected drop for empty file_path")
	}
}

func TestExtractPathHint_DropsMalformedJSON(t *testing.T) {
	if _, _, ok := ExtractPathHint([]byte("not-json"), "PreToolUse", "claude-code", "s1", time.Unix(0, 0)); ok {
		t.Fatal("expected drop for malformed JSON")
	}
}

func TestDedupCache_BasenameDistinguishes(t *testing.T) {
	c := NewPathHintDedupCache(5 * time.Second)
	t0 := time.Unix(1000, 0)
	if !c.Mark("s1", "/a/b", "c.go", t0) {
		t.Fatal("first call should be fresh")
	}
	if c.Mark("s1", "/a/b", "c.go", t0.Add(2*time.Second)) {
		t.Fatal("same key within window should dedup")
	}
	if !c.Mark("s1", "/a/b", "d.go", t0.Add(2*time.Second)) {
		t.Fatal("different basename should NOT dedup (basename in key)")
	}
	if !c.Mark("s1", "/a/b", "c.go", t0.Add(6*time.Second)) {
		t.Fatal("after window should be fresh again")
	}
}

func TestDedupCache_ZeroWindowAlwaysFresh(t *testing.T) {
	c := NewPathHintDedupCache(0)
	t0 := time.Unix(1000, 0)
	if !c.Mark("s1", "/a/b", "c.go", t0) {
		t.Fatal("first call should be fresh")
	}
	if !c.Mark("s1", "/a/b", "c.go", t0) {
		t.Fatal("zero window should never dedup")
	}
}
