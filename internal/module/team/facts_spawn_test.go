// internal/module/team/facts_spawn_test.go
package teammod

import (
	"errors"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// The lead host's side of a forwarded spawn (cross-host team spec §4.5, §9 X4b; plan X4b-1): the `registered` and
// `spawn_failed` facts bind to the pending remote_spawns op {id = mk, host_id = the principal's, team_id}; the op holds a
// seat while it runs; registered writes the member row (active) and the first task in the transaction that logs the fact.

var errInjected = errors.New("injected crash")

func (f *fixture) remoteSpawn(id, host, subject string) {
	f.t.Helper()
	r := remoteSpawnRow{ID: id, TeamID: uid(1), HostID: host, OriginSessionID: "sid-1", Cwd: "/w/r", Title: "worker", Model: "sonnet",
		Effort: "low", TaskSubject: subject, TaskDescription: "brief", TaskDoneJSON: `["it works"]`, State: remoteSpawnRunning, CreatedAt: f.clock.Load(), UpdatedAt: f.clock.Load()}
	if _, err := f.m.store.db.Exec(remoteSpawnInsertSQL, r.insertArgs()...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) spawnState(id string) (state, reason string) {
	f.t.Helper()
	if err := f.m.store.db.QueryRow(`SELECT state, reason FROM remote_spawns WHERE id = ?`, id).Scan(&state, &reason); err != nil {
		f.t.Fatal(err)
	}
	return
}

func registeredFact(id, op string) team.TeamFact {
	return team.TeamFact{ID: id, Kind: team.FactRegistered, ToHostID: "h:1", TeamID: uid(1), MK: op, MemberSession: "msid-9", Ref: "_mref99",
		PID: 4242, ProcStart: "Mon Oct 12 10:00:00 2026", Pane: "%7", Title: "worker"}
}

func spawnFailedFact(id, op, reason string) team.TeamFact {
	return team.TeamFact{ID: id, Kind: team.FactSpawnFailed, ToHostID: "h:1", TeamID: uid(1), MK: op, Reason: reason}
}

func (f *fixture) seats() int {
	f.t.Helper()
	n, err := seatsTaken(f.m.store.db, uid(1), "")
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

// A running forwarded op holds a seat (spec §4.2 seat rule). Mutation gate: drop the remote_spawns term → red.
func TestRemoteSpawn_ARunningOpHoldsASeatUntilItEnds(t *testing.T) {
	f := factFixture(t)
	before := f.seats()
	f.remoteSpawn("op1", "lead:1", "")
	if got := f.seats(); got != before+1 {
		t.Fatalf("seats %d → %d, want +1 for a running forwarded op", before, got)
	}
	if code, body := f.postFact(leadPrincipal(), spawnFailedFact(factUUID1, "op1", team.SpawnReasonCreateFailed)); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if got := f.seats(); got != before {
		t.Fatalf("seats = %d after the op failed, want %d", got, before)
	}
}

func TestRemoteSpawn_RegisteredWritesTheActiveMemberAndTheFirstTask(t *testing.T) {
	f := factFixture(t)
	f.remoteSpawn("op1", "lead:1", "build it")
	before := f.seats()
	code, body := f.postFact(leadPrincipal(), registeredFact(factUUID1, "op1"))
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var state, sid, ref, host, mk, pane string
	var pid int
	if err := f.m.store.db.QueryRow(`SELECT state, session_id, ref, host_id, mk, pane_id, pid FROM team_members WHERE spawn_op = 'op1'`).
		Scan(&state, &sid, &ref, &host, &mk, &pane, &pid); err != nil {
		t.Fatal(err)
	}
	if state != "active" || sid != "msid-9" || ref != "_mref99" || host != "lead:1" || mk != "op1" || pane != "%7" || pid != 4242 {
		t.Fatalf("member = %s %s %s %s %s %s %d", state, sid, ref, host, mk, pane, pid)
	}
	if st, _ := f.spawnState("op1"); st != "done" {
		t.Fatalf("op = %s", st)
	}
	if got := f.seats(); got != before {
		t.Fatalf("seats %d → %d: the op's seat and the member's must count once", before, got)
	}
	task, ok, err := f.m.store.TaskBySpawnOp(uid(1), "op1")
	if err != nil || !ok || task.Subject != "build it" || task.OwnerKey != "op1" || len(task.DoneWhen) != 1 {
		t.Fatalf("task = %+v ok=%v err=%v", task, ok, err)
	}
}

func TestRemoteSpawn_RegisteredWithoutATaskCreatesNone(t *testing.T) {
	f := factFixture(t)
	f.remoteSpawn("op1", "lead:1", "")
	if code, body := f.postFact(leadPrincipal(), registeredFact(factUUID1, "op1")); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if _, ok, _ := f.m.store.TaskBySpawnOp(uid(1), "op1"); ok {
		t.Fatal("a task was created for a spawn without one")
	}
}

// §11 crash cut: the member, the task, the op and the log entry are ONE transaction. Mutation gate: log after commit → red.
func TestRemoteSpawn_NothingStaysIfTheLogEntryFails(t *testing.T) {
	f := factFixture(t)
	f.remoteSpawn("op1", "lead:1", "build it")
	f.m.store.failBeforeFactLog = func() error { return errInjected }
	if code, _ := f.postFact(leadPrincipal(), registeredFact(factUUID1, "op1")); code != http.StatusInternalServerError {
		t.Fatalf("status = %d", code)
	}
	var n int
	_ = f.m.store.db.QueryRow(`SELECT COUNT(*) FROM team_members WHERE spawn_op = 'op1'`).Scan(&n)
	if _, ok, _ := f.m.store.TaskBySpawnOp(uid(1), "op1"); n != 0 || ok {
		t.Fatalf("a failed fact left %d member row(s) / task %v behind", n, ok)
	}
	if st, _ := f.spawnState("op1"); st != "running" {
		t.Fatalf("op = %s", st)
	}
	f.m.store.failBeforeFactLog = nil
	if code, body := f.postFact(leadPrincipal(), registeredFact(factUUID1, "op1")); code != 200 {
		t.Fatalf("retry = %d %s", code, body)
	}
}

func TestRemoteSpawn_SpawnFailedEndsTheOpWithItsReason(t *testing.T) {
	f := factFixture(t)
	f.remoteSpawn("op1", "lead:1", "build it")
	if code, body := f.postFact(leadPrincipal(), spawnFailedFact(factUUID1, "op1", team.SpawnReasonStartTimeout)); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if st, reason := f.spawnState("op1"); st != "failed" || reason != team.SpawnReasonStartTimeout {
		t.Fatalf("op = %s{%s}", st, reason)
	}
	if _, ok, _ := f.m.store.TaskBySpawnOp(uid(1), "op1"); ok {
		t.Fatal("a failed spawn created a task")
	}
	// an unknown reason is a malformed fact, not stored
	if code, body := f.postFact(leadPrincipal(), spawnFailedFact(factUUID2, "op1", "made_up")); code != 400 || errCode(t, body) != "bad_request" {
		t.Fatalf("unknown reason = %d %s", code, body)
	}
}

// §4.5 binding: the op is THIS host's, with this id and team; a fact for another host's op, another team's, or no op is
// not_your_member (stored). Mutation gate: drop the host clause → red.
func TestRemoteSpawn_FactsBindToTheOpOfThisHostAndTeam(t *testing.T) {
	f := factFixture(t)
	f.remoteSpawn("op1", "hostN", "")
	f.remoteSpawn("op2", "lead:1", "")
	wrongTeam := registeredFact(factUUID2, "op2")
	wrongTeam.TeamID = uid(2)
	for name, fact := range map[string]team.TeamFact{
		"another host's op": registeredFact(factUUID1, "op1"),
		"another team":      wrongTeam,
		"unknown op":        spawnFailedFact(factUUID3, "nope", team.SpawnReasonCreateFailed),
	} {
		if code, body := f.postFact(leadPrincipal(), fact); code != 409 || errCode(t, body) != "not_your_member" {
			t.Fatalf("%s: %d %s", name, code, body)
		}
	}
	for _, id := range []string{"op1", "op2"} {
		if st, _ := f.spawnState(id); st != "running" {
			t.Fatalf("op %s = %s: a fact moved an op it is not bound to", id, st)
		}
	}
}

// Rule 5: monotonic. An op that is no longer running (done, failed by a void or an unpairing) is not moved by a late fact;
// a team that ended is left as it ended (D4).
func TestRemoteSpawn_LateFactsFindTheOpPastRunningAndAreIgnored(t *testing.T) {
	f := factFixture(t)
	f.remoteSpawn("op1", "lead:1", "")
	f.remoteSpawn("op2", "lead:1", "")
	if _, err := f.m.store.db.Exec(`UPDATE remote_spawns SET state = 'failed', reason = 'remote_unreachable' WHERE id = 'op1'`); err != nil {
		t.Fatal(err)
	}
	if code, body := f.postFact(leadPrincipal(), registeredFact(factUUID1, "op1")); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var n int
	_ = f.m.store.db.QueryRow(`SELECT COUNT(*) FROM team_members WHERE spawn_op = 'op1'`).Scan(&n)
	if st, _ := f.spawnState("op1"); st != "failed" || n != 0 {
		t.Fatalf("a late registered moved a failed op: %s, %d member rows (mutation: no running CAS → red)", st, n)
	}
	if _, err := f.m.store.db.Exec(`UPDATE teams SET ended_at = 5 WHERE id = ?`, uid(1)); err != nil {
		t.Fatal(err)
	}
	if code, body := f.postFact(leadPrincipal(), registeredFact(factUUID2, "op2")); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	_ = f.m.store.db.QueryRow(`SELECT COUNT(*) FROM team_members WHERE spawn_op = 'op2'`).Scan(&n)
	if n != 0 {
		t.Fatal("a registered for an ended team made a member")
	}
}

// A registered whose session is already an active member here would break team_members_one_member for good (the fact
// would roll back and be sent forever): the op fails session_conflict instead, as an adopt does.
func TestRemoteSpawn_ASessionAlreadyActiveHereFailsTheOp(t *testing.T) {
	f := factFixture(t)
	f.remoteRow("abc12", "hostN", "mkx", rowActive) // session "sid-abc12"
	f.remoteSpawn("op1", "lead:1", "")
	fact := registeredFact(factUUID1, "op1")
	fact.MemberSession = "sid-abc12"
	if code, body := f.postFact(leadPrincipal(), fact); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if st, reason := f.spawnState("op1"); st != "failed" || reason != "session_conflict" {
		t.Fatalf("op = %s{%s}", st, reason)
	}
}

func TestRemoteSpawn_RegisteredNeedsItsFields(t *testing.T) {
	f := factFixture(t)
	f.remoteSpawn("op1", "lead:1", "")
	for name, mut := range map[string]func(*team.TeamFact){
		"no session":    func(x *team.TeamFact) { x.MemberSession = "" },
		"no ref":        func(x *team.TeamFact) { x.Ref = "" },
		"no pid":        func(x *team.TeamFact) { x.PID = 0 },
		"no proc_start": func(x *team.TeamFact) { x.ProcStart = "" },
	} {
		fact := registeredFact(map[string]string{"no session": factUUID1, "no ref": factUUID2, "no pid": factUUID3, "no proc_start": factUUID4}[name], "op1")
		mut(&fact)
		if code, body := f.postFact(leadPrincipal(), fact); code != 400 || errCode(t, body) != "bad_request" {
			t.Fatalf("%s: %d %s", name, code, body)
		}
	}
}

// §3.2: an unpaired host fails its running forwarded ops (reason = the unpairing's), in the same transaction as the rest.
func TestRemoteSpawn_UnpairingFailsTheRunningOpsOfThatHost(t *testing.T) {
	f := factFixture(t)
	f.remoteSpawn("op1", "hostM", "")
	f.remoteSpawn("op2", "hostN", "")
	if err := f.m.unpairHost("hostM", "unpaired"); err != nil {
		t.Fatal(err)
	}
	if st, reason := f.spawnState("op1"); st != "failed" || reason != "unpaired" {
		t.Fatalf("op1 = %s{%s}", st, reason)
	}
	if st, _ := f.spawnState("op2"); st != "running" {
		t.Fatalf("op2 = %s: another host's op was failed", st)
	}
}

// §3.3: a spawn command not done within 10 minutes is voided, and its op fails remote_unreachable, freeing the seat.
func TestRemoteSpawn_TheTenMinuteVoidFailsTheOp(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteSpawn("op1", "hostM", "")
	c := f.cmd("op1", CmdSpawn, "hostM", "op1")
	f.enqueue(c)
	f.clock.Add(commandExpiryMS + 1)
	f.m.expireCommands()
	if st, reason := f.spawnState("op1"); st != "failed" || reason != "remote_unreachable" {
		t.Fatalf("op = %s{%s}", st, reason)
	}
	if len(f.commandsOf(CmdVoid)) != 1 {
		t.Fatalf("%d void commands", len(f.commandsOf(CmdVoid)))
	}
}

// D4: an ended team's ops are left as they were; a late spawn_failed is answered and logged, not applied.
// Mutation gate: drop the live-team condition → red.
func TestRemoteSpawn_SpawnFailedOfAnEndedTeamIsIgnored(t *testing.T) {
	f := factFixture(t)
	f.remoteSpawn("op1", "lead:1", "")
	if _, err := f.m.store.db.Exec(`UPDATE teams SET ended_at = 5 WHERE id = ?`, uid(1)); err != nil {
		t.Fatal(err)
	}
	if code, body := f.postFact(leadPrincipal(), spawnFailedFact(factUUID1, "op1", team.SpawnReasonCreateFailed)); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if st, _ := f.spawnState("op1"); st != "running" {
		t.Fatalf("op = %s: a fact moved the op of an ended team", st)
	}
	if factLogCount(t, f) != 1 {
		t.Fatal("the ignored fact was not logged")
	}
}
