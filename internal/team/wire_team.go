package team

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// ---- P4: teams, members, spawn, kill (spec §7.1–§7.3, U20) ----

// Team error codes (APIError.Error on /api/team/*); 409 and CLI exit 13.
const (
	ErrNotLead         = "not_lead"          // the origin leads no live team
	ErrTeamFull        = "team_full"         // live members plus running spawns reached grant.max_members
	ErrCwdOutsideGrant = "cwd_outside_grant" // the cwd, symlinks evaluated, is under no granted root
	ErrNotYourMember   = "not_your_member"   // kill: the target is no member of the caller's team
	ErrCommandPending  = "command_pending"   // a remote member has an adopt, release or kill in flight that a kill / release must wait for (cross-host spec §4.2)
)

// Spawn failure reasons: SpawnOp.Reason when State is SpawnFailed. The CLI
// exits 14 on SpawnReasonStartTimeout and 1 on every other reason (§14).
const (
	SpawnReasonStartTimeout = "member_start_timeout"  // not registered within SpawnRegisterS; its tmux session was killed
	SpawnReasonCreateFailed = "session_create_failed" // the tmux session could not be created
	SpawnReasonLaunchFailed = "launch_failed"         // the launch line was not sent; the session was killed
	SpawnReasonNameTaken    = "tmux_name_taken"       // SpawnTmuxName(id) existed before this op created it
	SpawnReasonAbandoned    = "abandoned"             // boot found the op's tmux session gone mid-spawn
)

// MemberState is a member row's state (spec §7.3).
type MemberState string

const (
	MemberActive MemberState = "active"
	MemberKilled MemberState = "killed" // pdx kill
	MemberGone   MemberState = "gone"   // its session ended without a kill
	// MemberReleased: the lead let it go (pdx release); the session lives on.
	// Only the store of a later PR (PL-1b) writes it.
	MemberReleased MemberState = "released"
)

// The states of a REMOTE member row on the lead's host (cross-host team spec §4.2): the lead host is the source of truth
// and a command to the member host is in flight in each of the first three. Seats count all of them.
const (
	MemberJoining   MemberState = "joining"   // the adopt is approved and sent; the member host has not answered
	MemberReleasing MemberState = "releasing" // the lead let it go; the release is in flight
	MemberKilling   MemberState = "killing"   // the lead killed it; the kill is in flight
	MemberFailed    MemberState = "failed"    // the adopt was refused or void; the seat is free
)

// SpawnState is a spawn op's state. A running op is resumed at boot from
// its Step (spec §9.3).
type SpawnState string

const (
	SpawnRunning SpawnState = "running"
	SpawnDone    SpawnState = "done"   // Member is set
	SpawnFailed  SpawnState = "failed" // Reason is set
)

// Spawn steps: SpawnOp.Step, how far a spawn got. Persisted after each
// step, so a retry after a restart continues from it and nothing opens
// twice (spec §7.2 step 3, §9.3).
const (
	StepAccepted       = "accepted"        // the op row exists, no tmux session yet
	StepSessionCreated = "session_created" // the tmux session exists, its id and instance recorded
	StepLaunched       = "launched"        // the launch line was sent to window 0
	StepRegistered     = "registered"      // the member's frame and registry entry were seen
)

// Team limits and texts (spec §6.1 step 4, §7.1, §7.2, U20).
const (
	TeamEndLeadGone = "lead_gone" // Team.EndReason: the lead's conversation ended (§7.1)

	SpawnRegisterS = 20 // §7.2 step 5: a launched member must register within this
	SpawnPollWaitS = 25 // POST /api/team/spawns answers within this, running or not

	// DefaultMemberCommand is host config team.member_command when unset
	// (§7.2 step 4): the expansion of the cld-yolo alias.
	DefaultMemberCommand = "claude --dangerously-skip-permissions"

	// MemberBriefPrefixFmt is the brief's first line (§7.2); the arguments
	// are the lead's address and the team id.
	MemberBriefPrefixFmt = "[pdx team] 你是 %s 的 member（team %s）。接力由 lead 決定，不要自己接力。"

	// ReminderAtActivation goes to stderr when `pdx lead request` is
	// approved; stdout stays the grant JSON alone (U20 (b)).
	ReminderAtActivation = "已成為 lead。預設模型不固定：spawn member 時請依工作需求用 --model 指定（例：--model sonnet 做機械性修改、--model opus 做設計）。"

	// ReminderNoModel goes to stderr when `pdx spawn` has no --model; the
	// spawn goes on and the exit code is unchanged (U20 (c)).
	ReminderNoModel = "提醒：沒有指定 --model，member 會用這台主機當下的預設模型。"
)

// Efforts are the levels Claude Code's --effort takes (M25), lowest first,
// for usage text. ValidEffort does not read this slice, so a caller that
// changes it cannot widen what the daemon appends to a launch line.
var Efforts = []string{"low", "medium", "high", "xhigh", "max"}

