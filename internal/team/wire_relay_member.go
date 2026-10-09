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
	RelayStallTimeoutS       = 900                       // no progress after the claim
)

// RelayCreateResponse is POST /api/team/relays' body: 201 for a new op, 200 for a replay of the same id.
type RelayCreateResponse struct {
	Op RelayOp `json:"op"`
}
