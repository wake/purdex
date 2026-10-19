// cmd/pdx/spawn_poolname_test.go
package main

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// `pdx spawn` says the member name the team reserved for the spawn (name pool v2 spec §5.2): once, on stderr, as a reservation and
// not a promise. Stdout stays the one JSON line.

func named(f func(team.SpawnRequest) answer, code, display string) func(team.SpawnRequest) answer {
	return func(req team.SpawnRequest) answer {
		a := f(req)
		op := a.body.(team.SpawnOp)
		op.PoolName, op.PoolDisplayName = code, display
		a.body = op
		return a
	}
}

func TestSpawnPoolName_IsPrintedOnceOnStderrAsAReservation(t *testing.T) {
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{
		named(spawnRunning, "freya", "Freya"), named(spawnRunning, "freya", "Freya"), named(spawnDone, "freya", "Freya")}}
	code, stdout, stderr := driveTeamCmd(t, runSpawnCmd, d, "--model", "sonnet")
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if n := strings.Count(stderr, "→ Freya（預訂）"); n != 1 {
		t.Fatalf("stderr = %q, want the reservation said once (%d times)", stderr, n)
	}
	if strings.Contains(stdout, "Freya") || strings.Count(strings.TrimSpace(stdout), "\n") != 0 {
		t.Fatalf("stdout = %q, want the one JSON line only", stdout)
	}
}

func TestSpawnPoolName_NothingReservedSaysNothing(t *testing.T) {
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDone}}
	if code, _, stderr := driveTeamCmd(t, runSpawnCmd, d, "--model", "sonnet"); code != ExitOK || strings.Contains(stderr, "預訂") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

// A daemon that gives the code but not a display name still names it.
func TestSpawnPoolName_FallsBackToTheCode(t *testing.T) {
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{named(spawnDone, "freya", "")}}
	if _, _, stderr := driveTeamCmd(t, runSpawnCmd, d, "--model", "sonnet"); !strings.Contains(stderr, "→ freya（預訂）") {
		t.Fatalf("stderr = %q", stderr)
	}
}
