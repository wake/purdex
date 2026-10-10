// internal/module/team/member_index_test.go
package teammod

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// The unique index on a session's seat (#2152 follow-up): `team_members_one_active` covered state = 'active' only, so a row that
// a kill had claimed (killing) freed its session and a second membership could take it. `team_members_one_member` covers
// 'active' AND 'killing'. The migration swaps them on a team.db written before — keeping every row, resolving the rows that
// would break the new index, and never failing the boot. Every test here opens a real file store.

const (
	oldIndex = "team_members_one_active"
	newIndex = "team_members_one_member"
)

func indexSQL(t *testing.T, db *sql.DB, name string) (string, bool) {
	t.Helper()
	var s sql.NullString
	err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&s)
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return s.String, true
}

// legacyDB opens a store, makes it look like a team.db written before this change (the old index, no new one), fills it with rows
// given as (spawn_op, session, state, updated_at), and closes it. Returns the path.
func legacyDB(t *testing.T, rows ...[4]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "team.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Create(openApproval("tm-1", "sid-lead", 1000), "h1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP INDEX IF EXISTS ` + newIndex + `;
		CREATE UNIQUE INDEX IF NOT EXISTS ` + oldIndex + ` ON team_members (session_id) WHERE state = 'active'`); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		// no unique index in the way for the conflicting rows: drop it, insert, put it back is not possible with duplicates, so the
		// legacy index is dropped while the rows go in and only recreated when it can hold (the tests that need a duplicate
		// active-and-killing pair only have one active row, which the old index allows)
		if _, err := s.db.Exec(`INSERT INTO team_members (spawn_op, team_id, host_id, session_id, ref, cwd, tmux_session, state, created_at, updated_at, origin)
			VALUES (?, 'tm-1', 'h:1', ?, ?, '/w', 'tm-x', ?, 1, ?, 'spawned')`, r[0], r[1], "_"+r[0].(string), r[2], r[3]); err != nil {
			t.Fatalf("legacy row %v: %v", r, err)
		}
	}
	s.Close()
	return path
}

func stateOf(t *testing.T, db *sql.DB, spawnOp string) string {
	t.Helper()
	var st string
	if err := db.QueryRow(`SELECT state FROM team_members WHERE spawn_op = ?`, spawnOp).Scan(&st); err != nil {
		t.Fatalf("row %s: %v", spawnOp, err)
	}
	return st
}

func TestOneMemberIndex_AFreshDBHasTheNewIndexAndNotTheOld(t *testing.T) {
	s := openTestStore(t)
	if got, ok := indexSQL(t, s.db, newIndex); !ok || !strings.Contains(got, "'killing'") || !strings.Contains(got, "'active'") {
		t.Fatalf("new index = %q ok=%v, want one over active and killing", got, ok)
	}
	if _, ok := indexSQL(t, s.db, oldIndex); ok {
		t.Fatal("the old index is still there: the base schema would have to stop creating it, or every boot brings it back")
	}
}

func TestOneMemberIndex_ItCoversActiveAndKilling(t *testing.T) {
	s := openTestStore(t)
	ins := func(op, sid, state string) error {
		_, err := s.db.Exec(`INSERT INTO team_members (spawn_op, team_id, host_id, session_id, ref, cwd, tmux_session, state, created_at, updated_at, origin)
			VALUES (?, 'tm-1', 'h:1', ?, ?, '/w', 'tm-x', ?, 1, 1, 'spawned')`, op, sid, "_"+op, state)
		return err
	}
	if err := ins("a1", "sid-1", "active"); err != nil {
		t.Fatal(err)
	}
	if err := ins("k1", "sid-1", "killing"); err == nil {
		t.Error("a killing row was allowed beside an active one of the same session")
	}
	if err := ins("k2", "sid-2", "killing"); err != nil {
		t.Fatal(err)
	}
	if err := ins("k3", "sid-2", "killing"); err == nil {
		t.Error("two killing rows were allowed for one session")
	}
	if err := ins("a2", "sid-2", "active"); err == nil {
		t.Error("an active row was allowed beside a killing one")
	}
	for i, st := range []string{"released", "gone", "killed", "joining", "failed"} {
		if err := ins("done"+string(rune('a'+i)), "sid-1", st); err != nil {
			t.Errorf("a %s row of an active session was refused: %v", st, err)
		}
	}
}

func TestOneMemberIndex_UpgradeSwapsTheIndexAndKeepsEveryRow(t *testing.T) {
	path := legacyDB(t, [4]any{"a1", "sid-1", "active", 5}, [4]any{"r1", "sid-1", "released", 4}, [4]any{"k1", "sid-2", "killing", 6})
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("open a legacy db: %v", err)
	}
	defer s.Close()
	if _, ok := indexSQL(t, s.db, oldIndex); ok {
		t.Error("the old index survived the upgrade")
	}
	if got, ok := indexSQL(t, s.db, newIndex); !ok || !strings.Contains(got, "'killing'") {
		t.Errorf("new index = %q ok=%v", got, ok)
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM team_members`).Scan(&n)
	if n != 3 || stateOf(t, s.db, "a1") != "active" || stateOf(t, s.db, "k1") != "killing" || stateOf(t, s.db, "r1") != "released" {
		t.Fatalf("rows after the upgrade: %d, a1=%s k1=%s r1=%s", n, stateOf(t, s.db, "a1"), stateOf(t, s.db, "k1"), stateOf(t, s.db, "r1"))
	}
	// and the new index is in force
	if err := s.InsertMember(newMember("dup", "tm-1", "sid-2", "_dup", 9)); err == nil {
		t.Error("a second active row for a killing session was inserted on the upgraded db")
	}
}

