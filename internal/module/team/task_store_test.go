package teammod

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

const (
	tTeamA = "3f2a9c01-aaaa-4000-8000-000000000001"
	// tTeamB shares tTeamA's first six hex chars: same display-id prefix.
	tTeamB = "3f2a9c02-bbbb-4000-8000-000000000002"
)

// seedTaskTeam makes a live team with one active member (key mkey, session
// msid) and returns the member key.
func seedTaskTeam(t *testing.T, s *Store, teamID, leadSID, mkey, msid string) string {
	t.Helper()
	seedTeam(t, s, teamID, leadSID, 1)
	seedMember(t, s, mkey, teamID, msid, 1)
	return mkey
}

func newTask(teamID, owner, subject string, at int64) TaskRow {
	return TaskRow{TeamID: teamID, Subject: subject, OwnerKey: owner, CreatedByRef: "_lead01", CreatedAt: at, UpdatedAt: at}
}

func mustCreateTask(t *testing.T, s *Store, row TaskRow) TaskRow {
	t.Helper()
	got, err := s.CreateTask(row)
	if err != nil {
		t.Fatalf("create task %q: %v", row.Subject, err)
	}
	return got
}

func TestCreateTask_SeqIsPerTeamAndMonotonic(t *testing.T) {
	s := openTestStore(t)
	ma := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	mb := seedTaskTeam(t, s, tTeamB, "lead-b", "op-b", "sess-b")

	for want := 1; want <= 3; want++ {
		got := mustCreateTask(t, s, newTask(tTeamA, ma, "a task", int64(10+want)))
		if got.Seq != want || got.Status != team.TaskPending {
			t.Fatalf("team A task %d = seq %d status %s", want, got.Seq, got.Status)
		}
	}
	if got := mustCreateTask(t, s, newTask(tTeamB, mb, "b task", 20)); got.Seq != 1 {
		t.Fatalf("team B starts its own sequence, got seq %d", got.Seq)
	}

	// A deleted task keeps its seq: the next one is never a reuse.
	if _, err := s.SetTaskStatus(tTeamA, 3, team.TaskDeleted, team.TaskByLead, 30); err != nil {
		t.Fatal(err)
	}
	if got := mustCreateTask(t, s, newTask(tTeamA, ma, "after delete", 31)); got.Seq != 4 {
		t.Fatalf("a deleted task's seq was reused: got %d, want 4", got.Seq)
	}
}

func TestCreateTask_RoundTripsEveryField(t *testing.T) {
	s := openTestStore(t)
	m := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	mustCreateTask(t, s, newTask(tTeamA, m, "first", 5))
	in := newTask(tTeamA, m, "second", 7)
	in.Description = "line one\nline two"
	in.DoneWhen = []string{"tests green", "pr open"}
	in.BlockedBy = []int{1}
	in.SpawnOp = "spawn-1"
	got := mustCreateTask(t, s, in)
	back, ok, err := s.GetTask(tTeamA, got.Seq)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if back.Subject != "second" || back.Description != in.Description || back.OwnerKey != m ||
		back.CreatedByRef != "_lead01" || back.SpawnOp != "spawn-1" || back.CreatedAt != 7 || back.UpdatedAt != 7 ||
		back.Status != team.TaskPending || back.TeamID != tTeamA || back.Seq != 2 {
		t.Fatalf("round trip lost a field: %+v", back)
	}
	if len(back.DoneWhen) != 2 || back.DoneWhen[1] != "pr open" || len(back.BlockedBy) != 1 || back.BlockedBy[0] != 1 {
		t.Fatalf("json columns: done_when=%v blocked_by=%v", back.DoneWhen, back.BlockedBy)
	}
	first, _, _ := s.GetTask(tTeamA, 1)
	if first.DoneWhen == nil || first.BlockedBy == nil || len(first.DoneWhen) != 0 || len(first.BlockedBy) != 0 {
		t.Fatalf("empty json columns must decode to empty non-nil slices: %+v", first)
	}
	if _, ok, err := s.GetTask(tTeamA, 99); err != nil || ok {
		t.Fatalf("missing task: ok=%v err=%v", ok, err)
	}
}

