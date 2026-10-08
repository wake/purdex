package team

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	ipeers "github.com/wake/purdex/internal/peers"
)

// ---- T: team tasks (spec docs/specs/2026-10-09-team-task-report-spec.md) ----

// TaskStatus is a task's stored state. "blocked" is never stored: it is
// derived from blocked_by at view time.
type TaskStatus string

const (
	TaskPending    TaskStatus = "pending"
	TaskInProgress TaskStatus = "in_progress"
	TaskCompleted  TaskStatus = "completed"
	TaskDeleted    TaskStatus = "deleted"
)

// TaskActor is who asks for a status change: the team's lead, or the task's
// owner (the member it is assigned to).
type TaskActor string

const (
	TaskByLead  TaskActor = "lead"
	TaskByOwner TaskActor = "owner"
)

// Task field limits.
const (
	MaxTaskSubjectRunes   = 80
	MaxDoneWhenLines      = 10
	MaxDoneWhenLineRunes  = 200
	MaxTaskDescriptionLen = 32 * 1024 // bytes
)

// TaskOwner is the member a task is assigned to, read at view time: Ref is
// the member's current ref, State its row state (active, killed, gone,
// released).
type TaskOwner struct {
	Ref     string `json:"ref"`
	Address string `json:"address,omitempty"`
	Title   string `json:"title,omitempty"`
	State   string `json:"state,omitempty"`
}

// TaskMetadata is what reports attach to a task.
type TaskMetadata struct {
	Branch string   `json:"branch,omitempty"`
	PRs    []int    `json:"prs,omitempty"`
	SHAs   []string `json:"shas,omitempty"`
}

// TaskReportStamp is the newest report on a task.
type TaskReportStamp struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	At      int64  `json:"at"`
}

// TaskTurnStamp is the owner's newest finished turn, cut to one sentence.
type TaskTurnStamp struct {
	Summary string `json:"summary"`
	At      int64  `json:"at"`
}

// Task is the wire view of one task. ID is the display id (TaskDisplayID).
// Blocks and Blocked are derived at view time.
type Task struct {
	ID          string           `json:"id"`
	TeamID      string           `json:"team_id"`
	Subject     string           `json:"subject"`
	Description string           `json:"description,omitempty"`
	DoneWhen    []string         `json:"done_when"`
	Status      TaskStatus       `json:"status"`
	Owner       TaskOwner        `json:"owner"`
	Blocks      []string         `json:"blocks"`
	BlockedBy   []string         `json:"blocked_by"`
	Blocked     bool             `json:"blocked"`
	CreatedBy   string           `json:"created_by"`
	CreatedAt   int64            `json:"created_at"`
	UpdatedAt   int64            `json:"updated_at"`
	Metadata    TaskMetadata     `json:"metadata,omitzero"`
	LastReport  *TaskReportStamp `json:"last_report,omitempty"`
	LastTurn    *TaskTurnStamp   `json:"last_turn,omitempty"`
}

// taskTeamPrefix is the first six hex chars of a team id, dashes removed,
// lower case.
func taskTeamPrefix(teamID string) string {
	p := strings.ToLower(strings.ReplaceAll(teamID, "-", ""))
	if len(p) > 6 {
		p = p[:6]
	}
	return p
}

// TaskDisplayID is the id a task shows on the wire: <team6>-<seq>. It is a
// display id, not a key: callers parse the seq and look it up with their own
// team id, so two teams sharing a prefix never meet.
func TaskDisplayID(teamID string, seq int) string {
	return taskTeamPrefix(teamID) + "-" + strconv.Itoa(seq)
}

// ParseTaskID returns the seq of a display id of THIS team. It is ok only
// when the prefix is this team's and the seq is a positive decimal without
// sign, leading zeros or whitespace.
func ParseTaskID(id, teamID string) (seq int, ok bool) {
	prefix, seq, ok := ParseTaskIDSyntax(id)
	if !ok || prefix != taskTeamPrefix(teamID) {
		return 0, false
	}
	return seq, true
}

// taskPrefixLen is the length of a display id's team prefix.
const taskPrefixLen = 6

