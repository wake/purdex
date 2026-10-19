package teammod

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The member host of a forwarded spawn (name pool v2 spec §5.2): it reserves exactly the name the lead host chose, stores it on its op,
// and gives it back at every end of the op. A name held here, or one it cannot use, leaves the session to be named by this host's own
// rules, and the spawn goes on.

func poolSpawnCommand(id, cwd, poolName string) team.TeamCommand {
	c := spawnCommand(id, cwd)
	c.PoolName = poolName
	return c
}

func TestRemoteSpawnPoolName_TheMemberHostReservesExactlyTheLeadsChoice(t *testing.T) {
	f, root := remoteSpawnFixture(t)
	n := withNames(f, "")
	f.holdRunners()
	code, body := f.postCmd(leadPrincipal(), poolSpawnCommand(cmdUUID1, root, "mia"))
	if code != http.StatusOK || outcomeState(t, body) != "accepted" {
		t.Fatalf("spawn = %d %s", code, body)
	}
	tmuxName, _ := team.SpawnTmuxName(cmdUUID1)
	if len(n.exact) != 1 || n.exact[0] != (exactCall{"mia", cmdUUID1, tmuxName}) {
		t.Fatalf("ReserveExact calls = %+v, want mia for the command's op and its tmux name", n.exact)
	}
	if op, _, _ := f.m.store.GetSpawnOp(cmdUUID1); op.PoolName != "mia" {
		t.Fatalf("op = %+v, want pool_name mia (a restart rebuilds the reservation from it)", op)
	}
	if got := n.releasedOps(); len(got) != 0 {
		t.Fatalf("released %v while the op runs", got)
	}
}

// A name held here: no reservation, the op is accepted without one. Mutation gate: store the lead's choice anyway → red.
func TestRemoteSpawnPoolName_AHeldOrUnusableNameLeavesTheSpawnWithoutOne(t *testing.T) {
	for name, err := range map[string]error{
		"held here":           ipeers.ErrNameTaken,
		"not a persona code":  ipeers.ErrNameUnknown,
		"the service failing": errors.New("pool busy"),
	} {
		f, root := remoteSpawnFixture(t)
		n := withNames(f, "")
		n.exactErr = err
		f.holdRunners()
		code, body := f.postCmd(leadPrincipal(), poolSpawnCommand(cmdUUID1, root, "mia"))
		if code != http.StatusOK || outcomeState(t, body) != "accepted" {
			t.Fatalf("%s: spawn = %d %s", name, code, body)
		}
		if op, found, _ := f.m.store.GetSpawnOp(cmdUUID1); !found || op.PoolName != "" {
			t.Fatalf("%s: op = %+v found=%v, want it accepted with no pool name", name, op, found)
		}
	}
}

// A command without a name (an older lead host) reserves nothing.
func TestRemoteSpawnPoolName_NoNameNoReservation(t *testing.T) {
	f, root := remoteSpawnFixture(t)
	n := withNames(f, "")
	f.holdRunners()
	if code, body := f.postCmd(leadPrincipal(), spawnCommand(cmdUUID1, root)); code != http.StatusOK {
		t.Fatalf("spawn = %d %s", code, body)
	}
	if len(n.exact) != 0 {
		t.Fatalf("ReserveExact calls = %+v", n.exact)
	}
}

// A command that is refused or fails after the name was reserved gives it back. Mutation gate: no release → red.
func TestRemoteSpawnPoolName_ARefusedCommandReleasesIt(t *testing.T) {
	t.Run("cwd outside the grant", func(t *testing.T) {
		f, _ := remoteSpawnFixture(t)
		n := withNames(f, "")
		f.holdRunners()
		// SpawnCwd is "" for a cwd under no root: the name is not even reserved
		if code, _ := f.postCmd(leadPrincipal(), poolSpawnCommand(cmdUUID1, "/elsewhere", "mia")); code != http.StatusConflict {
			t.Fatalf("spawn = %d, want a refusal", code)
		}
		if len(n.exact) != 0 {
			t.Fatalf("reserved %+v for a command that was going to be refused", n.exact)
		}
	})
	t.Run("expired", func(t *testing.T) {
		f, root := remoteSpawnFixture(t)
		n := withNames(f, "")
		f.holdRunners()
		c := poolSpawnCommand(cmdUUID1, root, "mia")
		c.CreatedAt = 1 // queued long ago
		if code, body := f.postCmd(leadPrincipal(), c); code != http.StatusConflict {
			t.Fatalf("spawn = %d %s, want command_expired", code, body)
		}
		if got := n.releasedOps(); len(got) != 1 || got[0] != cmdUUID1 {
			t.Fatalf("released = %v, want the expired command's name given back", got)
		}
	})
	t.Run("capacity", func(t *testing.T) {
		f, root := remoteSpawnFixture(t)
		n := withNames(f, "")
		f.holdRunners()
		for i := 0; i < maxPerLeadHost; i++ { // the lead host already holds as many as it may
			f.acceptOp(10+i, root, func(o *spawnRow) { o.LeadHostID = "lead:1" })
		}
		if code, body := f.postCmd(leadPrincipal(), poolSpawnCommand(cmdUUID1, root, "mia")); code != http.StatusConflict {
			t.Fatalf("spawn = %d %s, want capacity_exceeded", code, body)
		}
		if got := n.releasedOps(); len(got) != 1 || got[0] != cmdUUID1 {
			t.Fatalf("released = %v, want the refused command's name given back", got)
		}
	})
}

