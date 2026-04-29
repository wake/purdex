package agent

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// captureDevLog redirects log output to an in-memory buffer for the duration
// of the test. The previous output is restored via t.Cleanup so subsequent
// tests don't leak the redirected destination.
func captureDevLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

// devLogPostValid POSTs a valid PdxStop hook event for tmux session "dev",
// pane "%9", agent "cc", PurdexName "PdxStop". The provider is registered as
// fakeAgentProvider returning Valid=true Status=idle. Returns the recorder so
// callers can also inspect HTTP status.
func devLogPostValid(t *testing.T, m *Module) *httptest.ResponseRecorder {
	t.Helper()
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
		},
	})
	body := `{"tmux_session":"dev","tmux_pane_id":"%9","sender_pid":99,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxStop","raw_event":{},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", w.Code, w.Body.String())
	}
	return w
}

// TestHandler_DevModeLog_HookTrigger verifies the [hook] trigger line is
// emitted at handleEvent entry under PDX_DEV_MODE=1 with chain_id derived
// from the active trace collector.
func TestHandler_DevModeLog_HookTrigger(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	buf := captureDevLog(t)
	m := newTestModule(t)

	devLogPostValid(t, m)

	if m.traceSink != nil {
		m.traceSink.FlushForTest()
	}
	out := buf.String()
	line := findLogLine(t, out, `\[hook\] trigger`)
	if !strings.Contains(line, "session=dev") {
		t.Errorf("[hook] trigger missing session=dev: %s", line)
	}
	if !strings.Contains(line, "agent=cc") {
		t.Errorf("[hook] trigger missing agent=cc: %s", line)
	}
	if !strings.Contains(line, "purdex_name=PdxStop") {
		t.Errorf("[hook] trigger missing purdex_name=PdxStop: %s", line)
	}
	chainID := extractField(line, "chain_id=")
	if chainID == "" {
		t.Errorf("[hook] trigger chain_id should be non-empty: %s", line)
	}
}

// TestHandler_NoDevModeLog_Production verifies that with PDX_DEV_MODE unset
// (or set to anything other than "1"), no W4 dev log labels surface. This
// covers all six P2 tasks at once and is exercised under PDX_DEV_MODE=0 to
// pin the gate's strict equality semantics.
func TestHandler_NoDevModeLog_Production(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "0")
	// Defensive double-check: even if some test framework leaks the env var,
	// confirm the helper agrees production mode is in effect for this run.
	if os.Getenv("PDX_DEV_MODE") == "1" {
		t.Fatalf("PDX_DEV_MODE leaked from outer test environment")
	}
	buf := captureDevLog(t)
	m := newTestModule(t)

	devLogPostValid(t, m)

	if m.traceSink != nil {
		m.traceSink.FlushForTest()
	}
	out := buf.String()
	for _, label := range []string{"[hook]", "[derive]", "[handler]", "[broadcast]"} {
		if strings.Contains(out, label) {
			t.Errorf("production-mode log should not contain %s, got:\n%s", label, out)
		}
	}
}

// TestHandler_DevModeLog_DeriveVerifyPassed verifies the [derive]
// verify_passed line under PDX_DEV_MODE=1 for the valid path. Fields:
// agent / purdex_name / status / reason (empty on the success branch) /
// chain_id.
func TestHandler_DevModeLog_DeriveVerifyPassed(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	buf := captureDevLog(t)
	m := newTestModule(t)

	devLogPostValid(t, m)

	if m.traceSink != nil {
		m.traceSink.FlushForTest()
	}
	out := buf.String()
	line := findLogLine(t, out, `\[derive\] verify_passed`)
	if !strings.Contains(line, "agent=cc") {
		t.Errorf("[derive] verify_passed missing agent=cc: %s", line)
	}
	if !strings.Contains(line, "purdex_name=PdxStop") {
		t.Errorf("[derive] verify_passed missing purdex_name=PdxStop: %s", line)
	}
	if !strings.Contains(line, "status=idle") {
		t.Errorf("[derive] verify_passed missing status=idle: %s", line)
	}
	if !strings.Contains(line, "reason=") {
		t.Errorf("[derive] verify_passed missing reason= field: %s", line)
	}
	if extractField(line, "chain_id=") == "" {
		t.Errorf("[derive] verify_passed chain_id should be non-empty: %s", line)
	}
}

// TestHandler_DevModeLog_DeriveSkipped verifies the [derive] skipped line
// under PDX_DEV_MODE=1 for the catalog-miss / invalid-derive path. Fields:
// agent / purdex_name / reason / chain_id.
func TestHandler_DevModeLog_DeriveSkipped(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	buf := captureDevLog(t)
	m := newTestModule(t)
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: false}
		},
		events: []agentpkg.HookEventSpec{},
	})
	body := `{"tmux_session":"dev","tmux_pane_id":"%9","sender_pid":99,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"BogusEvent","raw_event":{},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	if m.traceSink != nil {
		m.traceSink.FlushForTest()
	}
	out := buf.String()
	line := findLogLine(t, out, `\[derive\] skipped`)
	if !strings.Contains(line, "agent=cc") {
		t.Errorf("[derive] skipped missing agent=cc: %s", line)
	}
	if !strings.Contains(line, "purdex_name=BogusEvent") {
		t.Errorf("[derive] skipped missing purdex_name=BogusEvent: %s", line)
	}
	if !strings.Contains(line, "reason=event_not_in_catalog") {
		t.Errorf("[derive] skipped missing reason=event_not_in_catalog: %s", line)
	}
	if extractField(line, "chain_id=") == "" {
		t.Errorf("[derive] skipped chain_id should be non-empty: %s", line)
	}
}

