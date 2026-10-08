package teammod

import (
	"errors"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// The owner-scoped store calls take the authorization into their own
// transaction: the task must belong to ownerKey, and ownerKey must still be
// an active member of a live team, read where the write happens. Anything
// else is ErrTaskNotFound and nothing is written, so a caller that checked
// earlier cannot act on a stale answer (T-1b1 review: TOCTOU).
const ownerControl = "nothing changed (control)"

// ownerChanges are the ways the owner's right to task 1 can end after a
// caller checked it.
var ownerChanges = map[string]string{
	"the task was reassigned":  `UPDATE tasks SET owner_key = 'op-b' WHERE seq = 1`,
	"the member was killed":    `UPDATE team_members SET state = 'killed' WHERE spawn_op = 'op-a'`,
	"the member was released":  `UPDATE team_members SET state = 'released' WHERE spawn_op = 'op-a'`,
	"the member is gone":       `UPDATE team_members SET state = 'gone' WHERE spawn_op = 'op-a'`,
	"the team ended":           `UPDATE teams SET ended_at = 99, end_reason = 'x'`,
	"the member left the team": `UPDATE team_members SET team_id = '` + tTeamB + `' WHERE spawn_op = 'op-a'`,
	ownerControl:               `SELECT 1`,
}

// ownerFixture is team A with members op-a and op-b, task 1 owned by op-a
// with two reports, and change applied.
func ownerFixture(t *testing.T, change string) *Store {
	t.Helper()
	s := openTestStore(t)
	seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	seedMember(t, s, "op-b", tTeamA, "sess-b", 1)
	mustCreateTask(t, s, newTask(tTeamA, "op-a", "work", 10))
	mustInsertReport(t, s, newReport(tTeamA, 1, "op-a", team.ReportProgress, 1, 11))
	mustInsertReport(t, s, newReport(tTeamA, 1, "op-a", team.ReportProgress, 2, 12))
	if _, err := s.db.Exec(ownerChanges[change]); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSetTaskStatusByOwner_RefusesWhenTheOwnerLostTheTask(t *testing.T) {
	for name := range ownerChanges {
		t.Run(name, func(t *testing.T) {
			s := ownerFixture(t, name)
			before := mustGetTask(t, s, tTeamA, 1)
			got, err := s.SetTaskStatusByOwner(tTeamA, 1, "op-a", team.TaskInProgress, 50)
			if name == ownerControl {
				if err != nil || got.Status != team.TaskInProgress || got.UpdatedAt != 50 {
					t.Fatalf("control = %+v, %v", got, err)
				}
				return
			}
			if !errors.Is(err, ErrTaskNotFound) {
				t.Fatalf("err = %v, want ErrTaskNotFound", err)
			}
			if after := mustGetTask(t, s, tTeamA, 1); !reflect.DeepEqual(after, before) {
				t.Fatalf("a refused change wrote: %+v -> %+v", before, after)
			}
		})
	}
}

func TestSetTaskStatusByOwner_UnknownTaskOtherMemberAndTheOwnersTable(t *testing.T) {
	s := ownerFixture(t, ownerControl)
	if _, err := s.SetTaskStatusByOwner(tTeamA, 9, "op-a", team.TaskInProgress, 50); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("no such seq: %v", err)
	}
	if _, err := s.SetTaskStatusByOwner(tTeamA, 1, "op-b", team.TaskInProgress, 50); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("another member: %v", err)
	}
	// The owner's table applies: no delete, no closing a pending task.
	for _, to := range []team.TaskStatus{team.TaskDeleted, team.TaskCompleted} {
		if _, err := s.SetTaskStatusByOwner(tTeamA, 1, "op-a", to, 50); !errors.Is(err, ErrBadTaskTransition) {
			t.Fatalf("owner -> %s: %v", to, err)
		}
	}
	if mustGetTask(t, s, tTeamA, 1).Status != team.TaskPending {
		t.Fatal("a refused move changed the status")
	}
}

