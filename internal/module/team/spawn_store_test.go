package teammod

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/wake/purdex/internal/team"
)

// newSpawn is a just-accepted spawn op of team teamID: a fresh UUID v4 id
// and the tmux name derived from it.
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

func mustGetSpawn(t *testing.T, s *Store, id string) spawnRow {
	t.Helper()
	got, ok, err := s.GetSpawnOp(id)
	if err != nil || !ok {
		t.Fatalf("get spawn %s: ok=%v err=%v", id, ok, err)
	}
	return got
}

// Spec §9.3: a spawn op is keyed by the client's id. A retry with the same
// id and request inserts nothing and answers the stored row; the same id
// with another request answers the stored hash, so the handler can tell
// the two apart (P4-5: join or 409 id_conflict). spawn_ops is created on
// a team.db written before it existed.
func TestSpawnStore_CreateIsIdempotentByIDAndHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	old, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec(`DROP TABLE spawn_ops`); err != nil { // a team.db from before P4-4
		t.Fatal(err)
	}
	old.Close()
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	op := newSpawn(t, "team-1", 1000)
	stored, hash, inserted, err := s.CreateSpawnOp(op, "h1")
	if err != nil || !inserted || hash != "h1" || !reflect.DeepEqual(stored, op) {
		t.Fatalf("first create: stored=%+v hash=%q inserted=%v err=%v", stored, hash, inserted, err)
	}
	retry := op
	retry.Title = "other" // must NOT overwrite the stored row
	stored2, hash2, inserted2, err := s.CreateSpawnOp(retry, "h1")
	if err != nil || inserted2 || hash2 != "h1" || !reflect.DeepEqual(stored2, op) {
		t.Fatalf("retry: stored=%+v hash=%q inserted=%v err=%v", stored2, hash2, inserted2, err)
	}
	if _, hash3, inserted3, err := s.CreateSpawnOp(retry, "h2"); err != nil || inserted3 || hash3 != "h1" {
		t.Fatalf("same id, other request: hash=%q inserted=%v err=%v (the caller compares h2 != h1)", hash3, inserted3, err)
	}
	if got := mustGetSpawn(t, s, op.ID); !reflect.DeepEqual(got, op) {
		t.Fatalf("get: %+v, want %+v", got, op)
	}
	if _, ok, err := s.GetSpawnOp(uuid.NewString()); err != nil || ok {
		t.Fatalf("get unknown: ok=%v err=%v", ok, err)
	}

	// A new op starts accepted and running, with the tmux name of its own id.
	bad := map[string]func(*spawnRow){
		"not a uuid":     func(r *spawnRow) { r.ID = "spawn-1" },
		"foreign name":   func(r *spawnRow) { r.TmuxName = "tm-0000000000" },
		"no team":        func(r *spawnRow) { r.TeamID = "" },
		"no origin":      func(r *spawnRow) { r.OriginSessionID = "" },
		"no cwd":         func(r *spawnRow) { r.Cwd = "" },
		"past accepted":  func(r *spawnRow) { r.Step = team.StepLaunched },
		"already failed": func(r *spawnRow) { r.State = team.SpawnFailed },
	}
	for name, mutate := range bad {
		r := newSpawn(t, "team-1", 2000)
		mutate(&r)
		if _, _, _, err := s.CreateSpawnOp(r, "h"); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if running, err := s.ListRunningSpawnOps(); err != nil || len(running) != 1 {
		t.Fatalf("only the first op is stored: %d running, err=%v", len(running), err)
	}
}

