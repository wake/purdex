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
	// Intercepted: the member's mod answered {skip} to an idle compaction, so nothing was compacted (RT-4 T42). The daemon tells the lead
	// to decide, once per idle period; none of a compaction's bookkeeping runs.
	Intercepted bool `json:"intercepted,omitempty"`
	// Period (with Intercepted): the idle period's identity, when the session's last main turn ended, unix ms by the mod's clock (0 =
	// the mod cannot say: the daemon's own period stands in). One decision per value, so a short turn between two interceptions that the
	// liveness tick never saw still opens a new one, and a retry 54 minutes later is the same period.
	Period int64 `json:"period,omitempty"`
}

// RelayCompactedResponse says whether the lead was told.
type RelayCompactedResponse struct {
	Noticed bool `json:"noticed"`
	// Route (with Intercepted): what the daemon decided for the intercept, by the session's authoritative role. InterceptRouteMember: it is
	// an active local member (the lead was told, now or earlier in this period, or the member is running). InterceptRouteSilent: it is
	// not one (released, or the mod's role is stale): the mod drops its role and says hello again, so the next idle compaction runs.
	Route string `json:"route,omitempty"`
}

// RelayClaimResponse answers claim: the op and who leads the member's team.
type RelayClaimResponse struct {
	Op   RelayOp    `json:"op"`
	Lead *RelayLead `json:"lead,omitempty"`
	// Reuse: the op's file is the member's fresh handoff (a half or an earlier handoff nothing has made stale): the mod writes nothing, it
	// reports writing and written (RT-3c, spec §4.3). Only a handoff op ever carries it.
	Reuse bool `json:"reuse,omitempty"`
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
	ErrNoOpenAsk        = "no_open_ask"       // --deny of a member that has no open ask (RT-2b)
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

// MinManualRelayModVersion is the mod protocol that sends `begin --manual` for a person's /relay (MR-1); an older mod
// answers a member's /relay with its own refusal.
const MinManualRelayModVersion = 4

// RelayManualNoticeFmt is the lead's notice that a person relayed one of its members by hand (MR-1, D3): the old and
// the new ref.
const RelayManualNoticeFmt = "[pdx team] member 由使用者手動接力：%s → %s"

// RelayAskNoticeFmt takes the member's address, its bare 6-character ref, its title, the integer percentage, the minutes
// left (rounded up) and the ref twice (approve, deny).
const RelayAskNoticeFmt = "[pdx team] member %s [%s]「%s」已用 %d%%，申請接力。\n%d 分鐘內請回覆：同意 pdx relay _%s；不同意 pdx relay _%s --deny"

// RelayAskRemoteNoticeFmt is the notice of a member on another host: the same words as before RT-2b (a remote ask expires as it
// always did and cannot be denied yet, S-8); the last argument is the whole ref the lead types — <alias>/_<ref>.
const RelayAskRemoteNoticeFmt = "[pdx team] member %s [%s]「%s」已用 %d%%，申請接力。\n%d 分鐘內同意請執行：pdx relay %s（不同意不用回覆，過期即作罷）"

// RelayAskFinalSuffix is appended to a notice of an ask at RelayLastAskPct or more: the member's last one (RT-2b T23).
const RelayAskFinalSuffix = "\n這是最後一次申請；沒有回覆的話這個 member 會暫停作業。"

// RelayAskUnansweredLeadFmt is the lead's notice that a member's final ask went unanswered and the member is paused: the member's
// address, ref, title, and the bare ref twice.
const RelayAskUnansweredLeadFmt = "[pdx team] member %s [%s]「%s」接力申請 5 分鐘沒有回應，已暫停作業。請執行 pdx relay _%s（接力）或 pdx relay _%s --deny（不接力，恢復作業）。"

// UnattendedAskAcceptedFmt is the lead's notice that the daemon accepted a member's ask while the host is unattended (RT-2b T22,
// S-4): the member's address, its title, the integer percentage and the member pool left.
const UnattendedAskAcceptedFmt = "[pdx team] 無人值守：member %s「%s」已用 %d%%，已自動接力（member 額度剩 %d）"

// Relay ask states (relay_asks.state) and the reasons of a withdrawal.
const (
	RelayAskOpen      = "open"
	RelayAskAccepted  = "accepted"
	RelayAskExpired   = "expired"
	RelayAskWithdrawn = "withdrawn"

	RelayAskWithdrawDenied     = "denied" // the lead declined it (pdx relay _ref --deny)
	RelayAskWithdrawPaused     = "paused" // unattended accepted it and the lead's pool was empty: the member was paused instead
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

// RelayDenyRequest is POST /api/team/relays/deny (RT-2b): the lead declines the open ask of one of its members.
type RelayDenyRequest struct {
	OriginInbox string `json:"origin_inbox"`
	Target      string `json:"target"` // the member's ref or address
}

// RelayDenyResponse says which ask was closed.
type RelayDenyResponse struct {
	AskID string `json:"ask_id"`
}
