package teammod

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/wake/purdex/internal/team"
)

// newSpawn is a just-accepted spawn op of team teamID, named after its id.
func newSpawn(t *testing.T, teamID string, at int64) spawnRow {
	t.Helper()
	id := uuid.NewString()
	name, err := team.SpawnTmuxName(id)
	if err != nil {
		t.Fatal(err)
	}
	return spawnRow{ID: id, TeamID: teamID, HostID: "h:1", OriginSessionID: "sid-lead", Cwd: "/w/x", Title: "worker",
		Model: "opus[1m]", Effort: "high", TmuxName: name, Step: team.StepAccepted, State: team.SpawnRunning,
		CreatedAt: at, UpdatedAt: at}
}

func mustCreateSpawn(t *testing.T, s *Store, op spawnRow) {
	t.Helper()
	if _, _, inserted, err := s.CreateSpawnOp(op, "hash-"+op.ID); err != nil || !inserted {
		t.Fatalf("create spawn %s: inserted=%v err=%v", op.ID, inserted, err)
	}
}

// mustStep advances op from from with upd and requires the CAS to have won
// (want) or lost (!want) without an error.
func mustStep(t *testing.T, s *Store, id, from string, upd spawnUpdate, want bool) {
	t.Helper()
	if won, err := s.AdvanceSpawnOp(id, from, upd); err != nil || won != want {
		t.Fatalf("advance %s from %s with %+v: won=%v err=%v, want won=%v", id, from, upd, won, err, want)
	}
}

// Spec §9.3: a spawn op is keyed by the client's id. A retry inserts
// nothing and answers the stored row and hash, so the handler can tell a
// retry from another request with the same id (P4-5: join or 409
// id_conflict). spawn_ops is added to a team.db written before it existed.
func TestSpawnStore_CreateIsIdempotentByIDAndHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	old, err := OpenStore(path)
	if err == nil {
		_, err = old.db.Exec(`DROP TABLE spawn_ops`) // a team.db from before P4-4
		old.Close()
	}
	s, err2 := OpenStore(path)
	if err != nil || err2 != nil {
		t.Fatal(err, err2)
	}
	t.Cleanup(func() { s.Close() })

	op := newSpawn(t, "team-1", 1000)
	retry := op
	retry.Title = "other" // must NOT overwrite the stored row
	for i, c := range []struct {
		row      spawnRow
		hash     string
		inserted bool
	}{{op, "h1", true}, {retry, "h1", false}, {retry, "h2", false}} {
		stored, hash, inserted, err := s.CreateSpawnOp(c.row, c.hash)
		if err != nil || inserted != c.inserted || hash != "h1" || !reflect.DeepEqual(stored, op) {
			t.Fatalf("create %d: stored=%+v hash=%q inserted=%v err=%v", i, stored, hash, inserted, err)
		}
	}
	if got, ok, err := s.GetSpawnOp(op.ID); err != nil || !ok || !reflect.DeepEqual(got, op) {
		t.Fatalf("get: %+v ok=%v err=%v", got, ok, err)
	}
	if _, ok, err := s.GetSpawnOp(uuid.NewString()); err != nil || ok {
		t.Fatalf("get unknown: ok=%v err=%v", ok, err)
	}
	// A new op starts accepted and running, with the tmux name of its own id.
	for name, mutate := range map[string]func(*spawnRow){
		"not a uuid": func(r *spawnRow) { r.ID = "spawn-1" }, "foreign name": func(r *spawnRow) { r.TmuxName = "tm-0000000000" },
		"no team": func(r *spawnRow) { r.TeamID = "" }, "no origin": func(r *spawnRow) { r.OriginSessionID = "" },
		"no cwd": func(r *spawnRow) { r.Cwd = "" }, "past accepted": func(r *spawnRow) { r.Step = team.StepLaunched },
		"already failed": func(r *spawnRow) { r.State = team.SpawnFailed },
	} {
		r := newSpawn(t, "team-1", 2000)
		mutate(&r)
		if _, _, _, err := s.CreateSpawnOp(r, "h"); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if n, err := s.CountRunningSpawns("team-1", ""); err != nil || n != 1 {
		t.Fatalf("only the first op is stored: %d running, err=%v", n, err)
	}
}

