package teammod

import (
	"errors"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// defaultFixture is team A with members op-a (tasks 1, 2) and op-b (task 3),
// every task pending.
func defaultFixture(t *testing.T) *Store {
	t.Helper()
	s := openTestStore(t)
	seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	seedMember(t, s, "op-b", tTeamA, "sess-b", 1)
	mustCreateTask(t, s, newTask(tTeamA, "op-a", "one", 10))
	mustCreateTask(t, s, newTask(tTeamA, "op-a", "two", 11))
	mustCreateTask(t, s, newTask(tTeamA, "op-b", "b's", 12))
	return s
}

func setStatus(t *testing.T, s *Store, seq int, to team.TaskStatus) {
	t.Helper()
	if _, err := s.SetTaskStatus(tTeamA, seq, to, team.TaskByLead, 20); err != nil {
		t.Fatalf("task %d -> %s: %v", seq, to, err)
	}
}

// A report with no task is resolved INSIDE the transaction that stores it: the
// member's only in_progress task as it is at that moment. Mutation gate:
// resolve it before the transaction → the handler's race tests red.
func TestInsertReportByOwner_DefaultTaskIsResolvedInTheTransaction(t *testing.T) {
	s := defaultFixture(t)
	setStatus(t, s, 2, team.TaskInProgress)
	setStatus(t, s, 3, team.TaskInProgress) // b's: never considered
	rep, task, replay, err := s.InsertReportByOwner(newReport(tTeamA, 0, "op-a", team.ReportProgress, 1, 50))
	if err != nil || replay || rep.TaskSeq != 2 || task.Seq != 2 || task.LastReportAt != 50 {
		t.Fatalf("= %+v %+v replay=%v err=%v, want it on task 2", rep, task, replay, err)
	}
	if got := mustGetTask(t, s, tTeamA, 1); got.LastReportAt != 0 {
		t.Fatalf("task 1 was reported on: %+v", got)
	}
}

func TestInsertReportByOwner_DefaultTaskNoneOrSeveralWritesNothing(t *testing.T) {
	s := defaultFixture(t)
	setStatus(t, s, 3, team.TaskInProgress) // only b's is in progress

	var d *DefaultTaskError
	_, _, _, err := s.InsertReportByOwner(newReport(tTeamA, 0, "op-a", team.ReportProgress, 1, 50))
	if !errors.Is(err, ErrNoDefaultTask) || !errors.As(err, &d) || !reflect.DeepEqual(d.Open, []int{1, 2}) || len(d.InProgress) != 0 {
		t.Fatalf("none in progress: %v %+v, want ErrNoDefaultTask with open [1 2]", err, d)
	}

	setStatus(t, s, 1, team.TaskInProgress)
	setStatus(t, s, 2, team.TaskInProgress)
	_, _, _, err = s.InsertReportByOwner(newReport(tTeamA, 0, "op-a", team.ReportProgress, 1, 50))
	if !errors.Is(err, ErrAmbiguousDefaultTask) || !errors.As(err, &d) || !reflect.DeepEqual(d.Open, []int{1, 2}) || !reflect.DeepEqual(d.InProgress, []int{1, 2}) {
		t.Fatalf("two in progress: %v %+v, want ErrAmbiguousDefaultTask", err, d)
	}
	if n := countReports(t, s, tTeamA); n != 0 {
		t.Fatalf("%d reports stored", n)
	}

	// A member that is not live gets the usual not-found, never the open list.
	if err := s.SetMemberState("op-a", team.MemberKilled, 5); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.InsertReportByOwner(newReport(tTeamA, 0, "op-a", team.ReportProgress, 1, 50)); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("killed member: %v, want ErrTaskNotFound", err)
	}
}