func TestCreateTask_RefusesInvalidFieldsBeforeTouchingTheDB(t *testing.T) {
	s := openTestStore(t)
	m := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	bad := []TaskRow{
		newTask(tTeamA, m, "", 1),
		newTask(tTeamA, m, "two\nlines", 1),
		func() TaskRow { r := newTask(tTeamA, m, "x", 1); r.DoneWhen = []string{""}; return r }(),
		func() TaskRow { r := newTask(tTeamA, m, "x", 1); r.Description = "a\xff"; return r }(),
		newTask("", m, "x", 1),
		newTask(tTeamA, "", "x", 1),
	}
	for i, r := range bad {
		if _, err := s.CreateTask(r); err == nil {
			t.Errorf("case %d: want an error", i)
		}
	}
	if rows, _ := s.ListTasks(tTeamA, "", true); len(rows) != 0 {
		t.Fatalf("a refused create stored %d rows", len(rows))
	}
}

// Two stores on one file (two connections, as two daemons or two pools
// would be) that both read MAX(seq) before either inserts must still end
// with distinct seqs: the write lock is taken before the read.
func TestCreateTask_TwoConnectionsNeverShareASeq(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	s1, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s1.Close() })
	db2, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db2.Close() })
	s2 := &Store{db: db2}
	m := seedTaskTeam(t, s1, tTeamA, "lead-a", "op-a", "sess-a")

	// A barrier right after the seq read. Serialised creates (the correct
	// behaviour) never both arrive, so the first waits a bounded time.
	var mu sync.Mutex
	arrived := 0
	both := make(chan struct{})
	seam := func() {
		mu.Lock()
		arrived++
		if arrived == 2 {
			close(both)
		}
		mu.Unlock()
		select {
		case <-both:
		case <-time.After(300 * time.Millisecond):
		}
	}
	s1.afterTaskSeqRead, s2.afterTaskSeqRead = seam, seam

	type res struct {
		row TaskRow
		err error
	}
	out := make(chan res, 2)
	for _, st := range []*Store{s1, s2} {
		go func() {
			row, err := st.CreateTask(newTask(tTeamA, m, "racing", 10))
			out <- res{row, err}
		}()
	}
	a, b := <-out, <-out
	if a.err != nil || b.err != nil {
		t.Fatalf("both creates must succeed: %v / %v", a.err, b.err)
	}
	if a.row.Seq == b.row.Seq || a.row.Seq+b.row.Seq != 3 {
		t.Fatalf("seqs = %d and %d, want 1 and 2", a.row.Seq, b.row.Seq)
	}
}

func TestCreateTask_SamePrefixTeamsDoNotCollide(t *testing.T) {
	s := openTestStore(t)
	ma := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	mb := seedTaskTeam(t, s, tTeamB, "lead-b", "op-b", "sess-b")
	if team.TaskDisplayID(tTeamA, 1) != team.TaskDisplayID(tTeamB, 1) {
		t.Fatal("fixture: the two teams must share a display id for seq 1")
	}
	mustCreateTask(t, s, newTask(tTeamA, ma, "in A", 10))
	mustCreateTask(t, s, newTask(tTeamB, mb, "in B", 11))

	a, okA, _ := s.GetTask(tTeamA, 1)
	b, okB, _ := s.GetTask(tTeamB, 1)
	if !okA || !okB || a.Subject != "in A" || b.Subject != "in B" {
		t.Fatalf("lookups crossed teams: A=%+v B=%+v", a, b)
	}
	// A status change in B does not touch A's task of the same seq.
	if _, err := s.SetTaskStatus(tTeamB, 1, team.TaskInProgress, team.TaskByLead, 12); err != nil {
		t.Fatal(err)
	}
	if a, _, _ := s.GetTask(tTeamA, 1); a.Status != team.TaskPending || a.UpdatedAt != 10 {
		t.Fatalf("team A's task changed with team B's: %+v", a)
	}
	// The display id resolves only against the caller's own team prefix.
	id := team.TaskDisplayID(tTeamA, 1)
	if _, ok := team.ParseTaskID(id, "ffffff01-0000-4000-8000-000000000000"); ok {
		t.Fatal("a team with another prefix must not parse the id")
	}
	// A blocker is looked up in the same team only: B has seqs 1-3, A has
	// only 1, so A's next task (seq 2) cannot wait on seq 3.
	mustCreateTask(t, s, newTask(tTeamB, mb, "B's second", 14))
	mustCreateTask(t, s, newTask(tTeamB, mb, "B's third", 15))
	r := newTask(tTeamA, ma, "blocked on B's seq 3", 16)
	r.BlockedBy = []int{3}
	if _, err := s.CreateTask(r); !errors.Is(err, ErrBlockedByUnknown) {
		t.Fatalf("another team's seq 3 is no blocker: err = %v", err)
	}
}

