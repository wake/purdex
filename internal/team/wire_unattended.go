package team

import "encoding/json"

// ---- U23: unattended mode (unattended spec D-U23-1…7) ----
//
// One switch per host, stored in the daemon's host config and set only by
// the team module's route (the App's). While it is on, the daemon approves
// the AutoApprovable kinds itself, through the same statements a click runs,
// recorded with the decider UnattendedClient().

const (
	CapabilityUnattended = "relay.unattended.v1" // /api/info capabilities (D-U23-5)
	UnattendedEventType  = "team.unattended"     // HostEvent.Type
	ClientKindUnattended = "unattended"          // Client.Kind of a daemon auto-approval (D-U23-2)
	UnattendedLabel      = "無人值守模式"              // Client.Label of it

	UnattendedPageDefault = 50  // GET /api/team/unattended's page size when limit is absent
	UnattendedPageMax     = 200 // and its cap

	// UnattendedLeadMaxMembers caps the grant of a lead request the daemon
	// approves itself: min(requested, 3) members (U25 / D-U24-7). A click's
	// grant is the person's and is not capped.
	UnattendedLeadMaxMembers = 3
)

// UnattendedClient is decided_by of an auto-approval. It has no Addr: no
// client decided.
func UnattendedClient() Client {
	return Client{Kind: ClientKindUnattended, Label: UnattendedLabel}
}

// AutoApprovable reports whether the switch approves requests of kind k:
// lead and self_relay (U23) and adopt (U24 PL-1c). The hook kinds are never
// approved by the daemon (U23 "不在範圍內").
func AutoApprovable(k Kind) bool {
	return k == KindSelfRelay || k == KindMemberRelay
}

// Lead and adopt requests are no longer approved by the daemon (relay/team rules v2, RT-1b): an agent's own request is refused
// while the switch is on, and one open when it goes on is closed cancelled with this reason. A person's typed command is not an
// agent's request and is never refused (RT-1a person tokens).
const (
	ErrUnattendedRefused  = "unattended_refused" // 409 on create; close_reason of a swept row
	UnattendedRefusedText = "無人值守模式不開放申請，請等待模式解除後，依情況重新申請"
)

// UnattendedRefusable reports whether an open request of kind k is refused (closed, not approved) when the switch is on.
func UnattendedRefusable(k Kind) bool { return k == KindLead || k == KindAdopt }

// UnattendedState is the switch as stored and as answered.
type UnattendedState struct {
	On        bool    `json:"on"`
	Since     int64   `json:"since"`                // unix ms of the last off→on; 0 = never on. The list starts here (D-U23-6).
	ChangedAt int64   `json:"changed_at"`           // unix ms of the last change; 0 = never written
	ChangedBy *Client `json:"changed_by,omitempty"` // who changed it last; Addr is set by the daemon
}

// UnattendedToggle is one on/off event of the switch (name pool v2 spec §8): when, which way, and by whom (the client's kind and
// label; the address is not kept). The hostconfig module records one with every change of the switch, in the same
// transaction.
type UnattendedToggle struct {
	At int64  `json:"at"` // unix ms of the change
	On bool   `json:"on"`
	By Client `json:"by"`
}

// UnattendedEventLister is the read side of those records, asserted on the registry value of the hostconfig module next to
// UnattendedStore.
type UnattendedEventLister interface {
	// UnattendedEvents is the newest limit events, newest first. Never nil.
	UnattendedEvents(limit int) ([]UnattendedToggle, error)
}

// UnattendedPutRequest is PUT /api/team/unattended. On is a pointer so a
// missing field is told from false.
type UnattendedPutRequest struct {
	On     *bool  `json:"on"`
	Client Client `json:"client"`
}

// UnattendedView is the answer of GET and PUT /api/team/unattended: the
// state, flattened, and one page of the requests auto-approved since Since,
// newest first.
type UnattendedView struct {
	UnattendedState
	Approved   []Approval `json:"approved"`              // never null (MarshalJSON)
	Truncated  bool       `json:"truncated"`             // more rows exist before NextBefore
	NextBefore int64      `json:"next_before,omitempty"` // the next page's cursor: a decided_at
	Swept      int        `json:"swept,omitempty"`       // PUT only: open requests the switch-on approved
	Pending    int        `json:"pending,omitempty"`     // PUT only: auto-approvable requests still open after the sweep
	Refused    int        `json:"refused,omitempty"`     // PUT only: open lead / adopt requests the switch-on refused (cancelled, unattended_refused)
	// Quotas is every live session of the host with its relay chain's numbers (#2062), for the panel's steppers.
	// null when the quotas could not be read (the page above stays valid); [] when they were read and the host has no
	// live session; never absent. A client keeps what it had on null and replaces it on [].
	Quotas []SessionQuota `json:"quotas"`
	// Held is the open self_relay requests the daemon could not approve because their chain's quota is 0 (the rule is on):
	// they wait for a person, or for the quota to be raised (approved within one tick). Independent of the approved page
	// above. null when it could not be read, [] when read and none; never absent.
	Held []Approval `json:"held"`
	// Log is the latest 30 decisions and switch changes, newest first (decision_log.go); never null (MarshalJSON). The old
	// Approved page above stays for an App that does not know team.decision_log.v1.
	Log []UnattendedLogEntry `json:"log"`
	// ListFailed (PUT only): the write took effect — the state above is
	// what is stored — but the list could not be read, so Approved is
	// empty and says nothing; the client GETs the list.
	ListFailed bool `json:"list_failed,omitempty"`
}

// MarshalJSON writes an empty page as approved:[]: the App replaces its
// list from the answer, so "none" must not arrive as null.
func (v UnattendedView) MarshalJSON() ([]byte, error) {
	type plain UnattendedView // no methods: avoids recursion; keeps the embedded state flattened
	if v.Approved == nil {
		v.Approved = []Approval{}
	}
	if v.Log == nil {
		v.Log = []UnattendedLogEntry{}
	}
	return json.Marshal(plain(v))
}

// UnattendedEventValue is HostEvent.Value (JSON string) for
// UnattendedEventType: {op:"snapshot", state} to each new subscriber,
// {op:"changed", state} after every change.
type UnattendedEventValue struct {
	Op    string          `json:"op"` // "snapshot" | "changed"
	State UnattendedState `json:"state"`
}
