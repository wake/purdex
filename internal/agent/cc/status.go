package cc

import (
	"encoding/json"

	"github.com/wake/purdex/internal/agent"
)

func deriveCCStatus(purdexName string, rawEvent json.RawMessage) agent.DeriveResult {
	var raw map[string]any
	_ = json.Unmarshal(rawEvent, &raw)

	switch purdexName {
	case "PdxSessionStart":
		if raw["source"] == "compact" {
			return agent.DeriveResult{Valid: false, Reason: "compact_ignored"}
		}
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusIdle,
			Model:  strVal(raw, "modelName"),
		}

	case "PdxUserPromptSubmit":
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusRunning,
		}

	case "PdxPostToolUse":
		// W6-1a: cc fires PostToolUse after a tool completes — including
		// after the user grants a paused permission prompt. Mapping to
		// running unblocks the waiting → running transition that has no
		// other hook between Notification(permission_prompt)/PermissionRequest
		// and the eventual Stop.
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusRunning,
			Detail: map[string]any{
				"tool_name": raw["tool_name"],
			},
		}

	case "PdxNotification":
		nt := strVal(raw, "notification_type")
		var status agent.Status
		switch nt {
		case "permission_prompt", "elicitation_dialog":
			status = agent.StatusWaiting
		case "idle_prompt", "auth_success":
			status = agent.StatusIdle
		default:
			return agent.DeriveResult{Valid: false, Reason: "notification_unknown_type"}
		}
		return agent.DeriveResult{
			Valid:  true,
			Status: status,
			Detail: map[string]any{
				"notification_type": nt,
				"message":           raw["message"],
			},
		}

	case "PdxPermissionRequest":
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusWaiting,
			Detail: map[string]any{
				"tool_name": raw["tool_name"],
			},
		}

	case "PdxStop":
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusIdle,
			Model:  strVal(raw, "modelName"),
			Detail: map[string]any{
				"last_assistant_message": raw["last_assistant_message"],
			},
		}

	case "PdxStopFailure":
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusError,
			Detail: map[string]any{
				"error_details": raw["error_details"],
				"error":         raw["error"],
			},
		}

	case "PdxSessionEnd":
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusClear,
		}

	case "PdxSubagentStart", "PdxSubagentStop":
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
