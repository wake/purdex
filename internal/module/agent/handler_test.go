package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

	// Spec: empty tmux_session is still OK (returns 200) but skips DB storage.
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

	// Verify NOT stored — empty tmux_session should be skipped.
	ev, err := m.events.Get("")
	if err != nil {
		t.Fatalf("events.Get: %v", err)
	}
	if ev != nil {
		t.Error("event with empty tmux_session should not be stored in DB, but was found")
	}
}

// writeSettingsJSON writes content into <dir>/.claude/settings.json and returns the file path.
func writeSettingsJSON(t *testing.T, dir string, content string) string {
	t.Helper()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	p := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
	return p
}

// TestHandleHookStatus_NoSettingsFile — HOME points to a fresh temp dir
// that has no .claude/settings.json; handler should report installed:false.
func TestHandleHookStatus_NoSettingsFile(t *testing.T) {
	m := newTestModule(t)

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	req := httptest.NewRequest("GET", "/api/agent/hook-status", nil)
	w := httptest.NewRecorder()
	m.handleHookStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp["installed"] != false {
		t.Errorf("installed: want false, got %v", resp["installed"])
	}

	issues, _ := resp["issues"].([]any)
	if len(issues) == 0 {
		t.Fatal("issues should contain at least one entry")
	}
	found := false
	for _, iss := range issues {
		if s, ok := iss.(string); ok && strings.Contains(s, "settings.json not found") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("issues should mention 'settings.json not found', got %v", issues)
	}
}

// TestHandleHookStatus_WithHooks — settings.json has a valid tbox hook command
// for every expected event; handler should report installed:true.
func TestHandleHookStatus_WithHooks(t *testing.T) {
	m := newTestModule(t)

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	hookEvents := []string{"SessionStart", "UserPromptSubmit", "Stop", "StopFailure", "Notification", "PermissionRequest", "SessionEnd"}

	// Build hooks map with a tbox hook entry for each event.
	hooksMap := map[string]any{}
	for _, ev := range hookEvents {
		hooksMap[ev] = []any{
			map[string]any{
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": "/usr/local/bin/tbox hook " + ev,
					},
				},
			},
		}
	}
	settings := map[string]any{"hooks": hooksMap}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	writeSettingsJSON(t, tmp, string(data))

	req := httptest.NewRequest("GET", "/api/agent/hook-status", nil)
	w := httptest.NewRecorder()
	m.handleHookStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp["installed"] != true {
		t.Errorf("installed: want true, got %v", resp["installed"])
	}

	issues, _ := resp["issues"].([]any)
	if len(issues) != 0 {
		t.Errorf("issues: want empty, got %v", issues)
	}

	events, ok := resp["events"].(map[string]any)
	if !ok {
		t.Fatal("events field missing or wrong type")
	}
	for _, ev := range hookEvents {
		evData, ok := events[ev].(map[string]any)
		if !ok {
			t.Errorf("events[%s]: missing", ev)
			continue
		}
		if evData["installed"] != true {
			t.Errorf("events[%s].installed: want true, got %v", ev, evData["installed"])
		}
		cmd, _ := evData["command"].(string)
		if !strings.Contains(cmd, "tbox hook") {
			t.Errorf("events[%s].command: want to contain 'tbox hook', got %q", ev, cmd)
		}
	}
}

// TestHandleHookStatus_EmptyHooks — settings.json exists but hooks object is empty;
// all events should be reported as not installed.
func TestHandleHookStatus_EmptyHooks(t *testing.T) {
	m := newTestModule(t)

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	writeSettingsJSON(t, tmp, `{"hooks":{}}`)

	req := httptest.NewRequest("GET", "/api/agent/hook-status", nil)
	w := httptest.NewRecorder()
	m.handleHookStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp["installed"] != false {
		t.Errorf("installed: want false, got %v", resp["installed"])
	}

	events, ok := resp["events"].(map[string]any)
	if !ok {
		t.Fatal("events field missing or wrong type")
	}

	hookEvents := []string{"SessionStart", "UserPromptSubmit", "Stop", "StopFailure", "Notification", "PermissionRequest", "SessionEnd"}
	for _, ev := range hookEvents {
		evData, ok := events[ev].(map[string]any)
		if !ok {
			t.Errorf("events[%s]: missing", ev)
			continue
		}
		if evData["installed"] != false {
			t.Errorf("events[%s].installed: want false, got %v", ev, evData["installed"])
		}
	}
}

// TestFindTboxCommand exercises the findTboxCommand helper directly.
func TestFindTboxCommand(t *testing.T) {
	tests := []struct {
		name    string
		entries any
		want    string
	}{
		{
			name: "nil entries",
			entries: nil,
			want: "",
		},
		{
			name: "not a slice",
			entries: "string",
			want: "",
		},
		{
			name: "empty slice",
			entries: []any{},
			want: "",
		},
		{
			name: "entry without hooks key",
			entries: []any{
				map[string]any{"matcher": ".*"},
			},
			want: "",
		},
		{
			name: "hooks list with no tbox command",
			entries: []any{
				map[string]any{
					"hooks": []any{
						map[string]any{"type": "command", "command": "/usr/bin/notify-send done"},
					},
				},
			},
			want: "",
		},
		{
			name: "hooks list with tbox hook command",
			entries: []any{
				map[string]any{
					"hooks": []any{
						map[string]any{"type": "command", "command": "/opt/tbox hook Stop"},
					},
				},
			},
			want: "/opt/tbox hook Stop",
		},
		{
			name: "multiple entries, tbox in second",
			entries: []any{
				map[string]any{
					"hooks": []any{
						map[string]any{"type": "command", "command": "echo hello"},
					},
				},
				map[string]any{
					"hooks": []any{
						map[string]any{"type": "command", "command": "/usr/local/bin/tbox hook Notification"},
					},
				},
			},
			want: "/usr/local/bin/tbox hook Notification",
		},
		{
			name: "hook map has no command field",
			entries: []any{
				map[string]any{
					"hooks": []any{
						map[string]any{"type": "command"},
					},
				},
			},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := findTboxCommand(tc.entries)
			if got != tc.want {
				t.Errorf("findTboxCommand: want %q, got %q", tc.want, got)
			}
		})
	}
}

// TestHandleEvent_StoresAgentType verifies that agent_type from the request
// body is persisted and can be read back from the store.
func TestHandleEvent_StoresAgentType(t *testing.T) {
	m := newTestModule(t)

	body := `{"tmux_session":"dev","event_name":"Stop","raw_event":{},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	ev, err := m.events.Get("dev")
	if err != nil {
		t.Fatalf("events.Get: %v", err)
	}
	if ev == nil {
		t.Fatal("event not stored")
	}
	if ev.AgentType != "cc" {
		t.Errorf("agent_type: want cc, got %q", ev.AgentType)
	}
}
