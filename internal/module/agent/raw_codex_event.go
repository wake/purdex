package agent

import "encoding/json"

// parseCodexTurnID extracts the codex turn_id field from a raw hook payload.
// It is fail-soft on every error path — JSON parse error, missing field,
// non-string type, and empty-string value all return "". This lets the
// applyFrameEvent caller dispatch via the spec §3.3.D parse-failure table
// (skip detach when a peer ref carries a non-empty SourceTurnID, fall back to
// wildcard detach when every peer ref is also empty) without re-checking each
// failure mode.
//
// AgentType pre-gating (req.AgentType == "codex") is the caller's
// responsibility; this helper only inspects the raw payload and never
// re-checks the provider.
//
// Spec ref: docs/specs/2026-05-01-lights-l2-proxy-detach-on-stop-spec.md §3.3.B
func parseCodexTurnID(raw json.RawMessage) string {
	var payload struct {
		TurnID string `json:"turn_id"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return payload.TurnID
}
