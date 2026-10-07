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

// stepFrom is the valid update after step from, carrying exactly the facts
// of the step it reaches (done after registered).
func stepFrom(from string, at int64) spawnUpdate {
	switch from {
	case team.StepAccepted:
		return spawnUpdate{Step: team.StepSessionCreated, TmuxID: "$7", TmuxInstance: "inst-1", PaneID: "%9", At: at}
	case team.StepSessionCreated:
		return spawnUpdate{Step: team.StepLaunched, LaunchedAt: at, At: at}
	case team.StepLaunched:
		return spawnUpdate{Step: team.StepRegistered, SessionID: "sid-member", At: at}
	}
	return spawnUpdate{Step: team.StepRegistered, State: team.SpawnDone, At: at}
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
	// A new op starts accepted and running, with the tmux name of its own id,
	// what its runner needs, a valid model and effort, and no milestone yet
	// (R2 finding 3); the request hash is required.
	for name, mutate := range map[string]func(*spawnRow){
		"not a uuid": func(r *spawnRow) { r.ID = "spawn-1" }, "foreign name": func(r *spawnRow) { r.TmuxName = "tm-0000000000" },
		"no team": func(r *spawnRow) { r.TeamID = "" }, "no origin": func(r *spawnRow) { r.OriginSessionID = "" },
		"no cwd": func(r *spawnRow) { r.Cwd = "" }, "past accepted": func(r *spawnRow) { r.Step = team.StepLaunched },
		"already failed": func(r *spawnRow) { r.State = team.SpawnFailed }, "no host": func(r *spawnRow) { r.HostID = "" },
		"no time": func(r *spawnRow) { r.CreatedAt = 0 }, "updated before created": func(r *spawnRow) { r.UpdatedAt = 1 },
		"bad model": func(r *spawnRow) { r.Model = "x;y" }, "bad effort": func(r *spawnRow) { r.Effort = "ultra" },
		"tmux id": func(r *spawnRow) { r.TmuxID = "$1" }, "pane": func(r *spawnRow) { r.PaneID = "%1" },
		"launched": func(r *spawnRow) { r.LaunchedAt = 5 }, "session": func(r *spawnRow) { r.SessionID = "s" },
		"reason": func(r *spawnRow) { r.Reason = team.SpawnReasonAbandoned },
	} {
		r := newSpawn(t, "team-1", 2000)
		mutate(&r)
		if _, _, _, err := s.CreateSpawnOp(r, "h"); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, _, _, err := s.CreateSpawnOp(newSpawn(t, "team-1", 2000), ""); err == nil {
		t.Error("no request hash: want an error")
	}
	noModel := newSpawn(t, "team-1", 2000)
	noModel.Model, noModel.Effort = "", ""
	mustCreateSpawn(t, s, noModel) // both are optional (U20)
	if n, err := s.CountRunningSpawns("team-1", ""); err != nil || n != 2 {
		t.Fatalf("only the two valid ops are stored: %d running, err=%v", n, err)
	}
}

// Each runner step is a CAS from the recorded step on a running op (spec
// §9.3): a second runner or a retry that read the same step loses. Steps
// never go back or skip; only registered ends done; a fail keeps the step.
// Each step must carry exactly its own facts (R2 finding 2): an empty or
// partial payload, or another step's field, is an error.
func TestSpawnStore_AdvanceIsACASOnStep(t *testing.T) {
	s := openTestStore(t)
	op := newSpawn(t, "team-1", 1000)
	mustCreateSpawn(t, s, op)
	created := stepFrom(team.StepAccepted, 1100)
	mustStep(t, s, op.ID, team.StepAccepted, created, true)
	// The loser's ids never land: the DeepEqual after done still sees $7.
	mustStep(t, s, op.ID, team.StepAccepted, spawnUpdate{Step: team.StepSessionCreated, TmuxID: "$8", TmuxInstance: "inst-2", PaneID: "%8", At: 1200}, false)
	for _, bad := range []struct {
		from string
		upd  spawnUpdate
	}{
		{team.StepSessionCreated, spawnUpdate{Step: team.StepRegistered, SessionID: "s", At: 1}},                    // a skip
		{team.StepSessionCreated, spawnUpdate{Step: team.StepAccepted, At: 1}},                                      // back
		{team.StepLaunched, spawnUpdate{Step: "warming", At: 1}},                                                    // unknown
		{team.StepSessionCreated, spawnUpdate{Step: team.StepLaunched, State: team.SpawnFailed, At: 1}},             // FailSpawnOp's job
		{team.StepLaunched, spawnUpdate{Step: team.StepLaunched, State: team.SpawnDone, At: 1}},                     // done before registered
		{team.StepLaunched, spawnUpdate{Step: team.StepRegistered, State: team.SpawnDone, SessionID: "s", At: 1}},   // registered and done at once
		{team.StepAccepted, spawnUpdate{Step: team.StepSessionCreated, At: 1}},                                      // empty payload
		{team.StepAccepted, spawnUpdate{Step: team.StepSessionCreated, TmuxID: "$1", TmuxInstance: "i", At: 1}},     // no pane
		{team.StepSessionCreated, spawnUpdate{Step: team.StepLaunched, At: 1}},                                      // no launched_at
		{team.StepSessionCreated, spawnUpdate{Step: team.StepLaunched, LaunchedAt: 1, TmuxID: "$9", At: 1}},         // another step's field
		{team.StepLaunched, spawnUpdate{Step: team.StepRegistered, At: 1}},                                          // no session
		{team.StepRegistered, spawnUpdate{Step: team.StepRegistered, State: team.SpawnDone, SessionID: "s", At: 1}}, // done carries none
		{team.StepAccepted, func() spawnUpdate { u := created; u.At = 0; return u }()},                              // no time
	} {
		if _, err := s.AdvanceSpawnOp(op.ID, bad.from, bad.upd); err == nil {
			t.Errorf("%s → %+v: want an error", bad.from, bad.upd)
		}
	}
	mustStep(t, s, op.ID, team.StepSessionCreated, stepFrom(team.StepSessionCreated, 1400), true)
	mustStep(t, s, op.ID, team.StepLaunched, stepFrom(team.StepLaunched, 1500), true)
	done := stepFrom(team.StepRegistered, 1600)
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
	for _, from := range []string{team.StepAccepted, team.StepSessionCreated, team.StepLaunched, team.StepRegistered} {
		mustStep(t, s, a4.ID, from, stepFrom(from, 1100), true)
	}
	for _, c := range []struct {
		team, except string
		want         int
	}{{"team-a", "", 2}, {"team-a", a1.ID, 1}, {"team-a", a3.ID, 2}, {"team-b", "", 1}, {"team-b", b1.ID, 0}, {"team-c", "", 0}} {
		if n, err := s.CountRunningSpawns(c.team, c.except); err != nil || n != c.want {
			t.Errorf("count %s except %q = %d, want %d (err %v)", c.team, c.except, n, c.want, err)
		}
	}
	running, err := s.ListRunningSpawnOps(5000)
	var ids []string
	for _, op := range running {
		ids = append(ids, op.ID)
	}
	if want := []string{b1.ID, a1.ID, a2.ID}; err != nil || !reflect.DeepEqual(ids, want) {
		t.Fatalf("running = %v (err %v), want %v (oldest first)", ids, err, want)
	}
	if running, err := openTestStore(t).ListRunningSpawnOps(5000); err != nil || running == nil || len(running) != 0 {
		t.Fatalf("no running op: %v err=%v (never nil)", running, err)
	}
}

// A running row that lost what its step needs (R2 findings 2 and 3: a
// damaged team.db, a writer that bypassed the store) is never resumed:
// advancing it is an error, and ListRunningSpawnOps fails it abandoned
// (and logs it) instead of handing it to the boot runner on every start.
func TestSpawnStore_CorruptRowsAreFailedNotResumed(t *testing.T) {
	s := openTestStore(t)
	good := newSpawn(t, "team-1", 1000)
	mustCreateSpawn(t, s, good)
	mustStep(t, s, good.ID, team.StepAccepted, stepFrom(team.StepAccepted, 1100), true)
	var ids []string
	for _, set := range []string{ // one corruption each, as a SET clause
		`step = 'session_created', tmux_id = '$1', tmux_instance = 'i'`,                             // no pane
		`step = 'launched', tmux_id = '$1', tmux_instance = 'i', pane_id = '%1'`,                    // no launched_at
		`step = 'registered', tmux_id = '$1', tmux_instance = 'i', pane_id = '%1', launched_at = 5`, // no session
		`session_id = 'sid-x'`, `model = 'x;y'`, `effort = 'ultra'`, `step = 'warming'`,
		`tmux_name = 'tm-0000000000'`, `created_at = 0`, `host_id = ''`, `reason = 'abandoned'`,
	} {
		op := newSpawn(t, "team-1", 2000)
		mustCreateSpawn(t, s, op)
		if _, err := s.db.Exec(`UPDATE spawn_ops SET `+set+` WHERE id = ?`, op.ID); err != nil {
			t.Fatal(set, err)
		}
		ids = append(ids, op.ID)
	}
	if won, err := s.AdvanceSpawnOp(ids[0], team.StepSessionCreated, stepFrom(team.StepSessionCreated, 3000)); err == nil || won {
		t.Fatalf("advance of a row without its pane: won=%v err=%v, want an error", won, err)
	}
	for i := 0; i < 2; i++ { // the second read finds them already failed
		running, err := s.ListRunningSpawnOps(5000 + int64(i))
		if err != nil || len(running) != 1 || running[0].ID != good.ID {
			t.Fatalf("read %d: running = %+v err=%v, want the good op alone", i, running, err)
		}
	}
	for i, id := range ids {
		if got, _, _ := s.GetSpawnOp(id); got.State != team.SpawnFailed || got.Reason != team.SpawnReasonAbandoned || got.UpdatedAt != 5000 {
			t.Errorf("corrupt row %d: %+v, want failed abandoned at 5000", i, got)
		}
	}
}