func TestCreateTask_BlockedByUnknownAndCycle(t *testing.T) {
	s := openTestStore(t)
	m := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	mustCreateTask(t, s, newTask(tTeamA, m, "one", 1))
	mustCreateTask(t, s, newTask(tTeamA, m, "two", 2))

	with := func(blockedBy ...int) TaskRow {
		r := newTask(tTeamA, m, "x", 3)
		r.BlockedBy = blockedBy
		return r
	}
	for name, by := range map[string][]int{"missing": {9}, "zero": {0}, "negative": {-1}, "one missing of two": {1, 9}} {
		if _, err := s.CreateTask(with(by...)); !errors.Is(err, ErrBlockedByUnknown) {
			t.Errorf("%s: err = %v, want ErrBlockedByUnknown", name, err)
		}
	}
	// Self reference: the new task's own seq (3).
	if _, err := s.CreateTask(with(3)); !errors.Is(err, ErrBlockedByCycle) {
		t.Errorf("self reference: err = %v, want ErrBlockedByCycle", err)
	}
	// Duplicates are deduped silently; a finished blocker is fine.
	if _, err := s.SetTaskStatus(tTeamA, 1, team.TaskDeleted, team.TaskByLead, 4); err != nil {
		t.Fatal(err)
	}
	got, err := s.CreateTask(with(1, 1, 2))
	if err != nil {
		t.Fatalf("dedupe + deleted blocker: %v", err)
	}
	if len(got.BlockedBy) != 2 || got.BlockedBy[0] != 1 || got.BlockedBy[1] != 2 {
		t.Fatalf("blocked_by = %v, want [1 2]", got.BlockedBy)
	}
	if rows, _ := s.ListTasks(tTeamA, "", true); len(rows) != 3 {
		t.Fatalf("refused creates must store nothing: %d rows", len(rows))
	}
}

// A graph that already points at a seq that does not exist yet (1 -> 3,
// 2 -> 1): creating 3 behind 2 would close the loop 3 -> 2 -> 1 -> 3.
func TestCreateTask_BlockedByCycleThroughExistingEdges(t *testing.T) {
	s := openTestStore(t)
	m := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	for _, row := range []struct {
		seq int
		by  string
	}{{1, "[3]"}, {2, "[1]"}} {
		if _, err := s.db.Exec(`INSERT INTO tasks (team_id, seq, subject, status, owner_key, blocked_by_json, created_by_ref, created_at, updated_at)
			VALUES (?, ?, 'seed', 'pending', ?, ?, '_lead01', 1, 1)`, tTeamA, row.seq, m, row.by); err != nil {
			t.Fatal(err)
		}
	}
	r := newTask(tTeamA, m, "closes the loop", 5)
	r.BlockedBy = []int{2}
	if _, err := s.CreateTask(r); !errors.Is(err, ErrBlockedByCycle) {
		t.Fatalf("err = %v, want ErrBlockedByCycle", err)
	}
	// An acyclic blocker on the same graph is fine.
	ok := newTask(tTeamA, m, "no loop", 5)
	ok.BlockedBy = []int{}
	if _, err := s.CreateTask(ok); err != nil {
		t.Fatal(err)
	}
}

func TestCreateTask_OwnerNotActive(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, tTeamA, "lead-a", 1)
	seedMember(t, s, "op-live", tTeamA, "s-live", 1)
	seedMember(t, s, "op-killed", tTeamA, "s-killed", 1)
	seedMember(t, s, "op-gone", tTeamA, "s-gone", 1)
	seedMember(t, s, "op-released", tTeamA, "s-released", 1)
	if ok, err := s.MarkMemberKilled("op-killed", "s-killed", 2); err != nil || !ok {
		t.Fatalf("kill: ok=%v err=%v", ok, err)
	}
	if ok, err := s.MarkMemberGone("op-gone", "s-gone", 2); err != nil || !ok {
		t.Fatalf("gone: ok=%v err=%v", ok, err)
	}
	if _, err := s.db.Exec(`UPDATE team_members SET state = 'released' WHERE spawn_op = 'op-released'`); err != nil {
		t.Fatal(err)
	}
	seedTeam(t, s, tTeamB, "lead-b", 1)
	seedMember(t, s, "op-other", tTeamB, "s-other", 1)

	for _, key := range []string{"op-killed", "op-gone", "op-released", "op-other", "op-nonexistent"} {
		if _, err := s.CreateTask(newTask(tTeamA, key, "x", 3)); !errors.Is(err, ErrOwnerNotActive) {
			t.Errorf("owner %s: err = %v, want ErrOwnerNotActive", key, err)
		}
	}
	if _, err := s.CreateTask(newTask(tTeamA, "op-live", "x", 3)); err != nil {
		t.Fatalf("an active member of the team owns a task: %v", err)
	}
	if rows, _ := s.ListTasks(tTeamA, "", true); len(rows) != 1 {
		t.Fatalf("refused creates must store nothing: %d rows", len(rows))
	}
}

