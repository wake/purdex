package codex

import (
	"encoding/json"

	"github.com/wake/purdex/internal/agent"
)

func deriveCodexStatus(eventName string, rawEvent json.RawMessage) agent.DeriveResult {
	var raw map[string]any
	_ = json.Unmarshal(rawEvent, &raw)

	switch eventName {
	case "SessionStart":
		return agent.DeriveResult{Valid: true, Status: agent.StatusIdle}

	case "UserPromptSubmit":
		return agent.DeriveResult{Valid: true, Status: agent.StatusRunning}

	case "Notification":
		// Mirror cc/status.go subtype mapping; codex hooks share the pdx
		// hook CLI schema, so notification_type values align.
		nt := strVal(raw, "notification_type")
		var status agent.Status
		switch nt {
		case "permission_prompt", "elicitation_dialog":
			status = agent.StatusWaiting
		case "idle_prompt", "auth_success":
			status = agent.StatusIdle
		default:
			return agent.DeriveResult{Valid: false}
		}
		return agent.DeriveResult{
			Valid:  true,
			Status: status,
			Detail: map[string]any{
				"notification_type": nt,
				"message":           raw["message"],
			},
		}

	case "PermissionRequest":
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusWaiting,
			Detail: map[string]any{
				"tool_name": raw["tool_name"],
			},
		}

	case "Stop":
		return agent.DeriveResult{Valid: true, Status: agent.StatusIdle}

	case "StopFailure":
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusError,
			Detail: map[string]any{
				"error_details": raw["error_details"],
				"error":         raw["error"],
			},
		}

	case "SessionEnd":
		return agent.DeriveResult{Valid: true, Status: agent.StatusClear}

	case "SubagentStart", "SubagentStop":
		// Detail-only event: Valid=true, Status="" (mirrors cc/opencode).
		return agent.DeriveResult{
			Valid:  true,
			Detail: map[string]any{"agent_id": raw["agent_id"]},
		}
	}

	return agent.DeriveResult{Valid: false}
}

func strVal(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
