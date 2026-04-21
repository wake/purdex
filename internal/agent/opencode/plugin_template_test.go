package opencode

import "testing"

func TestPluginState_TaskStartMapsSubagentStart(t *testing.T) {
	state := newPluginState()
	event, ok := state.handleTaskStart("call-1", map[string]any{
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
	if len(state.activeSubagents) != 1 || state.activeSubagents["call-1"] != "Explore" {
		t.Fatalf("activeSubagents = %#v, want call-1 => Explore", state.activeSubagents)
	}
}

func TestPluginState_TaskStartDuplicateCallIDIgnored(t *testing.T) {
	state := newPluginState()
	if _, ok := state.handleTaskStart("call-1", map[string]any{"agent": "Explore"}); !ok {
		t.Fatal("first start should be accepted")
	}
	if _, ok := state.handleTaskStart("call-1", map[string]any{"agent": "Plan"}); ok {
		t.Fatal("duplicate start should be ignored")
	}
	if got := state.activeSubagents["call-1"]; got != "Explore" {
		t.Fatalf("activeSubagents[call-1] = %q, want Explore", got)
	}
}

func TestPluginState_TaskStartEmptyCallIDIgnored(t *testing.T) {
	state := newPluginState()
	if _, ok := state.handleTaskStart("", map[string]any{"agent": "Explore"}); ok {
		t.Fatal("empty callID should be ignored")
	}
	if len(state.activeSubagents) != 0 {
		t.Fatalf("activeSubagents = %#v, want empty", state.activeSubagents)
	}
}

func TestPluginState_TaskStopMapsSubagentStop(t *testing.T) {
	state := newPluginState()
	if _, ok := state.handleTaskStart("call-1", map[string]any{"agent": "Explore"}); !ok {
		t.Fatal("task start should be accepted")
	}
	event, ok := state.handleTaskStop("call-1", "done", "all good")
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
	if _, ok := state.handleTaskStop("missing", "done", "all good"); ok {
		t.Fatal("unknown callID stop should be ignored")
	}
}

func TestPluginState_TaskStopBeforeStartIgnored(t *testing.T) {
	state := newPluginState()
	if _, ok := state.handleTaskStop("call-1", "done", "all good"); ok {
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
