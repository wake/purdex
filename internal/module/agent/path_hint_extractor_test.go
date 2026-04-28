package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mkRaw(toolName, filePath string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"cwd":        "/repo",
		"tool_name":  toolName,
		"tool_input": map[string]any{"file_path": filePath},
	})
	return b
}

func mkRawWithCwd(cwd, toolName, filePath string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"cwd":        cwd,
		"tool_name":  toolName,
		"tool_input": map[string]any{"file_path": filePath},
	})
	return b
}

func mkRawNoCwd(toolName, filePath string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"tool_name":  toolName,
		"tool_input": map[string]any{"file_path": filePath},
	})
	return b
}

func TestExtractPathHint_AbsoluteRead(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	h, basename, ok := ExtractPathHint(mkRaw("Read", "/repo/src/c.go"), "PdxPreToolUse", "cc", "abc123", "", now)
	if !ok {
		t.Fatal("expected hint, got drop")
	}
	if h.SchemaVersion != 1 || h.AgentID != "cc" || h.SessionCode != "abc123" ||
		h.Cwd != "/repo" || h.Dir != "/repo/src" || h.Kind != PathHintKindRead {
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
		h, _, ok := ExtractPathHint(mkRaw(tc.tool, "/repo/src/c"), "PdxPreToolUse", "cc", "s1", "", time.Unix(0, 0))
		if !ok || h.Kind != tc.kind {
			t.Errorf("%s expected kind=%s, got ok=%v kind=%s", tc.tool, tc.kind, ok, h.Kind)
		}
	}
}

func TestExtractPathHint_NotebookEditUsesNotebookPath(t *testing.T) {
	// CC NotebookEdit hook puts the path in tool_input.notebook_path, not file_path.
	raw, _ := json.Marshal(map[string]any{
		"cwd":        "/repo",
		"tool_name":  "NotebookEdit",
		"tool_input": map[string]any{"notebook_path": "/repo/notebooks/foo.ipynb"},
	})
	h, basename, ok := ExtractPathHint(raw, "PdxPreToolUse", "cc", "s1", "", time.Unix(0, 0))
	if !ok {
		t.Fatal("NotebookEdit with notebook_path should produce a hint")
	}
	if h.Dir != "/repo/notebooks" || h.Kind != PathHintKindEdit {
		t.Errorf("unexpected hint: %+v", h)
	}
	if basename != "foo.ipynb" {
		t.Errorf("basename = %q, want foo.ipynb", basename)
	}
}

func TestExtractPathHint_FilePathTakesPrecedenceOverNotebookPath(t *testing.T) {
	// If both fields are present (defensive), file_path wins.
	raw, _ := json.Marshal(map[string]any{
		"cwd":       "/repo",
		"tool_name": "Edit",
		"tool_input": map[string]any{
			"file_path":     "/repo/src/c.go",
			"notebook_path": "/repo/notebooks/x.ipynb",
		},
	})
	h, basename, ok := ExtractPathHint(raw, "PdxPreToolUse", "cc", "s1", "", time.Unix(0, 0))
	if !ok {
		t.Fatal("expected hint")
	}
	if h.Dir != "/repo/src" || basename != "c.go" {
		t.Errorf("file_path should win; got dir=%q basename=%q", h.Dir, basename)
	}
}

func TestExtractPathHint_DropsRelative(t *testing.T) {
	if _, _, ok := ExtractPathHint(mkRaw("Read", "rel/path.go"), "PdxPreToolUse", "cc", "s1", "", time.Unix(0, 0)); ok {
		t.Fatal("expected drop for non-absolute path")
	}
}

func TestExtractPathHint_DropsUnknownTool(t *testing.T) {
	if _, _, ok := ExtractPathHint(mkRaw("Bash", "/repo/src"), "PdxPreToolUse", "cc", "s1", "", time.Unix(0, 0)); ok {
		t.Fatal("expected drop for non-file tool")
	}
}

func TestExtractPathHint_DropsWrongEventName(t *testing.T) {
	if _, _, ok := ExtractPathHint(mkRaw("Read", "/repo/src"), "SessionStart", "cc", "s1", "", time.Unix(0, 0)); ok {
		t.Fatal("expected drop for non-PreToolUse/PostToolUse event")
	}
}

func TestExtractPathHint_AcceptsPostToolUse(t *testing.T) {
	if _, _, ok := ExtractPathHint(mkRaw("Read", "/repo/src/c.go"), "PdxPostToolUse", "cc", "s1", "", time.Unix(0, 0)); !ok {
		t.Fatal("PostToolUse should also qualify")
	}
}

