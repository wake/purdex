package teammod

import (
	"errors"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// The owner-scoped report calls (T-1b2) read the member's right where they
// write or read: the task must belong to ownerKey and ownerKey must still be
// an active member of a live team, inside the same transaction. Replays are
// included: a caller that lost the task must not get the stored report back.
// Mutation gates: drop the guard from InsertReportByOwner → the insert and
// replay rows red; skip it on the replay path only → the replay rows red;
// drop it from ListReportsForOwner → the list rows red.

func TestInsertReportByOwner_RefusesWhenTheOwnerLostTheTask(t *testing.T) {
	for name := range ownerChanges {
		t.Run(name, func(t *testing.T) {
			s := ownerFixture(t, name)
			before := mustGetTask(t, s, tTeamA, 1)
			n := countReports(t, s, tTeamA)
			rep, task, replay, err := s.InsertReportByOwner(newReport(tTeamA, 1, "op-a", team.ReportAck, 3, 50))
			if name == ownerControl {
				if err != nil || replay || rep.ID != reportID(3) || task.Status != team.TaskInProgress || task.UpdatedAt != 50 {
					t.Fatalf("control = %+v %+v replay=%v err=%v", rep, task, replay, err)
				}
				return
			}
			if !errors.Is(err, ErrTaskNotFound) {
				t.Fatalf("err = %v, want ErrTaskNotFound", err)
			}
			if rep.ID != "" || task.Seq != 0 || replay {
				t.Fatalf("a refused insert returned %+v %+v replay=%v", rep, task, replay)
			}
			if after := mustGetTask(t, s, tTeamA, 1); !reflect.DeepEqual(after, before) {
				t.Fatalf("a refused insert changed the task: %+v -> %+v", before, after)
			}
			if got := countReports(t, s, tTeamA); got != n {
				t.Fatalf("a refused insert stored a report: %d -> %d", n, got)
			}
		})
	}
}

// A replay is answered only to a caller that is still authorised.
func TestInsertReportByOwner_ReplayIsGuardedToo(t *testing.T) {
	for name := range ownerChanges {
		t.Run(name, func(t *testing.T) {
			s := ownerFixture(t, name)
			// Report 1 is stored (by the fixture) with exactly this content.
			rep, _, replay, err := s.InsertReportByOwner(newReport(tTeamA, 1, "op-a", team.ReportProgress, 1, 11))
			if name == ownerControl {
				if err != nil || !replay || rep.ID != reportID(1) {
					t.Fatalf("control replay = %+v replay=%v err=%v", rep, replay, err)
				}
				return
			}
			if !errors.Is(err, ErrTaskNotFound) || rep.ID != "" || replay {
				t.Fatalf("replay by an unauthorised caller = %+v replay=%v err=%v, want ErrTaskNotFound and nothing", rep, replay, err)
			}
		})
	}
}

func TestInsertReportByOwner_OtherMemberAndIDReuse(t *testing.T) {
	s := ownerFixture(t, ownerControl)
	// Another member of the team does not own task 1.
	if _, _, _, err := s.InsertReportByOwner(newReport(tTeamA, 1, "op-b", team.ReportAck, 3, 50)); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("another member: %v", err)
	}
	if _, _, _, err := s.InsertReportByOwner(newReport(tTeamA, 9, "op-a", team.ReportAck, 3, 50)); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("no such task: %v", err)
	}
	// An authorised caller reusing an id with other content is still ErrReportIDReused.
	r := newReport(tTeamA, 1, "op-a", team.ReportProgress, 1, 11)
	r.Summary = "something else"
	if _, _, _, err := s.InsertReportByOwner(r); !errors.Is(err, ErrReportIDReused) {
		t.Fatalf("id reused: %v", err)
	}
}

func TestListReportsForOwner_ReadsOnlyWhileTheOwnerHoldsTheTask(t *testing.T) {
	for name := range ownerChanges {
		t.Run(name, func(t *testing.T) {
			s := ownerFixture(t, name)
			got, ok, err := s.ListReportsForOwner(tTeamA, 1, "op-a", 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if name == ownerControl {
				if !ok || len(got) != 2 || got[0].ID != reportID(2) {
					t.Fatalf("control = %v ok=%v", got, ok)
				}
				return
			}
			if ok || len(got) != 0 {
				t.Fatalf("a member that lost the task listed %v ok=%v", got, ok)
			}
		})
	}
}

func TestListReportsForOwner_ScopeAndSince(t *testing.T) {
	s := ownerFixture(t, ownerControl)
	for name, c := range map[string]struct {
		seq int
		key string
	}{
		"another member": {1, "op-b"}, "no such task": {9, "op-a"}, "no task": {0, "op-a"}, "no key": {1, ""},
	} {
		if got, ok, err := s.ListReportsForOwner(tTeamA, c.seq, c.key, 0, 0); err != nil || ok || len(got) != 0 {
			t.Fatalf("%s: %v ok=%v err=%v", name, got, ok, err)
		}
	}
	got, ok, err := s.ListReportsForOwner(tTeamA, 1, "op-a", 12, 0)
	if err != nil || !ok || len(got) != 1 || got[0].ID != reportID(2) {
		t.Fatalf("since 12 = %v ok=%v err=%v", got, ok, err)
	}
	got, ok, err = s.ListReportsForOwner(tTeamA, 1, "op-a", 99, 0)
	if err != nil || !ok || got == nil || len(got) != 0 {
		t.Fatalf("since 99 = %#v ok=%v err=%v, want an empty non-nil list", got, ok, err)
	}
}