// A legacy db may hold the pair the new index forbids (an active row and a killing one of one session, or two killing rows).
// The boot must not fail on it: the active row stays, the killing one ends gone; of two killing rows the newest stays.
func TestOneMemberIndex_ConflictingRowsAreResolvedNeverFatal(t *testing.T) {
	path := legacyDB(t,
		[4]any{"a1", "sid-1", "active", 5}, [4]any{"k1", "sid-1", "killing", 6}, // active + killing: the active one stays
		[4]any{"k2", "sid-2", "killing", 5}, [4]any{"k3", "sid-2", "killing", 8}, // two killing: the newest stays
		[4]any{"k4", "sid-3", "killing", 5},                                      // alone: untouched
		[4]any{"a2", "sid-4", "active", 5}, [4]any{"r2", "sid-4", "released", 6}, // active + released: untouched
	)
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("a db with conflicting rows must still open: %v", err)
	}
	defer s.Close()
	want := map[string]string{"a1": "active", "k1": "gone", "k2": "gone", "k3": "killing", "k4": "killing", "a2": "active", "r2": "released"}
	for op, st := range want {
		if got := stateOf(t, s.db, op); got != st {
			t.Errorf("row %s = %s, want %s", op, got, st)
		}
	}
	var ended int64
	var reason string
	if err := s.db.QueryRow(`SELECT ended_at, end_reason FROM team_members WHERE spawn_op = 'k1'`).Scan(&ended, &reason); err != nil || ended == 0 || reason == "" {
		t.Errorf("the row it ended gone has ended_at=%d end_reason=%q err=%v, want both set", ended, reason, err)
	}
	if _, ok := indexSQL(t, s.db, newIndex); !ok {
		t.Error("the new index was not created after the conflicts were resolved")
	}
}

func TestOneMemberIndex_ARerunChangesNothing(t *testing.T) {
	path := legacyDB(t, [4]any{"a1", "sid-1", "active", 5}, [4]any{"k1", "sid-1", "killing", 6})
	for i := 0; i < 3; i++ {
		s, err := OpenStore(path)
		if err != nil {
			t.Fatalf("open #%d: %v", i+1, err)
		}
		if stateOf(t, s.db, "a1") != "active" || stateOf(t, s.db, "k1") != "gone" {
			t.Fatalf("open #%d: a1=%s k1=%s", i+1, stateOf(t, s.db, "a1"), stateOf(t, s.db, "k1"))
		}
		if _, ok := indexSQL(t, s.db, newIndex); !ok {
			t.Fatalf("open #%d: no new index", i+1)
		}
		if _, ok := indexSQL(t, s.db, oldIndex); ok {
			t.Fatalf("open #%d: the old index is back", i+1)
		}
		s.Close()
	}
}

// If the new index cannot be created for a reason this code did not foresee, the boot still goes on: the swap rolls back whole
// (the old index stays — no unprotected window) and nothing is returned as an error. Here the name is taken by a table.
func TestOneMemberIndex_AFailingCreateRollsBackAndNeverFailsTheBoot(t *testing.T) {
	path := legacyDB(t, [4]any{"a1", "sid-1", "active", 5}, [4]any{"k1", "sid-1", "killing", 6})
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE ` + newIndex + ` (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("a failing index swap must not fail the boot: %v", err)
	}
	defer s.Close()
	if _, ok := indexSQL(t, s.db, oldIndex); !ok {
		t.Error("the old index is gone although the new one could not be created (the swap must roll back whole)")
	}
	if got := stateOf(t, s.db, "k1"); got != "killing" {
		t.Errorf("k1 = %s: the conflict resolution must roll back with the failed swap", got)
	}
}

// The base schema runs at EVERY boot (CREATE … IF NOT EXISTS). If it still named the old index, each boot would create it and the
// migration drop it again — an index that exists only because the schema and the migration fight. Mutation gate: put it back → red.
func TestOneMemberIndex_TheBaseSchemaDoesNotCreateTheOldIndex(t *testing.T) {
	if strings.Contains(teamSchema, oldIndex) && strings.Contains(teamSchema, "CREATE UNIQUE INDEX IF NOT EXISTS "+oldIndex) {
		t.Fatalf("teamSchema creates %s at every boot", oldIndex)
	}
	if strings.Contains(teamSchema, "CREATE UNIQUE INDEX IF NOT EXISTS "+newIndex) {
		t.Fatalf("teamSchema creates %s: it is migrateOneMemberIndex's, together with the resolution of the rows it would reject", newIndex)
	}
}
