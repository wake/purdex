package team

import "encoding/json"

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
	// RelayForwarded is a lead's relay of a member that lives on another host: the op is recorded here and the `relay`
	// command is on its way (member relay spec D10). Not terminal; it ends only by a compare-and-set from this state.
	RelayForwarded RelayState = "forwarded"
	RelayClaimed   RelayState = "claimed"
	RelayWriting   RelayState = "writing"
	RelayWritten   RelayState = "written"
	RelayCleared   RelayState = "cleared"
	RelayDone      RelayState = "done"
	RelayFailed    RelayState = "failed"
	RelayCancelled RelayState = "cancelled"
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
	RelayReasonHalf              = "half"                // cancelled: a half (or handoff) op whose file was written - its end, not a failure (RT-3a1, S-9)
	RelayBeginReasonIdle         = "idle"                // begin's reason for an idle relay ask (the threshold ask has none)
	RelayReasonResumed           = "resumed"             // cancelled: a prompt arrived while an idle ask was open (idle relay D6); only an idle op takes it
	RelayReasonAbandoned         = "abandoned"           // cancelled
	RelayReasonPausedNoQuota     = "paused_no_quota"     // cancelled: the held card's session was paused when the unattended switch went on (UP-2b)
	RelayReasonModReloaded       = "mod_reloaded"        // failed: the mod reloaded (pdx setup) while the write was under way (#2441)
)

// Relay error codes (APIError.Error on /api/relay/*); 409 unless noted.
const (
	ErrMemberRelayIsLeads = "member_relay_is_leads" // a member's relay is the lead's (U9)
	ErrSelfRelayOff       = "self_relay_off"        // the host switch is off
	ErrSelfRelayPaused    = "self_relay_paused"     // the session is paused
	ErrReuseInvalid       = "reuse_invalid"         // begin's reuse_op is not this session's fresh half (RT-3a1)
	ErrRelayOpen          = "relay_open"            // an op is already open for this session; carries Op
	ErrUnknownSession     = "unknown_session"       // 404: session_id is not a live CC session on this host
	ErrBadTransition      = "bad_transition"        // report: the op's state does not lead to this one; carries Op
)

// Relay limits (spec §8.1, §8.7).
const (
	RelayThresholdPct     = 70    // U1: used ≥ 70 % triggers the ask
	RelayIdleMinutes      = 40    // idle relay D1: a session idle this long is told to / asks for a relay
	RelayIdleThresholdPct = 40    // idle relay D1: … when its context use is at least this much
	RelayMinGrowth        = 20000 // §8.1 loop guard: tokens a seeded conversation must grow before asking again
	SelfRelayDeadlineS    = 600   // §8.7: 10 minutes, absolute
	// SelfRelayIdleDeadlineS is an idle card's life while the host switch is off (RT-3a1 T29): five minutes, then it times out.
	SelfRelayIdleDeadlineS = 300
	RelayDir               = "relay" // <data_dir>/relay/<op id>.md
)

