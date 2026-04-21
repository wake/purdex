package opencode_test

import (
	"encoding/json"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/opencode"
)

func deriveViaProvider(eventName string, rawEvent map[string]any) agent.DeriveResult {
	p := opencode.NewProvider()
	raw, _ := json.Marshal(rawEvent)
	return p.DeriveStatus(eventName, raw)
}

func TestOpenCodeDeriveStatus_SessionStart(t *testing.T) {
	r := deriveViaProvider("SessionStart", map[string]any{})
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestOpenCodeDeriveStatus_UserPromptSubmit(t *testing.T) {
	r := deriveViaProvider("UserPromptSubmit", map[string]any{"modelName": "openai/gpt-5.4"})
	if !r.Valid || r.Status != agent.StatusRunning {
		t.Fatalf("expected running, got %+v", r)
	}
	if r.Model != "openai/gpt-5.4" {
		t.Fatalf("expected model extraction, got %+v", r)
	}
}

func TestOpenCodeDeriveStatus_PermissionRequest(t *testing.T) {
	r := deriveViaProvider("PermissionRequest", map[string]any{"request_type": "permission", "permission": "bash"})
	if !r.Valid || r.Status != agent.StatusWaiting {
		t.Fatalf("expected waiting, got %+v", r)
	}
	if r.Detail["permission"] != "bash" {
		t.Fatalf("expected detail.permission bash, got %+v", r.Detail)
	}
}

func TestOpenCodeDeriveStatus_SubagentStart(t *testing.T) {
	r := deriveViaProvider("SubagentStart", map[string]any{"agent_id": "call-1", "agent_type": "Explore"})
	if !r.Valid {
		t.Fatal("SubagentStart should be valid")
	}
	if r.Status != "" {
		t.Fatalf("SubagentStart should not set status, got %q", r.Status)
	}
	if r.Detail["agent_id"] != "call-1" {
		t.Fatalf("agent_id = %#v, want call-1", r.Detail["agent_id"])
	}
}

func TestOpenCodeDeriveStatus_SubagentStop(t *testing.T) {
	r := deriveViaProvider("SubagentStop", map[string]any{"agent_id": "call-1", "agent_type": "Explore", "title": "done"})
	if !r.Valid {
		t.Fatal("SubagentStop should be valid")
	}
	if r.Status != "" {
		t.Fatalf("SubagentStop should not set status, got %q", r.Status)
	}
	if r.Detail["agent_type"] != "Explore" {
		t.Fatalf("agent_type = %#v, want Explore", r.Detail["agent_type"])
	}
}

func TestOpenCodeDeriveStatus_Stop(t *testing.T) {
	r := deriveViaProvider("Stop", map[string]any{})
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestOpenCodeDeriveStatus_StopFailure(t *testing.T) {
	r := deriveViaProvider("StopFailure", map[string]any{"error": "provider_error", "error_details": "boom"})
	if !r.Valid || r.Status != agent.StatusError {
		t.Fatalf("expected error, got %+v", r)
	}
	if r.Detail["error_details"] != "boom" {
		t.Fatalf("expected error details, got %+v", r.Detail)
	}
}

func TestOpenCodeDeriveStatus_SessionEnd(t *testing.T) {
	r := deriveViaProvider("SessionEnd", map[string]any{})
	if !r.Valid || r.Status != agent.StatusClear {
		t.Fatalf("expected clear, got %+v", r)
	}
}

func TestOpenCodeDeriveStatus_UnknownEvent(t *testing.T) {
	r := deriveViaProvider("Notification", map[string]any{})
	if r.Valid {
		t.Fatalf("unexpected valid result: %+v", r)
	}
}
