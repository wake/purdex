// Package team is the wire contract of the lead/team feature (spec §6, §9):
// the approval request, its states and error codes, the request and
// response bodies of /api/team/*, and the approval.request host event. It
// is a leaf: cmd/pdx and the daemon module both import it, it imports
// nothing of theirs.
package team

import "encoding/json"

const EventType = "approval.request" // HostEvent.Type

type Kind string

const (
	KindLead      Kind = "lead"
	KindSelfRelay Kind = "self_relay" // accepted from P5a on; P2 answers 400 unsupported_kind
)

type State string

const (
	StateOpen      State = "open"
	StateApproved  State = "approved"
	StateDenied    State = "denied"
	StateTimeout   State = "timeout"   // U7: counts as a denial
	StateCancelled State = "cancelled" // requester DELETEd
	StateAbandoned State = "abandoned" // lease ran out, or origin session gone
)

// Error codes (APIError.Error)
const (
	ErrBadRequest       = "bad_request"
	ErrOriginUnknown    = "origin_unknown"
	ErrUnsupportedKind  = "unsupported_kind"
	ErrIDConflict       = "id_conflict"        // same id, different hash
	ErrRequestOpen      = "request_open"       // 409, carries the open Approval
	ErrAlreadyLead      = "already_lead"       // 409, enforced from P4 (needs the teams table)
	ErrMemberCannotLead = "member_cannot_lead" // 409, enforced from P4
	ErrAlreadyDecided   = "already_decided"    // 409, carries the closed Approval
	ErrNotFound         = "not_found"
	ErrNotReady         = "not_ready" // 503 while stopping
)

// Limits (spec §6.1, §6.2, §9.1)
const (
	DefaultMaxMembers = 3
	MaxMaxMembers     = 8
	DefaultWaitS      = 540 // 9 min
	MaxWaitS          = 600 // 10 min
	LeaseS            = 30
	MaxPollWaitS      = 25
	BootGraceS        = 30
)

// Origin is the requesting CC session, attributed by inbox (spec §6.2).
type Origin struct {
	SessionID string `json:"session_id"`
	Ref       string `json:"ref"`  // "_xxxxxx"
	Name      string `json:"name"` // registry name, may be ""
	PID       int    `json:"pid"`
	ProcStart string `json:"proc_start"`
	Cwd       string `json:"cwd"`
	Tmux      string `json:"tmux"`              // "<session>:@<win>.%<pane>" or ""
	Title     string `json:"title,omitempty"`   // the session's title (pdx msg name), "" when none
	Address   string `json:"address,omitempty"` // "<alias>/<name>" for a routable name, else "<alias>/_<ref>"
}

// LeadPayload is Approval.Payload for KindLead.
type LeadPayload struct {
	Reason     string   `json:"reason"`
	MaxMembers int      `json:"max_members"` // normalised: 0→3, cap 8
	Roots      []string `json:"roots"`       // normalised: absolute, Clean; default [origin.Cwd]
}

// Grant is what the user approved (edited in the dialog). P4 turns it into a team.
type Grant struct {
	MaxMembers int      `json:"max_members"`
	Roots      []string `json:"roots"`
}

// Client is the audit label of whoever decided (spec §6.5). Addr is set by the daemon from RemoteAddr.
type Client struct {
	Kind  string `json:"kind"`  // "app"
	Label string `json:"label"` // "Purdex.app @ air26"
	Addr  string `json:"addr,omitempty"`
}

type Approval struct {
	ID         string          `json:"id"`
	Kind       Kind            `json:"kind"`
	HostID     string          `json:"host_id"`
	Origin     Origin          `json:"origin"`
	Payload    json.RawMessage `json:"payload"` // LeadPayload for lead
	State      State           `json:"state"`
	CreatedAt  int64           `json:"created_at"`           // unix ms
	DeadlineAt int64           `json:"deadline_at"`          // unix ms, absolute
	LeaseUntil int64           `json:"lease_until"`          // unix ms
	DecidedBy  *Client         `json:"decided_by,omitempty"` // approved / denied only
	DecidedAt  int64           `json:"decided_at,omitempty"` // any close
	Grant      *Grant          `json:"grant,omitempty"`      // approved only
}

// CreateApprovalRequest is POST /api/team/approvals.
type CreateApprovalRequest struct {
	ID          string   `json:"id"` // UUID v4 from the CLI; idempotency key
	Kind        Kind     `json:"kind"`
	OriginInbox string   `json:"origin_inbox"` // CLAUDE_CODE_MESSAGING_SOCKET of the caller
	Reason      string   `json:"reason"`
	MaxMembers  int      `json:"max_members,omitempty"`
	Roots       []string `json:"roots,omitempty"`
	WaitS       int      `json:"wait_s,omitempty"` // 0→540, cap 600
}

// DecideRequest is POST /api/team/approvals/{id}/decide.
type DecideRequest struct {
	Decision string `json:"decision"`        // "approve" | "deny"
	Grant    *Grant `json:"grant,omitempty"` // approve only; nil → the payload's values
	Client   Client `json:"client"`
}

// APIError is every non-2xx body on /api/team/*.
type APIError struct {
	Error    string    `json:"error"`
	Detail   string    `json:"detail,omitempty"`
	Approval *Approval `json:"approval,omitempty"` // request_open, already_decided
}

// InflightResponse is GET /api/team/inflight (spec §9.5): what a restart of
// this daemon would interrupt. Neither field is omitempty — the restart
// confirm reads a zero too. RelaysActive is a literal 0 until P6.
type InflightResponse struct {
	ApprovalsOpen int `json:"approvals_open"`
	RelaysActive  int `json:"relays_active"`
}

// EventValue is HostEvent.Value (JSON string) for EventType.
//
//	{op:"opened", approval}            on create
//	{op:"closed", approval}            on every close
//	{op:"snapshot", approvals:[...]}   to each new subscriber (OnSubscribe); approvals is [] when empty, never null
type EventValue struct {
	Op        string     `json:"op"`
	Approval  *Approval  `json:"approval,omitempty"`
	Approvals []Approval `json:"approvals,omitempty"` // MarshalJSON emits [] for snapshot
}

// MarshalJSON keeps the struct tags' shape for opened/closed and makes a
// snapshot's approvals an explicit array: a nil slice would be dropped by
// omitempty (or printed as null without it), and the SPA replaces a host's
// whole set from the snapshot, so "no open requests" must arrive as [].
func (v EventValue) MarshalJSON() ([]byte, error) {
	type plain EventValue // no methods: avoids recursion
	if v.Op != "snapshot" {
		return json.Marshal(plain(v))
	}
	approvals := v.Approvals
	if approvals == nil {
		approvals = []Approval{}
	}
	return json.Marshal(struct {
		Op        string     `json:"op"`
		Approvals []Approval `json:"approvals"`
	}{Op: v.Op, Approvals: approvals})
}
