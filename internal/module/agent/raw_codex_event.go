package agent

import "encoding/json"

// maxCodexTurnIDLen caps the accepted turn_id length. The codex CLI emits
// short opaque identifiers (~30 bytes in practice); 512 bytes leaves ample
// headroom for any conceivable forward-compat ID format. Anything longer is
// rejected as malformed (parse-failure semantics) — without a cap, a hostile
// or malformed payload could write a multi-megabyte string into
// subagents_json + trace + WS broadcast (round-2 A4 DoS vector).
const maxCodexTurnIDLen = 512

// parseCodexTurnID extracts the codex turn_id field from a raw hook payload.
// It is fail-soft on every error path — JSON parse error, missing field,
// non-string type, empty-string value, and over-cap length all return "".
// This lets the applyFrameEvent caller dispatch via the spec §3.3.D
// parse-failure table (skip detach when a peer ref carries a non-empty
// SourceTurnID, fall back to wildcard detach when every peer ref is also
// empty) without re-checking each failure mode.
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
	if len(payload.TurnID) > maxCodexTurnIDLen {
		return ""
	}
	return payload.TurnID
}
