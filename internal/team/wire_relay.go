package team

// ---- P5a: relay ops, lineage, switches, pause (spec §8.1, §8.3, §8.4, §8.7) ----

// RelayKind is who started a relay op (spec §8.1).
type RelayKind string

const (
	RelayKindSelf   RelayKind = "self"   // the session's own mod, after the user's approval (U13)
	RelayKindMember RelayKind = "member" // the lead, with pdx relay <ref> (P6)
)

// RelayState is a relay op's state (spec §8.1). A self op starts in
// awaiting_approval; a member op in requested.
type RelayState string

const (
	RelayAwaitingApproval RelayState = "awaiting_approval"
	RelayRequested        RelayState = "requested"
	RelayClaimed          RelayState = "claimed"
	RelayWriting          RelayState = "writing"
	RelayWritten          RelayState = "written"
	RelayCleared          RelayState = "cleared"
	RelayDone             RelayState = "done"
	RelayFailed           RelayState = "failed"
	RelayCancelled        RelayState = "cancelled"
)

// Terminal reports whether s is a final state (done, failed, cancelled).
func (s RelayState) Terminal() bool {
	return s == RelayDone || s == RelayFailed || s == RelayCancelled
}

// Relay reasons: RelayOp.Reason for failed and cancelled.
const (
	RelayReasonHandoffIncomplete  = "handoff_incomplete"  // failed
	RelayReasonMemberUnresponsive = "member_unresponsive" // failed (P6)
	RelayReasonMemberGone         = "member_gone"         // failed (P6)
	RelayReasonDaemonUnavailable  = "daemon_unavailable"  // failed
	RelayReasonDenied             = "denied"              // cancelled
	RelayReasonTimeout            = "timeout"             // cancelled
	RelayReasonCompacted          = "compacted"           // cancelled
	RelayReasonAbandoned          = "abandoned"           // cancelled
)

// Relay error codes (APIError.Error on /api/relay/*); 409 unless noted.
const (
	ErrMemberRelayIsLeads = "member_relay_is_leads" // a member's relay is the lead's (U9)
	ErrSelfRelayOff       = "self_relay_off"        // the host switch is off
	ErrSelfRelayPaused    = "self_relay_paused"     // the session is paused
	ErrRelayOpen          = "relay_open"            // an op is already open for this session; carries Op
	ErrUnknownSession     = "unknown_session"       // 404: session_id is not a live CC session on this host
	ErrBadTransition      = "bad_transition"        // report: the op's state does not lead to this one; carries Op
)

// Relay limits (spec §8.1, §8.7).
const (
	RelayThresholdPct  = 70      // U1: used ≥ 70 % triggers the ask
	RelayMinGrowth     = 20000   // §8.1 loop guard: tokens a seeded conversation must grow before asking again
	SelfRelayDeadlineS = 600     // §8.7: 10 minutes, absolute
	RelayDir           = "relay" // <data_dir>/relay/<op id>.md
)

// RelayOp is one relay operation, self or member (spec §8.1).
type RelayOp struct {
	ID             string     `json:"id"`
	Kind           RelayKind  `json:"kind"`
	HostID         string     `json:"host_id"`
	SessionID      string     `json:"session_id"`               // the session being relayed (old id)
	NewSessionID   string     `json:"new_session_id,omitempty"` // after cleared
	Ref            string     `json:"ref"`                      // old ref
	NewRef         string     `json:"new_ref,omitempty"`
	TeamID         string     `json:"team_id,omitempty"`    // member relays (P6)
	RequestID      string     `json:"request_id,omitempty"` // the self_relay approval row
	State          RelayState `json:"state"`
	Reason         string     `json:"reason,omitempty"`
	HandoffPath    string     `json:"handoff_path"` // <data_dir>/relay/<op id>.md
	Pruned         bool       `json:"pruned,omitempty"`
	UsedPercentage *float64   `json:"used_percentage,omitempty"`
	CreatedAt      int64      `json:"created_at"`
	UpdatedAt      int64      `json:"updated_at"`
}

// SelfRelayPayload is Approval.Payload for KindSelfRelay (dialog body, spec §8.7).
type SelfRelayPayload struct {
	OpID           string  `json:"op_id"`
	UsedPercentage float64 `json:"used_percentage"`
	Window         int     `json:"window"`
	ModelID        string  `json:"model_id,omitempty"`
	Effort         string  `json:"effort,omitempty"`
}

// RelayHelloRequest is POST /api/relay/hello.
type RelayHelloRequest struct {
	SessionID  string `json:"session_id"`
	ModVersion string `json:"mod_version,omitempty"` // the mod ↔ daemon PROTOCOL version as a decimal string ("1"; P5b-1's VERSION), not a pdx release; P6 compares it for relay_unsupported
	Agent      string `json:"agent,omitempty"`       // "cc"
}

// RelayHelloResponse answers hello: the session's role and effective self-relay state.
type RelayHelloResponse struct {
	OK        bool   `json:"ok"`
	Role      string `json:"role"`       // "none" | "lead" | "member"
	SelfRelay string `json:"self_relay"` // "on" | "off" | "paused"
	Threshold int    `json:"threshold"`  // RelayThresholdPct
	MinGrowth int    `json:"min_growth"` // RelayMinGrowth
}

// RelayBeginRequest is POST /api/relay/begin. It carries no model or
// effort: the daemon fills SelfRelayPayload.ModelID / Effort itself from
// the session's last statusline reading (agent.ContextUsageReader, P1 —
// the only place Claude Code reports them, M21); the mod has no effort
// accessor (MP8) and need not pass what the daemon already knows.
type RelayBeginRequest struct {
	SessionID      string  `json:"session_id"`
	Self           bool    `json:"self"`
	UsedPercentage float64 `json:"used_percentage"`
	Window         int     `json:"window"`
}

// RelayBeginResponse is begin's 201 body.
type RelayBeginResponse struct {
	Op        RelayOp `json:"op"`
	RequestID string  `json:"request_id"`
}

// RelaySelfRequest is POST /api/relay/self: the per-session pause.
type RelaySelfRequest struct {
	SessionID string `json:"session_id"`
	Action    string `json:"action"` // "off" | "on" | "status"
}

// RelaySelfResponse answers self: the effective state and what makes it so.
type RelaySelfResponse struct {
	SelfRelay  string `json:"self_relay"`  // "on" | "off" | "paused"
	HostSwitch bool   `json:"host_switch"` // the host switch that applies to this session's role
	Member     bool   `json:"member"`      // a member has no switch (U13); false until P4
}

// RelayReportRequest is POST /api/relay/ops/{id}/report.
type RelayReportRequest struct {
	State        RelayState `json:"state"`
	NewSessionID string     `json:"new_session_id,omitempty"` // cleared
	Error        string     `json:"error,omitempty"`          // failed: the reason
}

// LineageReaderKey is the service-registry key under which the team module
// publishes its LineageReader; the peers module reads it at request time
// (team depends on peers, so peers cannot import the team module).
const LineageReaderKey = "team.lineage"

// LineageReader answers, for every session id that heads a relay chain, the
// refs it took over from — newest first, the whole chain, uncapped (§8.4).
type LineageReader interface {
	PreviousRefs() (map[string][]string, error)
}
