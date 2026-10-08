package teammod

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// rid is the n-th test report id (a lower-case UUID).
func reportID(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }

// newReport is a valid report of kind k on task (teamID, seq) by member key,
// with just the fields its kind requires.
func newReport(teamID string, seq int, key string, k team.ReportKind, n int, at int64) ReportRow {
	r := ReportRow{TeamID: teamID, TaskSeq: seq, ID: reportID(n), MemberKey: key, Kind: k, Summary: "summary " + string(k), CreatedAt: at}
	switch k {
	case team.ReportQuestion, team.ReportBlocked:
		r.Needs = "lead"
	case team.ReportReady:
		r.PR, r.Reviews = 12, []string{"R1=job-1"}
	case team.ReportMerged:
		r.PR, r.SHA = 12, "abcdef1"
	}
	return r
}

func mustInsertReport(t *testing.T, s *Store, r ReportRow) (ReportRow, TaskRow, bool) {
	t.Helper()
	got, task, replay, err := s.InsertReport(r)
	if err != nil {
		t.Fatalf("insert report %s (%s): %v", r.ID, r.Kind, err)
	}
	return got, task, replay
}

func mustGetTask(t *testing.T, s *Store, teamID string, seq int) TaskRow {
	t.Helper()
	got, ok, err := s.GetTask(teamID, seq)
	if err != nil || !ok {
		t.Fatalf("get task %s/%d: ok=%v err=%v", teamID, seq, ok, err)
	}
	return got
}