// ParseTaskIDSyntax checks the shape of a display id without any team id:
// six lower-case hex chars, a dash, and a seq that is a positive decimal
// without sign, leading zero or whitespace and fits an int. The CLI uses it to
// refuse a malformed id before asking the daemon; whether the id names a task
// of the caller's team is the daemon's to say (ParseTaskID).
func ParseTaskIDSyntax(id string) (prefix string, seq int, ok bool) {
	if len(id) < taskPrefixLen+2 || id[taskPrefixLen] != '-' {
		return "", 0, false
	}
	prefix = id[:taskPrefixLen]
	for i := 0; i < len(prefix); i++ {
		if c := prefix[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", 0, false
		}
	}
	digits := id[taskPrefixLen+1:]
	if digits[0] == '0' {
		return "", 0, false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return "", 0, false
		}
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return prefix, n, true
}

// firstControl returns the first control character (C0, DEL or C1) of s that
// allow does not permit.
func firstControl(s string, allow string) (rune, bool) {
	for _, r := range s {
		if unicode.IsControl(r) && !strings.ContainsRune(allow, r) {
			return r, true
		}
	}
	return 0, false
}

// validLine checks a one-line text of 1..maxRunes runes that is also
// peer-safe: no control character at all (line breaks and tabs included, so
// no ESC sequence can reach a terminal either).
func validLine(what, s string, maxRunes int) error {
	if err := ipeers.ValidateText(s); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if r, bad := firstControl(s, ""); bad {
		return fmt.Errorf("%s must be one line of plain text, it has the control character %U", what, r)
	}
	if n := utf8.RuneCountInString(s); n > maxRunes {
		return fmt.Errorf("%s is %d runes, at most %d", what, n, maxRunes)
	}
	return nil
}

// ValidTaskSubject checks a task subject: 1-80 runes, one line, peer-safe.
func ValidTaskSubject(s string) error {
	return validLine("subject", s, MaxTaskSubjectRunes)
}

// ValidDoneWhen checks the done-when lines: at most 10, each 1-200 runes, one
// line, peer-safe. nil and empty are fine.
func ValidDoneWhen(lines []string) error {
	if len(lines) > MaxDoneWhenLines {
		return fmt.Errorf("done_when has %d lines, at most %d", len(lines), MaxDoneWhenLines)
	}
	for i, l := range lines {
		if err := validLine(fmt.Sprintf("done_when[%d]", i), l, MaxDoneWhenLineRunes); err != nil {
			return err
		}
	}
	return nil
}

// ValidTaskDescription checks a task description: empty is allowed, else at
// most 32 KiB and peer-safe; line breaks (\n, \r) and tabs pass, every other
// control character is refused.
func ValidTaskDescription(s string) error {
	return validMultiline("description", s, MaxTaskDescriptionLen)
}

// validMultiline is the free-text rule shared by descriptions and report
// bodies: empty is allowed, else at most maxBytes and peer-safe; \n, \r and
// tabs pass, every other control character is refused.
func validMultiline(what, s string, maxBytes int) error {
	if s == "" {
		return nil
	}
	if len(s) > maxBytes {
		return fmt.Errorf("%s is %d bytes, at most %d", what, len(s), maxBytes)
	}
	if err := ipeers.ValidateText(s); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if r, bad := firstControl(s, "\n\r\t"); bad {
		return fmt.Errorf("%s has the control character %U", what, r)
	}
	return nil
}

// TaskTransitionAllowed is the one status-change table, shared by the task
// routes and by report effects:
//
//	pending     -> in_progress  lead or owner
//	in_progress -> completed    lead or owner
//	pending     -> completed    lead only
//	pending, in_progress -> deleted  lead only
//
// Nothing leaves completed or deleted (decision D-T7: more work is a new
// task), and a same-status move is not a transition.
func TaskTransitionAllowed(from, to TaskStatus, by TaskActor) bool {
	if by != TaskByLead && by != TaskByOwner {
		return false
	}
	switch {
	case from == TaskPending && to == TaskInProgress:
		return true
	case from == TaskInProgress && to == TaskCompleted:
		return true
	case from == TaskPending && to == TaskCompleted:
		return by == TaskByLead
	case (from == TaskPending || from == TaskInProgress) && to == TaskDeleted:
		return by == TaskByLead
	}
	return false
}
