package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wake/tmux-box/internal/store"
)

func newTestModule(t *testing.T) *Module {
	t.Helper()
	events, err := store.OpenAgentEvent(":memory:")
	if err != nil {
		t.Fatalf("open agent event store: %v", err)
	}
	t.Cleanup(func() { events.Close() })
	return &Module{events: events}
}

func TestHandleEvent_StoresAndReturns(t *testing.T) {
	m := newTestModule(t)

	body := `{"tmux_session":"work","event_name":"agent:lifecycle:start","raw_event":{"session_id":"abc"}}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("status field: want ok, got %s", resp["status"])
	}

	// Verify stored in AgentEventStore
	ev, err := m.events.Get("work")
	if err != nil {
		t.Fatalf("events.Get: %v", err)
	}
	if ev == nil {
		t.Fatal("event not found in store")
	}
	if ev.EventName != "agent:lifecycle:start" {
		t.Errorf("event_name: want agent:lifecycle:start, got %s", ev.EventName)
	}
	if string(ev.RawEvent) != `{"session_id":"abc"}` {
		t.Errorf("raw_event: want {\"session_id\":\"abc\"}, got %s", string(ev.RawEvent))
	}
}

func TestHandleEvent_BadJSON(t *testing.T) {
	m := newTestModule(t)

	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestHandleEvent_MissingTmuxSession(t *testing.T) {
	m := newTestModule(t)

	body := `{"tmux_session":"","event_name":"agent:lifecycle:stop","raw_event":{}}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	// Spec: empty tmux_session is still OK (returns 200)
	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("status field: want ok, got %s", resp["status"])
	}

	// Verify stored with empty key
	ev, err := m.events.Get("")
	if err != nil {
		t.Fatalf("events.Get: %v", err)
	}
	if ev == nil {
		t.Fatal("event not found in store for empty tmux_session")
	}
	if ev.EventName != "agent:lifecycle:stop" {
		t.Errorf("event_name: want agent:lifecycle:stop, got %s", ev.EventName)
	}
}