// A replay of a command keeps the name of an op that is still open and gives it back for one that has ended.
func TestRemoteSpawnPoolName_AReplayKeepsAnOpenOpsNameOnly(t *testing.T) {
	f, root := remoteSpawnFixture(t)
	n := withNames(f, "")
	f.holdRunners()
	c := poolSpawnCommand(cmdUUID1, root, "mia")
	if code, body := f.postCmd(leadPrincipal(), c); code != http.StatusOK {
		t.Fatalf("spawn = %d %s", code, body)
	}
	if code, _ := f.postCmd(leadPrincipal(), c); code != http.StatusOK {
		t.Fatalf("replay = %d", code)
	}
	if got := n.releasedOps(); len(got) != 0 {
		t.Fatalf("released %v for the replay of a running op", got)
	}
	f.m.failSpawn(cmdUUID1, team.SpawnReasonCreateFailed)
	n.mu.Lock()
	n.released = nil
	n.mu.Unlock()
	if code, _ := f.postCmd(leadPrincipal(), c); code != http.StatusOK {
		t.Fatalf("replay after the end = %d", code)
	}
	if got := n.releasedOps(); len(got) != 1 || got[0] != cmdUUID1 {
		t.Fatalf("released = %v, want the ended op's name not held again by the replay", got)
	}
}

// The ends of a forwarded op: registered, and the lead host's team ending. (A fail and the other local ends are the same code as a
// local op's and are covered by TestSpawnPoolName_EveryEndReleasesIt.)
func TestRemoteSpawnPoolName_TheEndsOfAForwardedOpReleaseIt(t *testing.T) {
	t.Run("registered", func(t *testing.T) {
		f, root := remoteSpawnFixture(t)
		n := withNames(f, "")
		f.register("%0", "sid-m1")
		if code, body := f.postCmd(leadPrincipal(), poolSpawnCommand(cmdUUID1, root, "mia")); code != http.StatusOK {
			t.Fatalf("spawn = %d %s", code, body)
		}
		f.m.spawnWG.Wait()
		if op, _, _ := f.m.store.GetSpawnOp(cmdUUID1); op.State != team.SpawnDone {
			t.Fatalf("op = %+v, want done", op)
		}
		if got := n.releasedOps(); len(got) != 1 || got[0] != cmdUUID1 {
			t.Fatalf("released = %v, want the registered op's name given back", got)
		}
	})
	t.Run("the lead host ends the team", func(t *testing.T) {
		f, root := remoteSpawnFixture(t)
		n := withNames(f, "")
		f.holdRunners()
		if code, body := f.postCmd(leadPrincipal(), poolSpawnCommand(cmdUUID1, root, "mia")); code != http.StatusOK {
			t.Fatalf("spawn = %d %s", code, body)
		}
		if code, body := f.postCmd(leadPrincipal(), endOf(cmdUUID3, "team-L")); code != http.StatusOK && code != http.StatusServiceUnavailable {
			t.Fatalf("end = %d %s", code, body)
		}
		if got := n.releasedOps(); len(got) != 1 || got[0] != cmdUUID1 {
			t.Fatalf("released = %v, want the abandoned op's name given back", got)
		}
	})
}

// codex R1 on #3532: pool_name is a wire field like the others (256 bytes, UTF-8, no control character): a bad one is a 400, not a spawn
// quietly left without a name. Mutation gate: leave it out of validateCommand → red.
func TestRemoteSpawnPoolName_ABadFieldIsRefused(t *testing.T) {
	for name, bad := range map[string]string{"too long": strings.Repeat("a", 300), "a control character": "mi\x01a"} {
		f, root := remoteSpawnFixture(t)
		n := withNames(f, "")
		f.holdRunners()
		code, body := f.postCmd(leadPrincipal(), poolSpawnCommand(cmdUUID1, root, bad))
		if code != http.StatusBadRequest {
			t.Fatalf("%s: spawn = %d %s, want 400", name, code, body)
		}
		if _, found, _ := f.m.store.GetSpawnOp(cmdUUID1); found || len(n.exact) != 0 {
			t.Fatalf("%s: a refused command left an op (%v) or reserved %+v", name, found, n.exact)
		}
	}
}