// RelayOp is one relay operation, self or member (spec §8.1).
type RelayOp struct {
	ID           string     `json:"id"`
	Kind         RelayKind  `json:"kind"`
	HostID       string     `json:"host_id"`
	SessionID    string     `json:"session_id"`               // the session being relayed (old id)
	NewSessionID string     `json:"new_session_id,omitempty"` // after cleared
	Ref          string     `json:"ref"`                      // old ref
	NewRef       string     `json:"new_ref,omitempty"`
	TeamID       string     `json:"team_id,omitempty"`    // member relays (P6)
	RequestID    string     `json:"request_id,omitempty"` // the self_relay or member_relay approval row (a member op created requested has none)
	State        RelayState `json:"state"`
	Reason       string     `json:"reason,omitempty"`
	HandoffPath  string     `json:"handoff_path"`         // <data_dir>/relay/<op id>.md
	PID          int        `json:"pid,omitempty"`        // the process the op is bound to (P6-2a); 0 on an op from before it
	PaneID       string     `json:"pane_id,omitempty"`    // the tmux pane "%N" it runs in, "" when none
	SeenAt       int64      `json:"seen_at,omitempty"`    // when the member's mod first saw the control message (P6-2b-2); 0 = not yet
	ProcStart    string     `json:"proc_start,omitempty"` // the process's start time as the registry shows it; pid + proc_start is the identity (a pid is reused)
	// Mode is how far the op goes (RT-3a1): "" a full relay, RelayModeHalf the handoff file only (an idle session's half, S-9), RelayModeHandoff
	// the file and then the member's kill (RT-3c).
	Mode           string   `json:"mode,omitempty"`
	Pruned         bool     `json:"pruned,omitempty"`
	UsedPercentage *float64 `json:"used_percentage,omitempty"`
	CreatedAt      int64    `json:"created_at"`
	UpdatedAt      int64    `json:"updated_at"`
}

// SelfRelayPayload is Approval.Payload for KindSelfRelay (dialog body, spec §8.7).
type SelfRelayPayload struct {
	OpID           string  `json:"op_id"`
	UsedPercentage float64 `json:"used_percentage"`
	Window         int     `json:"window"`
	ModelID        string  `json:"model_id,omitempty"`
	Effort         string  `json:"effort,omitempty"`
	// Manual: a person typed /relay (MR-1, U-M1). It is the row's, never a caller's: every later gate reads it from here.
	Manual bool `json:"manual,omitempty"`
	// Reason / IdleMinutes: the ask is an idle relay (idle relay spec §2). The row's, copied from begin.
	Reason      string `json:"reason,omitempty"`
	IdleMinutes int    `json:"idle_minutes,omitempty"`
	// Reask: the request was made with a valid release token (UP-3b): a card for a person, never paused and never approved by the
	// daemon, whatever the switch and the quota say. The row's, set by the daemon when it consumed the token; a caller cannot send it.
	Reask bool `json:"reask,omitempty"`
	// ReaskID is the (spent) release token the card was opened with: a replay of the begin must carry the same one.
	ReaskID string `json:"reask_id,omitempty"`
	// Person: the begin was a person's own /relay (RT-1a), approved at begin by the person. The row's, set by the daemon when it
	// consumed the token; a caller cannot send it.
	Person bool `json:"person,omitempty"`
	// Half / ReuseOp: what the caller asked for (RT-3a1), kept so a replay of the begin can be told from another request.
	Half    bool   `json:"half,omitempty"`
	ReuseOp string `json:"reuse_op,omitempty"`
	// HalfOnTimeout: an idle card whose session's mod (protocol >= MinHalfModVersion) writes a half when the card times out. The row's, set
	// by the daemon at begin; the push says "a handoff file in 5 minutes" only for such a card (RT-3a2).
	HalfOnTimeout bool `json:"half_on_timeout,omitempty"`
}

// RelayLastAskPct is the context use at which a self-relay ask is the LAST one (relay/team rules v2, §4.2): from here an ask that
// nobody answers pauses the session instead of timing out, whatever PDX_RELAY_THRESHOLD says.
const RelayLastAskPct = 90

// UnansweredDeadlineMs is the deadline_at of an unanswered final card: 9999-12-31, far enough to mean "none".
const UnansweredDeadlineMs int64 = 253402300799000

// RelayWaiting is the final card a reloaded mod must pick up again (RelayHelloResponse.Waiting): the session's open op
// awaiting approval whose card is unanswered.
type RelayWaiting struct {
	RequestID    string  `json:"request_id"`
	Op           RelayOp `json:"op"`
	UnansweredAt int64   `json:"unanswered_at"`
}

// RelayFreshHalf is a session's fresh half: the handoff file an idle half wrote, and when.
type RelayFreshHalf struct {
	OpID      string `json:"op_id"`
	Path      string `json:"path"`
	WrittenAt int64  `json:"written_at"`
}