// The expected table is written out here, not derived from
// team.TaskTransitionAllowed: the store must refuse exactly these.
func TestSetTaskStatus_TransitionTable(t *testing.T) {
	type edge struct {
		from, to team.TaskStatus
		by       team.TaskActor
	}
	ok := map[edge]bool{
		{team.TaskPending, team.TaskInProgress, team.TaskByLead}:    true,
		{team.TaskPending, team.TaskInProgress, team.TaskByOwner}:   true,
		{team.TaskInProgress, team.TaskCompleted, team.TaskByLead}:  true,
		{team.TaskInProgress, team.TaskCompleted, team.TaskByOwner}: true,
		{team.TaskPending, team.TaskCompleted, team.TaskByLead}:     true,
		{team.TaskPending, team.TaskDeleted, team.TaskByLead}:       true,
		{team.TaskInProgress, team.TaskDeleted, team.TaskByLead}:    true,
	}
	all := []team.TaskStatus{team.TaskPending, team.TaskInProgress, team.TaskCompleted, team.TaskDeleted}
	s := openTestStore(t)
	m := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	for _, from := range all {
		for _, to := range all {
			for _, by := range []team.TaskActor{team.TaskByLead, team.TaskByOwner} {
				row := mustCreateTask(t, s, newTask(tTeamA, m, "t", 10))
				if _, err := s.db.Exec(`UPDATE tasks SET status = ? WHERE team_id = ? AND seq = ?`, string(from), tTeamA, row.Seq); err != nil {
					t.Fatal(err)
				}
				got, err := s.SetTaskStatus(tTeamA, row.Seq, to, by, 99)
				want := ok[edge{from, to, by}]
				if want {
					if err != nil || got.Status != to || got.UpdatedAt != 99 {
						t.Errorf("%s -> %s by %s: got %+v err %v, want allowed", from, to, by, got, err)
					}
					continue
				}
				if !errors.Is(err, ErrBadTaskTransition) {
					t.Errorf("%s -> %s by %s: err = %v, want ErrBadTaskTransition", from, to, by, err)
				}
				after, _, _ := s.GetTask(tTeamA, row.Seq)
				if after.Status != from || after.UpdatedAt != 10 {
					t.Errorf("%s -> %s by %s: a refused change touched the row: %+v", from, to, by, after)
				}
			}
		}
	}
	if _, err := s.SetTaskStatus(tTeamA, 9999, team.TaskInProgress, team.TaskByLead, 1); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("missing task: err = %v, want ErrTaskNotFound", err)
	}
	if _, err := s.SetTaskStatus(tTeamB, 1, team.TaskInProgress, team.TaskByLead, 1); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("another team's seq: err = %v, want ErrTaskNotFound", err)
	}
}

