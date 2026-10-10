// internal/module/team/spawn_residue_test.go
package teammod

import (
	"errors"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// #2384 points 1 and 3: how a local spawn ends when its team ends or when a runner aborts.
// Mutation gates: endTeamTx without the spawn_ops update / a runner that does not reap its failed op / abortSpawn that
// kills before it wins the fail → red.

func taskedOp(op *spawnRow) {
	op.TaskSubject, op.TaskDescription, op.TaskDoneJSON = "接 U1-3", "照 plan 做", `["PR merged"]`
}

// A REAL EndTeam while the runner is at a step: the op ends failed with the team (no member, no task), and the session
// the runner left running is reaped. The negative: another team's op is not touched.
func TestSpawnTeamEnd_FailsTheRunningOpAndTheRunnerReapsItsSession(t *testing.T) {
	for _, step := range []string{team.StepAccepted, team.StepLaunched, team.StepRegistered} {
		t.Run(step, func(t *testing.T) {
			f, root := newSpawnFixture(t, 2)
			f.register("%0", "sid-m1")
			waitReached, release := holdAt(f, step)
			id := f.acceptOp(1, root, taskedOp)
			f.m.startSpawn(id)
			waitReached()
			ended, err := f.m.store.EndTeam(uid(1), "sid-1", team.TeamEndLeadGone, f.clock.Load())
			if err != nil || !ended {
				t.Fatalf("end team: %v %v", ended, err)
			}
			if op, _, _ := f.m.store.GetSpawnOp(id); op.State != team.SpawnFailed || op.Reason != team.SpawnReasonAbandoned {
				t.Fatalf("op right after the end = %+v, want failed abandoned (in the end's own transaction)", op)
			}
			release()
			f.m.spawnWG.Wait()
			f.assertNoMemberNoTask("team ended")
			name, _ := team.SpawnTmuxName(id)
			if f.tmux.HasSession(name) {
				t.Fatalf("session %s left running (kills %+v)", name, f.tmux.KillIfInstanceCalls())
			}
		})
	}
}

// A POST that waits on the op is answered when the team's end failed it, not after the long poll runs out.
func TestSpawnTeamEnd_WakesTheWaitingRequest(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.m.spawnWait = 30 * time.Second
	f.register("%0", "sid-m1")
	waitReached, release := holdAt(f, team.StepLaunched)
	answered := make(chan team.SpawnOp, 1)
	go func() {
		_, op, _ := f.spawn(1, root, nil)
		answered <- op
	}()
	waitReached()
	if ended, err := f.m.store.EndTeam(uid(1), "sid-1", team.TeamEndLeadGone, f.clock.Load()); err != nil || !ended {
		t.Fatalf("end team: %v %v", ended, err)
	}
	release()
	select {
	case op := <-answered:
		if op.State != team.SpawnFailed {
			t.Fatalf("answer = %+v, want failed", op)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting request was not woken when its op was failed by the team end")
	}
}

// Two runners of one op: the one that wins the finish keeps its session when the other one aborts.
func TestSpawnAbort_NeverKillsTheSessionOfAFinishedOp(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.register("%0", "sid-m1")
	waitReached, release := holdAt(f, team.StepRegistered)
	id := f.acceptOp(1, root, taskedOp)
	f.m.startSpawn(id) // runner 1, held just before its finish
	waitReached()
	f.m.startSpawn(id) // runner 2 passes and wins the finish
	waitFor(t, func() bool { op, _, _ := f.m.store.GetSpawnOp(id); return op.State == team.SpawnDone })
	f.m.store.failSpawnDone = func() error { return errors.New("injected: runner 1's finish fails") }
	release()
	f.m.spawnWG.Wait()
	op, _, _ := f.m.store.GetSpawnOp(id)
	if op.State != team.SpawnDone {
		t.Fatalf("op = %+v, want done", op)
	}
	if ms, _ := f.m.store.MembersOf(uid(1)); len(ms) != 1 {
		t.Fatalf("members = %d, want the finished one", len(ms))
	}
	if calls := f.tmux.KillIfInstanceCalls(); len(calls) != 0 {
		t.Fatalf("the finished op's session was killed: %+v", calls)
	}
}

// #2384 point 2: a member row of this spawn_op already exists when the finish runs (left by an older run). The finish
// goes on only when it is the same half-finished member — same team, session, tmux identity, origin, and active; anything
// else rolls the finish back, writes no task, and the abort follows.
func TestSpawnFinish_AnExistingMemberRowIsVerified(t *testing.T) {
	const tmuxID, inst = "$0", "4242:1700000000" // what the fake executor gives the created session
	cases := []struct {
		name    string
		edit    func(*memberRow)
		resumed bool
	}{
		{"same active member", func(m *memberRow) {}, true},
		{"terminal", func(m *memberRow) { m.State = team.MemberGone }, false},
		{"another session", func(m *memberRow) { m.SessionID = "sid-other" }, false},
		{"another tmux identity", func(m *memberRow) { m.TmuxID = "$9" }, false},
		{"another generation", func(m *memberRow) { m.TmuxInstance = "1:1" }, false},
		{"another host", func(m *memberRow) { m.HostID = "h:other" }, false},
		{"another pane", func(m *memberRow) { m.PaneID = "%9" }, false},
		{"another origin", func(m *memberRow) { m.Origin = team.MemberOriginAdopted }, false},
	}
	for _, tasked := range []bool{true, false} {
		for _, c := range cases {
			name := c.name + map[bool]string{true: "/with task", false: "/no task"}[tasked]
			t.Run(name, func(t *testing.T) {
				f, root := newSpawnFixture(t, 2)
				f.register("%0", "sid-m1")
				old := newMember(spawnID(1), uid(1), "sid-m1", "_m1", 1)
				old.TmuxID, old.TmuxInstance, old.PaneID = tmuxID, inst, "%0"
				c.edit(&old)
				if err := f.m.store.InsertMember(old); err != nil {
					t.Fatal(err)
				}
				edit := func(r *team.SpawnRequest) {}
				if tasked {
					edit = withTask
				}
				f.spawn(1, root, edit)
				op, _, _ := f.m.store.GetSpawnOp(spawnID(1))
				tasks, _ := f.m.store.ListTasks(uid(1), "", true)
				if c.resumed {
					if op.State != team.SpawnDone || (tasked && len(tasks) != 1) {
						t.Fatalf("op %+v, %d tasks: the matching half-finished member should be finished", op, len(tasks))
					}
					return
				}
				if op.State != team.SpawnFailed || op.Reason != team.SpawnReasonAbandoned || len(tasks) != 0 {
					t.Fatalf("op %+v, %d tasks: a member row that is not this spawn's must fail the finish", op, len(tasks))
				}
			})
		}
	}
}

// killSpawnSession says whether a kill is settled: a failed kill is not (so an abort does not mark the session done and the
// runner that stops on the op tries again); a generation that moved or a session already gone is.
func TestKillSpawnSession_ReportsWhetherTheKillIsSettled(t *testing.T) {
	f, _ := newSpawnFixture(t, 2)
	f.tmux.FailKillIfInstance = true
	if f.m.killSpawnSession("op", "$0", "4242:1700000000") {
		t.Fatal("a failed kill was reported as settled")
	}
	f.tmux.FailKillIfInstance = false
	if !f.m.killSpawnSession("op", "$7", "4242:1700000000") { // no such session: gone already
		t.Fatal("a session that is already gone was reported as unsettled")
	}
	if !f.m.killSpawnSession("op", "$0", "1:1") { // another generation: not this op's any more
		t.Fatal("a moved generation was reported as unsettled")
	}
}
