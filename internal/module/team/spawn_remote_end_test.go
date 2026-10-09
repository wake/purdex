// internal/module/team/spawn_remote_end_test.go
package teammod

import (
	"net/http"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// #2327: the lead host's team ended while a forwarded spawn was still running here. `end` fails that op (in the
// transaction that ends the team's rows, so the runner cannot register a member after it) and its tmux session goes.
// Mutation gate: no abort / abort without the kill / abort of another team's op → red.

// holdAt blocks the runner of a forwarded op just before it runs step; the returned release waits for the runner to be
// there, then lets it go on.
func holdAt(f *fixture, step string) (waitReached, release func()) {
	reached, gate := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.m.beforeSpawnStep = func(op spawnRow) {
		if op.Step == step {
			once.Do(func() { close(reached); <-gate })
		}
	}
	return func() { <-reached }, func() { close(gate) }
}

// The lead host's half: a host that holds only a running forwarded spawn (no live member row yet) is told `end` too; a
// finished spawn is not a reason.
func TestRemoteEnd_AHostWithOnlyARunningSpawnGetsTheEnd(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	f.remoteSpawn("spawn-n", "hostN", "n")
	f.remoteSpawn("spawn-m", "hostM", "m") // hostM has a live row too: still one end
	f.remoteSpawn("spawn-o", "hostO", "o")
	if _, err := f.m.store.db.Exec(`UPDATE remote_spawns SET state = 'done' WHERE id = 'spawn-o'`); err != nil {
		t.Fatal(err)
	}
	tm := endTeamOf(t, f)
	if ended, err := f.m.store.EndTeamWithCommands(tm, team.TeamEndLeadGone, f.clock.Load(), f.m.leadTuple(tm), f.m.newID); err != nil || !ended {
		t.Fatalf("end = %v %v", ended, err)
	}
	ends := f.commandsOf(CmdEnd)
	if len(ends) != 2 || ends[0].HostID != "hostM" || ends[1].HostID != "hostN" {
		t.Fatalf("ends = %+v, want hostM and hostN", ends)
	}
	// the lead's own record of those ops closes with the team; a finished one is left as it was
	for id, want := range map[string]string{"spawn-n": "failed", "spawn-m": "failed", "spawn-o": "done"} {
		if st, _ := f.spawnState(id); st != want {
			t.Fatalf("%s = %s, want %s", id, st, want)
		}
	}
}

// A kill that fails is not an answered `end`: the lead host retries the same command, which kills it then.
func TestRemoteSpawn_TeamEndRetriesAFailedKill(t *testing.T) {
	f, root := remoteSpawnFixture(t)
	waitReached, releaseRunner := holdAt(f, team.StepLaunched)
	if code, body := f.postCmd(leadPrincipal(), spawnCommand(cmdUUID1, root)); code != http.StatusOK {
		t.Fatalf("spawn = %d %s", code, body)
	}
	waitReached()
	f.tmux.FailKillIfInstance = true
	if code, body := f.postCmd(leadPrincipal(), endOf(cmdUUID3, "team-L")); code != http.StatusServiceUnavailable {
		t.Fatalf("end with a failing kill = %d %s, want 503", code, body)
	}
	releaseRunner()
	f.m.spawnWG.Wait()
	f.tmux.FailKillIfInstance = false
	name, _ := team.SpawnTmuxName(cmdUUID1)
	if !f.tmux.HasSession(name) {
		t.Skip("the runner's own cleanup already took the session")
	}
	if code, body := f.postCmd(leadPrincipal(), endOf(cmdUUID3, "team-L")); code != http.StatusOK {
		t.Fatalf("retried end = %d %s", code, body)
	}
	if f.tmux.HasSession(name) {
		t.Fatal("the retried end did not kill the session")
	}
}

func endOf(id, teamID string) team.TeamCommand {
	c := relCmd(id, team.CommandEnd, "")
	c.ToHostID, c.TeamID = "h:1", teamID
	return c
}

func TestRemoteSpawn_TeamEndAbortsAnUnregisteredOp(t *testing.T) {
	for _, step := range []string{team.StepAccepted, team.StepLaunched} {
		t.Run(step, func(t *testing.T) {
			f, root := remoteSpawnFixture(t)
			waitReached, releaseRunner := holdAt(f, step)
			if code, body := f.postCmd(leadPrincipal(), spawnCommand(cmdUUID1, root)); code != http.StatusOK {
				t.Fatalf("spawn = %d %s", code, body)
			}
			waitReached() // the runner is at the step; the end comes while it is there
			// another team's end leaves the op alone
			if code, body := f.postCmd(leadPrincipal(), endOf(cmdUUID2, "team-other")); code != http.StatusOK {
				t.Fatalf("other end = %d %s", code, body)
			}
			if op, _, _ := f.m.store.GetSpawnOp(cmdUUID1); op.State != team.SpawnRunning {
				t.Fatalf("another team's end touched the op: %+v", op)
			}
			if code, body := f.postCmd(leadPrincipal(), endOf(cmdUUID3, "team-L")); code != http.StatusOK {
				t.Fatalf("end = %d %s", code, body)
			}
			releaseRunner()
			f.m.spawnWG.Wait()

			op, _, _ := f.m.store.GetSpawnOp(cmdUUID1)
			if op.State != team.SpawnFailed || op.Reason != team.SpawnReasonAbandoned {
				t.Fatalf("op = %+v, want failed abandoned", op)
			}
			if _, ok, _ := f.m.store.RemoteMember(cmdUUID1); ok {
				t.Fatal("an ended team's spawn registered a remote member")
			}
			name, _ := team.SpawnTmuxName(cmdUUID1)
			if f.tmux.HasSession(name) {
				t.Fatalf("session %s left running (kills %+v)", name, f.tmux.KillIfInstanceCalls())
			}
		})
	}
}
