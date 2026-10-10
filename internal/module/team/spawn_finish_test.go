// internal/module/team/spawn_finish_test.go
package teammod

import (
	"errors"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// #2105: spawnFinish writes the op's registered → done, the member and the spawn's first task in ONE transaction. A finish
// that fails or loses leaves none of the three behind; the abort then reaps the session as before.
// Mutation gate: the done write back outside the member/task transaction → red.

func (f *fixture) assertNoMemberNoTask(label string) {
	f.t.Helper()
	if ms, err := f.m.store.MembersOf(uid(1)); err != nil || len(ms) != 0 {
		f.t.Fatalf("%s: members = %+v err=%v, want none", label, ms, err)
	}
	if ts, err := f.m.store.ListTasks(uid(1), "", true); err != nil || len(ts) != 0 {
		f.t.Fatalf("%s: tasks = %+v err=%v, want none", label, ts, err)
	}
}

// The write of "done" fails: no member, no task, the op failed abandoned and its session killed.
func TestSpawnFinish_AFailedDoneLeavesNoMemberAndNoTask(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.register("%0", "sid-m1")
	f.m.store.failSpawnDone = func() error { return errors.New("injected: done not written") }
	code, op, _ := f.spawn(1, root, withTask)
	if code == 200 && op.State == team.SpawnDone {
		t.Fatalf("spawn = %d %+v, want it failed", code, op)
	}
	got, _, _ := f.m.store.GetSpawnOp(spawnID(1))
	if got.State != team.SpawnFailed || got.Reason != team.SpawnReasonAbandoned {
		t.Fatalf("op = %+v, want failed abandoned", got)
	}
	f.assertNoMemberNoTask("failed done")
	if name, _ := team.SpawnTmuxName(spawnID(1)); f.tmux.HasSession(name) {
		t.Fatalf("session %s left running (kills %+v)", name, f.tmux.KillIfInstanceCalls())
	}
}

// The op is ended by another party between the registration and the finish (an abort, a team end): the finish loses, and no
// member or task is written for an op that is no longer running.
func TestSpawnFinish_LosingToAnAbortLeavesNoMemberAndNoTask(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.register("%0", "sid-m1")
	waitReached, release := holdAt(f, team.StepRegistered)
	id := f.acceptOp(1, root, func(op *spawnRow) {
		op.TaskSubject, op.TaskDescription, op.TaskDoneJSON = "接 U1-3", "照 plan 做", `["PR merged"]`
	})
	f.m.startSpawn(id)
	waitReached()
	if won, err := f.m.store.FailSpawnOp(id, team.SpawnReasonAbandoned, f.clock.Load()); err != nil || !won {
		t.Fatalf("fail op: %v %v", won, err)
	}
	release()
	f.m.spawnWG.Wait()
	f.assertNoMemberNoTask("lost to an abort")
}

// The happy path still writes all three: done, the member and the task.
func TestSpawnFinish_WritesTheOpMemberAndTaskTogether(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.register("%0", "sid-m1")
	code, op, e := f.spawn(1, root, withTask)
	if code != 200 || op.State != team.SpawnDone {
		t.Fatalf("spawn = %d %+v %+v", code, op, e)
	}
	ms, _ := f.m.store.MembersOf(uid(1))
	ts, _ := f.m.store.ListTasks(uid(1), "", true)
	if len(ms) != 1 || len(ts) != 1 {
		t.Fatalf("members %d tasks %d, want 1 and 1", len(ms), len(ts))
	}
}
