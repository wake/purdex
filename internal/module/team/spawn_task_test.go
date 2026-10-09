package teammod

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// Plan T-2: a spawn that carries a task creates it in the transaction that
// inserts the member row.

func withTask(r *team.SpawnRequest) {
	r.Task = &team.SpawnTask{Subject: "接 U1-3", Description: "照 plan 做", DoneWhen: []string{"PR merged", "tests green"}}
}

// The op stores the task, the member registers, and the task exists: owned by
// the member's key, created by the lead, keyed to the spawn, pending; the
// POST answers its display id.
func TestSpawn_TaskCreatedWithTheMemberRow(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.register("%0", "sid-m1")
	code, op, e := f.spawn(1, root, withTask)
	if code != 200 || op.State != team.SpawnDone || op.Member == nil {
		t.Fatalf("spawn = %d %+v %+v", code, op, e)
	}
	task, found, err := f.m.store.TaskBySpawnOp(uid(1), spawnID(1))
	if err != nil || !found {
		t.Fatalf("task of the spawn: %v %v", found, err)
	}
	if task.Seq != 1 || task.Subject != "接 U1-3" || task.Description != "照 plan 做" || task.Status != team.TaskPending ||
		task.OwnerKey != spawnID(1) || task.CreatedByRef != ipeers.RefID("sid-1") || len(task.DoneWhen) != 2 {
		t.Fatalf("task = %+v", task)
	}
	if want := team.TaskDisplayID(uid(1), 1); op.TaskID != want {
		t.Fatalf("task_id = %q, want %q", op.TaskID, want)
	}
}

// The member row and the task are one transaction: when the task cannot be
// written, the member is not either (a member never exists without its task).
// Mutation gate: insert the task after the member's own commit → red.
func TestSpawn_MemberAndTaskAreOneTransaction(t *testing.T) {
	f, _ := newSpawnFixture(t, 2)
	mem := newMember(spawnID(7), uid(1), "sid-m7", ipeers.RefID("sid-m7"), 1)
	mem.State = team.MemberGone // not an active member: createTaskIn refuses the owner
	task := &TaskRow{TeamID: uid(1), Subject: "s", OwnerKey: mem.SpawnOp, CreatedByRef: "_aaaaaa", SpawnOp: mem.SpawnOp, CreatedAt: 1, UpdatedAt: 1}
	if _, err := f.m.store.InsertMemberAndTask(mem, task); err == nil {
		t.Fatal("a task for a member that is not active was created")
	}
	if rows, _ := f.m.store.MembersOf(uid(1)); len(rows) != 0 {
		t.Fatalf("the member survived the failed task: %+v", rows)
	}
}

// A finish that runs again (a crash between the member and the op's own
// advance) adds no second task and answers the first.
func TestSpawn_ReplayCreatesNoSecondTask(t *testing.T) {
	f, _ := newSpawnFixture(t, 2)
	mem := newMember(spawnID(8), uid(1), "sid-m8", ipeers.RefID("sid-m8"), 1)
	task := &TaskRow{TeamID: uid(1), Subject: "s", OwnerKey: mem.SpawnOp, CreatedByRef: "_aaaaaa", SpawnOp: mem.SpawnOp, CreatedAt: 1, UpdatedAt: 1}
	first, err := f.m.store.InsertMemberAndTask(mem, task)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.m.store.InsertMemberAndTask(mem, task)
	if err != nil || second.Seq != first.Seq {
		t.Fatalf("second = %+v %v, first %+v", second, err, first)
	}
	rows, _ := f.m.store.ListTasks(uid(1), "", true)
	if len(rows) != 1 {
		t.Fatalf("tasks = %d, want 1", len(rows))
	}
}

