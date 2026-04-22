package observation

// EvidenceKeyRoleResolution is the canonical EvidenceRef.Key under which
// observation producers publish the role-resolution state. The apply pipeline
// routes pending-production and promote decisions off this typed signal,
// replacing the earlier verify_reason string-matching contract (P1-8 / D4.1).
const EvidenceKeyRoleResolution = "role_resolution"

// RoleResolutionState is the typed contract for "can the actor role be
// determined for this observation?". It replaces implicit verify_reason
// regex-scanning at the apply layer.
//
// Producers (hook / probe / sweep builders, added in T4) MUST attach an
// EvidenceRef with Key == EvidenceKeyRoleResolution and Value set to one of
// the three canonical values below. The apply pipeline (T5) reads it via
// RoleResolutionFromEvidence.
type RoleResolutionState string

const (
	// RoleResolved indicates the actor role is positively determined for this
	// observation. Apply pipeline may proceed toward promotion.
	RoleResolved RoleResolutionState = "RoleResolved"

	// RoleRetryableUnresolved indicates the role could not be determined but
	// a later observation may succeed (e.g. pane_unresolvable,
	// sender_uncertain, identify_mismatch). Apply pipeline routes the
	// observation to pending and waits for a follow-up.
	RoleRetryableUnresolved RoleResolutionState = "RoleRetryableUnresolved"

	// RoleTerminalUnresolved indicates the role cannot be determined and
	// retry is pointless (e.g. session_not_found, sweep reports frame
	// identity missing). Apply pipeline drops the observation.
	RoleTerminalUnresolved RoleResolutionState = "RoleTerminalUnresolved"
)

// isValidRoleResolutionState reports whether s is one of the three canonical
// enum values. Used as the internal allow-list for RoleResolutionFromEvidence.
func isValidRoleResolutionState(s RoleResolutionState) bool {
	switch s {
	case RoleResolved, RoleRetryableUnresolved, RoleTerminalUnresolved:
		return true
	}
	return false
}

// RoleResolutionFromEvidence scans ev for the first EvidenceRef whose Key
// matches EvidenceKeyRoleResolution and returns its parsed enum value plus a
// boolean ok flag.
//
// Behavior:
//   - Key absent → ("", false).
//   - Key present, Value is a string equal to one of the canonical enum
//     values → (enum, true).
//   - Key present, Value is already a RoleResolutionState whose underlying
//     string is one of the canonical values → (enum, true).
//   - Key present but Value is the wrong type (int, bool, nil, struct, bytes,
//     …) or an unrecognized string → ("", false).
//   - Multiple matching entries → the first one wins; subsequent duplicates
//     are ignored (producers should not emit duplicates, but apply-layer
//     behavior must be deterministic).
func RoleResolutionFromEvidence(ev []EvidenceRef) (RoleResolutionState, bool) {
	for _, ref := range ev {
		if ref.Key != EvidenceKeyRoleResolution {
			continue
		}

		// First matching key wins. Type-switch on the Value; reject anything
		// that is not a canonical enum value.
		switch v := ref.Value.(type) {
		case RoleResolutionState:
			if isValidRoleResolutionState(v) {
				return v, true
			}
			return "", false
		case string:
			s := RoleResolutionState(v)
			if isValidRoleResolutionState(s) {
				return s, true
			}
			return "", false
		default:
			return "", false
		}
	}
	return "", false
}
