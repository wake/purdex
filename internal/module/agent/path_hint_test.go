package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPathHint_V1Minimal_JSON(t *testing.T) {
	h := PathHint{
		SchemaVersion: 1,
		AgentID:       "claude-code",
		SessionCode:   "abc123",
		Dir:           "/a/b",
		Kind:          PathHintKindRead,
		Timestamp:     time.Unix(1000, 0).UTC(),
	}
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, banned := range []string{`"path"`, `"basename"`, `"pathKind"`, `"baseDir"`, `"confidence"`, `"toolName"`, `"hostId"`} {
		if strings.Contains(s, banned) {
			t.Errorf("payload must not contain %s; got %s", banned, s)
		}
	}
	var got PathHint
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SchemaVersion != 1 || got.Dir != "/a/b" || got.Kind != PathHintKindRead {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
}

func TestPathHintRingBuffer_AddAndCap(t *testing.T) {
	r := NewPathHintRingBuffer(3)
	for i := 0; i < 5; i++ {
		r.Push(PathHint{SchemaVersion: 1, Dir: "/d/" + string(rune('a'+i))})
	}
	got := r.Snapshot()
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	if got[0].Dir != "/d/c" || got[2].Dir != "/d/e" {
		t.Errorf("unexpected ring contents: %+v", got)
	}
}