// A retry of a report that had no task names no task again, but it is the same
// report: it finds the stored one even after the member moved on.
func TestInsertReportByOwner_DefaultTaskRetryReplaysTheStoredReport(t *testing.T) {
	s := defaultFixture(t)
	setStatus(t, s, 1, team.TaskInProgress)
	first, _, _, err := s.InsertReportByOwner(newReport(tTeamA, 0, "op-a", team.ReportProgress, 1, 50))
	if err != nil || first.TaskSeq != 1 {
		t.Fatalf("first = %+v, %v", first, err)
	}
	setStatus(t, s, 1, team.TaskCompleted)
	setStatus(t, s, 2, team.TaskInProgress)
	again, _, replay, err := s.InsertReportByOwner(newReport(tTeamA, 0, "op-a", team.ReportProgress, 1, 60))
	if err != nil || !replay || again.TaskSeq != 1 || countReports(t, s, tTeamA) != 1 {
		t.Fatalf("retry = %+v replay=%v err=%v, want a replay of the report on task 1", again, replay, err)
	}
}

func TestInsertReport_WithoutATaskIsRefused(t *testing.T) {
	s := defaultFixture(t)
	if _, _, _, err := s.InsertReport(newReport(tTeamA, 0, "op-a", team.ReportProgress, 1, 50)); err == nil {
		t.Fatal("the unguarded insert accepted no task")
	}
}

// The report id is the CLI's idempotency key and belongs to the member that
// minted it: another member of the team using the same id neither collides nor
// learns that it exists. Mutation gate: leave member_key out of the replay
// lookup → red.
func TestInsertReport_SameIDFromAnotherMemberIsIndependent(t *testing.T) {
	a := newReport(tTeamA, 1, "op-a", team.ReportReady, 1, 100)
	b := newReport(tTeamA, 3, "op-b", team.ReportDone, 1, 110)
	b.Summary = "b's own words"
	for name, byOwner := range map[string]bool{"by owner": true, "unguarded": false} {
		t.Run(name, func(t *testing.T) {
			s := defaultFixture(t)
			insert := s.InsertReport
			if byOwner {
				insert = s.InsertReportByOwner
			}
			if _, _, replay, err := insert(a); err != nil || replay {
				t.Fatalf("a: replay=%v err=%v", replay, err)
			}
			_, bTask, replay, err := insert(b)
			if err != nil || replay || bTask.Status != team.TaskCompleted {
				t.Fatalf("b with a's id: replay=%v err=%v task=%+v, want an independent insert", replay, err, bTask)
			}
			if n := countReports(t, s, tTeamA); n != 2 {
				t.Fatalf("%d rows, want one per member", n)
			}
			if got := mustGetTask(t, s, tTeamA, 1); got.Status == team.TaskCompleted || len(got.Metadata.PRs) != 1 {
				t.Fatalf("b's report touched a's task: %+v", got)
			}
			// Each member's retry is its own replay; a changed body is a reuse.
			for _, r := range []ReportRow{a, b} {
				if _, _, replay, err := insert(r); err != nil || !replay {
					t.Fatalf("%s retry: replay=%v err=%v", r.MemberKey, replay, err)
				}
				r.Summary = "changed"
				if _, _, _, err := insert(r); !errors.Is(err, ErrReportIDReused) {
					t.Fatalf("%s changed: %v, want ErrReportIDReused", r.MemberKey, err)
				}
			}

			gotA, okA, _ := s.GetReportOf(tTeamA, "op-a", reportID(1))
			gotB, okB, _ := s.GetReportOf(tTeamA, "op-b", reportID(1))
			if !okA || !okB || gotA.Kind != team.ReportReady || gotB.Summary != "b's own words" {
				t.Fatalf("GetReportOf: %+v %v / %+v %v", gotA, okA, gotB, okB)
			}
			if _, ok, _ := s.GetReportOf(tTeamA, "op-zzz", reportID(1)); ok {
				t.Fatal("GetReportOf found an id of a member that never used it")
			}
			if first, ok, _ := s.GetReport(tTeamA, reportID(1)); !ok || first.MemberKey != "op-a" {
				t.Fatalf("GetReport = %+v ok=%v, want the first stored", first, ok)
			}
		})
	}
}