func TestListTasks_HidesFinishedUnlessAll(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, tTeamA, "lead-a", 1)
	seedMember(t, s, "op-1", tTeamA, "s-1", 1)
	seedMember(t, s, "op-2", tTeamA, "s-2", 1)
	if rows, err := s.ListTasks(tTeamA, "", false); err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("empty list must be a non-nil empty slice: %v %v", rows, err)
	}
	t1 := mustCreateTask(t, s, newTask(tTeamA, "op-1", "pending 1", 10))
	t2 := mustCreateTask(t, s, newTask(tTeamA, "op-2", "progress 2", 20))
	t3 := mustCreateTask(t, s, newTask(tTeamA, "op-1", "done 3", 30))
	t4 := mustCreateTask(t, s, newTask(tTeamA, "op-2", "deleted 4", 40))
	t5 := mustCreateTask(t, s, newTask(tTeamA, "op-1", "same time as 1", 10))
	mustSet := func(seq int, to team.TaskStatus, at int64) {
		t.Helper()
		if _, err := s.SetTaskStatus(tTeamA, seq, to, team.TaskByLead, at); err != nil {
			t.Fatal(err)
		}
	}
	mustSet(t2.Seq, team.TaskInProgress, 25)
	mustSet(t3.Seq, team.TaskInProgress, 31)
	mustSet(t3.Seq, team.TaskCompleted, 32)
	mustSet(t4.Seq, team.TaskDeleted, 41)
	_ = t1

	seqs := func(owner string, all bool) []int {
		t.Helper()
		rows, err := s.ListTasks(tTeamA, owner, all)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]int, len(rows))
		for i, r := range rows {
			out[i] = r.Seq
		}
		return out
	}
	eq := func(got, want []int) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	// updated_at DESC, then seq DESC: t2(25), t5(10, seq 5), t1(10, seq 1).
	if got := seqs("", false); !eq(got, []int{t2.Seq, t5.Seq, t1.Seq}) {
		t.Errorf("open tasks = %v", got)
	}
	// all: t4(41), t3(32), t2(25), t5, t1.
	if got := seqs("", true); !eq(got, []int{t4.Seq, t3.Seq, t2.Seq, t5.Seq, t1.Seq}) {
		t.Errorf("all tasks = %v", got)
	}
	if got := seqs("op-1", false); !eq(got, []int{t5.Seq, t1.Seq}) {
		t.Errorf("op-1 open = %v", got)
	}
	if got := seqs("op-1", true); !eq(got, []int{t3.Seq, t5.Seq, t1.Seq}) {
		t.Errorf("op-1 all = %v", got)
	}
	if rows, _ := s.ListTasks(tTeamB, "", true); len(rows) != 0 {
		t.Errorf("another team's list leaked %d rows", len(rows))
	}
}

func TestReassignTask(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, tTeamA, "lead-a", 1)
	seedMember(t, s, "op-1", tTeamA, "s-1", 1)
	seedMember(t, s, "op-2", tTeamA, "s-2", 1)
	seedMember(t, s, "op-3", tTeamA, "s-3", 1)
	seedTeam(t, s, tTeamB, "lead-b", 1)
	seedMember(t, s, "op-b", tTeamB, "s-b", 1)
	if _, err := s.MarkMemberKilled("op-3", "s-3", 2); err != nil {
		t.Fatal(err)
	}

	row := mustCreateTask(t, s, newTask(tTeamA, "op-1", "work", 10))
	if _, err := s.SetTaskStatus(tTeamA, row.Seq, team.TaskInProgress, team.TaskByOwner, 11); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReassignTask(tTeamA, row.Seq, "op-2", 12)
	if err != nil || got.OwnerKey != "op-2" || got.Status != team.TaskPending || got.UpdatedAt != 12 {
		t.Fatalf("reassign: %+v err %v", got, err)
	}
	for _, key := range []string{"op-3", "op-b", "op-none"} {
		if _, err := s.ReassignTask(tTeamA, row.Seq, key, 13); !errors.Is(err, ErrOwnerNotActive) {
			t.Errorf("to %s: err = %v, want ErrOwnerNotActive", key, err)
		}
	}
	if after, _, _ := s.GetTask(tTeamA, row.Seq); after.OwnerKey != "op-2" || after.UpdatedAt != 12 {
		t.Errorf("a refused reassign touched the row: %+v", after)
	}
	if _, err := s.ReassignTask(tTeamA, 99, "op-1", 14); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("missing task: err = %v", err)
	}
	for _, st := range []team.TaskStatus{team.TaskCompleted, team.TaskDeleted} {
		if _, err := s.db.Exec(`UPDATE tasks SET status = ? WHERE team_id = ? AND seq = ?`, string(st), tTeamA, row.Seq); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ReassignTask(tTeamA, row.Seq, "op-1", 15); !errors.Is(err, ErrBadTaskTransition) {
			t.Errorf("reassign of a %s task: err = %v, want ErrBadTaskTransition", st, err)
		}
	}
}

