package teammod

import (
	"encoding/json"
	"testing"

	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// The remote row state machine (cross-host team spec §4.2, §11 Monotonic / Membership generation / Seats; plan X3b-1a).
// Every answer is fed to SettleCommand by hand, as the pump would, against rows written directly in their states.

func answerOf(id string, outcome any) peersmod.CallResult {
	o, _ := json.Marshal(outcome)
	b, _ := json.Marshal(team.TeamCommandAnswer{ID: id, HostID: "hostM", Outcome: o})
	return peersmod.CallResult{Class: peersmod.ClassDone, Body: b}
}

func refusedBy(code string) peersmod.CallResult {
	return peersmod.CallResult{Class: peersmod.ClassRefused, Code: code}
}

func applied(sid, ref string) team.AdoptOutcome {
	return team.AdoptOutcome{State: "applied", MemberSession: sid, Ref: ref, PID: 77, ProcStart: "t0", Title: "ios", Cwd: "/w/ios", Tmux: "main:@1.%2"}
}

// settle enqueues command id of kind for row mk and settles it with res through the real outcome applier.
func (f *fixture) settleRemote(kind, id, mk string, res peersmod.CallResult) {
	f.t.Helper()
	f.enqueue(f.cmd(id, kind, "hostM", mk))
	if _, err := f.m.store.SettleCommand(id, res, f.clock.Load(), remoteOutcomes{m: f.m}); err != nil {
		f.t.Fatalf("settle %s: %v", id, err)
	}
}

func TestRemote_AdoptAppliedJoiningBecomesActiveWithWhatTheHostReported(t *testing.T) {
	f, _ := cmdFixture(t)
	f.remoteRow("op-j", "hostM", "mk1", rowJoining)
	f.settleRemote(CmdAdopt, "a1", "mk1", answerOf("a1", applied("sid-ios", "_ios123")))
	if st, _ := f.memberRowState("op-j"); st != rowActive {
		t.Fatalf("row = %s, want active", st)
	}
	var sid, ref, title string
	var pid int
	_ = f.m.store.db.QueryRow(`SELECT session_id, ref, title, pid FROM team_members WHERE spawn_op = 'op-j'`).Scan(&sid, &ref, &title, &pid)
	if sid != "sid-ios" || ref != "_ios123" || title != "ios" || pid != 77 {
		t.Fatalf("row = %s %s %s %d", sid, ref, title, pid)
	}
}

func TestRemote_AdoptRefusedOrWrongHostFailsTheRowAndFreesTheSeat(t *testing.T) {
	for _, res := range []peersmod.CallResult{refusedBy("host_not_allowed"), {Class: peersmod.ClassWrongHost, Code: "wrong_host"}} {
		f, _ := cmdFixture(t)
		f.remoteRow("op-j", "hostM", "mk1", rowJoining)
		before, _ := seatsTaken(f.m.store.db, uid(1), "")
		f.settleRemote(CmdAdopt, "a1", "mk1", res)
		if st, why := f.memberRowState("op-j"); st != rowFailed || why != res.Code {
			t.Fatalf("row = %s/%s, want failed/%s", st, why, res.Code)
		}
		if after, _ := seatsTaken(f.m.store.db, uid(1), ""); after != before-1 {
			t.Fatalf("seats %d → %d, want one freed", before, after)
		}
	}
}

// §11 Monotonic. Mutation gate: set active unconditionally → a release while joining ends active → red.
func TestRemote_ReleaseWhileJoiningThenTheAdoptAnswersLate_EndsReleasedNeverActive(t *testing.T) {
	f, _ := cmdFixture(t)
	f.remoteRow("op-j", "hostM", "mk1", rowReleasing) // the lead released a joining row: it is releasing now
	f.settleRemote(CmdAdopt, "a1", "mk1", answerOf("a1", applied("sid-ios", "_ios123")))
	if st, _ := f.memberRowState("op-j"); st != rowReleasing {
		t.Fatalf("a late applied moved a releasing row to %s", st)
	}
	f.settleRemote(CmdRelease, "r1", "mk1", answerOf("r1", map[string]string{"state": "ok"}))
	if st, _ := f.memberRowState("op-j"); st != string(team.MemberReleased) {
		t.Fatalf("row = %s, want released", st)
	}
}

func TestRemote_ReleaseRefusedWithAnyCodeIsStillReleased(t *testing.T) {
	for _, code := range []string{"not_your_member", "wrong_host", "unsupported_kind"} {
		f, _ := cmdFixture(t)
		f.remoteRow("op-r", "hostM", "mk1", rowReleasing)
		f.settleRemote(CmdRelease, "r1", "mk1", refusedBy(code))
		if st, why := f.memberRowState("op-r"); st != string(team.MemberReleased) || why != code {
			t.Fatalf("%s: row = %s/%s, want released/%s", code, st, why, code)
		}
	}
}

func TestRemote_KillAnswers(t *testing.T) {
	cases := []struct {
		name string
		res  peersmod.CallResult
		want string
		why  string
	}{
		{"killed", answerOf("k1", map[string]string{"state": "killed"}), "killed", ""},
		{"gone", answerOf("k1", map[string]string{"state": "gone"}), "gone", ""},
		{"host_not_allowed returns to active", refusedBy("host_not_allowed"), "active", "host_not_allowed"},
		{"not_your_member is gone", refusedBy("not_your_member"), "gone", "not_your_member"},
		{"any other refusal returns to active", refusedBy("kill_failed"), "active", "kill_failed"},
	}
	for _, c := range cases {
		f, _ := cmdFixture(t)
		f.remoteRow("op-k", "hostM", "mk1", rowKilling)
		f.settleRemote(CmdKill, "k1", "mk1", c.res)
		if st, why := f.memberRowState("op-k"); st != c.want || why != c.why {
			t.Errorf("%s: row = %s/%s, want %s/%s", c.name, st, why, c.want, c.why)
		}
	}
}

// §11: `ended` racing a kill is one terminal state — here the row is already gone when the kill answer arrives.
func TestRemote_AnAnswerForARowAlreadyGoneIsIgnored(t *testing.T) {
	f, _ := cmdFixture(t)
	f.remoteRow("op-k", "hostM", "mk1", rowGone)
	f.settleRemote(CmdKill, "k1", "mk1", answerOf("k1", map[string]string{"state": "killed"}))
	if st, _ := f.memberRowState("op-k"); st != rowGone {
		t.Fatalf("row = %s, want gone (one terminal state)", st)
	}
}

// §11 Membership generation: an answer for the earlier membership (another mk) of the same host does not touch the new one.
func TestRemote_AnOldMembershipsAnswerDoesNotTouchAReAdoptedRow(t *testing.T) {
	f, _ := cmdFixture(t)
	f.remoteRow("op-new", "hostM", "mk-new", rowActive)
	f.settleRemote(CmdRelease, "r-old", "mk-old", answerOf("r-old", map[string]string{"state": "ok"}))
	if st, _ := f.memberRowState("op-new"); st != rowActive {
		t.Fatalf("the re-adopted row = %s, want active", st)
	}
}

// The team ended: rows stay as they ended (D4, agreed 2026-10-09), and an applied that arrives afterwards cannot make one
// active. Mutation gate: drop the live-team condition → red.
func TestRemote_AnAppliedAfterTheTeamEndedDoesNotActivateTheRow(t *testing.T) {
	f, _ := cmdFixture(t)
	f.remoteRow("op-j", "hostM", "mk1", rowJoining)
	if _, err := f.m.store.db.Exec(`UPDATE teams SET ended_at = 5 WHERE id = ?`, uid(1)); err != nil {
		t.Fatal(err)
	}
	f.settleRemote(CmdAdopt, "a1", "mk1", answerOf("a1", applied("sid-ios", "_ios123")))
	if st, _ := f.memberRowState("op-j"); st != rowJoining {
		t.Fatalf("row of an ended team = %s, want unchanged", st)
	}
}

func TestRemote_EndLeadMovedAndVoidTouchNoRow(t *testing.T) {
	f, _ := cmdFixture(t)
	f.remoteRow("op-a", "hostM", "mk1", rowActive)
	for i, k := range []string{CmdEnd, CmdLeadMoved, CmdVoid} {
		id := string(rune('a' + i))
		f.settleRemote(k, "x-"+id, "mk1", refusedBy("not_voidable"))
		f.settleRemote(k, "y-"+id, "mk1", answerOf("y-"+id, map[string]string{"state": "ok"}))
	}
	if st, _ := f.memberRowState("op-a"); st != rowActive {
		t.Fatalf("row = %s", st)
	}
}

// An applied answer that is not an applied outcome is an error: nothing is marked done (the command is sent again).
func TestRemote_AMalformedAdoptAnswerIsNotSettled(t *testing.T) {
	f, _ := cmdFixture(t)
	f.remoteRow("op-j", "hostM", "mk1", rowJoining)
	f.enqueue(f.cmd("a1", CmdAdopt, "hostM", "mk1"))
	if _, err := f.m.store.SettleCommand("a1", answerOf("a1", map[string]string{"state": "maybe"}), 1, remoteOutcomes{m: f.m}); err == nil {
		t.Fatal("a non-applied adopt outcome was settled")
	}
	if f.cmdState("a1").State != cmdPending {
		t.Fatal("the command left pending")
	}
}

// Seats: joining, releasing and killing hold a seat like active; the terminal states free it.
func TestRemote_SeatsCountTheStatesWithACommandInFlight(t *testing.T) {
	f, _ := cmdFixture(t)
	for i, st := range []string{rowJoining, rowActive, rowReleasing, rowKilling, string(team.MemberReleased), rowFailed, rowGone, string(team.MemberKilled)} {
		f.remoteRow("op-s"+string(rune('a'+i)), "hostM", "mk"+string(rune('a'+i)), st)
	}
	if n, err := seatsTaken(f.m.store.db, uid(1), ""); err != nil || n != 4 {
		t.Fatalf("seats = %d err=%v, want 4 (joining, active, releasing, killing)", n, err)
	}
}

// A remote row is the member host's: the local sweepers (liveness, usage, notices) never look at it.
func TestRemote_TheLocalSweepersLeaveRemoteRowsAlone(t *testing.T) {
	f, _ := cmdFixture(t)
	seedMember(t, f.m.store, "op-local", uid(1), "sid-local", f.clock.Load())
	f.remoteRow("op-rem", "hostM", "mk1", rowActive)
	rows, err := f.m.store.ActiveMembersOfLiveTeams()
	if err != nil || len(rows) != 1 || rows[0].SpawnOp != "op-local" {
		t.Fatalf("rows = %+v err=%v, want only the local member", rows, err)
	}
	f.m.markGoneMembers()
	if st, _ := f.memberRowState("op-rem"); st != rowActive {
		t.Fatalf("markGoneMembers touched a remote row: %s", st)
	}
}

// §3.2 with D4: an unpaired host's rows of a live team go gone; those of an ended team stay as they ended.
func TestRemote_UnpairingLeavesAnEndedTeamsRowsAlone(t *testing.T) {
	f, _ := cmdFixture(t)
	f.remoteRow("op-a", "hostM", "mk1", rowActive)
	if _, err := f.m.store.db.Exec(`UPDATE teams SET ended_at = 5 WHERE id = ?`, uid(1)); err != nil {
		t.Fatal(err)
	}
	if err := f.m.unpairHost("hostM", "unpaired"); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.memberRowState("op-a"); st != rowActive {
		t.Fatalf("row of an ended team = %s, want unchanged", st)
	}
}

// codex R1/attack: a kill answer is killed or gone, nothing else is a result. Mutation gate: default to killed → red.
func TestRemote_AnUnknownKillAnswerIsNotSettled(t *testing.T) {
	for name, res := range map[string]peersmod.CallResult{
		"unknown state": answerOf("k1", map[string]string{"state": "maybe"}),
		"no outcome":    {Class: peersmod.ClassDone, Body: json.RawMessage(`{"id":"k1","host_id":"hostM"}`)},
	} {
		f, _ := cmdFixture(t)
		f.remoteRow("op-k", "hostM", "mk1", rowKilling)
		f.enqueue(f.cmd("k1", CmdKill, "hostM", "mk1"))
		if _, err := f.m.store.SettleCommand("k1", res, 1, remoteOutcomes{m: f.m}); err == nil {
			t.Fatalf("%s: settled", name)
		}
		if st, _ := f.memberRowState("op-k"); st != rowKilling || f.cmdState("k1").State != cmdPending {
			t.Fatalf("%s: row=%s command=%s", name, st, f.cmdState("k1").State)
		}
	}
}

// A remote session id equal to a LOCAL active session must not wedge the settle forever (the unique index would roll it
// back each time): the membership fails session_conflict.
func TestRemote_ASessionIdAlreadyActiveHereFailsTheMembership(t *testing.T) {
	f, _ := cmdFixture(t)
	seedMember(t, f.m.store, "op-local", uid(1), "sid-same", f.clock.Load())
	f.remoteRow("op-j", "hostM", "mk1", rowJoining)
	f.settleRemote(CmdAdopt, "a1", "mk1", answerOf("a1", applied("sid-same", "_dup123")))
	if st, why := f.memberRowState("op-j"); st != rowFailed || why != "session_conflict" {
		t.Fatalf("row = %s/%s, want failed/session_conflict", st, why)
	}
	if f.cmdState("a1").State != cmdDone {
		t.Fatal("the command stayed pending: it would be sent again forever")
	}
}

// An answer is bound to its command's team: the same member key in another team's row is not touched.
func TestRemote_AnOutcomeIsBoundToItsCommandsTeam(t *testing.T) {
	f, _ := cmdFixture(t)
	f.remoteRow("op-j", "hostM", "mk1", rowReleasing)
	c := f.cmd("r1", CmdRelease, "hostM", "mk1")
	body, _ := json.Marshal(map[string]any{"id": "r1", "kind": CmdRelease, "to_host_id": "hostM", "team_id": "other-team", "mk": "mk1"})
	c.TeamID, c.Body = "other-team", body
	f.enqueue(c)
	if _, err := f.m.store.SettleCommand("r1", answerOf("r1", map[string]string{"state": "ok"}), 1, remoteOutcomes{m: f.m}); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.memberRowState("op-j"); st != rowReleasing {
		t.Fatalf("another team's command moved the row to %s", st)
	}
}

// Seats: a running spawn op whose member row is already in flight counts once.
func TestRemote_ARunningSpawnWithAnInFlightRowIsOneSeat(t *testing.T) {
	f, _ := cmdFixture(t)
	if _, err := f.m.store.db.Exec(`INSERT INTO spawn_ops (id, team_id, host_id, request_hash, origin_session_id, cwd, tmux_name, step, state, created_at, updated_at)
		VALUES ('op-s', ?, 'h:1', 'x', 'sid-1', '/w', 'tm', 'launch', 'running', 1, 1)`, uid(1)); err != nil {
		t.Skipf("spawn_ops insert: %v", err)
	}
	f.remoteRow("op-s", "hostM", "mk1", rowReleasing)
	if n, _ := seatsTaken(f.m.store.db, uid(1), ""); n != 1 {
		t.Fatalf("seats = %d, want 1", n)
	}
}
