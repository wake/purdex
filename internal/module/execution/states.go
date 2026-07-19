package execution

// Status is the Purdex runtime SOT execution status (spec §5.1). It is the
// authority for liveness: a "live execution" is one whose status is accepted or
// running. Ploom only projects this state machine; it never advances it.
type Status string

const (
	StatusAccepted  Status = "accepted"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

// IsTerminal reports whether s is a terminal status (completed or failed) from
// which no further transition is legal.
func (s Status) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed
}

// ValidStatus reports whether s is one of the four defined statuses.
func ValidStatus(s Status) bool {
	switch s {
	case StatusAccepted, StatusRunning, StatusCompleted, StatusFailed:
		return true
	default:
		return false
	}
}

// validTransitions enumerates the legal forward edges of the state machine
// (spec §5.1):
//
//	accepted ──> running ──> completed
//	   │            │
//	   └──> failed <┘
//
// Terminal statuses (completed, failed) have no outgoing edges, so recovery and
// reporting can never revive a finished execution.
var validTransitions = map[Status]map[Status]bool{
	StatusAccepted: {StatusRunning: true, StatusFailed: true},
	StatusRunning:  {StatusCompleted: true, StatusFailed: true},
}

// CanTransition reports whether advancing from → to is a legal state-machine
// edge. Self-transitions, backward moves, skips (accepted→completed), moves out
// of a terminal state, and unknown statuses all return false.
func CanTransition(from, to Status) bool {
	if !ValidStatus(from) || !ValidStatus(to) {
		return false
	}
	return validTransitions[from][to]
}

// LaunchState is the launch fence (spec §4.3). It is NOT a liveness signal — it
// only decides whether recovery should relaunch. Liveness is read from Status.
type LaunchState string

const (
	LaunchNone      LaunchState = "none"
	LaunchLaunching LaunchState = "launching"
	LaunchLaunched  LaunchState = "launched"
)

// ValidLaunchState reports whether ls is one of the three defined launch states.
func ValidLaunchState(ls LaunchState) bool {
	switch ls {
	case LaunchNone, LaunchLaunching, LaunchLaunched:
		return true
	default:
		return false
	}
}

// OutcomeSource records how a terminal outcome was classified (spec §5.3). It is
// nullable in the row until the execution reaches a terminal status.
type OutcomeSource string

const (
	// OutcomeResult: outcome classified from a parsed `result` protocol event.
	OutcomeResult OutcomeSource = "result"
	// OutcomeExitOnly: degraded classification from process exit only (no
	// `result` event) — recorded for audit (spec §5.3 known limitation).
	OutcomeExitOnly OutcomeSource = "exit_only"
)
