package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
)

func TestBuildHookPayload(t *testing.T) {
	stdin := strings.NewReader(`{"type":"Stop","session_id":"abc123"}`)
	p := buildHookPayload("my-session", "Stop", stdin, "", hookProvenance{
		TmuxPaneID:      "%5",
		SenderPID:       123,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})

	if p.TmuxSession != "my-session" {
		t.Errorf("TmuxSession = %q, want %q", p.TmuxSession, "my-session")
	}
	if p.EventName != "Stop" {
		t.Errorf("EventName = %q, want %q", p.EventName, "Stop")
	}
	if p.TmuxPaneID != "%5" {
		t.Errorf("TmuxPaneID = %q, want %%5", p.TmuxPaneID)
	}
	if p.SenderPID != 123 {
		t.Errorf("SenderPID = %d, want 123", p.SenderPID)
	}
	if p.SenderStartTime != "Sun Apr 20 01:30:00 2026" {
		t.Errorf("SenderStartTime = %q, want expected", p.SenderStartTime)
	}

	// raw_event should be the original JSON
	var raw map[string]interface{}
	if err := json.Unmarshal(p.RawEvent, &raw); err != nil {
		t.Fatalf("unmarshal raw_event: %v", err)
	}
	if raw["type"] != "Stop" {
		t.Errorf("raw_event.type = %v, want %q", raw["type"], "Stop")
	}
	if raw["session_id"] != "abc123" {
		t.Errorf("raw_event.session_id = %v, want %q", raw["session_id"], "abc123")
	}
}

func TestBuildHookPayload_EmptyStdin(t *testing.T) {
	stdin := strings.NewReader("")
	p := buildHookPayload("sess", "Start", stdin, "", hookProvenance{
		TmuxPaneID:      "%7",
		SenderPID:       456,
		SenderStartTime: "Sun Apr 20 01:40:00 2026",
	})

	if p.EventName != "Start" {
		t.Errorf("EventName = %q, want %q", p.EventName, "Start")
	}
	if string(p.RawEvent) != "{}" {
		t.Errorf("RawEvent = %s, want {}", string(p.RawEvent))
	}
	if p.SenderPID != 456 || p.TmuxPaneID != "%7" {
		t.Errorf("provenance lost: %+v", p)
	}
}

func TestBuildHookPayload_WithAgentType(t *testing.T) {
	stdin := strings.NewReader(`{"hook_event_name":"Stop"}`)
	p := buildHookPayload("sess", "Stop", stdin, "cc", hookProvenance{})

	if p.AgentType != "cc" {
		t.Errorf("AgentType = %q, want %q", p.AgentType, "cc")
	}
}

func TestBuildHookPayload_EmptyAgent(t *testing.T) {
	stdin := strings.NewReader(`{}`)
	p := buildHookPayload("sess", "Stop", stdin, "", hookProvenance{})

	if p.AgentType != "" {
		t.Errorf("AgentType = %q, want empty", p.AgentType)
	}
}

func TestPostHookEvent(t *testing.T) {
	var received hookPayload
	var receivedAuth string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		receivedAuth = r.Header.Get("Authorization")

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &received); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	p := hookPayload{
		TmuxSession:     "test-sess",
		TmuxPaneID:      "%9",
		EventName:       "Stop",
		RawEvent:        json.RawMessage(`{"foo":"bar"}`),
		SenderPID:       777,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	}

	if err := postHookEvent(ts.URL+"/api/agent/event", "my-secret", p); err != nil {
		t.Fatalf("postHookEvent: %v", err)
	}

	if received.TmuxSession != "test-sess" {
		t.Errorf("received TmuxSession = %q, want %q", received.TmuxSession, "test-sess")
	}
	if received.EventName != "Stop" {
		t.Errorf("received EventName = %q, want %q", received.EventName, "Stop")
	}
	if received.TmuxPaneID != "%9" {
		t.Errorf("received TmuxPaneID = %q, want %%9", received.TmuxPaneID)
	}
	if received.SenderPID != 777 {
		t.Errorf("received SenderPID = %d, want 777", received.SenderPID)
	}
	if string(received.RawEvent) != `{"foo":"bar"}` {
		t.Errorf("received RawEvent = %s, want %s", string(received.RawEvent), `{"foo":"bar"}`)
	}
	if receivedAuth != "Bearer my-secret" {
		t.Errorf("Authorization = %q, want %q", receivedAuth, "Bearer my-secret")
	}
}

func TestPostHookEvent_NoToken(t *testing.T) {
	var receivedAuth string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	p := hookPayload{
		TmuxSession:     "x",
		EventName:       "Stop",
		RawEvent:        json.RawMessage(`{}`),
		SenderPID:       1,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	}

	if err := postHookEvent(ts.URL+"/api/agent/event", "", p); err != nil {
		t.Fatalf("postHookEvent: %v", err)
	}
	if receivedAuth != "" {
		t.Errorf("Authorization = %q, want empty (no token)", receivedAuth)
	}
}

func TestPostHookEvent_ServerDown(t *testing.T) {
	p := hookPayload{
		TmuxSession:     "x",
		EventName:       "Stop",
		RawEvent:        json.RawMessage(`{}`),
		SenderPID:       1,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	}

	// Use a port that is almost certainly not listening
	err := postHookEvent("http://127.0.0.1:1/api/agent/event", "", p)
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
}

func TestRunHook_PopulatesProvenance(t *testing.T) {
	origSession := queryTmuxSessionFn
	origPaneID := queryTmuxPaneIDFn
	origResolve := resolveHookProvenanceFn
	origPost := postHookEventFn
	origLoad := loadConfigFn
	origStdin := os.Stdin
	t.Cleanup(func() {
		queryTmuxSessionFn = origSession
		queryTmuxPaneIDFn = origPaneID
		resolveHookProvenanceFn = origResolve
		postHookEventFn = origPost
		loadConfigFn = origLoad
		os.Stdin = origStdin
	})

	queryTmuxSessionFn = func() string { return "work" }
	queryTmuxPaneIDFn = func() string { return "%5" }
	resolveHookProvenanceFn = func() hookProvenance {
		return hookProvenance{
			TmuxPaneID:      "%5",
			SenderPID:       36649,
			SenderStartTime: "Sun Apr 20 01:30:00 2026",
		}
	}
	loadConfigFn = func(string) (config.Config, error) { return config.Config{}, nil }

	var got hookPayload
	postHookEventFn = func(_ string, _ string, payload hookPayload) error {
		got = payload
		return nil
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString(`{"hook_event_name":"SessionStart"}`); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	_ = w.Close()
	os.Stdin = r

	runHook([]string{"--agent", "cc", "SessionStart"})

	if got.TmuxSession != "work" {
		t.Fatalf("tmux_session = %q, want work", got.TmuxSession)
	}
	if got.TmuxPaneID != "%5" {
		t.Fatalf("tmux_pane_id = %q, want %%5", got.TmuxPaneID)
	}
	if got.SenderPID != 36649 {
		t.Fatalf("sender_pid = %d, want 36649", got.SenderPID)
	}
	if got.SenderStartTime != "Sun Apr 20 01:30:00 2026" {
		t.Fatalf("sender_start_time = %q, want expected", got.SenderStartTime)
	}
	if got.SenderUncertain {
		t.Fatal("sender_uncertain = true, want false")
	}
}
