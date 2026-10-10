package team

// ---- P6-2a: the member-relay wire (plan v3 "PR P6-2a"; spec §8.2, §8.3) ----

// RelayCreateRequest is POST /api/team/relays (spec §8.2 step 1): the lead asks for a member's relay.
type RelayCreateRequest struct {
	ID          string `json:"id"` // the op id, minted by the CLI (UUID v4), so a replay is the same op
	OriginInbox string `json:"origin_inbox"`
	Target      string `json:"target"` // the member's ref or address
}

// RelayClaimRequest is POST /api/relay/ops/{id}/claim: the member's mod takes the op.
type RelayClaimRequest struct {
	SessionID string `json:"session_id"`
}

// RelaySeenRequest is POST /api/relay/ops/{id}/seen: the member's mod saw the control message (it may be mid-turn and
// claim later). Only the target session may say so.
type RelaySeenRequest struct {
	SessionID string `json:"session_id"`
}

// RelayCompactedRequest is POST /api/relay/compacted (P7-2): a session's mod reports a compaction it did not intercept.
// Trigger is "auto" or "manual"; the daemon decides whether anyone is told.
type RelayCompactedRequest struct {
	SessionID string `json:"session_id"`
	Trigger   string `json:"trigger"`
}

// RelayCompactedResponse says whether the lead was told.
type RelayCompactedResponse struct {
	Noticed bool `json:"noticed"`
}

// RelayClaimResponse answers claim: the op and who leads the member's team.
type RelayClaimResponse struct {
	Op   RelayOp    `json:"op"`
	Lead *RelayLead `json:"lead,omitempty"`
}

// RelayLead is how a member reaches its lead.
type RelayLead struct {
	Address string `json:"address"`
	Ref     string `json:"ref"`
	TeamID  string `json:"team_id"`
}

// Member-relay error codes (409); not_your_op exits 13 in the CLI.
const (
	ErrRelayUnsupported = "relay_unsupported" // the member's mod is absent or older than MinMemberRelayModVersion
	ErrNotYourOp        = "not_your_op"       // claim from a session other than the op's target
)

// Member-relay constants.
const (
	MinMemberRelayModVersion = 2                         // the mod ↔ daemon protocol version that handles the control message
	RelayControlPrefix       = "[pdx-relay:control] op=" // the control message's text prefix, then the op id
	RelayClaimTimeoutS       = 60                        // unseen request → member_unresponsive (P6-4b)
	RelayStallTimeoutS       = 900                       // no progress after the claim; also: a seen op whose member has run one turn this long gets the lead's one notice (#2439)
	RelayBusyCapS            = 3600                      // a seen, unclaimed op waits at most this long after seen_at for a running turn to end (#2439)
	RelayIdleGraceS          = 120                       // a seen op whose member is idle (its turn ended) and still unclaimed this long → member_unresponsive (#2439)
)

// RelayCreateResponse is POST /api/team/relays' body: 201 for a new op, 200 for a replay of the same id.
type RelayCreateResponse struct {
	Op RelayOp `json:"op"`
}

// RelayBusyNoticeFmt is the lead's one notice when a seen member relay has waited RelayStallTimeoutS for a running turn:
// it takes the member's address and bare ref. The 15 and the 45 are RelayStallTimeoutS and RelayBusyCapS minus it.
const RelayBusyNoticeFmt = "[pdx team] member %s [%s] 這一輪已跑 15 分鐘，接力會在它的回合結束時進行（最多再等 45 分鐘）"

// MemberRelayPayload is Approval.Payload for KindMemberRelay: the text of the card that asks a person to approve a
// member's relay when the lead's pool is spent out. Nothing in it is trusted at approve.
type MemberRelayPayload struct {
	OpID            string   `json:"op_id"`
	TeamID          string   `json:"team_id"`
	LeadRef         string   `json:"lead_ref"`
	LeadTitle       string   `json:"lead_title,omitempty"`
	MemberSessionID string   `json:"member_session_id"`
	MemberRef       string   `json:"member_ref"`
	MemberTitle     string   `json:"member_title,omitempty"`
	UsedPercentage  *float64 `json:"used_percentage,omitempty"`
}

// ---- Member relay ask (spec 2026-10-10-member-relay-ask-spec-plan §3.5) ----

// Member-relay-ask constants. The hold window is the user's decision D4: five minutes from the request.
const (
	RelayAskHoldS          = 300 // an ask the lead does not accept within this many seconds is closed as expired
	MinMemberAskModVersion = 3   // the mod protocol that asks for itself; older mods keep the 70%-and-idle notice
)

// RelayAskNoticeFmt takes the member's address, its bare 6-character ref, its title, the integer percentage, the minutes
// left (rounded up) and the ref again.
const RelayAskNoticeFmt = "[pdx team] member %s [%s]「%s」已用 %d%%，申請接力。\n%d 分鐘內同意請執行：pdx relay _%s（不同意不用回覆，過期即作罷）"

// Relay ask states (relay_asks.state) and the reasons of a withdrawal.
const (
	RelayAskOpen      = "open"
	RelayAskAccepted  = "accepted"
	RelayAskExpired   = "expired"
	RelayAskWithdrawn = "withdrawn"

	RelayAskWithdrawCompacted  = "compacted"
	RelayAskWithdrawMemberLeft = "member_left"
)

// RelayAskRequest is POST /api/relay/ask: a member's mod asks its lead to relay it. RequestID is the mod's UUID, so a
// replay is the same ask.
type RelayAskRequest struct {
	RequestID string `json:"request_id"`
	SessionID string `json:"session_id"`
	UsedPct   int    `json:"used_pct"`
	Window    int    `json:"window"`
}

// RelayAskResponse answers an ask: the ask's id and state, when it expires (unix ms), and whether this was a replay.
type RelayAskResponse struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	ExpiresAt int64  `json:"expires_at"`
	Replay    bool   `json:"replay,omitempty"`
}
