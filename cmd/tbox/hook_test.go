package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildHookPayload(t *testing.T) {
	stdin := strings.NewReader(`{"type":"Stop","session_id":"abc123"}`)
	p := buildHookPayload("my-session", "Stop", stdin)

	if p.TmuxSession != "my-session" {
		t.Errorf("TmuxSession = %q, want %q", p.TmuxSession, "my-session")
	}
	if p.EventName != "Stop" {
		t.Errorf("EventName = %q, want %q", p.EventName, "Stop")
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
	p := buildHookPayload("sess", "Start", stdin)

	if p.EventName != "Start" {
		t.Errorf("EventName = %q, want %q", p.EventName, "Start")
	}
	if string(p.RawEvent) != "{}" {
		t.Errorf("RawEvent = %s, want {}", string(p.RawEvent))
	}
}

func TestPostHookEvent(t *testing.T) {
	var received hookPayload

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

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
		TmuxSession: "test-sess",
		EventName:   "Stop",
		RawEvent:    json.RawMessage(`{"foo":"bar"}`),
	}

	if err := postHookEvent(ts.URL+"/api/agent/event", p); err != nil {
		t.Fatalf("postHookEvent: %v", err)
	}

	if received.TmuxSession != "test-sess" {
		t.Errorf("received TmuxSession = %q, want %q", received.TmuxSession, "test-sess")
	}
	if received.EventName != "Stop" {
		t.Errorf("received EventName = %q, want %q", received.EventName, "Stop")
	}
	if string(received.RawEvent) != `{"foo":"bar"}` {
		t.Errorf("received RawEvent = %s, want %s", string(received.RawEvent), `{"foo":"bar"}`)
	}
}

func TestPostHookEvent_ServerDown(t *testing.T) {
	p := hookPayload{
		TmuxSession: "x",
		EventName:   "Stop",
		RawEvent:    json.RawMessage(`{}`),
	}

	// Use a port that is almost certainly not listening
	err := postHookEvent("http://127.0.0.1:1/api/agent/event", p)
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
}
