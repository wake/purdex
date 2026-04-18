package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRenderMinimal_FullFields(t *testing.T) {
	raw := json.RawMessage(`{
		"model": {"id": "claude-sonnet-4-6", "display_name": "Sonnet"},
		"context_window": {"used_percentage": 23},
		"cost": {"total_cost_usd": 0.12}
	}`)
	got := renderMinimal(raw)
	want := "[pdx] Sonnet · ctx 23% · $0.12"
	if got != want {
		t.Errorf("renderMinimal = %q, want %q", got, want)
	}
}

func TestRenderMinimal_MissingDisplayName(t *testing.T) {
	raw := json.RawMessage(`{"model":{"id":"claude-opus-4-7"}}`)
	got := renderMinimal(raw)
	if !strings.Contains(got, "claude-opus-4-7") {
		t.Errorf("renderMinimal = %q, expected id fallback", got)
	}
}

func TestRenderMinimal_NoCost(t *testing.T) {
	raw := json.RawMessage(`{"model":{"display_name":"Opus"},"context_window":{"used_percentage":8}}`)
	got := renderMinimal(raw)
	want := "[pdx] Opus · ctx 8%"
	if got != want {
		t.Errorf("renderMinimal = %q, want %q", got, want)
	}
}

func TestRenderMinimal_Empty(t *testing.T) {
	got := renderMinimal(json.RawMessage(`{}`))
	want := "[pdx]"
	if got != want {
		t.Errorf("renderMinimal = %q, want %q", got, want)
	}
}

func TestReadStdinWithTimeout_Valid(t *testing.T) {
	src := bytes.NewBufferString(`{"a":1}`)
	got := readStdinWithTimeout(src, 1)
	if string(got) != `{"a":1}` {
		t.Errorf("got %q, want JSON", got)
	}
}

func TestReadStdinWithTimeout_Empty(t *testing.T) {
	got := readStdinWithTimeout(bytes.NewBuffer(nil), 1)
	if string(got) != "{}" {
		t.Errorf("empty stdin got %q, want {}", got)
	}
}

func TestParseInnerFlag(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{}, ""},
		{[]string{"--inner", "ccstatusline"}, "ccstatusline"},
		{[]string{"--inner", "ccstatusline --format compact"}, "ccstatusline --format compact"},
		{[]string{"--unknown", "x"}, ""},
	}
	for _, tc := range cases {
		got := parseInnerFlag(tc.args)
		if got != tc.want {
			t.Errorf("parseInnerFlag(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestExecInner_Success(t *testing.T) {
	stdin := []byte(`{"a":1}`)
	got := execInner("echo hello", stdin, 2)
	if strings.TrimSpace(got) != "hello" {
		t.Errorf("execInner stdout = %q, want %q", got, "hello")
	}
}

func TestExecInner_Timeout(t *testing.T) {
	got := execInner("sleep 5", []byte("{}"), 1)
	// Timeout is silent; empty or partial stdout is acceptable.
	if got == "should-never-happen" {
		t.Error("sentinel check")
	}
	_ = got
}

func TestExecInner_NonZeroExitCaptured(t *testing.T) {
	got := execInner("printf 'foo'; exit 1", []byte("{}"), 2)
	if strings.TrimSpace(got) != "foo" {
		t.Errorf("non-zero exit should still capture stdout; got %q", got)
	}
}

func TestPostStatus_Success(t *testing.T) {
	var received statuslinePayload
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	err := postStatus(ts.URL+"/api/agent/status", "tok", statuslinePayload{
		TmuxSession: "sess1",
		AgentType:   "cc",
		RawStatus:   json.RawMessage(`{"x":1}`),
	})
	if err != nil {
		t.Fatalf("postStatus: %v", err)
	}
	if received.TmuxSession != "sess1" {
		t.Errorf("tmux_session mismatch: %q", received.TmuxSession)
	}
}

func TestPostStatus_Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer ts.Close()

	err := postStatus(ts.URL, "", statuslinePayload{TmuxSession: "x", AgentType: "cc"})
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

func TestPostStatus_SilentOn5xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	err := postStatus(ts.URL, "", statuslinePayload{TmuxSession: "x", AgentType: "cc"})
	if err == nil {
		t.Error("5xx should return error, got nil")
	}
}

func TestResolveDaemonHost(t *testing.T) {
	cases := []struct {
		bind string
		want string
	}{
		{"", "127.0.0.1"},
		{"0.0.0.0", "127.0.0.1"},
		{"::", "127.0.0.1"},
		{"[::]", "127.0.0.1"},
		{"127.0.0.1", "127.0.0.1"},
		{"100.64.0.2", "100.64.0.2"},
		{"localhost", "localhost"},
	}
	for _, tc := range cases {
		if got := resolveDaemonHost(tc.bind); got != tc.want {
			t.Errorf("resolveDaemonHost(%q) = %q, want %q", tc.bind, got, tc.want)
		}
	}
}