func countReports(t *testing.T, s *Store, teamID string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM reports WHERE team_id = ?`, teamID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// reportFixture is team A with one active member and one pending task (seq 1).
func reportFixture(t *testing.T) (*Store, string) {
	t.Helper()
	s := openTestStore(t)
	m := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	mustCreateTask(t, s, newTask(tTeamA, m, "work", 10))
	return s, m
}

func TestInsertReport_EffectsPerKind(t *testing.T) {
	s, m := reportFixture(t)
	n := 0
	next := func(k team.ReportKind, at int64) ReportRow { n++; return newReport(tTeamA, 1, m, k, n, at) }

	// progress, question, blocked change no status on a pending task but
	// stamp the last report.
	for i, k := range []team.ReportKind{team.ReportProgress, team.ReportQuestion, team.ReportBlocked} {
		at := int64(20 + i)
		_, task, replay := mustInsertReport(t, s, next(k, at))
		if replay || task.Status != team.TaskPending {
			t.Fatalf("%s: replay=%v status=%s, want a fresh insert and still pending", k, replay, task.Status)
		}
		if task.LastReportKind != string(k) || task.LastReportSummary != "summary "+string(k) || task.LastReportAt != at || task.UpdatedAt != at {
			t.Fatalf("%s: last report / updated_at not stamped: %+v", k, task)
		}
	}

	// ack: pending -> in_progress; a second ack leaves it in_progress.
	_, task, _ := mustInsertReport(t, s, next(team.ReportAck, 30))
	if task.Status != team.TaskInProgress || task.LastReportKind != "ack" || task.UpdatedAt != 30 {
		t.Fatalf("ack: %+v", task)
	}
	_, task, _ = mustInsertReport(t, s, next(team.ReportAck, 31))
	if task.Status != team.TaskInProgress || task.UpdatedAt != 31 {
		t.Fatalf("second ack: %+v", task)
	}

	// ready appends the pr once, in order.
	r := next(team.ReportReady, 40)
	_, task, _ = mustInsertReport(t, s, r)
	if !reflect.DeepEqual(task.Metadata.PRs, []int{12}) {
		t.Fatalf("ready 12: prs=%v", task.Metadata.PRs)
	}
	r = next(team.ReportReady, 41)
	r.PR = 5
	_, task, _ = mustInsertReport(t, s, r)
	if !reflect.DeepEqual(task.Metadata.PRs, []int{12, 5}) {
		t.Fatalf("ready 5: prs=%v, want [12 5]", task.Metadata.PRs)
	}
	_, task, _ = mustInsertReport(t, s, next(team.ReportReady, 42)) // pr 12 again, a new report
	if !reflect.DeepEqual(task.Metadata.PRs, []int{12, 5}) || task.Status != team.TaskInProgress {
		t.Fatalf("ready 12 again: prs=%v status=%s, want [12 5] and in_progress", task.Metadata.PRs, task.Status)
	}

	// merged appends the lower-cased sha once.
	r = next(team.ReportMerged, 50)
	r.SHA = "ABCDEF1"
	row, task, _ := mustInsertReport(t, s, r)
	if row.SHA != "abcdef1" || !reflect.DeepEqual(task.Metadata.SHAs, []string{"abcdef1"}) {
		t.Fatalf("merged: row sha=%q shas=%v, want lower-case", row.SHA, task.Metadata.SHAs)
	}
	if back, _, _ := s.GetReport(tTeamA, row.ID); back.SHA != "abcdef1" {
		t.Fatalf("stored sha = %q, want lower-case", back.SHA)
	}
	_, task, _ = mustInsertReport(t, s, next(team.ReportMerged, 51)) // abcdef1 again
	if !reflect.DeepEqual(task.Metadata.SHAs, []string{"abcdef1"}) {
		t.Fatalf("merged again: shas=%v, want one entry", task.Metadata.SHAs)
	}
	r = next(team.ReportMerged, 52)
	r.SHA = "0123456789abcdef0123456789abcdef01234567"
	_, task, _ = mustInsertReport(t, s, r)
	if len(task.Metadata.SHAs) != 2 || task.Metadata.SHAs[0] != "abcdef1" {
		t.Fatalf("merged second sha: %v", task.Metadata.SHAs)
	}

	// done completes it.
	_, task, _ = mustInsertReport(t, s, next(team.ReportDone, 60))
	if task.Status != team.TaskCompleted || task.LastReportKind != "done" || task.UpdatedAt != 60 {
		t.Fatalf("done: %+v", task)
	}

	// What InsertReport returned is what is stored.
	if db := mustGetTask(t, s, tTeamA, 1); !reflect.DeepEqual(db, task) {
		t.Fatalf("returned task differs from the stored one:\n got %+v\nstored %+v", task, db)
	}
}

func TestInsertReport_StoresTheRowRoundTrip(t *testing.T) {
	s, m := reportFixture(t)
	in := newReport(tTeamA, 1, m, team.ReportReady, 1, 77)
	in.Body = "findings\n| a | b |"
	in.Reviews = []string{"R1=job-1", "R2=job-2"}
	got, _, _ := mustInsertReport(t, s, in)
	back, ok, err := s.GetReport(tTeamA, reportID(1))
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(got, in) || !reflect.DeepEqual(back, in) {
		t.Fatalf("round trip lost a field:\n in   %+v\n got  %+v\n back %+v", in, got, back)
	}
	if _, ok, _ := s.GetReport(tTeamA, reportID(2)); ok {
		t.Fatal("an unknown id must not be found")
	}
	// A kind without kind fields stores an empty fields document.
	plain := newReport(tTeamA, 1, m, team.ReportProgress, 3, 78)
	mustInsertReport(t, s, plain)
	var fields string
	if err := s.db.QueryRow(`SELECT fields_json FROM reports WHERE id = ?`, reportID(3)).Scan(&fields); err != nil || fields != "{}" {
		t.Fatalf("fields_json = %q err=%v, want {}", fields, err)
	}
}

func TestInsertReport_ReplayAppliesNothingTwice(t *testing.T) {
	for _, k := range []team.ReportKind{team.ReportAck, team.ReportReady, team.ReportMerged, team.ReportDone} {
		t.Run(string(k), func(t *testing.T) {
			s, m := reportFixture(t)
			first := newReport(tTeamA, 1, m, k, 1, 100)
			_, after, replay := mustInsertReport(t, s, first)
			if replay {
				t.Fatal("the first insert is not a replay")
			}

			again := first
			again.CreatedAt = 999 // a retry is stamped later
			row, task, replay := mustInsertReport(t, s, again)
			if !replay {
				t.Fatal("same id and content: want replay=true")
			}
			if row.CreatedAt != 100 {
				t.Fatalf("replay returned created_at %d, want the stored 100", row.CreatedAt)
			}
			if !reflect.DeepEqual(task, after) {
				t.Fatalf("replay changed the task:\n before %+v\n after  %+v", after, task)
			}
			if task.LastReportAt != 100 || task.UpdatedAt != 100 {
				t.Fatalf("replay moved last_report_at / updated_at: %+v", task)
			}
			if n := countReports(t, s, tTeamA); n != 1 {
				t.Fatalf("%d report rows after a replay, want 1", n)
			}
			if db := mustGetTask(t, s, tTeamA, 1); !reflect.DeepEqual(db, after) {
				t.Fatalf("stored task changed by a replay:\n%+v\n%+v", after, db)
			}
		})
	}

	// A replay answers with the CURRENT task, not the one the first call saw.
	s, m := reportFixture(t)
	ready := newReport(tTeamA, 1, m, team.ReportReady, 1, 100)
	mustInsertReport(t, s, ready)
	mustInsertReport(t, s, newReport(tTeamA, 1, m, team.ReportDone, 2, 110))
	_, task, replay := mustInsertReport(t, s, ready)
	if !replay || task.Status != team.TaskCompleted || task.LastReportKind != "done" {
		t.Fatalf("replay of an older report: replay=%v task=%+v, want the current (completed) task", replay, task)
	}
	if !reflect.DeepEqual(task.Metadata.PRs, []int{12}) {
		t.Fatalf("prs after replay = %v", task.Metadata.PRs)
	}
}

func TestInsertReport_IDReusedWithDifferentContent(t *testing.T) {
	s, m := reportFixture(t)
	mb := seedTaskTeam(t, s, tTeamB, "lead-b", "op-b", "sess-b")
	mustCreateTask(t, s, newTask(tTeamB, mb, "b work", 10))
	mustCreateTask(t, s, newTask(tTeamA, m, "second", 11))

	base := newReport(tTeamA, 1, m, team.ReportReady, 1, 100)
	base.Body = "body"
	mustInsertReport(t, s, base)
	before := mustGetTask(t, s, tTeamA, 1)

	variants := map[string]func(*ReportRow){
		"summary": func(r *ReportRow) { r.Summary = "other" },
		"body":    func(r *ReportRow) { r.Body = "other" },
		"kind":    func(r *ReportRow) { r.Kind = team.ReportProgress; r.PR, r.Reviews = 0, nil },
		"pr":      func(r *ReportRow) { r.PR = 13 },
		"reviews": func(r *ReportRow) { r.Reviews = []string{"R1=job-2"} },
		"task":    func(r *ReportRow) { r.TaskSeq = 2 },
	}
	for name, mut := range variants {
		r := base
		mut(&r)
		if _, _, _, err := s.InsertReport(r); !errors.Is(err, ErrReportIDReused) {
			t.Errorf("%s differs: err = %v, want ErrReportIDReused", name, err)
		}
	}
	if n := countReports(t, s, tTeamA) + countReports(t, s, tTeamB); n != 1 {
		t.Fatalf("%d report rows, want only the first", n)
	}
	if after := mustGetTask(t, s, tTeamA, 1); !reflect.DeepEqual(after, before) {
		t.Fatalf("a refused reuse changed the task:\n%+v\n%+v", before, after)
	}
	if b := mustGetTask(t, s, tTeamB, 1); len(b.Metadata.PRs) != 0 || b.LastReportKind != "" {
		t.Fatalf("a refused reuse touched another team's task: %+v", b)
	}
}

func TestInsertReport_EffectAndRowCommitTogether(t *testing.T) {
	for _, k := range []team.ReportKind{team.ReportAck, team.ReportReady, team.ReportDone} {
		t.Run(string(k), func(t *testing.T) {
			s, m := reportFixture(t)
			before := mustGetTask(t, s, tTeamA, 1)
			boom := errors.New("injected failure after the insert")
			s.afterReportInsert = func() error { return boom }

			if _, _, _, err := s.InsertReport(newReport(tTeamA, 1, m, k, 1, 100)); !errors.Is(err, boom) {
				t.Fatalf("err = %v, want the injected failure", err)
			}
			if _, ok, _ := s.GetReport(tTeamA, reportID(1)); ok {
				t.Fatal("the report row survived a failed call")
			}
			if after := mustGetTask(t, s, tTeamA, 1); !reflect.DeepEqual(after, before) {
				t.Fatalf("the task changed although the call failed:\n before %+v\n after  %+v", before, after)
			}

			// Without the failure the same report goes through, so the rollback
			// left nothing behind (no id taken, no lock held).
			s.afterReportInsert = nil
			if _, _, replay, err := s.InsertReport(newReport(tTeamA, 1, m, k, 1, 100)); err != nil || replay {
				t.Fatalf("retry after rollback: replay=%v err=%v", replay, err)
			}
		})
	}
}

// The other direction: when the task update itself fails, the report row must
// not stay behind. A trigger makes every update of the task fail, which a seam
// after the insert cannot model.
func TestInsertReport_EffectAndRowCommitTogether_FailedEffect(t *testing.T) {
	s, m := reportFixture(t)
	if _, err := s.db.Exec(`CREATE TRIGGER tasks_no_update BEFORE UPDATE ON tasks
		BEGIN SELECT RAISE(ABORT, 'task update refused'); END`); err != nil {
		t.Fatal(err)
	}
	before := mustGetTask(t, s, tTeamA, 1)
	if _, _, _, err := s.InsertReport(newReport(tTeamA, 1, m, team.ReportAck, 1, 100)); err == nil {
		t.Fatal("want the failed task update to fail the call")
	}
	if _, ok, _ := s.GetReport(tTeamA, reportID(1)); ok {
		t.Fatal("the report row stayed although the task update failed")
	}
	if after := mustGetTask(t, s, tTeamA, 1); !reflect.DeepEqual(after, before) {
		t.Fatalf("the task changed: %+v", after)
	}
}

// The id is scoped to the team (primary key (team_id, id)): another team
// using the same id neither collides nor learns that it exists.
func TestInsertReport_SameIDInAnotherTeamIsIndependent(t *testing.T) {
	s, ma := reportFixture(t)
	mb := seedTaskTeam(t, s, tTeamB, "lead-b", "op-b", "sess-b")
	mustCreateTask(t, s, newTask(tTeamB, mb, "b work", 10))

	a := newReport(tTeamA, 1, ma, team.ReportReady, 1, 100)
	mustInsertReport(t, s, a)
	aTask := mustGetTask(t, s, tTeamA, 1)

	// Team B uses the very same id, with different content, on its own task.
	b := newReport(tTeamB, 1, mb, team.ReportDone, 1, 150)
	b.Summary = "b's own words"
	gotB, bTask, replay, err := s.InsertReport(b)
	if err != nil || replay {
		t.Fatalf("same id in another team: replay=%v err=%v, want an independent insert", replay, err)
	}
	if bTask.Status != team.TaskCompleted || gotB.TeamID != tTeamB {
		t.Fatalf("team B's report: row=%+v task=%+v", gotB, bTask)
	}
	if after := mustGetTask(t, s, tTeamA, 1); !reflect.DeepEqual(after, aTask) {
		t.Fatalf("team B's report touched team A's task:\n%+v\n%+v", aTask, after)
	}
	if countReports(t, s, tTeamA) != 1 || countReports(t, s, tTeamB) != 1 {
		t.Fatal("each team keeps its own row")
	}

	// Each team reads its own, and a team does not find the other's.
	if got, ok, _ := s.GetReport(tTeamA, reportID(1)); !ok || !reflect.DeepEqual(got, a) {
		t.Fatalf("team A's report: ok=%v %+v", ok, got)
	}
	if got, ok, _ := s.GetReport(tTeamB, reportID(1)); !ok || got.Summary != "b's own words" {
		t.Fatalf("team B's report: ok=%v %+v", ok, got)
	}
	if _, ok, _ := s.GetReport("3f2a9c03-cccc-4000-8000-000000000003", reportID(1)); ok {
		t.Fatal("a third team must not find the id")
	}

	// Inside one team and for one member the id is still taken, for whatever
	// task (another member's id space is separate: see
	// TestInsertReport_SameIDFromAnotherMemberIsIndependent).
	mustCreateTask(t, s, newTask(tTeamA, ma, "second", 11))
	other := a
	other.TaskSeq = 2
	if _, _, _, err := s.InsertReport(other); !errors.Is(err, ErrReportIDReused) {
		t.Errorf("other task in the same team: err = %v, want ErrReportIDReused", err)
	}
	// A retry in B is B's replay.
	if _, _, replay, err := s.InsertReport(b); err != nil || !replay {
		t.Fatalf("B's own retry: replay=%v err=%v", replay, err)
	}
}

// A late or out-of-order report still takes effect on status and metadata,
// but it never moves the stamps backwards.
func TestInsertReport_OlderReportNeverMovesStampsBackwards(t *testing.T) {
	s, m := reportFixture(t)
	mustInsertReport(t, s, newReport(tTeamA, 1, m, team.ReportDone, 1, 200))

	// A progress report stamped earlier arrives afterwards.
	row, task, replay := mustInsertReport(t, s, newReport(tTeamA, 1, m, team.ReportProgress, 2, 100))
	if replay || row.CreatedAt != 100 {
		t.Fatalf("the late report must be stored as it is: replay=%v row=%+v", replay, row)
	}
	if _, ok, _ := s.GetReport(tTeamA, reportID(2)); !ok {
		t.Fatal("the late report was not stored")
	}
	if task.Status != team.TaskCompleted || task.LastReportKind != "done" || task.LastReportAt != 200 || task.UpdatedAt != 200 {
		t.Fatalf("a late progress moved the stamps: %+v", task)
	}

	// Its metadata append still applies.
	_, task, _ = mustInsertReport(t, s, newReport(tTeamA, 1, m, team.ReportReady, 3, 150))
	if !reflect.DeepEqual(task.Metadata.PRs, []int{12}) || task.LastReportKind != "done" || task.UpdatedAt != 200 {
		t.Fatalf("a late ready: prs=%v last=%s updated=%d", task.Metadata.PRs, task.LastReportKind, task.UpdatedAt)
	}
	if db := mustGetTask(t, s, tTeamA, 1); !reflect.DeepEqual(db, task) {
		t.Fatalf("stored task differs:\n%+v\n%+v", task, db)
	}

	// Its status effect still applies too: a late ack starts a pending task
	// whose newest report is a later progress.
	s2, m2 := reportFixture(t)
	mustInsertReport(t, s2, newReport(tTeamA, 1, m2, team.ReportProgress, 1, 200))
	_, task, _ = mustInsertReport(t, s2, newReport(tTeamA, 1, m2, team.ReportAck, 2, 120))
	if task.Status != team.TaskInProgress || task.LastReportKind != "progress" || task.LastReportAt != 200 || task.UpdatedAt != 200 {
		t.Fatalf("a late ack: %+v", task)
	}

	// The same millisecond: the later write wins the stamp.
	_, task, _ = mustInsertReport(t, s2, newReport(tTeamA, 1, m2, team.ReportQuestion, 3, 200))
	if task.LastReportKind != "question" || task.LastReportAt != 200 || task.UpdatedAt != 200 {
		t.Fatalf("same-millisecond report did not win: %+v", task)
	}
	_, task, _ = mustInsertReport(t, s2, newReport(tTeamA, 1, m2, team.ReportProgress, 4, 200))
	if task.LastReportKind != "progress" {
		t.Fatalf("same-millisecond report did not win again: %+v", task)
	}
}

func TestInsertReport_FinishedTaskStoresNoStatusChange(t *testing.T) {
	finish := map[string]func(t *testing.T, s *Store){
		"completed": func(t *testing.T, s *Store) {
			if _, err := s.SetTaskStatus(tTeamA, 1, team.TaskCompleted, team.TaskByLead, 20); err != nil {
				t.Fatal(err)
			}
		},
		"deleted": func(t *testing.T, s *Store) {
			if _, err := s.SetTaskStatus(tTeamA, 1, team.TaskDeleted, team.TaskByLead, 20); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, do := range finish {
		for _, k := range []team.ReportKind{team.ReportAck, team.ReportProgress, team.ReportReady, team.ReportMerged, team.ReportDone} {
			t.Run(name+"/"+string(k), func(t *testing.T) {
				s, m := reportFixture(t)
				do(t, s)
				want := team.TaskStatus(name)

				row, task, replay := mustInsertReport(t, s, newReport(tTeamA, 1, m, k, 1, 100))
				if replay || row.ID != reportID(1) {
					t.Fatalf("the report must be stored: replay=%v row=%+v", replay, row)
				}
				if task.Status != want {
					t.Fatalf("a %s report moved a %s task to %s", k, want, task.Status)
				}
				if task.LastReportKind != string(k) || task.LastReportAt != 100 || task.UpdatedAt != 100 {
					t.Fatalf("last report not stamped on a %s task: %+v", want, task)
				}
				switch k {
				case team.ReportReady:
					if !reflect.DeepEqual(task.Metadata.PRs, []int{12}) {
						t.Fatalf("ready on a %s task: prs=%v, the append still applies", want, task.Metadata.PRs)
					}
				case team.ReportMerged:
					if !reflect.DeepEqual(task.Metadata.SHAs, []string{"abcdef1"}) {
						t.Fatalf("merged on a %s task: shas=%v, the append still applies", want, task.Metadata.SHAs)
					}
				}
				if db := mustGetTask(t, s, tTeamA, 1); db.Status != want {
					t.Fatalf("stored status = %s, want %s", db.Status, want)
				}
			})
		}
	}
}

func TestInsertReport_DoneFromPending(t *testing.T) {
	s, m := reportFixture(t)
	// The manual path refuses it for an owner; the report is authoritative.
	if _, err := s.SetTaskStatus(tTeamA, 1, team.TaskCompleted, team.TaskByOwner, 15); !errors.Is(err, ErrBadTaskTransition) {
		t.Fatalf("setup: an owner's manual pending->completed must be refused, got %v", err)
	}
	_, task, _ := mustInsertReport(t, s, newReport(tTeamA, 1, m, team.ReportDone, 1, 100))
	if task.Status != team.TaskCompleted {
		t.Fatalf("done from pending: status %s, want completed", task.Status)
	}
}

func TestInsertReport_UnknownTask(t *testing.T) {
	s, m := reportFixture(t) // team A task 1
	mb := seedTaskTeam(t, s, tTeamB, "lead-b", "op-b", "sess-b")

	// No such seq.
	if _, _, _, err := s.InsertReport(newReport(tTeamA, 99, m, team.ReportAck, 1, 100)); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("seq 99: err = %v, want ErrTaskNotFound", err)
	}
	// Team B has no task 1; team A's task 1 is not reachable through team B.
	if _, _, _, err := s.InsertReport(newReport(tTeamB, 1, mb, team.ReportAck, 2, 100)); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("another team's seq: err = %v, want ErrTaskNotFound", err)
	}
	if a := mustGetTask(t, s, tTeamA, 1); a.Status != team.TaskPending || a.LastReportKind != "" {
		t.Fatalf("a refused report touched team A's task: %+v", a)
	}
	if n := countReports(t, s, tTeamA) + countReports(t, s, tTeamB); n != 0 {
		t.Fatalf("%d report rows stored for a task that is not there", n)
	}
}

// The store does not decide whose task it is (the route does): a report by
// another member of the team is stored as given.
func TestInsertReport_StoreDoesNotCheckOwnership(t *testing.T) {
	s, _ := reportFixture(t)
	seedMember(t, s, "op-2", tTeamA, "sess-2", 2)
	if _, _, replay := mustInsertReport(t, s, newReport(tTeamA, 1, "op-2", team.ReportProgress, 1, 100)); replay {
		t.Fatal("want a fresh insert")
	}
}

func TestInsertReport_RefusesInvalidInput(t *testing.T) {
	s, m := reportFixture(t)
	bad := map[string]func(*ReportRow){
		"no id":          func(r *ReportRow) { r.ID = "" },
		"upper-case id":  func(r *ReportRow) { r.ID = "00000000-0000-4000-8000-00000000000A" },
		"no member":      func(r *ReportRow) { r.MemberKey = "" },
		"no team":        func(r *ReportRow) { r.TeamID = "" },
		"seq 0":          func(r *ReportRow) { r.TaskSeq = 0 },
		"unknown kind":   func(r *ReportRow) { r.Kind = "finished" },
		"stray pr":       func(r *ReportRow) { r.PR = 3 },
		"newline in sum": func(r *ReportRow) { r.Summary = "a\nb" },
	}
	for name, mut := range bad {
		r := newReport(tTeamA, 1, m, team.ReportAck, 1, 100)
		mut(&r)
		if _, _, _, err := s.InsertReport(r); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if n := countReports(t, s, tTeamA); n != 0 {
		t.Fatalf("%d rows stored from invalid input", n)
	}
	if task := mustGetTask(t, s, tTeamA, 1); task.Status != team.TaskPending || task.LastReportKind != "" {
		t.Fatalf("invalid input touched the task: %+v", task)
	}
}

func TestListReports_ScopeOrderSinceLimit(t *testing.T) {
	s := openTestStore(t)
	ma := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	mb := seedTaskTeam(t, s, tTeamB, "lead-b", "op-b", "sess-b")
	mustCreateTask(t, s, newTask(tTeamA, ma, "one", 1))
	mustCreateTask(t, s, newTask(tTeamA, ma, "two", 2))
	mustCreateTask(t, s, newTask(tTeamB, mb, "b one", 1))

	empty, err := s.ListReports(tTeamA, 0, 0, 0)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty list = %#v err=%v, want a non-nil empty slice", empty, err)
	}

	// n: id number; created_at ties on 30 are ordered by insertion, newest first.
	mustInsertReport(t, s, newReport(tTeamA, 1, ma, team.ReportProgress, 1, 10))
	mustInsertReport(t, s, newReport(tTeamA, 2, ma, team.ReportProgress, 2, 20))
	mustInsertReport(t, s, newReport(tTeamA, 1, ma, team.ReportProgress, 3, 30))
	mustInsertReport(t, s, newReport(tTeamA, 2, ma, team.ReportProgress, 4, 30))
	mustInsertReport(t, s, newReport(tTeamB, 1, mb, team.ReportProgress, 5, 25))

	ids := func(rs []ReportRow) []string {
		out := []string{}
		for _, r := range rs {
			out = append(out, r.ID)
		}
		return out
	}
	list := func(seq int, since int64, limit int) []string {
		t.Helper()
		rs, err := s.ListReports(tTeamA, seq, since, limit)
		if err != nil {
			t.Fatal(err)
		}
		return ids(rs)
	}
	eq := func(label string, got, want []string) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", label, got, want)
		}
	}

	eq("whole team, newest first", list(0, 0, 0), []string{reportID(4), reportID(3), reportID(2), reportID(1)})
	eq("task 1", list(1, 0, 0), []string{reportID(3), reportID(1)})
	eq("task 2", list(2, 0, 0), []string{reportID(4), reportID(2)})
	eq("task with no reports", list(9, 0, 0), []string{})
	eq("since is inclusive", list(0, 20, 0), []string{reportID(4), reportID(3), reportID(2)})
	eq("since after everything", list(0, 31, 0), []string{})
	eq("limit 2", list(0, 0, 2), []string{reportID(4), reportID(3)})
	eq("limit 1 with since", list(0, 20, 1), []string{reportID(4)})
	if rs, _ := s.ListReports(tTeamB, 0, 0, 0); len(rs) != 1 || rs[0].ID != reportID(5) {
		t.Errorf("team B sees %v", ids(rs))
	}
}

func TestListReports_LimitIsClampedTo200(t *testing.T) {
	s := openTestStore(t)
	m := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	mustCreateTask(t, s, newTask(tTeamA, m, "busy", 1))
	for i := 1; i <= 205; i++ {
		mustInsertReport(t, s, newReport(tTeamA, 1, m, team.ReportProgress, i, int64(i)))
	}
	for _, c := range []struct{ limit, want int }{{0, 200}, {-5, 200}, {500, 200}, {200, 200}, {1, 1}, {7, 7}} {
		rs, err := s.ListReports(tTeamA, 0, 0, c.limit)
		if err != nil || len(rs) != c.want {
			t.Errorf("limit %d: %d reports err=%v, want %d", c.limit, len(rs), err, c.want)
		}
	}
	rs, _ := s.ListReports(tTeamA, 0, 0, 3)
	if rs[0].ID != reportID(205) || rs[2].ID != reportID(203) {
		t.Errorf("newest first broke: %s … %s", rs[0].ID, rs[2].ID)
	}
}

func TestReportSchema_IdempotentOnReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	s1, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	m := seedTaskTeam(t, s1, tTeamA, "lead-a", "op-a", "sess-a")
	mustCreateTask(t, s1, newTask(tTeamA, m, "kept", 10))
	first := newReport(tTeamA, 1, m, team.ReportReady, 1, 20)
	mustInsertReport(t, s1, first)
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	got, ok, err := s2.GetReport(tTeamA, reportID(1))
	if err != nil || !ok || !reflect.DeepEqual(got, first) {
		t.Fatalf("report after reopen: %+v ok=%v err=%v", got, ok, err)
	}
	if _, task, replay := mustInsertReport(t, s2, first); !replay || !reflect.DeepEqual(task.Metadata.PRs, []int{12}) {
		t.Fatalf("replay after reopen: replay=%v prs=%v", replay, task.Metadata.PRs)
	}
	var idx int
	if err := s2.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'reports_task'`).Scan(&idx); err != nil || idx != 1 {
		t.Fatalf("reports_task index: n=%d err=%v", idx, err)
	}
}
