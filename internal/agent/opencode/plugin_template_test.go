package opencode

import (
	"strings"
	"testing"
)

func TestPluginState_TaskStartMapsSubagentStart(t *testing.T) {
	state := newPluginState()
	event, ok := state.handleTaskStart("sess-1", "call-1", map[string]any{
		"subagent_type": "Explore",
		"description":   "trace tree",
		"prompt":        "inspect process tree",
	})
	if !ok {
		t.Fatal("expected task start event")
	}
	if event.Name != "SubagentStart" {
		t.Fatalf("event name = %q, want SubagentStart", event.Name)
	}
	if event.Payload["agent_id"] != "call-1" {
		t.Fatalf("agent_id = %#v, want call-1", event.Payload["agent_id"])
	}
	if event.Payload["agent_type"] != "Explore" {
		t.Fatalf("agent_type = %#v, want Explore", event.Payload["agent_type"])
	}
	if len(state.activeSubagents) != 1 || state.activeSubagents["sess-1:call-1"] != "Explore" {
		t.Fatalf("activeSubagents = %#v, want sess-1:call-1 => Explore", state.activeSubagents)
	}
}

func TestPluginState_TaskStartDuplicateCallIDIgnored(t *testing.T) {
	state := newPluginState()
	if _, ok := state.handleTaskStart("sess-1", "call-1", map[string]any{"agent": "Explore"}); !ok {
		t.Fatal("first start should be accepted")
	}
	if _, ok := state.handleTaskStart("sess-1", "call-1", map[string]any{"agent": "Plan"}); ok {
		t.Fatal("duplicate start should be ignored")
	}
	if got := state.activeSubagents["sess-1:call-1"]; got != "Explore" {
		t.Fatalf("activeSubagents[sess-1:call-1] = %q, want Explore", got)
	}
}

func TestPluginState_TaskStartSameCallIDDifferentSessionAllowed(t *testing.T) {
	state := newPluginState()
	if _, ok := state.handleTaskStart("sess-1", "call-1", map[string]any{"agent": "Explore"}); !ok {
		t.Fatal("first start should be accepted")
	}
	if _, ok := state.handleTaskStart("sess-2", "call-1", map[string]any{"agent": "Plan"}); !ok {
		t.Fatal("same callID in different session should be accepted")
	}
	if got := state.activeSubagents["sess-2:call-1"]; got != "Plan" {
		t.Fatalf("activeSubagents[sess-2:call-1] = %q, want Plan", got)
	}
}

func TestPluginState_TaskStartEmptyCallIDIgnored(t *testing.T) {
	state := newPluginState()
	if _, ok := state.handleTaskStart("sess-1", "", map[string]any{"agent": "Explore"}); ok {
		t.Fatal("empty callID should be ignored")
	}
	if len(state.activeSubagents) != 0 {
		t.Fatalf("activeSubagents = %#v, want empty", state.activeSubagents)
	}
}

func TestPluginState_TaskStopMapsSubagentStop(t *testing.T) {
	state := newPluginState()
	if _, ok := state.handleTaskStart("sess-1", "call-1", map[string]any{"agent": "Explore"}); !ok {
		t.Fatal("task start should be accepted")
	}
	event, ok := state.handleTaskStop("sess-1", "call-1", "done", "all good")
	if !ok {
		t.Fatal("expected task stop event")
	}
	if event.Name != "SubagentStop" {
		t.Fatalf("event name = %q, want SubagentStop", event.Name)
	}
	if event.Payload["agent_id"] != "call-1" {
		t.Fatalf("agent_id = %#v, want call-1", event.Payload["agent_id"])
	}
	if event.Payload["agent_type"] != "Explore" {
		t.Fatalf("agent_type = %#v, want Explore", event.Payload["agent_type"])
	}
	if len(state.activeSubagents) != 0 {
		t.Fatalf("activeSubagents = %#v, want empty", state.activeSubagents)
	}
}

func TestPluginState_TaskStopUnknownCallIDIgnored(t *testing.T) {
	state := newPluginState()
	if _, ok := state.handleTaskStop("sess-1", "missing", "done", "all good"); ok {
		t.Fatal("unknown callID stop should be ignored")
	}
}

