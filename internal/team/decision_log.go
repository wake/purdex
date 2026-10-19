package team

// The unattended panel's log (name pool v2 spec §8, U16): the latest decisions of the team approvals, whoever decided, and the
// switch's own on/off events, merged newest first. It is `log` on UnattendedView; the old `approved` page stays for older Apps.

const (
	CapabilityDecisionLog = "team.decision_log.v1" // /api/info capabilities: UnattendedView.log

	LogTypeDecision = "decision" // UnattendedLogEntry.Type of a closed approval
	LogTypeToggle   = "toggle"   // and of a switch change

	// DecisionLogMax is how many entries `log` holds.
	DecisionLogMax = 30
)

// UnattendedLogEntry is one entry of `log`. A decision has kind, origin, decided_by (absent for a timeout: nobody decided) and
// state; a toggle has on and by. `at` is the approval's deadline for a timeout (the sweep that closes one can run long after a
// restart) and its decided_at for every other state; a toggle's is the time of the change. The address of a client is not shown.
type UnattendedLogEntry struct {
	At        int64   `json:"at"`
	Type      string  `json:"type"`
	Kind      Kind    `json:"kind,omitempty"`
	Origin    *Origin `json:"origin,omitempty"`
	DecidedBy *Client `json:"decided_by,omitempty"`
	State     State   `json:"state,omitempty"`
	On        *bool   `json:"on,omitempty"`
	By        *Client `json:"by,omitempty"`
}

// DecisionLogKind reports whether the log covers approvals of kind k: the team approvals (spec L11). The chat prompts
// (hook_ask, hook_permission) are left out: they would flood 30 rows.
func DecisionLogKind(k Kind) bool {
	switch k {
	case KindLead, KindAdopt, KindSelfRelay, KindMemberRelay, KindSpawnResources:
		return true
	}
	return false
}

// DecisionLogEntry is the log entry of a closed approval, and false for a row that is not a decision: an open one, one
// withdrawn by its requester (cancelled) or abandoned (lease ran out, origin gone), and the chat prompts. A lead or adopt request
// the daemon refused while the switch was on is stored cancelled with the reason ErrUnattendedRefused; it is the daemon's denial
// and is listed as one.
func DecisionLogEntry(a Approval) (UnattendedLogEntry, bool) {
	if !DecisionLogKind(a.Kind) {
		return UnattendedLogEntry{}, false
	}
	origin := a.Origin
	e := UnattendedLogEntry{Type: LogTypeDecision, Kind: a.Kind, Origin: &origin, State: a.State, At: a.DecidedAt}
	switch {
	case a.State == StateApproved || a.State == StateDenied:
	case a.State == StateTimeout:
		e.At = a.DeadlineAt
	case a.State == StateCancelled && a.CloseReason == ErrUnattendedRefused && UnattendedRefusable(a.Kind):
		e.State = StateDenied
		by := UnattendedClient()
		e.DecidedBy = &by
		return e, true
	default:
		return UnattendedLogEntry{}, false
	}
	if a.DecidedBy != nil {
		e.DecidedBy = &Client{Kind: a.DecidedBy.Kind, Label: a.DecidedBy.Label}
	}
	return e, true
}
