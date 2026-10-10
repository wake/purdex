// internal/module/team/spawn_residue_test.go
package teammod

import (
	"errors"
	"testing"

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
