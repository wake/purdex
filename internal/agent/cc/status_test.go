package cc_test

import (
	"encoding/json"
	"testing"

	"github.com/wake/tmux-box/internal/agent"
	cc "github.com/wake/tmux-box/internal/agent/cc"
)

func deriveViaProvider(eventName string, rawEvent map[string]any) agent.DeriveResult {
	p := cc.NewProvider(nil, nil, nil, nil)
	raw, _ := json.Marshal(rawEvent)
	return p.DeriveStatus(eventName, raw)
}

func TestCCDeriveStatus_SessionStart(t *testing.T) {
	r := deriveViaProvider("SessionStart", map[string]any{"source": "startup"})
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestCCDeriveStatus_SessionStartCompact(t *testing.T) {
	r := deriveViaProvider("SessionStart", map[string]any{"source": "compact"})
	if r.Valid {
		t.Fatal("compact SessionStart should be ignored")
	}
}

func TestCCDeriveStatus_UserPromptSubmit(t *testing.T) {
	r := deriveViaProvider("UserPromptSubmit", map[string]any{})
	if !r.Valid || r.Status != agent.StatusRunning {
		t.Fatalf("expected running, got %+v", r)
	}
}

func TestCCDeriveStatus_NotificationPermission(t *testing.T) {
	r := deriveViaProvider("Notification", map[string]any{"notification_type": "permission_prompt"})
	if !r.Valid || r.Status != agent.StatusWaiting {
		t.Fatalf("expected waiting, got %+v", r)
	}
}

func TestCCDeriveStatus_NotificationIdle(t *testing.T) {
	r := deriveViaProvider("Notification", map[string]any{"notification_type": "idle_prompt"})
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestCCDeriveStatus_PermissionRequest(t *testing.T) {
	r := deriveViaProvider("PermissionRequest", map[string]any{"tool_name": "Bash"})
	if !r.Valid || r.Status != agent.StatusWaiting {
		t.Fatalf("expected waiting, got %+v", r)
	}
	if r.Detail["tool_name"] != "Bash" {
		t.Fatalf("expected tool_name Bash in detail")
	}
}

func TestCCDeriveStatus_Stop(t *testing.T) {
	r := deriveViaProvider("Stop", map[string]any{"last_assistant_message": "Done"})
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestCCDeriveStatus_StopFailure(t *testing.T) {
	r := deriveViaProvider("StopFailure", map[string]any{"error": "OOM"})
	if !r.Valid || r.Status != agent.StatusError {
		t.Fatalf("expected error, got %+v", r)
	}
}

func TestCCDeriveStatus_SessionEnd(t *testing.T) {
	r := deriveViaProvider("SessionEnd", map[string]any{})
	if !r.Valid || r.Status != agent.StatusClear {
		t.Fatalf("expected clear, got %+v", r)
	}
}

func TestCCDeriveStatus_SubagentStart(t *testing.T) {
	r := deriveViaProvider("SubagentStart", map[string]any{"agent_id": "abc"})
	if !r.Valid {
		t.Fatal("SubagentStart should be valid")
	}
	if r.Status != "" {
		t.Fatalf("SubagentStart should not set status, got %s", r.Status)
	}
}

func TestCCDeriveStatus_UnknownEvent(t *testing.T) {
	r := deriveViaProvider("FutureEvent", map[string]any{})
	if r.Valid {
		t.Fatal("unknown event should be invalid")
	}
}

func TestCCDeriveStatus_ModelExtraction(t *testing.T) {
	r := deriveViaProvider("SessionStart", map[string]any{"source": "startup", "modelName": "opus-4"})
	if r.Model != "opus-4" {
		t.Fatalf("expected model opus-4, got %s", r.Model)
	}
}
