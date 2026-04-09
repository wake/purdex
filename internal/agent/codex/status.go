package codex

import (
	"encoding/json"

	"github.com/wake/tmux-box/internal/agent"
)

func deriveCodexStatus(eventName string, rawEvent json.RawMessage) agent.DeriveResult {
	switch eventName {
	case "SessionStart":
		return agent.DeriveResult{Valid: true, Status: agent.StatusIdle}
	case "UserPromptSubmit":
		return agent.DeriveResult{Valid: true, Status: agent.StatusRunning}
	case "Stop":
		return agent.DeriveResult{Valid: true, Status: agent.StatusIdle}
	}
	return agent.DeriveResult{Valid: false}
}