// modelRE is U20 (a)'s rule: an alias or a full model name, optionally
// with the 1M-context suffix. Go's $ is end of text, so no trailing newline
// passes.
var modelRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}(\[1m\])?$`)

// ValidModel reports whether s may be passed as `--model` (U20 (a)). The CLI
// checks it first (exit 2) and the daemon again (400 bad_request) before the
// launch line single-quotes it ("[1m]" would glob unquoted). "" is invalid:
// a request without a model leaves the field out.
func ValidModel(s string) bool { return modelRE.MatchString(s) }

// ValidEffort reports whether s is one of Efforts, exact case (M25).
func ValidEffort(s string) bool {
	switch s {
	case "low", "medium", "high", "xhigh", "max":
		return true
	}
	return false
}

// SpawnTmuxName is a member's tmux session name: "tm-" plus the first 10
// hex digits of its spawn op id, lowercase, with the dashes removed (spec
// §7.2 step 3, D4). It derives from the id alone, so a retry after a
// restart finds the session this op created. The id must be a canonical
// UUID v4 (isCanonicalUUIDv4), in either case; anything else is an error,
// because the name is a tmux target and must hold only hex digits.
func SpawnTmuxName(opID string) (string, error) {
	if !isCanonicalUUIDv4(opID) {
		return "", fmt.Errorf("spawn op id %q is not a canonical UUID v4", opID)
	}
	digits := strings.ReplaceAll(strings.ToLower(opID), "-", "")
	return "tm-" + digits[:10], nil
}

// isCanonicalUUIDv4 is the approvals handler's id rule (version 4 of the
// RFC 4122 variant) restricted to the 8-4-4-4-12 spelling, hex of either
// case: uuid.Parse alone also takes braces, a urn: prefix and 32 bare digits.
func isCanonicalUUIDv4(s string) bool {
	if len(s) != 36 {
		return false
	}
	u, err := uuid.Parse(s)
	return err == nil && u.Version() == 4 && u.Variant() == uuid.RFC4122
}

// Team is one lead's team (spec §7.1), created in the transaction that
// approves the lead request. Its id is that request's id (plan v3
// deviation 1), so RequestID == ID.
type Team struct {
	ID            string `json:"id"`
	HostID        string `json:"host_id"`
	TeamName      string `json:"team_name"`       // the team's current name; always present, "" = none
	TeamLabel     string `json:"team_label"`      // the team's short label (explicit or derived, D-L3); always present, "" = none
	LeadSessionID string `json:"lead_session_id"` // follows the lead through its relays (§8.4)
	LeadRef       string `json:"lead_ref"`        // "_xxxxxx", moves with LeadSessionID
	Grant         Grant  `json:"grant"`
	RequestID     string `json:"request_id"`
	CreatedAt     int64  `json:"created_at"`           // unix ms
	EndedAt       int64  `json:"ended_at,omitempty"`   // unix ms; 0 while the team is live
	EndReason     string `json:"end_reason,omitempty"` // TeamEndLeadGone
}

// MemberContext is a member's last statusline reading (P1), live or, from
// P4-6, persisted on its row. The shape is the peers wire's agent.context
// plus the model and effort the member actually runs (U20 (e)).
// UsedPercentage is nil until Claude Code reports one.
type MemberContext struct {
	UsedPercentage *float64 `json:"used_percentage"`
	Window         int      `json:"window"`
	ModelID        string   `json:"model_id,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	At             int64    `json:"at"` // unix ms when the daemon received it
}

// Member is one member of a team (spec §7.2 step 6, §7.3).
type Member struct {
	SessionID string `json:"session_id"`
	Ref       string `json:"ref"`     // "_xxxxxx"
	Address   string `json:"address"` // "<alias>/<name>" for a routable name, else "<alias>/_<ref>"
	TeamID    string `json:"team_id"`
	HostID    string `json:"host_id"` // the host the member runs on
	// HostAlias is that host's alias as the lead host calls it; "" = the lead's own host. A remote member's Address is
	// "<host_alias>/<ref>". ContextUnavailable: a remote member whose host did not answer (the CLI says so); never set
	// for a member on the lead's own host. Both additive (cross-host team spec §8).
	HostAlias          string         `json:"host_alias,omitempty"`
	ContextUnavailable bool           `json:"context_unavailable,omitempty"`
	Title              string         `json:"title,omitempty"`
	Cwd                string         `json:"cwd"`
	TmuxSession        string         `json:"tmux_session"` // SpawnTmuxName(SpawnOp)
	State              MemberState    `json:"state"`
	Origin             string         `json:"origin"`           // MemberOriginSpawned | MemberOriginAdopted; always present (a view without it is an older daemon's: spawned)
	Model              string         `json:"model,omitempty"`  // as asked at spawn (U20); "" = the host's default
	Effort             string         `json:"effort,omitempty"` // as asked at spawn (U20)
	Context            *MemberContext `json:"context,omitempty"`
	SpawnOp            string         `json:"spawn_op"`                // "" for an adopted member
	AdoptRequest       string         `json:"adopt_request,omitempty"` // adopted only: the approval that took it in
	CreatedAt          int64          `json:"created_at"`              // unix ms
	EndedAt            int64          `json:"ended_at,omitempty"`      // unix ms; 0 while the member is active
	// RelayQuota is the numbers of the member's relay chain (#2062); a member relays only through its lead, so its own
	// self_left matters again only after a release. Always present.
	RelayQuota RelayQuota `json:"relay_quota"`

	// Task and LastAt are GET /api/team's per-member display columns (T-1d1).
	// Both are optional: a daemon that predates them omits them and a CLI that
	// predates them ignores them, so either pairing works (the table prints "-").
	Task   *MemberTask `json:"task,omitempty"`    // the member's current task (D-T6); nil when it has none
	LastAt int64       `json:"last_at,omitempty"` // unix ms of its latest turn or report on that task; 0 = none
}