func TestGetTaskDetail_ReadsTaskAndReportsUnderTheSameGuard(t *testing.T) {
	for name := range ownerChanges {
		t.Run(name, func(t *testing.T) {
			s := ownerFixture(t, name)
			row, reports, ok, err := s.GetTaskDetail(tTeamA, 1, "op-a")
			if err != nil {
				t.Fatal(err)
			}
			if name == ownerControl {
				if !ok || row.Seq != 1 || len(reports) != 2 || reports[0].ID != reportID(2) {
					t.Fatalf("control = %+v %+v ok=%v", row, reports, ok)
				}
				return
			}
			if ok || row.Subject != "" || len(reports) != 0 {
				t.Fatalf("a refused read leaked: %+v %+v ok=%v", row, reports, ok)
			}
		})
	}
}

// ListTasksForOwner lists a member's own tasks only while the member is an
// active member of a live team, checked in the transaction that reads them;
// otherwise ok=false and nothing is returned. A task reassigned away simply
// is not in the list.
func TestListTasksForOwner_ChecksTheMemberWhereItReads(t *testing.T) {
	for name := range ownerChanges {
		t.Run(name, func(t *testing.T) {
			s := ownerFixture(t, name)
			rows, ok, err := s.ListTasksForOwner(tTeamA, "op-a", false)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case ownerControl:
				if !ok || len(rows) != 1 || rows[0].Seq != 1 {
					t.Fatalf("control = %+v ok=%v", rows, ok)
				}
			case "the task was reassigned":
				if !ok || rows == nil || len(rows) != 0 {
					t.Fatalf("after the reassign = %#v ok=%v, want an empty list", rows, ok)
				}
			default:
				if ok || len(rows) != 0 {
					t.Fatalf("a refused list leaked: %+v ok=%v", rows, ok)
				}
			}
		})
	}
}

func TestListTasksForOwner_HidesFinishedUnlessAllAndKeepsTheOrder(t *testing.T) {
	s := ownerFixture(t, ownerControl)
	mustCreateTask(t, s, newTask(tTeamA, "op-a", "second", 20))  // seq 2
	mustCreateTask(t, s, newTask(tTeamA, "op-b", "not a's", 21)) // seq 3
	if _, err := s.SetTaskStatus(tTeamA, 1, team.TaskCompleted, team.TaskByLead, 30); err != nil {
		t.Fatal(err)
	}
	seqs := func(all bool) []int {
		rows, ok, err := s.ListTasksForOwner(tTeamA, "op-a", all)
		if err != nil || !ok {
			t.Fatalf("list: ok=%v err=%v", ok, err)
		}
		out := []int{}
		for _, r := range rows {
			out = append(out, r.Seq)
		}
		return out
	}
	if got := seqs(false); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("unfinished = %v", got)
	}
	want, _ := s.ListTasks(tTeamA, "op-a", true)
	if got := seqs(true); len(got) != 2 || got[0] != want[0].Seq || got[1] != want[1].Seq {
		t.Fatalf("all = %v, want ListTasks' order %v", got, want)
	}
	if _, ok, _ := s.ListTasksForOwner(tTeamA, "", true); ok {
		t.Fatal("an empty owner key listed")
	}
}

// A lead (ownerKey "") reads any task of its team; a wrong team or seq is
// simply not found; reports are never nil.
func TestGetTaskDetail_LeadScopeAndEmptyReports(t *testing.T) {
	s := ownerFixture(t, ownerControl)
	mustCreateTask(t, s, newTask(tTeamA, "op-b", "quiet", 20)) // seq 2, no reports
	if row, reports, ok, err := s.GetTaskDetail(tTeamA, 1, ""); err != nil || !ok || row.OwnerKey != "op-a" || len(reports) != 2 {
		t.Fatalf("lead read = %+v %+v %v %v", row, reports, ok, err)
	}
	if _, reports, ok, _ := s.GetTaskDetail(tTeamA, 2, ""); !ok || reports == nil || len(reports) != 0 {
		t.Fatalf("quiet task: ok=%v reports=%#v, want an empty non-nil slice", ok, reports)
	}
	if _, _, ok, _ := s.GetTaskDetail(tTeamB, 1, ""); ok {
		t.Fatal("another team's id found a task")
	}
	if _, _, ok, _ := s.GetTaskDetail(tTeamA, 7, ""); ok {
		t.Fatal("no such seq found a task")
	}
}
