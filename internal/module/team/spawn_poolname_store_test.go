package teammod

import (
	"testing"

	"github.com/wake/purdex/internal/team"
)

// The member name reserved for a spawn (name pool v2 spec §5.2) is stored on its op, so a daemon restart can rebuild the
// reservation from the open ops.

func poolNameOp(t *testing.T, n int, poolName string) spawnRow {
	t.Helper()
	now := int64(1_000_000)
	op := spawnRow{ID: spawnID(n), TeamID: "team-1", HostID: "h:1", OriginSessionID: "sid-1", Cwd: "/w", Title: "t",
		Step: team.StepAccepted, State: team.SpawnRunning, CreatedAt: now, UpdatedAt: now, PoolName: poolName}
	name, err := team.SpawnTmuxName(op.ID)
	if err != nil {
		t.Fatal(err)
	}
	op.TmuxName = name
	return op
}

func TestSpawnPoolName_IsStoredWithTheOp(t *testing.T) {
	s := openTestStore(t)
	op := poolNameOp(t, 1, "mia")
	if _, _, _, err := insertSpawnOp(s.db, op, "hash"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetSpawnOp(op.ID)
	if err != nil || !ok || got.PoolName != "mia" {
		t.Fatalf("op = %+v ok=%v err=%v, want pool_name mia", got, ok, err)
	}
}

// A team.db written before spawn_ops.pool_name existed opens, keeps its rows and reads them as having no name. Running the
// migration twice is not an error. Mutation gate: skip the migration → the select fails (red).
func TestMigrateSpawnPoolName_AddsTheColumnKeepsRowsOnce(t *testing.T) {
	s := openTestStore(t)
	op := poolNameOp(t, 2, "")
	if _, _, _, err := insertSpawnOp(s.db, op, "hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE spawn_ops DROP COLUMN pool_name`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := migrateSpawnPoolName(s.db); err != nil {
		t.Fatal(err)
	}
	if err := migrateSpawnPoolName(s.db); err != nil { // twice: idempotent
		t.Fatal(err)
	}
	got, ok, err := s.GetSpawnOp(op.ID)
	if err != nil || !ok || got.PoolName != "" || got.Cwd != "/w" {
		t.Fatalf("old row = %+v ok=%v err=%v, want it kept with no pool name", got, ok, err)
	}
}
