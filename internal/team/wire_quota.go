package team

// ---- #2062: the per-session relay quota (docs/specs/2026-10-09-relay-quota-spec-plan.md) ----
//
// A quota belongs to a session's whole relay chain (its chain root in session_lineage): every session of the chain
// reads the same numbers. SelfLeft is how many times the chain may be relayed automatically while unattended mode is
// on; MemberPoolLeft is a lead's pool for relaying its members (spec §3.4). Both 0 by default. The rule that spends
// them is RQ-1b; RQ-1a stores, sets and shows them.

const (
	RelayQuotaRoute     = "/api/team/relay-quota" // PUT
	RelayQuotaEventType = "team.relay_quota"      // HostEvent.Type
	MaxRelayQuota       = 99                      // a quota is 0..99
)

// RelayQuota is the pair of numbers; always present on the displays that carry it (the App shows 0).
type RelayQuota struct {
	SelfLeft       int `json:"self_left"`
	MemberPoolLeft int `json:"member_pool_left"`
}

// RelayQuotaPutRequest is PUT /api/team/relay-quota: absolute values for the chain of SessionID (a field left out
// stays). The App's alone — the kind is the caller's own claim (told, not enforced, as the unattended switch);
// no pdx command writes it.
type RelayQuotaPutRequest struct {
	SessionID      string `json:"session_id"`
	SelfLeft       *int   `json:"self_left,omitempty"`
	MemberPoolLeft *int   `json:"member_pool_left,omitempty"`
	Client         Client `json:"client"`
}

// RelayQuotaView is the answer of the PUT: the stored row of the session's chain root.
type RelayQuotaView struct {
	SessionID     string `json:"session_id"`
	RootSessionID string `json:"root_session_id"`
	RelayQuota
	// PendingLineage: the session is its own chain root only because the lineage of a relay in flight has not been
	// written yet (its cleared has not committed). A value written now belongs to this provisional root and is NOT
	// migrated when the lineage appears — set it again once the relay is done.
	PendingLineage bool   `json:"pending_lineage,omitempty"`
	UpdatedAt      int64  `json:"updated_at"`
	UpdatedBy      string `json:"updated_by,omitempty"`
}

// RelayQuotaEventValue is the team.relay_quota HostEvent value: a chain's numbers changed.
type RelayQuotaEventValue struct {
	Op            string `json:"op"` // "changed"
	RootSessionID string `json:"root_session_id"`
	RelayQuota
}

// SessionQuota is one row of UnattendedView.Quotas: a live session of the host and its chain's numbers.
type SessionQuota struct {
	SessionID     string `json:"session_id"`
	RootSessionID string `json:"root_session_id"`
	Title         string `json:"title,omitempty"`
	Address       string `json:"address"`
	IsLead        bool   `json:"is_lead"`
	RelayQuota
}
