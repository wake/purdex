// internal/team/wire_command.go
package team

import "encoding/json"

// Cross-host team commands (L → M), spec docs/specs/2026-10-09-cross-host-team-spec-plan.md §6.2.
// POST /api/peers/team/commands carries one TeamCommand; the lead host's daemon is the source of truth and the
// member host applies it.

// Command kinds.
const (
	CommandAdopt     = "adopt"
	CommandRelease   = "release"
	CommandEnd       = "end"
	CommandLeadMoved = "lead_moved"
	CommandVoid      = "void"
	CommandSpawn     = "spawn" // not applied by this version (X4a)
)

// Command refusal codes (JSON 4xx bodies, {"error": code}).
const (
	ErrCommandBadRequest      = "bad_request"
	ErrCommandUnsupportedKind = "unsupported_kind"
	ErrCommandWrongHost       = "wrong_host"
	ErrCommandIDConflict      = "id_conflict"
	ErrCommandHostNotAllowed  = "host_not_allowed"
	ErrCommandNotYourMember   = "not_your_member"
	ErrCommandMKConflict      = "mk_conflict"
	ErrCommandVoided          = "command_void" // 409: the lead host voided this command id (spec §3.3)
	ErrCommandNotVoidable     = "not_voidable" // 409: a void names a command that is no adopt or spawn
)

// TeamLead is the lead's full origin tuple a command carries, which the member host needs to present the lead as a
// reply-capable sender (internal/peers/wire.go, reply.go) and to rebuild after a restart.
type TeamLead struct {
	SessionID string `json:"session_id"`
	Ref       string `json:"ref"`
	Title     string `json:"title,omitempty"`
	Address   string `json:"address"`
	PID       int    `json:"pid"`
	ProcStart string `json:"proc_start"`
}

// TeamCommand is the request body. ID is a UUID v4 minted by the lead host; ToHostID is the member host's own host
// id (a mismatch is 409 wrong_host). MK is the member key of the membership (the adopt command id), absent from
// the team-level kinds end and lead_moved, which act on every live row of TeamID from this lead host.
type TeamCommand struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	ToHostID string   `json:"to_host_id"`
	TeamID   string   `json:"team_id"`
	TeamName string   `json:"team_name,omitempty"`
	MK       string   `json:"mk,omitempty"`
	Lead     TeamLead `json:"lead"`

	// adopt
	TargetSessionID string `json:"target_session_id,omitempty"`
	TargetRef       string `json:"target_ref,omitempty"`
	// lead_moved: the lead's new session (Lead carries the rest of its tuple)
	LeadSessionID string `json:"lead_session_id,omitempty"`
	LeadRef       string `json:"lead_ref,omitempty"`
	// void
	CommandID string `json:"command_id,omitempty"`
}

// TeamCommandAnswer is the 200 body: the receiver's host id (the sender checks it is who it addressed) and the
// kind's outcome.
type TeamCommandAnswer struct {
	ID      string          `json:"id"`
	HostID  string          `json:"host_id"`
	Outcome json.RawMessage `json:"outcome"`
}

// AdoptOutcome is the adopt command's outcome when applied.
type AdoptOutcome struct {
	State         string `json:"state"` // "applied"
	MemberSession string `json:"member_session_id"`
	Ref           string `json:"ref"`
	PID           int    `json:"pid"`
	ProcStart     string `json:"proc_start"`
	Title         string `json:"title,omitempty"`
	Cwd           string `json:"cwd,omitempty"`
	Tmux          string `json:"tmux,omitempty"`
}

// CommandRefusal is a refusal's body (and the shape stored for one).
type CommandRefusal struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}
