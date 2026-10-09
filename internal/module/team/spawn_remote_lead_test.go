// internal/module/team/spawn_remote_lead_test.go
package teammod

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// `pdx spawn --host <alias>` on the lead host (cross-host team spec §4.5, §5.5, §7, §9 X4b; plan X4b-2): the capability is
// checked before anything is created (rule 7), the forwarded op and its `spawn` command are written in ONE transaction
// (rule 2), a seat is taken, and the POST answers by the op's fate: running while the member host works, done with the
// remote member and the first task when `registered` arrives, failed with the member host's code.

func leadSpawnFixture(t *testing.T) (*fixture, *fakeHostCaller) {
	t.Helper()
	f, fc := remoteFixture(t)
	f.m.spawnWait = 20 * time.Millisecond
	return f, fc
}

func (f *fixture) spawnRemote(i int, host string, edit func(*team.SpawnRequest)) (int, team.SpawnOp, team.APIError) {
	f.t.Helper()
	return f.spawn(i, "/w/r", func(r *team.SpawnRequest) {
		r.Host = host
		if edit != nil {
			edit(r)
		}
	})
}

func TestSpawnHost_CreatesTheOpAndTheCommandInOneTransaction(t *testing.T) {
	f, _ := leadSpawnFixture(t)
	before := f.seats()
	code, op, e := f.spawnRemote(1, "air26", func(r *team.SpawnRequest) {
		r.Title, r.Model, r.Effort = "worker", "sonnet", "low"
		r.Task = &team.SpawnTask{Subject: "build it", Description: "brief", DoneWhen: []string{"it works"}}
	})
	if code != 200 || op.State != team.SpawnRunning || op.ID != spawnID(1) || op.HostID != "hostM" {
		t.Fatalf("spawn = %d %+v %+v", code, op, e)
	}
	st, _ := f.spawnState(spawnID(1))
	cmds := f.commandsOf(CmdSpawn)
	if st != "running" || len(cmds) != 1 || cmds[0].ID != spawnID(1) || cmds[0].MK != spawnID(1) || cmds[0].HostID != "hostM" || cmds[0].State != cmdPending {
		t.Fatalf("op %s, commands %+v", st, cmds)
	}
	var tc team.TeamCommand
	_ = json.Unmarshal(cmds[0].Body, &tc)
	if tc.Cwd != "/w/r" || tc.Title != "worker" || tc.Model != "sonnet" || tc.Effort != "low" || tc.ToHostID != "hostM" || tc.TeamID != uid(1) ||
		tc.Lead.SessionID != "sid-1" || tc.Lead.Address == "" {
		t.Fatalf("body = %+v", tc)
	}
	if got := f.seats(); got != before+1 {
		t.Fatalf("seats %d → %d, want +1", before, got)
	}
}

// Crash cut (§11): the op does not exist without its command. Mutation gate: enqueue after the commit → red.
func TestSpawnHost_NoOpWithoutItsCommand(t *testing.T) {
	f, _ := leadSpawnFixture(t)
	f.enqueue(f.cmd(spawnID(1), CmdEnd, "hostM", "")) // the id the spawn would use is taken by another command
	code, _, _ := f.spawnRemote(1, "air26", nil)
	if code != http.StatusInternalServerError {
		t.Fatalf("status = %d", code)
	}
	var n int
	_ = f.m.store.db.QueryRow(`SELECT COUNT(*) FROM remote_spawns`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d op(s) without a command", n)
	}
}

// Rule 7: refused before anything is created.
func TestSpawnHost_RefusalsCreateNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		host   string
		caps   ipeers.TeamCaps
		status int
		code   string
	}{
		"kind not announced": {"air26", ipeers.TeamCaps{Kinds: []string{CmdAdopt}, AllowTeam: true}, 409, team.ErrRemoteUnsupported},
		"allow_team off":     {"air26", ipeers.TeamCaps{Kinds: allKinds, AllowTeam: false}, 409, "host_not_allowed"},
		"unknown host":       {"nobody", ipeers.TeamCaps{Kinds: allKinds, AllowTeam: true}, 400, team.ErrBadRequest},
	} {
		f, fc := leadSpawnFixture(t)
		fc.caps["hostM"] = tc.caps
		code, _, e := f.spawnRemote(1, tc.host, nil)
		if code != tc.status || e.Error != tc.code {
			t.Fatalf("%s: %d %+v, want %d %s", name, code, e, tc.status, tc.code)
		}
		var ops, cmds int
		_ = f.m.store.db.QueryRow(`SELECT COUNT(*) FROM remote_spawns`).Scan(&ops)
		_ = f.m.store.db.QueryRow(`SELECT COUNT(*) FROM team_commands`).Scan(&cmds)
		if ops != 0 || cmds != 0 {
			t.Fatalf("%s: %d op(s), %d command(s) created", name, ops, cmds)
		}
	}
}

