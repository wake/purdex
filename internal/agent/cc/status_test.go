package cc_test

import (
	"encoding/json"
	"testing"

	"github.com/wake/purdex/internal/agent"
	cc "github.com/wake/purdex/internal/agent/cc"
)

func deriveViaProvider(purdexName string, rawEvent map[string]any) agent.DeriveResult {
	p := cc.NewProvider(nil, nil, nil, nil)
	raw, _ := json.Marshal(rawEvent)
	return p.DeriveStatus(purdexName, raw)
}

func TestDeriveCCStatus_PdxSessionStart_Idle(t *testing.T) {
	r := deriveViaProvider("PdxSessionStart", map[string]any{"source": "startup"})
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestDeriveCCStatus_PdxSessionStart_Compact_InvalidWithReason(t *testing.T) {
	r := deriveViaProvider("PdxSessionStart", map[string]any{"source": "compact"})
	if r.Valid {
		t.Fatal("compact PdxSessionStart should be ignored")
	}
	if r.Reason != "compact_ignored" {
		t.Fatalf("expected reason=compact_ignored, got %q", r.Reason)
	}
}

func TestDeriveCCStatus_PdxNotification_UnknownTypeReason(t *testing.T) {
	r := deriveViaProvider("PdxNotification", map[string]any{"notification_type": "weird"})
	if r.Valid {
		t.Fatal("unknown notification_type should be invalid")
	}
	if r.Reason != "notification_unknown_type" {
		t.Fatalf("expected reason=notification_unknown_type, got %q", r.Reason)
	}
}

func TestDeriveCCStatus_UnknownEventEmptyReason(t *testing.T) {
	r := deriveViaProvider("PdxFutureEvent", map[string]any{})
	if r.Valid {
		t.Fatal("unknown event should be invalid")
	}
	if r.Reason != "" {
		t.Fatalf("expected empty Reason for unknown event name, got %q", r.Reason)
	}
}

func TestDeriveCCStatus_PdxUserPromptSubmit_Running(t *testing.T) {
	r := deriveViaProvider("PdxUserPromptSubmit", map[string]any{})
	if !r.Valid || r.Status != agent.StatusRunning {
		t.Fatalf("expected running, got %+v", r)
	}
}

func TestDeriveCCStatus_PdxNotification_PermissionPrompt_Waiting(t *testing.T) {
	r := deriveViaProvider("PdxNotification", map[string]any{"notification_type": "permission_prompt"})
	if !r.Valid || r.Status != agent.StatusWaiting {
		t.Fatalf("expected waiting, got %+v", r)
	}
}

func TestDeriveCCStatus_PdxNotification_IdlePrompt_Idle(t *testing.T) {
	r := deriveViaProvider("PdxNotification", map[string]any{"notification_type": "idle_prompt"})
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestDeriveCCStatus_PdxPermissionRequest_Waiting(t *testing.T) {
	r := deriveViaProvider("PdxPermissionRequest", map[string]any{"tool_name": "Bash"})
	if !r.Valid || r.Status != agent.StatusWaiting {
		t.Fatalf("expected waiting, got %+v", r)
	}
	if r.Detail["tool_name"] != "Bash" {
		t.Fatalf("expected tool_name Bash in detail")
	}
}

func TestDeriveCCStatus_PdxStop_Idle(t *testing.T) {
	r := deriveViaProvider("PdxStop", map[string]any{"last_assistant_message": "Done"})
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestDeriveCCStatus_PdxStopFailure_Error(t *testing.T) {
	r := deriveViaProvider("PdxStopFailure", map[string]any{"error": "OOM"})
	if !r.Valid || r.Status != agent.StatusError {
		t.Fatalf("expected error, got %+v", r)
	}
}

func TestDeriveCCStatus_PdxSessionEnd_Clear(t *testing.T) {
	r := deriveViaProvider("PdxSessionEnd", map[string]any{})
	if !r.Valid || r.Status != agent.StatusClear {
		t.Fatalf("expected clear, got %+v", r)
	}
}

func TestDeriveCCStatus_PdxSubagentStart_DetailOnly(t *testing.T) {
	r := deriveViaProvider("PdxSubagentStart", map[string]any{"agent_id": "abc"})
	if !r.Valid {
		t.Fatal("PdxSubagentStart should be valid")
	}
	if r.Status != "" {
		t.Fatalf("PdxSubagentStart should not set status, got %s", r.Status)
	}
}

func TestDeriveCCStatus_PdxModelExtraction(t *testing.T) {
	r := deriveViaProvider("PdxSessionStart", map[string]any{"source": "startup", "modelName": "opus-4"})
	if r.Model != "opus-4" {
		t.Fatalf("expected model opus-4, got %s", r.Model)
	}
}

// TestDeriveCCStatus_LegacySessionStart_Invalid asserts the pre-W2 raw
// event-name literal "SessionStart" is no longer a catalog match for cc.
// Phase 1's daemon path falls back through isLegacyHookForUnmigrated only
// for codex / opencode; for cc, a legacy literal is genuinely a catalog
// miss and DeriveStatus returns Valid=false with empty Reason.
func TestDeriveCCStatus_LegacySessionStart_Invalid(t *testing.T) {
	for _, legacy := range []string{"SessionStart", "UserPromptSubmit", "Stop", "SessionEnd", "Notification"} {
		r := deriveViaProvider(legacy, map[string]any{})
		if r.Valid {
			t.Errorf("legacy %q must not be valid post-rename", legacy)
		}
	}
}
