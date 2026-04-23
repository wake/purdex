package codex_test

import (
	"encoding/json"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/codex"
)

func deriveViaProvider(eventName string) agent.DeriveResult {
	p := codex.NewProvider()
	return p.DeriveStatus(eventName, json.RawMessage(`{}`))
}

func deriveWithRaw(eventName, rawJSON string) agent.DeriveResult {
	p := codex.NewProvider()
	return p.DeriveStatus(eventName, json.RawMessage(rawJSON))
}

func TestCodexDeriveStatus_SessionStart(t *testing.T) {
	r := deriveViaProvider("SessionStart")
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestCodexDeriveStatus_UserPromptSubmit(t *testing.T) {
	r := deriveViaProvider("UserPromptSubmit")
	if !r.Valid || r.Status != agent.StatusRunning {
		t.Fatalf("expected running, got %+v", r)
	}
}

func TestCodexDeriveStatus_Stop(t *testing.T) {
	r := deriveViaProvider("Stop")
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

// TestCodexDeriveStatus_UnknownEvent is the post-Phase-1 smoke test for
// unknown events. Notification is now a known event (see CD1-CD3), so we
// switch to a synthetic future-event name that no provider claims.
func TestCodexDeriveStatus_UnknownEvent(t *testing.T) {
	r := deriveViaProvider("FutureEvent")
	if r.Valid {
		t.Fatal("Codex should not handle unknown future events")
	}
}

// CD1: Notification(permission_prompt) → Waiting + detail
func TestCodexDeriveStatus_NotificationPermission(t *testing.T) {
	r := deriveWithRaw("Notification", `{"notification_type":"permission_prompt","message":"hi"}`)
	if !r.Valid {
		t.Fatalf("expected Valid, got %+v", r)
	}
	if r.Status != agent.StatusWaiting {
		t.Fatalf("status = %q, want waiting", r.Status)
	}
	if r.Detail["notification_type"] != "permission_prompt" {
		t.Fatalf("detail.notification_type = %v, want permission_prompt", r.Detail["notification_type"])
	}
	if r.Detail["message"] != "hi" {
		t.Fatalf("detail.message = %v, want hi", r.Detail["message"])
	}
}

// CD1b: Notification(elicitation_dialog) → Waiting (mirrors cc)
func TestCodexDeriveStatus_NotificationElicitation(t *testing.T) {
	r := deriveWithRaw("Notification", `{"notification_type":"elicitation_dialog"}`)
	if !r.Valid || r.Status != agent.StatusWaiting {
		t.Fatalf("expected waiting, got %+v", r)
	}
}

// CD2: Notification(idle_prompt) → Idle
func TestCodexDeriveStatus_NotificationIdle(t *testing.T) {
	r := deriveWithRaw("Notification", `{"notification_type":"idle_prompt"}`)
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

// CD2b: Notification(auth_success) → Idle (mirrors cc)
func TestCodexDeriveStatus_NotificationAuthSuccess(t *testing.T) {
	r := deriveWithRaw("Notification", `{"notification_type":"auth_success"}`)
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

// CD3: Notification(unknown subtype) → Valid=false + Reason=notification_unknown_type
// (so handler can distinguish payload-schema drift from truly unknown event names).
func TestCodexDeriveStatus_NotificationUnknown(t *testing.T) {
	r := deriveWithRaw("Notification", `{"notification_type":"weird"}`)
	if r.Valid {
		t.Fatalf("expected Valid=false for unknown notification subtype, got %+v", r)
	}
	if r.Reason != "notification_unknown_type" {
		t.Fatalf("expected reason=notification_unknown_type, got %q", r.Reason)
	}
}

// Truly unknown event names (not in catalog) must leave Reason empty so the
// handler can fall back to the generic "event_not_in_catalog" classification.
func TestCodexDeriveStatus_UnknownEventEmptyReason(t *testing.T) {
	r := deriveViaProvider("FutureMysteryEvent")
	if r.Valid {
		t.Fatalf("expected Valid=false, got %+v", r)
	}
	if r.Reason != "" {
		t.Fatalf("expected empty Reason for unknown event name, got %q", r.Reason)
	}
}

// CD4: PermissionRequest → Waiting + detail.tool_name
func TestCodexDeriveStatus_PermissionRequest(t *testing.T) {
	r := deriveWithRaw("PermissionRequest", `{"tool_name":"Bash"}`)
	if !r.Valid {
		t.Fatalf("expected Valid, got %+v", r)
	}
	if r.Status != agent.StatusWaiting {
		t.Fatalf("status = %q, want waiting", r.Status)
	}
	if r.Detail["tool_name"] != "Bash" {
		t.Fatalf("detail.tool_name = %v, want Bash", r.Detail["tool_name"])
	}
}

// CD5: SubagentStart → Valid=true, Status="" (detail-only), detail.agent_id
func TestCodexDeriveStatus_SubagentStart(t *testing.T) {
	r := deriveWithRaw("SubagentStart", `{"agent_id":"abc"}`)
	if !r.Valid {
		t.Fatalf("expected Valid, got %+v", r)
	}
	if r.Status != "" {
		t.Fatalf("status = %q, want empty (detail-only event)", r.Status)
	}
	if r.Detail["agent_id"] != "abc" {
		t.Fatalf("detail.agent_id = %v, want abc", r.Detail["agent_id"])
	}
}

// CD6: SubagentStop → Valid=true, Status=""
func TestCodexDeriveStatus_SubagentStop(t *testing.T) {
	r := deriveWithRaw("SubagentStop", `{"agent_id":"xyz"}`)
	if !r.Valid {
		t.Fatalf("expected Valid, got %+v", r)
	}
	if r.Status != "" {
		t.Fatalf("status = %q, want empty (detail-only event)", r.Status)
	}
	if r.Detail["agent_id"] != "xyz" {
		t.Fatalf("detail.agent_id = %v, want xyz", r.Detail["agent_id"])
	}
}

// CD7: SessionEnd → Clear
func TestCodexDeriveStatus_SessionEnd(t *testing.T) {
	r := deriveWithRaw("SessionEnd", `{}`)
	if !r.Valid || r.Status != agent.StatusClear {
		t.Fatalf("expected clear, got %+v", r)
	}
}

// CD8: StopFailure → Error + detail.error
func TestCodexDeriveStatus_StopFailure(t *testing.T) {
	r := deriveWithRaw("StopFailure", `{"error":"OOM","error_details":"out of memory"}`)
	if !r.Valid {
		t.Fatalf("expected Valid, got %+v", r)
	}
	if r.Status != agent.StatusError {
		t.Fatalf("status = %q, want error", r.Status)
	}
	if r.Detail["error"] != "OOM" {
		t.Fatalf("detail.error = %v, want OOM", r.Detail["error"])
	}
	if r.Detail["error_details"] != "out of memory" {
		t.Fatalf("detail.error_details = %v, want 'out of memory'", r.Detail["error_details"])
	}
}
