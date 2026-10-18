package agent

import (
	"testing"
	"time"
)

// #3316: the cwd fallback costs a tmux listing, so it is read only when the extractor needs it: a file tool of Pre/PostToolUse with an
// absolute path and no cwd of its own. Every other event is answered without it.
func TestExtractPathHintLazy_FallbackOnlyWhenNeeded(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	calls := 0
	fb := func() string { calls++; return "/from/cache" }

	// an event with a cwd of its own never asks
	if _, _, ok := ExtractPathHintLazy(mkRaw("Read", "/repo/a.go"), "PdxPreToolUse", "cc", "s1", fb, now); !ok || calls != 0 {
		t.Fatalf("with a cwd: ok=%v calls=%d", ok, calls)
	}
	// not a file tool, not Pre/PostToolUse, no path, relative path: nothing to decide, so nothing is read
	for name, c := range map[string]struct {
		raw   []byte
		event string
	}{
		"bash tool":     {mkRawNoCwd("Bash", "/repo/a.go"), "PdxPreToolUse"},
		"other event":   {mkRawNoCwd("Read", "/repo/a.go"), "PdxStop"},
		"relative path": {mkRawNoCwd("Read", "a.go"), "PdxPreToolUse"},
		"empty path":    {mkRawNoCwd("Read", ""), "PdxPostToolUse"},
	} {
		if _, _, ok := ExtractPathHintLazy(c.raw, c.event, "cc", "s1", fb, now); ok || calls != 0 {
			t.Fatalf("%s: ok=%v calls=%d", name, ok, calls)
		}
	}
	// no cwd on a file tool: the fallback is read, once, and used
	h, _, ok := ExtractPathHintLazy(mkRawNoCwd("Read", "/repo/a.go"), "PdxPreToolUse", "cc", "s1", fb, now)
	if !ok || calls != 1 || h.Cwd != "/from/cache" {
		t.Fatalf("no cwd: ok=%v calls=%d hint=%+v", ok, calls, h)
	}
	// a fallback that gives nothing (or none at all) drops the hint
	if _, _, ok := ExtractPathHintLazy(mkRawNoCwd("Read", "/repo/a.go"), "PdxPreToolUse", "cc", "s1", func() string { return "" }, now); ok {
		t.Fatal("an empty fallback made a hint")
	}
	if _, _, ok := ExtractPathHintLazy(mkRawNoCwd("Read", "/repo/a.go"), "PdxPreToolUse", "cc", "s1", nil, now); ok {
		t.Fatal("a nil fallback made a hint")
	}
}

// The string form is the same function with a fixed fallback.
func TestExtractPathHint_StringFallbackStillWorks(t *testing.T) {
	h, _, ok := ExtractPathHint(mkRawNoCwd("Edit", "/repo/a.go"), "PdxPostToolUse", "cc", "s1", "/cached", time.Unix(1, 0))
	if !ok || h.Cwd != "/cached" {
		t.Fatalf("ok=%v hint=%+v", ok, h)
	}
}