// TestHandler_DevModeLog_FrameApply verifies the [handler] frame_apply
// line under PDX_DEV_MODE=1 after applyFrameEvent succeeds. Fields:
// session / frame_id / lifecycle (= req.PurdexName) / decision / chain_id.
func TestHandler_DevModeLog_FrameApply(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	buf := captureDevLog(t)
	m := newTestModule(t)

	devLogPostValid(t, m)

	if m.traceSink != nil {
		m.traceSink.FlushForTest()
	}
	out := buf.String()
	line := findLogLine(t, out, `\[handler\] frame_apply`)
	if !strings.Contains(line, "session=dev") {
		t.Errorf("[handler] frame_apply missing session=dev: %s", line)
	}
	if !strings.Contains(line, "lifecycle=PdxStop") {
		t.Errorf("[handler] frame_apply missing lifecycle=PdxStop: %s", line)
	}
	if extractField(line, "decision=") == "" {
		t.Errorf("[handler] frame_apply decision should be non-empty: %s", line)
	}
	if extractField(line, "frame_id=") == "" {
		// Empty string is valid (skipped frames have no FrameID); just confirm the field is present.
		if !strings.Contains(line, "frame_id=") {
			t.Errorf("[handler] frame_apply missing frame_id= field: %s", line)
		}
	}
	if extractField(line, "chain_id=") == "" {
		t.Errorf("[handler] frame_apply chain_id should be non-empty: %s", line)
	}
}

// findLogLine returns the first log line matching the given regexp pattern,
// failing the test if none is found.
func findLogLine(t *testing.T, out, pattern string) string {
	t.Helper()
	re := regexp.MustCompile(pattern)
	for _, line := range strings.Split(out, "\n") {
		if re.MatchString(line) {
			return line
		}
	}
	t.Fatalf("no log line matched %q in:\n%s", pattern, out)
	return ""
}

// extractField pulls "value" out of a "key=value" pair within a log line.
// Returns "" if the key is absent. Stops at the next whitespace.
func extractField(line, key string) string {
	idx := strings.Index(line, key)
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(key):]
	if end := strings.IndexAny(rest, " \t\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}
