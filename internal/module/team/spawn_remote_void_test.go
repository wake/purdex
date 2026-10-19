// internal/module/team/spawn_remote_void_test.go
package teammod

import (
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// #3537: the lead host gave up on a forwarded spawn it never saw answered (`void`) while the op still ran here. The void
// fails that op (in its own transaction, so the runner cannot register a member after it), gives its reserved member
// name back and stops its tmux session. Mutation gate: refuse the void (the old answer) / no fail / no release / no kill /
// the kill of another host's op → red.

// voidOf is `void command_id` as the lead host sends it for the spawn command target.
func voidOf(id, target string) team.TeamCommand {
	return team.TeamCommand{ID: id, Kind: team.CommandVoid, ToHostID: "h:1", TeamID: "team-L", CommandID: target,
		Lead: team.TeamLead{SessionID: "lead-sid", Ref: "_lead01", Address: "lead/x [lead01]", PID: 7, ProcStart: "ps1"}}
}

func TestRemoteSpawnVoid_AStillRunningOpIsFailedItsNameReleasedAndItsSessionStopped(t *testing.T) {
	for _, step := range []string{team.StepAccepted, team.StepLaunched} {
		t.Run(step, func(t *testing.T) {
			f, root := remoteSpawnFixture(t)
			n := withNames(f, "")
			waitReached, releaseRunner := holdAt(f, step)
			if code, body := f.postCmd(leadPrincipal(), poolSpawnCommand(cmdUUID1, root, "mia")); code != http.StatusOK {
				t.Fatalf("spawn = %d %s", code, body)
			}
			waitReached()
			f.m.rosterSig = make(chan struct{}, 1) // the publisher is not running here: the signal stays for the test to read
			code, body := f.postCmd(leadPrincipal(), voidOf(cmdUUID2, cmdUUID1))
			if code != http.StatusOK || outcomeState(t, body) != "undone" {
				t.Fatalf("void = %d %s, want 200 undone", code, body)
			}
			// by the void itself: the runner is still held and has not looked at the op
			if got := n.releasedOps(); len(got) != 1 || got[0] != cmdUUID1 {
				t.Fatalf("released = %v right after the void, want the voided op's name given back", got)
			}
			select {
			case <-f.m.rosterSig:
			default:
				t.Fatal("the roster was not told the seat is free")
			}
			releaseRunner()
			f.m.spawnWG.Wait()

			op, _, _ := f.m.store.GetSpawnOp(cmdUUID1)
			if op.State != team.SpawnFailed || op.Reason != team.SpawnReasonAbandoned {
				t.Fatalf("op = %+v, want failed abandoned", op)
			}
			if _, ok, _ := f.m.store.RemoteMember(cmdUUID1); ok {
				t.Fatal("a voided spawn registered a remote member")
			}
			name, _ := team.SpawnTmuxName(cmdUUID1)
			if f.tmux.HasSession(name) {
				t.Fatalf("session %s left running (kills %+v)", name, f.tmux.KillIfInstanceCalls())
			}
			// the late copy of the spawn answers command_void and starts nothing
			if code, body := f.postCmd(leadPrincipal(), poolSpawnCommand(cmdUUID1, root, "mia")); code != http.StatusConflict || errCode(t, body) != team.ErrCommandVoided {
				t.Fatalf("late spawn = %d %s, want 409 command_void", code, body)
			}
		})
	}
}

// The session of a voided op is stopped by the void itself, not only by the runner: the runner is held here and never
// gets to look. A kill that fails is not an answered void: the lead host retries it and it is stopped then.
func TestRemoteSpawnVoid_AFailedKillIsRetryable(t *testing.T) {
	f, root := remoteSpawnFixture(t)
	waitReached, releaseRunner := holdAt(f, team.StepLaunched)
	if code, body := f.postCmd(leadPrincipal(), spawnCommand(cmdUUID1, root)); code != http.StatusOK {
		t.Fatalf("spawn = %d %s", code, body)
	}
	waitReached()
	f.tmux.FailKillIfInstance = true
	if code, body := f.postCmd(leadPrincipal(), voidOf(cmdUUID2, cmdUUID1)); code != http.StatusServiceUnavailable {
		t.Fatalf("void with a failing kill = %d %s, want 503", code, body)
	}
	f.tmux.FailKillIfInstance = false
	name, _ := team.SpawnTmuxName(cmdUUID1)
	if !f.tmux.HasSession(name) {
		t.Fatal("the session is gone before the retry: the test does not show who stops it")
	}
	if code, body := f.postCmd(leadPrincipal(), voidOf(cmdUUID2, cmdUUID1)); code != http.StatusOK {
		t.Fatalf("retried void = %d %s", code, body)
	}
	if f.tmux.HasSession(name) {
		t.Fatal("the retried void did not stop the session")
	}
	releaseRunner()
	f.m.spawnWG.Wait()
	// the answer was lost and the lead sends it once more: the session is gone, which is done, not a failure
	if code, body := f.postCmd(leadPrincipal(), voidOf(cmdUUID2, cmdUUID1)); code != http.StatusOK {
		t.Fatalf("replay after the kill = %d %s, want 200", code, body)
	}
}

// The void and the runner's registration race for the same op: both are a compare-and-set on a running op, so exactly
// one wins. Registration first → the member exists and the void is refused (undoing a member is X4a's); the void first →
// no member, ever. Mutation gate: void without the compare-and-set on running → red.
func TestRemoteSpawnVoid_ExactlyOneOfTheVoidAndTheRegistrationWins(t *testing.T) {
	t.Run("the void first", func(t *testing.T) {
		f, root := remoteSpawnFixture(t)
		f.register("%0", "sid-m1")
		waitReached, releaseRunner := holdAt(f, team.StepLaunched)
		if code, body := f.postCmd(leadPrincipal(), spawnCommand(cmdUUID1, root)); code != http.StatusOK {
			t.Fatalf("spawn = %d %s", code, body)
		}
		waitReached() // launched, about to register
		if code, body := f.postCmd(leadPrincipal(), voidOf(cmdUUID2, cmdUUID1)); code != http.StatusOK {
			t.Fatalf("void = %d %s", code, body)
		}
		releaseRunner()
		f.m.spawnWG.Wait()
		if op, _, _ := f.m.store.GetSpawnOp(cmdUUID1); op.State != team.SpawnFailed {
			t.Fatalf("op = %+v, want failed", op)
		}
		if _, ok, _ := f.m.store.RemoteMember(cmdUUID1); ok {
			t.Fatal("the registration won after the void")
		}
		if facts := f.factsToLead(); len(facts) != 0 {
			t.Fatalf("a voided spawn queued facts to the lead host: %+v", facts)
		}
	})
	t.Run("the registration first", func(t *testing.T) {
		f, root := remoteSpawnFixture(t)
		f.register("%0", "sid-m1")
		if code, body := f.postCmd(leadPrincipal(), spawnCommand(cmdUUID1, root)); code != http.StatusOK {
			t.Fatalf("spawn = %d %s", code, body)
		}
		f.m.spawnWG.Wait()
		if op, _, _ := f.m.store.GetSpawnOp(cmdUUID1); op.State != team.SpawnDone {
			t.Fatalf("op = %+v, want done", op)
		}
		code, body := f.postCmd(leadPrincipal(), voidOf(cmdUUID2, cmdUUID1))
		if code != http.StatusBadRequest || errCode(t, body) != team.ErrCommandUnsupportedKind {
			t.Fatalf("void of a registered spawn = %d %s, want 400 command_unsupported_kind", code, body)
		}
		if row, ok, _ := f.m.store.RemoteMember(cmdUUID1); !ok || row.State != remoteActive {
			t.Fatalf("the registered member was touched: %+v %v", row, ok)
		}
	})
}

// A void names one team's op: another team's void leaves it alone and is refused.
func TestRemoteSpawnVoid_IsScopedToItsTeam(t *testing.T) {
	f, root := remoteSpawnFixture(t)
	waitReached, releaseRunner := holdAt(f, team.StepLaunched)
	if code, body := f.postCmd(leadPrincipal(), spawnCommand(cmdUUID1, root)); code != http.StatusOK {
		t.Fatalf("spawn = %d %s", code, body)
	}
	waitReached()
	other := voidOf(cmdUUID2, cmdUUID1)
	other.TeamID = "team-other"
	if code, body := f.postCmd(leadPrincipal(), other); code != http.StatusConflict || errCode(t, body) != team.ErrCommandNotYourMember {
		t.Fatalf("another team's void = %d %s, want 409 command_not_your_member", code, body)
	}
	if op, _, _ := f.m.store.GetSpawnOp(cmdUUID1); op.State != team.SpawnRunning {
		t.Fatalf("another team's void touched the op: %+v", op)
	}
	releaseRunner()
	f.m.spawnWG.Wait()
}
