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

func TestCodexDeriveStatus_UnknownEvent(t *testing.T) {
	r := deriveViaProvider("Notification")
	if r.Valid {
		t.Fatal("Codex should not handle Notification")
	}
}
