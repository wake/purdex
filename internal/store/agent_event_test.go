// internal/store/agent_event_test.go
package store

import (
	"encoding/json"
	"testing"
)

func openTestAgentEventStore(t *testing.T) *AgentEventStore {
	t.Helper()
	s, err := OpenAgentEvent(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAgentEventStore_SetAndGet(t *testing.T) {
	s := openTestAgentEventStore(t)

	raw := json.RawMessage(`{"sessionId":"abc","hook_event_name":"Stop"}`)
	if err := s.Set("my-project", "Stop", raw, "cc"); err != nil {
		t.Fatalf("set: %v", err)
	}

	ev, err := s.Get("my-project")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if ev == nil {
		t.Fatal("expected event, got nil")
	}
	if ev.TmuxSession != "my-project" {
		t.Errorf("tmux_session: want my-project, got %s", ev.TmuxSession)
	}
	if ev.EventName != "Stop" {
		t.Errorf("event_name: want Stop, got %s", ev.EventName)
	}
	if ev.AgentType != "cc" {
		t.Errorf("agent_type: want cc, got %s", ev.AgentType)
	}
}

func TestAgentEventStore_AgentTypeDefault(t *testing.T) {
	s := openTestAgentEventStore(t)

	raw := json.RawMessage(`{}`)
	if err := s.Set("proj", "Stop", raw, ""); err != nil {
		t.Fatalf("set: %v", err)
	}

	ev, err := s.Get("proj")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if ev.AgentType != "" {
		t.Errorf("agent_type: want empty, got %q", ev.AgentType)
	}
}

func TestAgentEventStore_Overwrite(t *testing.T) {
	s := openTestAgentEventStore(t)

	raw1 := json.RawMessage(`{"hook_event_name":"SessionStart"}`)
	raw2 := json.RawMessage(`{"hook_event_name":"Stop"}`)
	s.Set("proj", "SessionStart", raw1, "cc")
	s.Set("proj", "Stop", raw2, "codex")

	ev, _ := s.Get("proj")
	if ev.EventName != "Stop" {
		t.Errorf("want Stop after overwrite, got %s", ev.EventName)
	}
	if ev.AgentType != "codex" {
		t.Errorf("want codex after overwrite, got %s", ev.AgentType)
	}
}

func TestAgentEventStore_GetMissing(t *testing.T) {
	s := openTestAgentEventStore(t)
	ev, err := s.Get("nonexistent")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if ev != nil {
		t.Error("expected nil for missing key")
	}
}

func TestAgentEventStore_ListAll(t *testing.T) {
	s := openTestAgentEventStore(t)
	s.Set("proj-a", "Stop", json.RawMessage(`{}`), "cc")
	s.Set("proj-b", "SessionStart", json.RawMessage(`{}`), "")

	all, err := s.ListAll()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2, got %d", len(all))
	}
	// Verify agent_type is persisted in list
	for _, ev := range all {
		if ev.TmuxSession == "proj-a" && ev.AgentType != "cc" {
			t.Errorf("proj-a agent_type: want cc, got %s", ev.AgentType)
		}
	}
}

func TestAgentEventStore_Delete(t *testing.T) {
	s := openTestAgentEventStore(t)
	s.Set("proj", "Stop", json.RawMessage(`{}`), "cc")
	s.Delete("proj")

	ev, _ := s.Get("proj")
	if ev != nil {
		t.Error("expected nil after delete")
	}
}