// RelayFreshRequest is POST /api/relay/fresh: the mod reports the half's file stale (S-10). Only stale:true is defined.
type RelayFreshRequest struct {
	SessionID string `json:"session_id"`
	OpID      string `json:"op_id"`
	Stale     bool   `json:"stale"`
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
	// IdleMinutes / IdleThreshold are the idle relay's numbers (RelayIdleMinutes, RelayIdleThresholdPct). Absent or 0
	// from a daemon older than the feature: the mod then keeps it off whatever its env says (idle relay spec D9).
	IdleMinutes   int `json:"idle_minutes,omitempty"`
	IdleThreshold int `json:"idle_threshold,omitempty"`
	// ActiveRelay is the relay the session was in the middle of when its mod (re)loaded (#2441): its open op in
	// claimed / writing / written, and the lead of a member op. A mod that holds no state of its own for that op
	// picks it up (written: /clear and seed; claimed / writing: failed, mod_reloaded). Absent for every other
	// state, and from a daemon older than this field (the mod then behaves as before).
	ActiveRelay *RelayClaimResponse `json:"active_relay,omitempty"`
	// FreshHalf is this session's fresh half (RT-3a1 C#18): an idle half whose handoff file is still current. A reloaded mod can reuse it.
	FreshHalf *RelayFreshHalf `json:"fresh_half,omitempty"`
	// Waiting is this session's unanswered final card (RT-2a1): a mod that reloaded and holds no request adopts it again.
	// Absent for an ordinary open card and from a daemon older than this field.
	Waiting *RelayWaiting `json:"waiting,omitempty"`
	// IdleIntercept is the level of Claude Code idle compactions this daemon decides for (RT-4): 1 = members. Absent from an older daemon.
	IdleIntercept int `json:"idle_intercept,omitempty"`
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
	// Manual says the begin is a person's /relay (member relay MR-1, U-M1): a member's refusals are lifted for it. It
	// grants nothing by itself: the request still opens a card a person must approve.
	Manual bool `json:"manual,omitempty"`
	// Reason is "" (the threshold ask) or RelayBeginReasonIdle; IdleMinutes is 1–59 iff the reason is idle, else 0.
	Reason      string `json:"reason,omitempty"`
	IdleMinutes int    `json:"idle_minutes,omitempty"`
	// ReaskID is the release token of a re-ask after a pause (`pdx relay begin --reask <uuid>`, UP-3b): a UUID the daemon wrote when it
	// resumed the session. Valid (this session's, unused, under 24 h) it is consumed and the begin opens a card for a person; unknown,
	// used or another session's it is ignored and the ordinary rules apply.
	ReaskID string `json:"reask_id,omitempty"`
	// ReuseOp (RT-3a1 T31) names a fresh half's op whose handoff file this begin reuses instead of writing a new one. Valid: the session's own
	// cancelled / half op, still the session's fresh half, its file there and not empty. Else 409 reuse_invalid and nothing is written.
	ReuseOp string `json:"reuse_op,omitempty"`
	// Half (with reason idle, RT-3a1) asks for the idle half: the daemon claims the op at once (mode half, approved by its own client, no
	// quota, no card) and the mod writes the handoff file only - no /clear, no seed. Without reason idle it is 400.
	Half bool `json:"half,omitempty"`
	// PersonToken (with Manual) says the begin is the person's own /relay: the one-use token the daemon minted for the mod
	// (POST /api/relay/person). Valid, it makes the relay direct: no card, no quota, whatever the switches. Invalid → 409
	// person_token_invalid and nothing is written. Without it a manual begin is a card, as before (an older mod).
	PersonToken string `json:"person_token,omitempty"`
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

// Op modes (RelayOp.Mode) and the first mod protocol that speaks the half path (RT-3a1).
const (
	RelayModeHalf    = "half"
	RelayModeHandoff = "handoff"
	// MinHalfModVersion is the mod protocol that runs the idle half itself; an older mod keeps today's auto-approve, never a half.
	MinHalfModVersion = 8
	// MinHandoffModVersion is the mod protocol that writes a handoff op's file and goes on (pdx kill --handoff, RT-3c).
	MinHandoffModVersion = 8
)

// A member's idle compaction that its mod intercepted (RT-4 T42): the first mod protocol that does it (the daemon then drops the two
// older idle notices for that member), and the one notice the lead gets, once per idle period. Args: address, title, the context ("55%" or "未知"), ref ×3.
const (
	// InterceptRouteMember / InterceptRouteSilent: the daemon's answers to an intercept report (RelayCompactedResponse.Route).
	InterceptRouteMember = "member"
	InterceptRouteSilent = "silent"
	// IdleInterceptMembers is the hello answer's idle_intercept level for a daemon that decides intercepts for members only; a mod skips an
	// idle compaction only when the level covers its session (absent or 0 = a daemon that does not decide: the compaction happens as before).
	IdleInterceptMembers = 1

	MinIdleInterceptModVersion = 9
	IdleInterceptNoticeFmt     = "[pdx team] member %s「%s」閒置，快取快過期，context %s。請決定：接力（pdx relay _%s）、交接後關閉（pdx kill _%s --handoff）、直接關閉（pdx kill _%s）。"
)

// The lead's texts about a handoff kill (RT-3c T38), word for word.
const (
	// HandoffClosedFmt: %s the member's address, %s its title, %s the handoff file.
	HandoffClosedFmt = "[pdx team] member %s「%s」已交接：%s，已關閉。內容請由你決定要不要寫進記憶與文件。"
	// HandoffNotClosedFmt: the file is written but the member was not closed (the kill did not complete, or the daemon stopped between).
	HandoffNotClosedFmt = "[pdx team] member %s「%s」的交接檔已寫好（%s），但關閉沒完成，請再下一次 pdx kill。"
)

// ---- The fresh handoffs of a host (RT-3a2 T32, S-21) ----

// RelayFreshRoute is GET /api/relay/fresh: the sessions that hold a fresh half now.
const RelayFreshRoute = "/api/relay/fresh"

// RelayFreshEventType is the HostEvent.Type of the same list: {op:"snapshot"} to each new subscriber, {op:"changed"} after each change.
const RelayFreshEventType = "relay.fresh"

// RelayFreshSession is one session's fresh half: the handoff file an idle half wrote for it.
type RelayFreshSession struct {
	SessionID string `json:"session_id"`
	Ref       string `json:"ref"`
	OpID      string `json:"op_id"`
	Path      string `json:"path"`
	WrittenAt int64  `json:"written_at"`
}

// RelayFreshView is the answer of GET /api/relay/fresh, oldest first.
type RelayFreshView struct {
	Sessions []RelayFreshSession `json:"sessions"` // never null (MarshalJSON)
}

// MarshalJSON writes no sessions as sessions:[], not null.
func (v RelayFreshView) MarshalJSON() ([]byte, error) {
	type plain RelayFreshView
	if v.Sessions == nil {
		v.Sessions = []RelayFreshSession{}
	}
	return json.Marshal(plain(v))
}

// RelayFreshEventValue is HostEvent.Value (JSON string) for RelayFreshEventType.
type RelayFreshEventValue struct {
	Op       string              `json:"op"` // "snapshot" | "changed"
	Sessions []RelayFreshSession `json:"sessions"`
}

// MarshalJSON writes no sessions as sessions:[], not null.
func (v RelayFreshEventValue) MarshalJSON() ([]byte, error) {
	type plain RelayFreshEventValue
	if v.Sessions == nil {
		v.Sessions = []RelayFreshSession{}
	}
	return json.Marshal(plain(v))
}