// MemberTask is the member's current task as `pdx team` shows it.
type MemberTask struct {
	ID      string     `json:"id"` // the display id (TaskDisplayID)
	Subject string     `json:"subject"`
	Status  TaskStatus `json:"status"`
}

// SpawnRequest is POST /api/team/spawns.
type SpawnRequest struct {
	ID          string `json:"id"`               // UUID v4 from the CLI; idempotency key
	OriginInbox string `json:"origin_inbox"`     // CLAUDE_CODE_MESSAGING_SOCKET of the lead
	Cwd         string `json:"cwd,omitempty"`    // absolute; the CLI defaults it to its working directory
	Title       string `json:"title,omitempty"`  // the member's title (pdx msg name)
	Model       string `json:"model,omitempty"`  // ValidModel; "" = the host's default model (U20)
	Effort      string `json:"effort,omitempty"` // ValidEffort; "" = the host's default effort (U20)
	// Task, when set, is the member's first task (T-2): the daemon creates it
	// in the transaction that inserts the member row. Its description is the brief.
	Task *SpawnTask `json:"task,omitempty"`
}

// SpawnTask is the task a spawn creates for its member (T-2).
type SpawnTask struct {
	Subject     string   `json:"subject"`
	Description string   `json:"description,omitempty"`
	DoneWhen    []string `json:"done_when,omitempty"`
}

// SpawnOp is a spawn operation, persisted step by step (spec §9.3). It is
// the 200 body of POST /api/team/spawns in every state.
type SpawnOp struct {
	ID          string     `json:"id"`
	TeamID      string     `json:"team_id"`
	HostID      string     `json:"host_id"`
	State       SpawnState `json:"state"`
	Step        string     `json:"step"`             // StepAccepted … StepRegistered
	Reason      string     `json:"reason,omitempty"` // failed only: a SpawnReason*
	Cwd         string     `json:"cwd"`
	Title       string     `json:"title"`
	Model       string     `json:"model"`
	Effort      string     `json:"effort"`
	TmuxSession string     `json:"tmux_session"`           // SpawnTmuxName(ID)
	LeadAddress string     `json:"lead_address,omitempty"` // for the brief's first line (§7.2)
	TaskID      string     `json:"task_id,omitempty"`      // done only: the display id of the task the spawn created (T-2)
	Member      *Member    `json:"member,omitempty"`       // done only
	CreatedAt   int64      `json:"created_at"`             // unix ms
	UpdatedAt   int64      `json:"updated_at"`             // unix ms
}

// KillRequest is POST /api/team/kill.
type KillRequest struct {
	OriginInbox string `json:"origin_inbox"` // CLAUDE_CODE_MESSAGING_SOCKET of the lead
	Target      string `json:"target"`       // the member's ref or address, as `pdx kill <ref>` takes it
}

// TeamView is GET /api/team: the caller's team and every member of it, in
// any state.
type TeamView struct {
	Team    Team     `json:"team"`
	Members []Member `json:"members"` // never null: MarshalJSON emits [] for none
	// InUse is the places the limit counts: active members plus spawns still starting (the count spawn and adopt check);
	// nil from a daemon that predates it, and then a reader counts the active members itself.
	InUse *int `json:"in_use,omitempty"`
	// LeadRelayQuota is the lead's own relay chain's numbers (#2062; the lead's self_left and the member pool);
	// nil from a daemon that predates it. Members have none of their own to show.
	LeadRelayQuota *RelayQuota `json:"lead_relay_quota,omitempty"`
}

// MarshalJSON keeps the struct tags' shape and makes "no members" an
// explicit []: consumers range over it, and a nil slice would print null.
func (v TeamView) MarshalJSON() ([]byte, error) {
	type plain TeamView // no methods: avoids recursion
	p := plain(v)
	if p.Members == nil {
		p.Members = []Member{}
	}
	return json.Marshal(p)
}
