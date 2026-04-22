// Package arbmode implements the AGENT_ARB_MODE feature flag state machine.
// It tracks the current and pending arbitrator mode, respects env-lock
// semantics, and promotes pending → current only at SessionStart.
package arbmode

// ArbMode identifies which arbitration strategy is active.
type ArbMode string

const (
	// ModePassthrough means the Arbitrator forwards observations unchanged
	// without applying any override logic.
	ModePassthrough ArbMode = "passthrough"

	// ModeAuthoritative means the Arbitrator actively adjudicates observations
	// and may accept, reject, or merge proposals.
	ModeAuthoritative ArbMode = "authoritative"
)

// IsValid reports whether m is one of the two defined ArbMode values.
func (m ArbMode) IsValid() bool {
	switch m {
	case ModePassthrough, ModeAuthoritative:
		return true
	}
	return false
}
