package team

import "encoding/json"

// ---- P8a: 分流 (spec §6.6 "分流 with the Purdex mod", U19) ----
//
// Two more approval kinds share approval_requests, its CAS, its event and
// its decide route: hook_ask (an AskUserQuestion the native dialog is
// showing) and hook_permission (a permission prompt; the mod half is P8b).
// The row lives exactly as long as the native dialog, so it has no deadline
// of its own: DeadlineAt is NoExpiryAt. A row the mod raised keeps the usual
// lease, renewed by every /api/ask/wait poll; a terminal_only row (no mod,
// nobody polls) has LeaseUntil = NoExpiryAt and closes on the session's
// PostToolUse / Stop report or when the session is gone.

const (
	KindHookAsk        Kind = "hook_ask"
	KindHookPermission Kind = "hook_permission"
)

// IsHookKind reports whether k is one of the 分流 kinds.
func IsHookKind(k Kind) bool { return k == KindHookAsk || k == KindHookPermission }

// Close states of the hook kinds only (spec §6.6 steps 3, 5, 6). approved
// is "answered remotely" for them; denied is a hook_permission deny.
const (
	StateAnsweredLocal    State = "answered_local"    // the terminal answered first
	StateTerminalOverride State = "terminal_override" // a remote decide won the CAS, but the terminal's answer stands
	StateDismissed        State = "dismissed"         // Esc, an interrupted turn, or the PostToolUse backstop
)

// NoExpiryAt is the deadline_at (and the terminal_only lease_until) of hook
// rows: 3000-01-01T00:00:00Z in unix ms. It is below 2^53, so the SPA reads
// it as the number it is; the sweeper's expiry checks never trip on it.
const NoExpiryAt int64 = 32503680000000

// Client.Kind of decided_by when the terminal decided (answered_local,
// terminal_override). The label is the same word: there is no app to name.
const ClientKindTerminal = "terminal"

// Error codes of the ask routes and of decide on a hook kind. The ask routes
// also answer ErrUnknownSession (404) and ErrBadTransition (409) — those two
// are declared once, in wire_relay.go (P5a-1a), and used from there; declaring
// them again here would be `redeclared in this block`.
const (
	ErrNoResponders = "no_responders" // 409: nobody remote can answer — the mod lets the native dialog run alone
	ErrAskOpen      = "ask_open"      // 409, carries the open Approval for this (session, tool_use_id)
	ErrTerminalOnly = "terminal_only" // 409: a read-only card cannot be decided
)

// HookAskPayload is Approval.Payload for KindHookAsk.
type HookAskPayload struct {
	ToolUseID    string          `json:"tool_use_id"`
	Questions    json.RawMessage `json:"questions"`               // AskUserQuestion input `questions` verbatim
	TerminalOnly bool            `json:"terminal_only,omitempty"` // no mod: the card is read-only
}

// HookPermissionPayload is Approval.Payload for KindHookPermission.
type HookPermissionPayload struct {
	ToolUseID    string          `json:"tool_use_id"` // "" on a PermissionRequest (CC sends none there)
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	Suggestions  json.RawMessage `json:"permission_suggestions,omitempty"`
	TerminalOnly bool            `json:"terminal_only,omitempty"`
}

// HookDecision rides in Grant's place for the hook kinds: the remote
// client's answer (decide), or the terminal's (report answered_local).
type HookDecision struct {
	Answers      map[string]string `json:"answers,omitempty"`       // hook_ask: question text → answer (multi-select comma-joined)
	Behavior     string            `json:"behavior,omitempty"`      // hook_permission: allow | deny
	UpdatedInput json.RawMessage   `json:"updated_input,omitempty"` // hook_permission
	Message      string            `json:"message,omitempty"`       // hook_permission deny reason
}

// AskBeginRequest is POST /api/ask/begin (the mod, through `pdx ask begin`).
type AskBeginRequest struct {
	SessionID string          `json:"session_id"`
	ToolUseID string          `json:"tool_use_id"`
	Kind      Kind            `json:"kind"`    // hook_ask | hook_permission
	Payload   json.RawMessage `json:"payload"` // HookAskPayload or HookPermissionPayload; tool_use_id and terminal_only are set by the daemon
}

// AskBeginResponse is the 201 body.
type AskBeginResponse struct {
	ID string `json:"id"`
}

// Ask wait states (GET /api/ask/wait/{id}): what `pdx ask wait` prints.
const (
	AskStillOpen      = "still_open"
	AskAnsweredRemote = "answered_remote"
	AskClosed         = "closed"
)

// AskWaitResponse is the 200 body of GET /api/ask/wait/{id}?wait=25: the
// row is still open after the wait; a remote client answered (hook set); or
// it closed another way (reason is the Approval.State).
type AskWaitResponse struct {
	State  string        `json:"state"`
	Hook   *HookDecision `json:"hook,omitempty"`
	Reason string        `json:"reason,omitempty"`
}

// AskReportRequest is POST /api/ask/report/{id} (the mod: the native dialog settled).
type AskReportRequest struct {
	State State         `json:"state"` // answered_local | dismissed
	Hook  *HookDecision `json:"hook,omitempty"`
}