// Without a task nothing changes: no task row, no task_id.
func TestSpawn_WithoutATaskIsUnchanged(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.register("%0", "sid-m1")
	_, op, _ := f.spawn(1, root, nil)
	if op.TaskID != "" {
		t.Fatalf("task_id = %q", op.TaskID)
	}
	if rows, _ := f.m.store.ListTasks(uid(1), "", true); len(rows) != 0 {
		t.Fatalf("tasks = %+v", rows)
	}
}

// The task is part of the spawn's fingerprint: the same id with another task
// is a reuse of the id; the same task joins; a bad task is a 400.
func TestSpawn_TheTaskIsPartOfTheRequestHash(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.holdRunners()
	if code, _, e := f.spawn(1, root, withTask); code != 200 {
		t.Fatalf("first: %d %+v", code, e)
	}
	if code, _, _ := f.spawn(1, root, withTask); code != 200 {
		t.Fatalf("the same task must join, got %d", code)
	}
	if code, _, e := f.spawn(1, root, func(r *team.SpawnRequest) { withTask(r); r.Task.Subject = "別的任務" }); code != http.StatusConflict || e.Error != team.ErrIDConflict {
		t.Fatalf("another task under the same id: %d %+v", code, e)
	}
	if code, _, e := f.spawn(1, root, nil); code != http.StatusConflict || e.Error != team.ErrIDConflict {
		t.Fatalf("no task under the same id: %d %+v", code, e)
	}
	for name, edit := range map[string]func(*team.SpawnRequest){
		"empty subject": func(r *team.SpawnRequest) { r.Task = &team.SpawnTask{} },
		"bad done-when": func(r *team.SpawnRequest) { r.Task = &team.SpawnTask{Subject: "s", DoneWhen: []string{""}} },
	} {
		if code, _, _ := f.spawn(2, root, edit); code != http.StatusBadRequest {
			t.Errorf("%s: code %d, want 400", name, code)
		}
	}
}

// A team.db written before T-2 gets the spawn task columns.
func TestSpawn_OldDatabaseGainsTheTaskColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE spawn_ops (id TEXT PRIMARY KEY, team_id TEXT NOT NULL, host_id TEXT NOT NULL,
		request_hash TEXT NOT NULL, origin_session_id TEXT NOT NULL, cwd TEXT NOT NULL, title TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '', effort TEXT NOT NULL DEFAULT '', tmux_name TEXT NOT NULL,
		tmux_id TEXT NOT NULL DEFAULT '', tmux_instance TEXT NOT NULL DEFAULT '', pane_id TEXT NOT NULL DEFAULT '',
		step TEXT NOT NULL, state TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', session_id TEXT NOT NULL DEFAULT '',
		launched_at INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("open an old team.db: %v", err)
	}
	defer s.Close()
	for _, col := range []string{"task_subject", "task_description", "task_done_json"} {
		if _, found, err := columnType(s.db, "spawn_ops", col); err != nil || !found {
			t.Errorf("column %s missing (%v)", col, err)
		}
	}
}

// pdx task mine is a member's: a lead asking is not_member; a member sees its
// own open tasks.
func TestTaskList_MineIsAMembersOnly(t *testing.T) {
	w := newTaskWorld(t)
	if _, _, e := w.createTask(leadInbox, w.ma.Ref, "own", nil); e.Error != "" {
		t.Fatal(e)
	}
	if _, _, e := w.createTask(leadInbox, w.mb.Ref, "theirs", nil); e.Error != "" {
		t.Fatal(e)
	}
	code, _, e := call[team.TaskList](w.fixture, http.MethodGet, "/api/team/tasks?mine=1&origin_inbox="+leadInbox, nil)
	if code != http.StatusConflict || e.Error != team.ErrNotMember {
		t.Fatalf("lead mine = %d %+v", code, e)
	}
	_, got, _ := call[team.TaskList](w.fixture, http.MethodGet, "/api/team/tasks?mine=1&origin_inbox="+maInbox, nil)
	if len(got.Tasks) != 1 || got.Tasks[0].Subject != "own" {
		t.Fatalf("member mine = %+v", got)
	}
}