func TestSpawnHost_TeamFullAndNotLeadAreRefused(t *testing.T) {
	f, _ := leadSpawnFixture(t)
	if _, err := f.m.store.db.Exec(`UPDATE teams SET grant_json = json_set(grant_json, '$.max_members', ?) WHERE id = ?`, f.seats(), uid(1)); err != nil {
		t.Fatal(err)
	}
	if code, _, e := f.spawnRemote(1, "air26", nil); code != 409 || e.Error != team.ErrTeamFull {
		t.Fatalf("full: %d %+v", code, e)
	}
	f2, _ := leadSpawnFixture(t)
	if _, err := f2.m.store.db.Exec(`UPDATE teams SET lead_session_id = 'someone-else' WHERE id = ?`, uid(1)); err != nil {
		t.Fatal(err)
	}
	if code, _, e := f2.spawnRemote(1, "air26", nil); code != 409 || e.Error != team.ErrNotLead {
		t.Fatalf("not lead: %d %+v", code, e)
	}
}

func TestSpawnHost_AReplayJoinsTheOpAndAnotherBodyIsAConflict(t *testing.T) {
	f, _ := leadSpawnFixture(t)
	if code, _, e := f.spawnRemote(1, "air26", nil); code != 200 {
		t.Fatalf("%d %+v", code, e)
	}
	if code, op, _ := f.spawnRemote(1, "air26", nil); code != 200 || op.ID != spawnID(1) || len(f.commandsOf(CmdSpawn)) != 1 {
		t.Fatalf("replay = %d %+v, %d commands", code, op, len(f.commandsOf(CmdSpawn)))
	}
	if code, _, e := f.spawnRemote(1, "air26", func(r *team.SpawnRequest) { r.Title = "other" }); code != 409 || e.Error != team.ErrIDConflict {
		t.Fatalf("other body = %d %+v", code, e)
	}
	if code, _, e := f.spawnRemote(1, "other", nil); code != 409 || e.Error != team.ErrIDConflict {
		t.Fatalf("other host = %d %+v", code, e)
	}
}

// The member host's answer to the spawn command (§6.2): `accepted` changes nothing; a refusal fails the op with its code and
// frees the seat. Mutation gate: ignore the refusal → the op stays running → red.
func TestSpawnHost_TheAnswerToTheCommandDrivesTheOp(t *testing.T) {
	f, _ := leadSpawnFixture(t)
	before := f.seats()
	f.remoteSpawn("opA", "hostM", "")
	f.enqueue(f.cmd("opA", CmdSpawn, "hostM", "opA"))
	if _, err := f.m.store.SettleCommand("opA", answerOf("opA", map[string]string{"state": "accepted"}), f.clock.Load(), remoteOutcomes{m: f.m}); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.spawnState("opA"); st != "running" {
		t.Fatalf("accepted moved the op to %s", st)
	}
	f.remoteSpawn("opB", "hostM", "")
	f.settleRemote(CmdSpawn, "opB", "opB", refusedBy("cwd_outside_grant"))
	if st, reason := f.spawnState("opB"); st != "failed" || reason != "cwd_outside_grant" {
		t.Fatalf("refused op = %s{%s}", st, reason)
	}
	if got := f.seats(); got != before+1 { // opA still runs; opB is free
		t.Fatalf("seats %d → %d, want +1 (opA only)", before, got)
	}
}

