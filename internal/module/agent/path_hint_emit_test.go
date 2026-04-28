package agent

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

type stubBroadcaster struct {
	mu    sync.Mutex
	calls []struct{ session, kind, value string }
}

func (s *stubBroadcaster) Broadcast(session, kind, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, struct{ session, kind, value string }{session, kind, value})
}

func (s *stubBroadcaster) snapshot() []struct{ session, kind, value string } {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]struct{ session, kind, value string }, len(s.calls))
	copy(out, s.calls)
	return out
}

func TestEmitPathHint_BroadcastV1Payload(t *testing.T) {
	b := &stubBroadcaster{}
	dedup := NewPathHintDedupCache(0)
	buf := NewPathHintRingBuffer(10)
	raw, _ := json.Marshal(map[string]any{
		"cwd":        "/repo",
		"tool_name":  "Read",
		"tool_input": map[string]any{"file_path": "/repo/src/c.go"},
	})
	now := time.Unix(1000, 0).UTC()

	EmitPathHint(b, dedup, buf, raw, "PreToolUse", "cc", "sess1", "", now)

	calls := b.snapshot()
	if len(calls) != 1 || calls[0].session != "sess1" || calls[0].kind != "agent.path_hint" {
		t.Fatalf("envelope mismatch: %+v", calls)
	}
	v := calls[0].value
	for _, banned := range []string{`"path":`, "c.go", `"basename"`} {
		if strings.Contains(v, banned) {
			t.Errorf("payload must not contain %q; got %s", banned, v)
		}
	}
	var hint PathHint
	if err := json.Unmarshal([]byte(v), &hint); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if hint.SchemaVersion != 1 || hint.Cwd != "/repo" || hint.Dir != "/repo/src" || hint.Kind != PathHintKindRead || hint.SessionCode != "sess1" {
		t.Errorf("payload mismatch: %+v", hint)
	}
	if got := buf.Snapshot(); len(got) != 1 {
		t.Errorf("expected ring buffer to capture hint, got %d entries", len(got))
	}
}

func TestEmitPathHint_DedupSuppresses(t *testing.T) {
	b := &stubBroadcaster{}
	dedup := NewPathHintDedupCache(5 * time.Second)
	buf := NewPathHintRingBuffer(10)
	raw, _ := json.Marshal(map[string]any{
		"cwd":        "/repo",
		"tool_name":  "Read",
		"tool_input": map[string]any{"file_path": "/repo/src/c.go"},
	})
	now := time.Unix(1000, 0).UTC()

	EmitPathHint(b, dedup, buf, raw, "PreToolUse", "cc", "sess1", "", now)
	EmitPathHint(b, dedup, buf, raw, "PreToolUse", "cc", "sess1", "", now)

	if calls := b.snapshot(); len(calls) != 1 {
		t.Errorf("expected 1 broadcast, got %d", len(calls))
	}
	if got := buf.Snapshot(); len(got) != 1 {
		t.Errorf("expected 1 ring entry, got %d", len(got))
	}
}

func TestEmitPathHint_DropsRelativePath(t *testing.T) {
	b := &stubBroadcaster{}
	dedup := NewPathHintDedupCache(0)
	buf := NewPathHintRingBuffer(10)
	raw, _ := json.Marshal(map[string]any{
		"cwd":        "/repo",
		"tool_name":  "Read",
		"tool_input": map[string]any{"file_path": "rel/path.go"},
	})
	EmitPathHint(b, dedup, buf, raw, "PreToolUse", "cc", "sess1", "", time.Unix(0, 0))
	if calls := b.snapshot(); len(calls) != 0 {
		t.Errorf("relative path should drop; got %+v", calls)
	}
}

func TestEmitPathHint_UsesCwdFallbackWhenRawMissing(t *testing.T) {
	b := &stubBroadcaster{}
	dedup := NewPathHintDedupCache(0)
	buf := NewPathHintRingBuffer(10)
	raw, _ := json.Marshal(map[string]any{
		"tool_name":  "Read",
		"tool_input": map[string]any{"file_path": "/repo/src/c.go"},
	})
	EmitPathHint(b, dedup, buf, raw, "PreToolUse", "cc", "sess1", "/repo", time.Unix(0, 0))
	calls := b.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 broadcast via fallback cwd, got %d", len(calls))
	}
	if !strings.Contains(calls[0].value, `"cwd":"/repo"`) {
		t.Errorf("expected fallback cwd in payload; got %s", calls[0].value)
	}
}
