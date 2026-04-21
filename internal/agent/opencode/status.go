package opencode

import (
	"encoding/json"

	"github.com/wake/purdex/internal/agent"
)

func deriveOpenCodeStatus(eventName string, rawEvent json.RawMessage) agent.DeriveResult {
	var raw map[string]any
	_ = json.Unmarshal(rawEvent, &raw)

	switch eventName {
	case "SessionStart":
		return agent.DeriveResult{Valid: true, Status: agent.StatusIdle}
	case "UserPromptSubmit":
		return agent.DeriveResult{Valid: true, Status: agent.StatusRunning, Model: strVal(raw, "modelName")}
	case "SubagentStart", "SubagentStop":
		return agent.DeriveResult{Valid: true, Detail: detailSubset(raw, "agent_id", "agent_type", "description", "prompt", "title", "output")}
	case "PermissionRequest":
		return agent.DeriveResult{Valid: true, Status: agent.StatusWaiting, Detail: detailSubset(raw, "request_type", "permission", "patterns", "questions")}
	case "Stop":
		return agent.DeriveResult{Valid: true, Status: agent.StatusIdle}
	case "StopFailure":
		return agent.DeriveResult{Valid: true, Status: agent.StatusError, Detail: detailSubset(raw, "error", "error_details")}
	case "SessionEnd":
		return agent.DeriveResult{Valid: true, Status: agent.StatusClear}
	default:
		return agent.DeriveResult{Valid: false}
	}
}

func detailSubset(raw map[string]any, keys ...string) map[string]any {
	out := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			out[key] = value
		}
	}
	return out
}

func strVal(raw map[string]any, key string) string {
	if value, ok := raw[key].(string); ok {
		return value
	}
	return ""
}