func TestExtractPathHint_DropsEmptyFilePath(t *testing.T) {
	if _, _, ok := ExtractPathHint(mkRaw("Read", ""), "PdxPreToolUse", "cc", "s1", "", time.Unix(0, 0)); ok {
		t.Fatal("expected drop for empty file_path")
	}
}

func TestExtractPathHint_DropsMalformedJSON(t *testing.T) {
	if _, _, ok := ExtractPathHint([]byte("not-json"), "PdxPreToolUse", "cc", "s1", "", time.Unix(0, 0)); ok {
		t.Fatal("expected drop for malformed JSON")
	}
}

func TestExtractPathHint_FallbackCwdWhenRawEventLacksIt(t *testing.T) {
	h, _, ok := ExtractPathHint(mkRawNoCwd("Read", "/repo/src/c.go"), "PdxPreToolUse", "cc", "s1", "/repo", time.Unix(0, 0))
	if !ok {
		t.Fatal("expected fallback cwd to be used")
	}
	if h.Cwd != "/repo" {
		t.Errorf("Cwd = %q, want /repo", h.Cwd)
	}
}

func TestExtractPathHint_DropsWhenNoCwdAvailable(t *testing.T) {
	if _, _, ok := ExtractPathHint(mkRawNoCwd("Read", "/repo/src/c.go"), "PdxPreToolUse", "cc", "s1", "", time.Unix(0, 0)); ok {
		t.Fatal("expected drop when neither raw_event nor fallback supplies cwd")
	}
}

func TestExtractPathHint_DropsRelativeCwd(t *testing.T) {
	if _, _, ok := ExtractPathHint(mkRawWithCwd("rel/dir", "Read", "/repo/src/c.go"), "PdxPreToolUse", "cc", "s1", "", time.Unix(0, 0)); ok {
		t.Fatal("expected drop for non-absolute cwd")
	}
}

func TestExtractPathHint_TrimsTrailingSlashCwd(t *testing.T) {
	h, _, ok := ExtractPathHint(mkRawWithCwd("/repo/", "Read", "/repo/src/c.go"), "PdxPreToolUse", "cc", "s1", "", time.Unix(0, 0))
	if !ok {
		t.Fatal("expected hint")
	}
	if h.Cwd != "/repo" {
		t.Errorf("Cwd = %q, want /repo (trimmed)", h.Cwd)
	}
}

func TestExtractPathHint_PreservesRootCwd(t *testing.T) {
	for _, in := range []string{"/", "//", "///"} {
		h, _, ok := ExtractPathHint(mkRawWithCwd(in, "Read", "/etc/passwd"), "PdxPreToolUse", "cc", "s1", "", time.Unix(0, 0))
		if !ok {
			t.Fatalf("expected hint for cwd=%q", in)
		}
		if h.Cwd != "/" {
			t.Errorf("cwd=%q normalized to %q, want /", in, h.Cwd)
		}
	}
}

func TestExtractPathHint_DropsOversizedRawEvent(t *testing.T) {
	huge := make([]byte, MaxRawEventBytes+1)
	for i := range huge {
		huge[i] = '"'
	}
	if _, _, ok := ExtractPathHint(huge, "PdxPreToolUse", "cc", "s1", "/repo", time.Unix(0, 0)); ok {
		t.Fatal("expected drop for raw event exceeding MaxRawEventBytes")
	}
}

func TestExtractPathHint_DropsOversizedFilePath(t *testing.T) {
	long := "/" + strings.Repeat("a", MaxFilePathBytes)
	if _, _, ok := ExtractPathHint(mkRaw("Read", long), "PdxPreToolUse", "cc", "s1", "", time.Unix(0, 0)); ok {
		t.Fatal("expected drop for file_path exceeding MaxFilePathBytes")
	}
}

func TestExtractPathHint_DropsControlCharsInPath(t *testing.T) {
	for _, bad := range []string{
		"/repo/src/\x00null.go",
		"/repo/src/\nnewline.go",
		"/repo/src/\ttab.go",
		"/repo/src/\x1bescape.go",
		"/repo/src/\x7fdel.go",
	} {
		if _, _, ok := ExtractPathHint(mkRaw("Read", bad), "PdxPreToolUse", "cc", "s1", "", time.Unix(0, 0)); ok {
			t.Errorf("expected drop for file_path with control char: %q", bad)
		}
	}
}

func TestExtractPathHint_DropsControlCharsInCwd(t *testing.T) {
	for _, bad := range []string{
		"/re\x00po",
		"/re\npo",
	} {
		if _, _, ok := ExtractPathHint(mkRawWithCwd(bad, "Read", "/repo/src/c.go"), "PdxPreToolUse", "cc", "s1", "", time.Unix(0, 0)); ok {
			t.Errorf("expected drop for cwd with control char: %q", bad)
		}
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