// Each runner step is a compare-and-set from the recorded step on a running
// op (spec §7.2 step 3, §9.3): a second runner, or a retry after a restart,
// that read the same step loses and changes nothing. A step records only
// the facts it learnt; the others are kept. Steps never go back or skip;
// only registered ends the op done. A fail keeps the step it reached.
func TestSpawnStore_AdvanceIsACASOnStep(t *testing.T) {
	s := openTestStore(t)
	op := newSpawn(t, "team-1", 1000)
	mustCreateSpawn(t, s, op)

	created := spawnUpdate{Step: team.StepSessionCreated, TmuxID: "$7", TmuxInstance: "inst-1", PaneID: "%9", At: 1100}
	if won, err := s.AdvanceSpawnOp(op.ID, team.StepAccepted, created); err != nil || !won {
		t.Fatalf("accepted → session_created: won=%v err=%v", won, err)
	}
	again := created
	again.TmuxID, again.At = "$8", 1200
	if won, err := s.AdvanceSpawnOp(op.ID, team.StepAccepted, again); err != nil || won {
		t.Fatalf("the same CAS twice: won=%v err=%v", won, err)
	}
	got := mustGetSpawn(t, s, op.ID)
	if got.Step != team.StepSessionCreated || got.TmuxID != "$7" || got.TmuxInstance != "inst-1" || got.PaneID != "%9" || got.UpdatedAt != 1100 {
		t.Fatalf("the loser changed the row: %+v", got)
	}

	for _, bad := range []struct{ from, to string }{
		{team.StepSessionCreated, team.StepRegistered}, // a skip
		{team.StepSessionCreated, team.StepAccepted},   // back
		{team.StepLaunched, "warming"},                 // unknown
	} {
		if _, err := s.AdvanceSpawnOp(op.ID, bad.from, spawnUpdate{Step: bad.to, At: 1300}); err == nil {
			t.Errorf("%s → %s: want an error", bad.from, bad.to)
		}
	}
	if _, err := s.AdvanceSpawnOp(op.ID, team.StepSessionCreated, spawnUpdate{Step: team.StepLaunched, State: team.SpawnFailed, At: 1300}); err == nil {
		t.Error("a step cannot fail an op (FailSpawnOp does)")
	}

	if won, err := s.AdvanceSpawnOp(op.ID, team.StepSessionCreated, spawnUpdate{Step: team.StepLaunched, LaunchedAt: 1400, At: 1400}); err != nil || !won {
		t.Fatalf("session_created → launched: won=%v err=%v", won, err)
	}
	if won, err := s.AdvanceSpawnOp(op.ID, team.StepLaunched, spawnUpdate{Step: team.StepRegistered, SessionID: "sid-member", At: 1500}); err != nil || !won {
		t.Fatalf("launched → registered: won=%v err=%v", won, err)
	}
	if _, err := s.AdvanceSpawnOp(op.ID, team.StepLaunched, spawnUpdate{Step: team.StepLaunched, State: team.SpawnDone, At: 1600}); err == nil {
		t.Error("done before registered: want an error")
	}
	if won, err := s.AdvanceSpawnOp(op.ID, team.StepRegistered, spawnUpdate{Step: team.StepRegistered, State: team.SpawnDone, At: 1600}); err != nil || !won {
		t.Fatalf("registered → done: won=%v err=%v", won, err)
	}
	got = mustGetSpawn(t, s, op.ID)
	want := op
	want.Step, want.State, want.TmuxID, want.TmuxInstance, want.PaneID = team.StepRegistered, team.SpawnDone, "$7", "inst-1", "%9"
	want.LaunchedAt, want.SessionID, want.UpdatedAt = 1400, "sid-member", 1600
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after done:\n got %+v\nwant %+v", got, want)
	}
	if won, err := s.AdvanceSpawnOp(op.ID, team.StepRegistered, spawnUpdate{Step: team.StepRegistered, State: team.SpawnDone, At: 1700}); err != nil || won {
		t.Fatalf("a done op does not advance: won=%v err=%v", won, err)
	}
	if won, err := s.FailSpawnOp(op.ID, team.SpawnReasonStartTimeout, 1700); err != nil || won {
		t.Fatalf("a done op does not fail: won=%v err=%v", won, err)
	}

	op2 := newSpawn(t, "team-1", 2000)
	mustCreateSpawn(t, s, op2)
	if _, err := s.FailSpawnOp(op2.ID, "tired", 2100); err == nil {
		t.Error("an unknown reason: want an error")
	}
	if won, err := s.FailSpawnOp(op2.ID, team.SpawnReasonCreateFailed, 2100); err != nil || !won {
		t.Fatalf("fail: won=%v err=%v", won, err)
	}
	if won, err := s.FailSpawnOp(op2.ID, team.SpawnReasonAbandoned, 2200); err != nil || won {
		t.Fatalf("fail twice: won=%v err=%v", won, err)
	}
	if got := mustGetSpawn(t, s, op2.ID); got.State != team.SpawnFailed || got.Reason != team.SpawnReasonCreateFailed ||
		got.Step != team.StepAccepted || got.UpdatedAt != 2100 {
		t.Fatalf("failed op: %+v", got)
	}
	if won, err := s.AdvanceSpawnOp(op2.ID, team.StepAccepted, created); err != nil || won {
		t.Fatalf("a failed op does not advance: won=%v err=%v", won, err)
	}
	if won, err := s.AdvanceSpawnOp(uuid.NewString(), team.StepAccepted, created); err != nil || won {
		t.Fatalf("an unknown op: won=%v err=%v", won, err)
	}
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
	for _, step := range []struct {
		from string
		upd  spawnUpdate
	}{
		{team.StepAccepted, spawnUpdate{Step: team.StepSessionCreated, At: 1100}},
		{team.StepSessionCreated, spawnUpdate{Step: team.StepLaunched, At: 1100}},
		{team.StepLaunched, spawnUpdate{Step: team.StepRegistered, At: 1100}},
		{team.StepRegistered, spawnUpdate{Step: team.StepRegistered, State: team.SpawnDone, At: 1100}},
	} {
		if won, err := s.AdvanceSpawnOp(a4.ID, step.from, step.upd); err != nil || !won {
			t.Fatalf("drive a4 from %s: won=%v err=%v", step.from, won, err)
		}
	}
	for _, c := range []struct {
		team, except string
		want         int
	}{{"team-a", "", 2}, {"team-a", a1.ID, 1}, {"team-a", a3.ID, 2}, {"team-b", "", 1}, {"team-b", b1.ID, 0}, {"team-c", "", 0}} {
		if n, err := s.CountRunningSpawns(c.team, c.except); err != nil || n != c.want {
			t.Errorf("count %s except %q = %d, want %d (err %v)", c.team, c.except, n, c.want, err)
		}
	}
	running, err := s.ListRunningSpawnOps()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, op := range running {
		ids = append(ids, op.ID)
	}
	if want := []string{b1.ID, a1.ID, a2.ID}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("running = %v, want %v (oldest first)", ids, want)
	}
	empty := openTestStore(t)
	if running, err := empty.ListRunningSpawnOps(); err != nil || running == nil || len(running) != 0 {
		t.Fatalf("no running op: %v err=%v (never nil)", running, err)
	}
}