// The POST is held while the op runs and answers when `registered` arrives: done, with the remote member (alias address)
// and the first task id, which was created on this host.
func TestSpawnHost_ThePostAnswersDoneWithTheMemberAndTheTaskWhenRegisteredArrives(t *testing.T) {
	f, fc := leadSpawnFixture(t)
	f.setLeadHost(true)
	fc.aliases["air26"], fc.caps["lead:1"] = "lead:1", ipeers.TeamCaps{Kinds: allKinds, AllowTeam: true}
	f.m.spawnWait = 5 * time.Second
	type res struct {
		code int
		op   team.SpawnOp
	}
	ch := make(chan res, 1)
	go func() {
		code, op, _ := f.spawnRemote(1, "air26", func(r *team.SpawnRequest) { r.Task = &team.SpawnTask{Subject: "build it"} })
		ch <- res{code, op}
	}()
	for i := 0; i < 200; i++ { // the op exists once the POST has been accepted
		var n int
		_ = f.m.store.db.QueryRow(`SELECT COUNT(*) FROM remote_spawns`).Scan(&n)
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	fact := registeredFact(factUUID1, spawnID(1))
	if code, body := f.postFact(leadPrincipal(), fact); code != 200 {
		t.Fatalf("fact %d %s", code, body)
	}
	select {
	case r := <-ch:
		if r.code != 200 || r.op.State != team.SpawnDone || r.op.Member == nil || r.op.Member.SessionID != "msid-9" || r.op.TaskID == "" {
			t.Fatalf("answer = %d %+v", r.code, r.op)
		}
		if r.op.Member.HostID != "lead:1" {
			t.Fatalf("member host = %s", r.op.Member.HostID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the POST was not woken by the registered fact")
	}
}

func TestSpawnHost_AFailedOpAnswersFailedWithTheReason(t *testing.T) {
	f, fc := leadSpawnFixture(t)
	f.setLeadHost(true)
	fc.aliases["air26"], fc.caps["lead:1"] = "lead:1", ipeers.TeamCaps{Kinds: allKinds, AllowTeam: true}
	if code, _, _ := f.spawnRemote(1, "air26", nil); code != 200 {
		t.Fatal(code)
	}
	if code, body := f.postFact(leadPrincipal(), spawnFailedFact(factUUID1, spawnID(1), team.SpawnReasonStartTimeout)); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	code, op, _ := f.spawnRemote(1, "air26", nil)
	if code != 200 || op.State != team.SpawnFailed || op.Reason != team.SpawnReasonStartTimeout {
		t.Fatalf("answer = %d %+v", code, op)
	}
}

// codex R1 P1: a command the member host accepted is no longer pending, yet its op still holds a seat until a fact arrives. If
// the host is unpaired in that interval the liveness scan must still find it. Mutation gate: scan only members and commands → red.
func TestSpawnHost_TheUnpairScanFindsAHostWithOnlyARunningOp(t *testing.T) {
	f, fc := leadSpawnFixture(t)
	f.remoteSpawn("op1", "hostM", "")
	fc.mu.Lock()
	fc.paired = map[string]bool{"hostM": false}
	fc.mu.Unlock()
	f.m.scanUnpaired()
	if st, reason := f.spawnState("op1"); st != "failed" || reason != "unpaired" {
		t.Fatalf("op = %s{%s}", st, reason)
	}
}

// codex R1 P2: a POST woken by a fact leaves nothing registered for its op. Mutation gate: remove the stale channel only → red.
func TestSpawnHost_ThePostLeavesNoWaiterBehind(t *testing.T) {
	f, fc := leadSpawnFixture(t)
	f.setLeadHost(true)
	fc.aliases["air26"], fc.caps["lead:1"] = "lead:1", ipeers.TeamCaps{Kinds: allKinds, AllowTeam: true}
	f.m.spawnWait = 5 * time.Second
	done := make(chan int, 1)
	go func() {
		code, _, _ := f.spawnRemote(1, "air26", nil)
		done <- code
	}()
	for i := 0; i < 200; i++ {
		var n int
		_ = f.m.store.db.QueryRow(`SELECT COUNT(*) FROM remote_spawns`).Scan(&n)
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if code, body := f.postFact(leadPrincipal(), registeredFact(factUUID1, spawnID(1))); code != 200 {
		t.Fatalf("fact %d %s", code, body)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the POST was not woken")
	}
	f.m.mu.Lock()
	left := len(f.m.waiters[spawnID(1)])
	f.m.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d waiter(s) left registered for the finished op", left)
	}
}

// codex attacker (high): a finished op is read back by its id even after the host is unpaired, the alias gone — the CLI whose
// first answer was lost must see the stored end (and its exit code), not bad_request. A request that does NOT match the op is
// still a conflict. Mutation gate: resolve the host before looking the id up → red.
func TestSpawnHost_AReplayAfterUnpairingReadsTheStoredEnd(t *testing.T) {
	f, fc := leadSpawnFixture(t)
	if code, _, e := f.spawnRemote(1, "air26", nil); code != 200 {
		t.Fatalf("%d %+v", code, e)
	}
	if err := f.m.unpairHost("hostM", "unpaired"); err != nil {
		t.Fatal(err)
	}
	fc.mu.Lock()
	delete(fc.aliases, "air26")
	fc.paired = map[string]bool{"hostM": false}
	fc.mu.Unlock()
	code, op, e := f.spawnRemote(1, "air26", nil)
	if code != 200 || op.State != team.SpawnFailed || op.Reason != "unpaired" {
		t.Fatalf("replay = %d %+v %+v", code, op, e)
	}
	if code, _, e := f.spawnRemote(1, "air26", func(r *team.SpawnRequest) { r.Title = "other" }); code != 409 || e.Error != team.ErrIDConflict {
		t.Fatalf("other body = %d %+v", code, e)
	}
	// an unknown name is not "the host that went away": only the text the first request named replays
	if code, _, e := f.spawnRemote(1, "some-other-name", nil); code != 409 || e.Error != team.ErrIDConflict {
		t.Fatalf("unknown name = %d %+v", code, e)
	}
	// a NEW id for the unpaired host is still refused
	if code, _, e := f.spawnRemote(2, "air26", nil); code != 400 || e.Error != team.ErrBadRequest {
		t.Fatalf("new id = %d %+v", code, e)
	}
}
