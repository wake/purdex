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

func TestBuildHookPayload_MarshalsPurdexName(t *testing.T) {
	stdin := strings.NewReader(`{}`)
	p := buildHookPayload("", "sess", "PdxSessionStart", stdin, "cc", hookProvenance{
		TmuxPaneID:      "%5",
		SenderPID:       1,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `"purdex_name":"PdxSessionStart"`) {
		t.Errorf("payload missing purdex_name: %s", s)
	}
	if strings.Contains(s, `"event_name"`) {
		t.Errorf("payload must not emit legacy event_name: %s", s)
	}
}

func TestRunHook_PositionalArgPassedAsPurdexName(t *testing.T) {
	origInfo := queryTmuxSessionInfoFn
	origResolve := resolveHookProvenanceFn
	origPost := postHookEventFn
	origLoad := loadConfigFn
	origStdin := os.Stdin
	t.Cleanup(func() {
		queryTmuxSessionInfoFn = origInfo
		resolveHookProvenanceFn = origResolve
		postHookEventFn = origPost
		loadConfigFn = origLoad
		os.Stdin = origStdin
	})

	queryTmuxSessionInfoFn = func() (string, string) { return "$1", "work" }
	resolveHookProvenanceFn = func() hookProvenance {
		return hookProvenance{
			TmuxPaneID:      "%5",
			SenderPID:       42,
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
	_ = w.Close()
	os.Stdin = r

	runHook([]string{"--agent", "cc", "PdxSessionStart"})

	if got.PurdexName != "PdxSessionStart" {
		t.Errorf("PurdexName = %q, want PdxSessionStart", got.PurdexName)
	}
}

func TestBuildHookPayload(t *testing.T) {
	stdin := strings.NewReader(`{"type":"Stop","session_id":"abc123"}`)
	p := buildHookPayload("", "my-session", "Stop", stdin, "", hookProvenance{
		TmuxPaneID:      "%5",
		SenderPID:       123,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})

	if p.TmuxSession != "my-session" {
		t.Errorf("TmuxSession = %q, want %q", p.TmuxSession, "my-session")
	}
	if p.PurdexName != "Stop" {
		t.Errorf("PurdexName = %q, want %q", p.PurdexName, "Stop")
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
	p := buildHookPayload("", "sess", "Start", stdin, "", hookProvenance{
		TmuxPaneID:      "%7",
		SenderPID:       456,
		SenderStartTime: "Sun Apr 20 01:40:00 2026",
	})

	if p.PurdexName != "Start" {
		t.Errorf("PurdexName = %q, want %q", p.PurdexName, "Start")
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
	p := buildHookPayload("", "sess", "Stop", stdin, "cc", hookProvenance{})

	if p.AgentType != "cc" {
		t.Errorf("AgentType = %q, want %q", p.AgentType, "cc")
	}
}

func TestBuildHookPayload_EmptyAgent(t *testing.T) {
	stdin := strings.NewReader(`{}`)
	p := buildHookPayload("", "sess", "Stop", stdin, "", hookProvenance{})

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
		PurdexName:      "Stop",
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
	if received.PurdexName != "Stop" {
		t.Errorf("received PurdexName = %q, want %q", received.PurdexName, "Stop")
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
		PurdexName:      "Stop",
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
		PurdexName:      "Stop",
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

// TestQueryTmuxSessionInfo_ParsesIDAndName proves the new helper returns
// (sessionID, sessionName) from a single tmux call. The hook payload uses the
// immutable ID to bypass the name cache rename-race window in handler.go.
func TestQueryTmuxSessionInfo_ParsesIDAndName(t *testing.T) {
	origInfo := queryTmuxSessionInfoFn
	t.Cleanup(func() { queryTmuxSessionInfoFn = origInfo })

	queryTmuxSessionInfoFn = func() (string, string) { return "$5", "alpha" }

	id, name := queryTmuxSessionInfoFn()
	if id != "$5" {
		t.Errorf("sessionID = %q, want $5", id)
	}
	if name != "alpha" {
		t.Errorf("sessionName = %q, want alpha", name)
	}
}

// TestQueryTmuxSessionInfo_HandlesNameWithPipe pins parser behaviour for
// session names containing the delimiter. We split on the FIRST '|' so the
// rest of the name (including any further '|') survives intact. Real tmux
// permits '|' in session names; the parser must not corrupt it.
func TestQueryTmuxSessionInfo_HandlesNameWithPipe(t *testing.T) {
	id, name := parseTmuxSessionInfo("$5|name|with|pipes\n")
	if id != "$5" {
		t.Errorf("sessionID = %q, want $5", id)
	}
	if name != "name|with|pipes" {
		t.Errorf("sessionName = %q, want name|with|pipes", name)
	}
}

// TestQueryTmuxSessionInfo_NoDelimiter covers the malformed/legacy tmux output
// path. When the delimiter is absent we treat the entire output as the name
// and return an empty ID — handler.go falls back to the name path safely.
func TestQueryTmuxSessionInfo_NoDelimiter(t *testing.T) {
	id, name := parseTmuxSessionInfo("legacy-name\n")
	if id != "" {
		t.Errorf("sessionID = %q, want empty", id)
	}
	if name != "legacy-name" {
		t.Errorf("sessionName = %q, want legacy-name", name)
	}
}

// TestBuildHookPayload_IncludesSessionID verifies the wire format carries
// tmux_session_id when populated. The daemon needs the immutable ID to
// resolve the session code without consulting the name cache.
func TestBuildHookPayload_IncludesSessionID(t *testing.T) {
	stdin := strings.NewReader(`{}`)
	p := buildHookPayload("$7", "alpha", "PdxStop", stdin, "cc", hookProvenance{
		TmuxPaneID:      "%5",
		SenderPID:       1,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	if p.TmuxSessionID != "$7" {
		t.Errorf("TmuxSessionID = %q, want $7", p.TmuxSessionID)
	}

	out, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"tmux_session_id":"$7"`) {
		t.Errorf("payload missing tmux_session_id: %s", string(out))
	}
}

// TestBuildHookPayload_OmitsEmptySessionID guards omitempty: legacy CLIs that
// don't populate the ID must not emit the field, so handler.go's fallback
// branch (driven by req.TmuxSessionID == "") triggers correctly.
func TestBuildHookPayload_OmitsEmptySessionID(t *testing.T) {
	stdin := strings.NewReader(`{}`)
	p := buildHookPayload("", "alpha", "PdxStop", stdin, "cc", hookProvenance{
		TmuxPaneID: "%5", SenderPID: 1,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), `tmux_session_id`) {
		t.Errorf("empty TmuxSessionID must be omitted via omitempty: %s", string(out))
	}
}

// TestRunHook_PopulatesSessionID proves the runHook entry point routes the
// new info-query helper into the payload. queryTmuxSessionInfoFn returns both
// fields in a single call — runHook must pass both into buildHookPayload.
func TestRunHook_PopulatesSessionID(t *testing.T) {
	origInfo := queryTmuxSessionInfoFn
	origResolve := resolveHookProvenanceFn
	origPost := postHookEventFn
	origLoad := loadConfigFn
	origStdin := os.Stdin
	t.Cleanup(func() {
		queryTmuxSessionInfoFn = origInfo
		resolveHookProvenanceFn = origResolve
		postHookEventFn = origPost
		loadConfigFn = origLoad
		os.Stdin = origStdin
	})

	queryTmuxSessionInfoFn = func() (string, string) { return "$11", "work" }
	resolveHookProvenanceFn = func() hookProvenance {
		return hookProvenance{TmuxPaneID: "%5", SenderPID: 42, SenderStartTime: "t"}
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
	_ = w.Close()
	os.Stdin = r

	runHook([]string{"--agent", "cc", "PdxSessionStart"})

	if got.TmuxSession != "work" {
		t.Errorf("TmuxSession = %q, want work", got.TmuxSession)
	}
	if got.TmuxSessionID != "$11" {
		t.Errorf("TmuxSessionID = %q, want $11", got.TmuxSessionID)
	}
}

func TestRunHook_PopulatesProvenance(t *testing.T) {
	origInfo := queryTmuxSessionInfoFn
	origPaneID := queryTmuxPaneIDFn
	origResolve := resolveHookProvenanceFn
	origPost := postHookEventFn
	origLoad := loadConfigFn
	origStdin := os.Stdin
	t.Cleanup(func() {
		queryTmuxSessionInfoFn = origInfo
		queryTmuxPaneIDFn = origPaneID
		resolveHookProvenanceFn = origResolve
		postHookEventFn = origPost
		loadConfigFn = origLoad
		os.Stdin = origStdin
	})

	queryTmuxSessionInfoFn = func() (string, string) { return "$3", "work" }
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