func TestPluginState_TaskStopBeforeStartIgnored(t *testing.T) {
	state := newPluginState()
	if _, ok := state.handleTaskStop("sess-1", "call-1", "done", "all good"); ok {
		t.Fatal("stop-before-start should be ignored")
	}
	if len(state.activeSubagents) != 0 {
		t.Fatalf("activeSubagents = %#v, want empty", state.activeSubagents)
	}
}

func TestPluginState_SuppressIdleAfterError(t *testing.T) {
	state := newPluginState()
	event, ok := state.handleSessionError("provider_error", "boom")
	if !ok {
		t.Fatal("expected stop failure event")
	}
	if event.Name != "StopFailure" {
		t.Fatalf("event name = %q, want StopFailure", event.Name)
	}
	if _, ok := state.handleSessionIdle(); ok {
		t.Fatal("first idle after error should be suppressed")
	}
	event, ok = state.handleSessionIdle()
	if !ok {
		t.Fatal("second idle should emit stop")
	}
	if event.Name != "Stop" {
		t.Fatalf("event name = %q, want Stop", event.Name)
	}
}

// TestExtractEmittedEvents is the fix-plan §2.3 PT1 assertion: the helper
// reliably pulls event names from emit('...') and emit("...") calls in a
// synthetic body, including across multiple lines and with incidental
// whitespace. This helper is test-only (plan §1.5); it underwrites the
// template/specs parity check, not runtime health.
func TestExtractEmittedEvents(t *testing.T) {
	body := `
  await emit('SessionStart', {foo: 1})
  await emit("UserPromptSubmit",
    {bar: 2})
  await emit('Stop', {})
  // not captured: commented out. // await emit('Ignored', {})
`
	got := extractEmittedEvents(body)
	want := map[string]bool{"SessionStart": true, "UserPromptSubmit": true, "Stop": true, "Ignored": true}
	// We deliberately include 'Ignored' from a comment — regex-based extraction
	// is known to see through comments. That is fine because this helper is
	// only used against the real template body where no such comments exist,
	// and PT2-PT4 tests assert the spec-side parity that would surface a
	// mismatch if such a ghost emit ever shipped.
	gotSet := make(map[string]bool, len(got))
	for _, n := range got {
		gotSet[n] = true
	}
	for n := range want {
		if !gotSet[n] {
			t.Errorf("extractEmittedEvents missing %q; got %v", n, got)
		}
	}
}

// TestExtractPdxPath_RoundtripEscapedLiterals is the fix-plan §2.3 PT6
// assertion: renderManagedPlugin writes pdxPath with %q (complete with
// escapes for backslash/quote/unicode/…) and extractPdxPath must be able
// to round-trip it. Using a naive regex (e.g. `"([^"]+)"`) would stop at
// the first escaped quote and then double-escape when re-rendered,
// producing a byte-mismatch on perfectly managed plugins — the v4 plan
// specifically calls this out.
func TestExtractPdxPath_RoundtripEscapedLiterals(t *testing.T) {
	cases := []string{
		"/path with spaces/pdx",
		`C:\Users\foo\pdx.exe`,
		`/weird"path/pdx`,
		"/使用者/pdx",
	}
	for _, input := range cases {
		body := renderManagedPlugin(input)
		got, ok := extractPdxPath(body)
		if !ok {
			t.Errorf("extractPdxPath failed for %q", input)
			continue
		}
		if got != input {
			t.Errorf("extractPdxPath(%q) round-trip = %q", input, got)
		}
	}
}

func TestRenderManagedPlugin_UsesInputModelAndSessionScopedSubagentKeys(t *testing.T) {
	rendered := renderManagedPlugin("/usr/local/bin/pdx")
	if !strings.Contains(rendered, "const model = input.model") {
		t.Fatalf("rendered plugin should read model from input.model: %s", rendered)
	}
	if !strings.Contains(rendered, "const subagentKey = input.sessionID + ':' + input.callID") {
		t.Fatalf("rendered plugin should scope subagent keys by sessionID+callID: %s", rendered)
	}
	if !strings.Contains(rendered, "if (activeSubagents.has(subagentKey)) return") {
		t.Fatalf("rendered plugin should ignore duplicate subagent starts: %s", rendered)
	}
}
