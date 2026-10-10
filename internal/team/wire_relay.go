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
	// #2439: why a member never answered. Unseen: its mod did not acknowledge the control message in RelayClaimTimeoutS
	// (terminal stuck on a dialog, Claude Code not running); Blocked: the same, or a turn that never ended, while the agent
	// says it waits on a prompt; BusyTimeout: seen, but the turn ran past RelayBusyCapS. All three exit 14 in the CLI.
	RelayReasonMemberUnseen      = "member_unseen"       // failed
	RelayReasonMemberBusyTimeout = "member_busy_timeout" // failed
	RelayReasonMemberBlocked     = "member_blocked"      // failed
	RelayReasonDaemonUnavailable = "daemon_unavailable"  // failed
	RelayReasonDenied            = "denied"              // cancelled
	RelayReasonTimeout           = "timeout"             // cancelled
	RelayReasonCompacted         = "compacted"           // cancelled
	RelayReasonAbandoned         = "abandoned"           // cancelled
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
	RequestID      string     `json:"request_id,omitempty"` // the self_relay or member_relay approval row (a member op created requested has none)
	State          RelayState `json:"state"`
	Reason         string     `json:"reason,omitempty"`
	HandoffPath    string     `json:"handoff_path"`         // <data_dir>/relay/<op id>.md
	PID            int        `json:"pid,omitempty"`        // the process the op is bound to (P6-2a); 0 on an op from before it
	PaneID         string     `json:"pane_id,omitempty"`    // the tmux pane "%N" it runs in, "" when none
	SeenAt         int64      `json:"seen_at,omitempty"`    // when the member's mod first saw the control message (P6-2b-2); 0 = not yet
	ProcStart      string     `json:"proc_start,omitempty"` // the process's start time as the registry shows it; pid + proc_start is the identity (a pid is reused)
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
	// RequestID is the approval row's id, minted by the CLI (UUID v4) so
	// that a begin replayed after a lost response is the SAME request: the
	// daemon answers an existing id with the op it opened, whatever its
	// state, instead of opening a second one. Optional: the daemon mints
	// one when it is empty (PR #1726 attacker A-1).
	RequestID string `json:"request_id,omitempty"`
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
	Member     bool   `json:"member"`      // an active member of a live team: it has no switch (U13)
}

// RelayReportRequest is POST /api/relay/ops/{id}/report.
type RelayReportRequest struct {
	State        RelayState `json:"state"`
	NewSessionID string     `json:"new_session_id,omitempty"` // cleared
	Error        string     `json:"error,omitempty"`          // failed: the reason
}

// ApprovalFeedKey is the service-registry key under which the team module publishes its ApprovalFeed (the
// conversation module reads it; team does not import conversation).
const ApprovalFeedKey = "team.approval-feed"

// ApprovalFeed is what a per-conversation stream needs from the team module (spec §8.2, U1-6d).
type ApprovalFeed interface {
	// SubscribeSession returns the open approvals whose Origin.SessionID is sessionID and arms fn for every later
	// opened / closed op of that session, atomically with respect to the module's broadcasts. fn runs under the
	// module's event lock: it must only enqueue (never block, never call back into the module, cancel included).
	// Filtering is by session id only; nothing is forwarded across a relay lineage.
	SubscribeSession(sessionID string, fn func(op string, a Approval)) (open []Approval, cancel func(), err error)
	// HoldResponder counts one more remote responder until release is called: terminal-only rows are created while
	// any is held, like with a /ws/host-events subscriber. Both cancel and release are idempotent.
	HoldResponder() (release func())
}

// ApprovalEventsKey is the service-registry key under which the team module publishes its ApprovalEvents (the push
// module reads it; team does not import push).
const ApprovalEventsKey = "team.approval-events"

// ApprovalEvents is the host-wide approval stream for a consumer that must not slow the approval paths (push spec §5.1).
type ApprovalEvents interface {
	// SubscribeApprovals returns every open approval and arms fn for every later opened / closed op, in one step under
	// the module's event lock (no op falls between the list and the first delivery). fn does NOT run under that lock:
	// each subscriber has its own bounded queue and goroutine, the module's publish is a non-blocking send, and an op
	// that does not fit is dropped and counted. fn may block, and may call unsubscribe. Ops reach fn in order.
	// unsubscribe stops delivery: nothing still queued is delivered after it. It does not wait for fn (fn may be the
	// caller), so ONE callback that was already taken off the queue when unsubscribe was called may still start or finish
	// afterwards; a consumer must tolerate that single late call.
	SubscribeApprovals(fn func(op string, a Approval)) (open []Approval, unsubscribe func())
}

// OpenApprovalsReader is a fresh read of the open approval set, for a consumer that needs the truth now rather than a
// copy it kept from the stream (push spec §6: the badge count). The team module also implements it, on the same object
// it registers under ApprovalEventsKey. The read may fail; a caller treats that as "unknown", never as an empty set.
type OpenApprovalsReader interface {
	OpenApprovals() ([]Approval, error)
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
