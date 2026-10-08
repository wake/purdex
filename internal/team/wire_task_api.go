package team

// ---- T-1b: the task routes' wire (plan "Shared contracts") ----

// Task route refusal codes: 409, and the CLI exits 13 on each
// (teamRefusalCodes). not_lead and not_your_member are in wire_team.go.
const (
	ErrNotMember    = "not_member"     // the origin is neither a live team's lead nor its active member
	ErrTaskNotFound = "task_not_found" // no such task IN THE CALLER'S SCOPE: another team's id, another member's task, a malformed id all answer it
	// ErrNotTaskOwner is declared for the CLI's refusal table (plan: "a
	// member changes a task it can see but does not own"). No T-1b1 path
	// produces it: a member only ever sees its own tasks, so a foreign one
	// answers task_not_found and existence never leaks.
	ErrNotTaskOwner      = "not_task_owner"
	ErrBadTaskTransition = "bad_task_transition" // the status change is not in the transition table
	ErrBlockedByUnknown  = "blocked_by_unknown"  // a blocked_by id is no task of this team
	ErrBlockedByCycle    = "blocked_by_cycle"    // the blockers would close a cycle
	ErrOwnerNotActive    = "owner_not_active"    // assigning to a member that is not active
)

// CreateTaskRequest is POST /api/team/tasks. To is a member's ref or
// address, as `pdx kill` takes it; BlockedBy are display ids of this team.
type CreateTaskRequest struct {
	OriginInbox string   `json:"origin_inbox"`
	To          string   `json:"to"`
	Subject     string   `json:"subject"`
	Description string   `json:"description,omitempty"`
	DoneWhen    []string `json:"done_when,omitempty"`
	BlockedBy   []string `json:"blocked_by,omitempty"`
}

// TaskStatusRequest is POST /api/team/tasks/{id}/status.
type TaskStatusRequest struct {
	OriginInbox string     `json:"origin_inbox"`
	Status      TaskStatus `json:"status"`
}

// ReassignTaskRequest is POST /api/team/tasks/{id}/reassign.
type ReassignTaskRequest struct {
	OriginInbox string `json:"origin_inbox"`
	To          string `json:"to"`
}

// TaskList is GET /api/team/tasks; Tasks is never null.
type TaskList struct {
	Tasks []Task `json:"tasks"`
}

// TaskDetail is GET /api/team/tasks/{id}: the task and its reports, newest
// first, at most 200. Reports is never null.
type TaskDetail struct {
	Task    Task     `json:"task"`
	Reports []Report `json:"reports"`
}