// Each runner step is a CAS from the recorded step on a running op (spec
// §9.3): a second runner or a retry that read the same step loses. Steps
// never go back or skip; only registered ends done; a fail keeps the step.
func TestSpawnStore_AdvanceIsACASOnStep(t *testing.T) {
	s := openTestStore(t)
	op := newSpawn(t, "team-1", 1000)
	mustCreateSpawn(t, s, op)
	created := spawnUpdate{Step: team.StepSessionCreated, TmuxID: "$7", TmuxInstance: "inst-1", PaneID: "%9", At: 1100}
	mustStep(t, s, op.ID, team.StepAccepted, created, true)
	// The loser's TmuxID never lands: the DeepEqual after done still sees $7.
	mustStep(t, s, op.ID, team.StepAccepted, spawnUpdate{Step: team.StepSessionCreated, TmuxID: "$8", At: 1200}, false)
	for _, bad := range []struct {
		from string
		upd  spawnUpdate
	}{
		{team.StepSessionCreated, spawnUpdate{Step: team.StepRegistered}},                        // a skip
		{team.StepSessionCreated, spawnUpdate{Step: team.StepAccepted}},                          // back
		{team.StepLaunched, spawnUpdate{Step: "warming"}},                                        // unknown
		{team.StepSessionCreated, spawnUpdate{Step: team.StepLaunched, State: team.SpawnFailed}}, // FailSpawnOp's job
		{team.StepLaunched, spawnUpdate{Step: team.StepLaunched, State: team.SpawnDone}},         // done before registered
		{team.StepLaunched, spawnUpdate{Step: team.StepRegistered, State: team.SpawnDone}},       // registered and done at once
	} {
		if _, err := s.AdvanceSpawnOp(op.ID, bad.from, bad.upd); err == nil {
			t.Errorf("%s → %+v: want an error", bad.from, bad.upd)
		}
	}
	mustStep(t, s, op.ID, team.StepSessionCreated, spawnUpdate{Step: team.StepLaunched, LaunchedAt: 1400, At: 1400}, true)
	mustStep(t, s, op.ID, team.StepLaunched, spawnUpdate{Step: team.StepRegistered, SessionID: "sid-member", At: 1500}, true)
	done := spawnUpdate{Step: team.StepRegistered, State: team.SpawnDone, At: 1600}
	mustStep(t, s, op.ID, team.StepRegistered, done, true)
	want := op
	want.Step, want.State, want.TmuxID, want.TmuxInstance, want.PaneID = team.StepRegistered, team.SpawnDone, "$7", "inst-1", "%9"
	want.LaunchedAt, want.SessionID, want.UpdatedAt = 1400, "sid-member", 1600
	if got, _, _ := s.GetSpawnOp(op.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("after done:\n got %+v\nwant %+v", got, want)
	}
	mustStep(t, s, op.ID, team.StepRegistered, done, false) // a done op does not advance …
	if won, err := s.FailSpawnOp(op.ID, team.SpawnReasonStartTimeout, 1700); err != nil || won {
		t.Fatalf("… nor fail: won=%v err=%v", won, err)
	}

	op2 := newSpawn(t, "team-1", 2000)
	mustCreateSpawn(t, s, op2)
	if _, err := s.FailSpawnOp(op2.ID, "tired", 2100); err == nil {
		t.Error("an unknown reason: want an error")
	}
	for i, want := range []bool{true, false} { // a second fail loses
		if won, err := s.FailSpawnOp(op2.ID, []string{team.SpawnReasonCreateFailed, team.SpawnReasonAbandoned}[i], 2100+int64(i)); err != nil || won != want {
			t.Fatalf("fail %d: won=%v err=%v", i, won, err)
		}
	}
	if got, _, _ := s.GetSpawnOp(op2.ID); got.State != team.SpawnFailed || got.Reason != team.SpawnReasonCreateFailed ||
		got.Step != team.StepAccepted || got.UpdatedAt != 2100 {
		t.Fatalf("failed op: %+v", got)
	}
	mustStep(t, s, op2.ID, team.StepAccepted, created, false)           // a failed op does not advance
	mustStep(t, s, uuid.NewString(), team.StepAccepted, created, false) // nor does an unknown one
}

// The limit check (P4-5) counts the running spawns of a team besides the
// one asking; boot resumes every running op, oldest first.
func TestSpawnStore_CountRunningExcludesSelf(t *testing.T) {
	s := openTestStore(t)
	a1, a2, a3, a4 := newSpawn(t, "team-a", 1000), newSpawn(t, "team-a", 1001), newSpawn(t, "team-a", 1002), newSpawn(t, "team-a", 1003)
	b1 := newSpawn(t, "team-b", 999)
	for _, op := range []spawnRow{a1, a2, a3, a4, b1} {
		mustCreateSpawn(t, s, op)
	}
	if won, err := s.FailSpawnOp(a3.ID, team.SpawnReasonStartTimeout, 1100); err != nil || !won {
		t.Fatal(err)
	}
	for _, from := range []string{team.StepAccepted, team.StepSessionCreated, team.StepLaunched} {
		mustStep(t, s, a4.ID, from, spawnUpdate{Step: nextSpawnStep[from], At: 1100}, true)
	}
	mustStep(t, s, a4.ID, team.StepRegistered, spawnUpdate{Step: team.StepRegistered, State: team.SpawnDone, At: 1100}, true)
	for _, c := range []struct {
		team, except string
		want         int
	}{{"team-a", "", 2}, {"team-a", a1.ID, 1}, {"team-a", a3.ID, 2}, {"team-b", "", 1}, {"team-b", b1.ID, 0}, {"team-c", "", 0}} {
		if n, err := s.CountRunningSpawns(c.team, c.except); err != nil || n != c.want {
			t.Errorf("count %s except %q = %d, want %d (err %v)", c.team, c.except, n, c.want, err)
		}
	}
	running, err := s.ListRunningSpawnOps()
	var ids []string
	for _, op := range running {
		ids = append(ids, op.ID)
	}
	if want := []string{b1.ID, a1.ID, a2.ID}; err != nil || !reflect.DeepEqual(ids, want) {
		t.Fatalf("running = %v (err %v), want %v (oldest first)", ids, err, want)
	}
	if running, err := openTestStore(t).ListRunningSpawnOps(); err != nil || running == nil || len(running) != 0 {
		t.Fatalf("no running op: %v err=%v (never nil)", running, err)
	}
}