func TestReassignTask_SameOwnerIsANoOp(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, tTeamA, "lead-a", 1)
	seedMember(t, s, "op-1", tTeamA, "s-1", 1)
	row := mustCreateTask(t, s, newTask(tTeamA, "op-1", "work", 10))
	if _, err := s.SetTaskStatus(tTeamA, row.Seq, team.TaskInProgress, team.TaskByOwner, 11); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReassignTask(tTeamA, row.Seq, "op-1", 99)
	if err != nil {
		t.Fatalf("same owner: %v", err)
	}
	if got.Status != team.TaskInProgress || got.UpdatedAt != 11 || got.OwnerKey != "op-1" {
		t.Fatalf("returned row = %+v, want the unchanged in_progress row", got)
	}
	if after, _, _ := s.GetTask(tTeamA, row.Seq); after.Status != team.TaskInProgress || after.UpdatedAt != 11 {
		t.Fatalf("stored row changed: %+v", after)
	}
	// A finished task is still refused, same owner or not.
	if _, err := s.db.Exec(`UPDATE tasks SET status = 'completed' WHERE team_id = ? AND seq = ?`, tTeamA, row.Seq); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReassignTask(tTeamA, row.Seq, "op-1", 100); !errors.Is(err, ErrBadTaskTransition) {
		t.Fatalf("completed task, same owner: err = %v, want ErrBadTaskTransition", err)
	}
}

// A COMMIT that fails must not hand its connection back to the pool with
// the transaction still open: the next statement on it would fail with
// "cannot start a transaction within a transaction". The seam fails the
// commit before it runs, which leaves the transaction open on the conn.
func TestImmediateTx_CommitFailureDiscardsTheConnection(t *testing.T) {
	s := openTestStore(t)
	m := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	first := mustCreateTask(t, s, newTask(tTeamA, m, "kept", 5))

	boom := errors.New("simulated commit failure")
	s.beforeTaskCommit = func() error { return boom }
	if _, err := s.CreateTask(newTask(tTeamA, m, "lost", 6)); !errors.Is(err, boom) {
		t.Fatalf("create: err = %v, want the commit failure", err)
	}
	if _, err := s.SetTaskStatus(tTeamA, first.Seq, team.TaskInProgress, team.TaskByLead, 7); !errors.Is(err, boom) {
		t.Fatalf("set status: err = %v, want the commit failure", err)
	}
	s.beforeTaskCommit = nil

	// Several rounds so every pooled connection is exercised.
	for i := 0; i < 4; i++ {
		got, err := s.CreateTask(newTask(tTeamA, m, "after", int64(10+i)))
		if err != nil {
			t.Fatalf("create after a failed commit (round %d): %v", i, err)
		}
		if got.Seq != 2+i {
			t.Fatalf("round %d: seq = %d, want %d (the failed create stored nothing)", i, got.Seq, 2+i)
		}
		if _, err := s.SetTaskStatus(tTeamA, got.Seq, team.TaskInProgress, team.TaskByLead, int64(20+i)); err != nil {
			t.Fatalf("set status after a failed commit (round %d): %v", i, err)
		}
	}
	rows, err := s.ListTasks(tTeamA, "", true)
	if err != nil || len(rows) != 5 {
		t.Fatalf("rows = %d err %v, want 5 (kept + 4)", len(rows), err)
	}
	for _, r := range rows {
		if r.Subject == "lost" {
			t.Fatal("the create whose commit failed was stored")
		}
	}
	if first, _, _ := s.GetTask(tTeamA, first.Seq); first.Status != team.TaskPending {
		t.Fatalf("the status change whose commit failed was stored: %+v", first)
	}
}

func TestTaskSchema_IdempotentOnReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	s1, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	m := seedTaskTeam(t, s1, tTeamA, "lead-a", "op-a", "sess-a")
	mustCreateTask(t, s1, newTask(tTeamA, m, "kept", 10))
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	got, ok, err := s2.GetTask(tTeamA, 1)
	if err != nil || !ok || got.Subject != "kept" {
		t.Fatalf("task after reopen: %+v ok=%v err=%v", got, ok, err)
	}
	if next := mustCreateTask(t, s2, newTask(tTeamA, m, "next", 11)); next.Seq != 2 {
		t.Fatalf("seq after reopen = %d, want 2", next.Seq)
	}
}

// spawn_op is unique when set (T-2 relies on it); empty is unconstrained.
func TestTaskSchema_SpawnOpIsUniqueWhenSet(t *testing.T) {
	s := openTestStore(t)
	m := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	a, b := newTask(tTeamA, m, "a", 1), newTask(tTeamA, m, "b", 1)
	mustCreateTask(t, s, a)
	mustCreateTask(t, s, b) // both spawn_op ''
	a.SpawnOp, b.SpawnOp = "spawn-x", "spawn-x"
	mustCreateTask(t, s, a)
	if _, err := s.CreateTask(b); err == nil {
		t.Fatal("a second task with the same spawn_op must be refused by the unique index")
	}
}
