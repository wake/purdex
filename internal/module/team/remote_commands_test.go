package teammod

import (
	"encoding/json"
	"net/http"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// Commands for remote rows (cross-host team spec §4.2, §3.1 rule 2, §11; plan X3b-1b): the row moves and the command is
// enqueued in ONE transaction; a kill waits for a pending adopt / release (409 command_pending); `end` and `lead_moved`
// follow the team's end and the lead's cleared in their own transactions.

const remoteRef = "_rabc12" // newMember's "_r"+spawnOp for spawn op "abc12"

var allKinds = []string{CmdAdopt, CmdRelease, CmdKill, CmdSpawn, CmdEnd, CmdLeadMoved, CmdVoid}

func remoteFixture(t *testing.T) (*fixture, *fakeHostCaller) {
	t.Helper()
	f, fc, _ := pumpFixture(t)
	fc.aliases = map[string]string{"air26": "hostM", "other": "hostN"}
	fc.caps = map[string]ipeers.TeamCaps{"hostM": {Kinds: allKinds, AllowTeam: true}, "hostN": {Kinds: allKinds, AllowTeam: true}}
	return f, fc
}

func (f *fixture) commandsOf(kind string) []commandRow {
	f.t.Helper()
	rows, err := f.m.store.db.Query(`SELECT `+commandCols+` FROM team_commands WHERE kind = ? ORDER BY rowid`, kind)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []commandRow
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			f.t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

func TestRemoteRelease_ActiveMovesTheRowAndEnqueuesInOneTransaction(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	before, _ := seatsTaken(f.m.store.db, uid(1), "")
	code, m, e, _ := f.release("air26/" + remoteRef)
	if code != http.StatusOK || m.State != team.MemberReleasing {
		t.Fatalf("release = %d %+v %+v", code, m, e)
	}
	cmds := f.commandsOf(CmdRelease)
	if len(cmds) != 1 || cmds[0].HostID != "hostM" || cmds[0].MK != "mk1" || cmds[0].State != cmdPending {
		t.Fatalf("commands = %+v", cmds)
	}
	var tc team.TeamCommand
	_ = json.Unmarshal(cmds[0].Body, &tc)
	if tc.ToHostID != "hostM" || tc.TeamID != uid(1) || tc.MK != "mk1" || tc.Lead.SessionID != "sid-1" {
		t.Fatalf("body = %+v", tc)
	}
	if after, _ := seatsTaken(f.m.store.db, uid(1), ""); after != before {
		t.Fatalf("seats %d → %d: a releasing row still holds its seat", before, after)
	}
	// the same call again: 200, no second command
	if code, m, _, _ := f.release("air26/" + remoteRef); code != http.StatusOK || m.State != team.MemberReleasing || len(f.commandsOf(CmdRelease)) != 1 {
		t.Fatalf("second release = %d %s, %d commands", code, m.State, len(f.commandsOf(CmdRelease)))
	}
}

// Crash cut (§11): the row does not move without its command. Mutation gate: enqueue after the commit → the row has moved
// although the enqueue failed → red.
func TestRemoteRelease_TheRowDoesNotMoveWithoutItsCommand(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	f.m.newID = func() string { return "dup" }
	f.enqueue(f.cmd("dup", CmdEnd, "hostM", "")) // the id the release would use is taken by another command
	code, _, _, _ := f.release("air26/" + remoteRef)
	if code != http.StatusInternalServerError {
		t.Fatalf("release = %d, want 500", code)
	}
	if st, _ := f.memberRowState("abc12"); st != rowActive {
		t.Fatalf("row = %s: it moved without its command", st)
	}
}

func TestRemoteRelease_JoiningFollowsTheAdoptFifo(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowJoining)
	if code, m, _, _ := f.release("air26/" + remoteRef); code != http.StatusOK || m.State != team.MemberReleasing {
		t.Fatalf("release of a joining row = %d %s", code, m.State)
	}
	if len(f.commandsOf(CmdRelease)) != 1 {
		t.Fatal("no release command")
	}
}

func TestRemoteRelease_KillingIsPending_FinishedRowsAnswerAsTheyAre(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowKilling)
	if code, _, e, _ := f.release("air26/" + remoteRef); code != http.StatusConflict || e.Error != team.ErrCommandPending {
		t.Fatalf("release of a killing row = %d %s", code, e.Error)
	}
	for _, st := range []string{string(team.MemberReleased), rowFailed, rowGone, string(team.MemberKilled)} {
		f2, _ := remoteFixture(t)
		f2.remoteRow("abc12", "hostM", "mk1", st)
		if code, m, _, _ := f2.release("air26/" + remoteRef); code != http.StatusOK || string(m.State) != st || len(f2.commandsOf(CmdRelease)) != 0 {
			t.Fatalf("%s: release = %d %s", st, code, m.State)
		}
	}
}

// §4.2: a kill is only from active. Mutation gate: allow it from joining / releasing → red.
func TestRemoteKill_OnlyFromActive_ElseCommandPending(t *testing.T) {
	for _, st := range []string{rowJoining, rowReleasing} {
		f, _ := remoteFixture(t)
		f.remoteRow("abc12", "hostM", "mk1", st)
		code, _, e, _ := f.killTarget("air26/" + remoteRef)
		if code != http.StatusConflict || e.Error != team.ErrCommandPending {
			t.Fatalf("kill while %s = %d %s, want 409 command_pending", st, code, e.Error)
		}
		if st2, _ := f.memberRowState("abc12"); st2 != st || len(f.commandsOf(CmdKill)) != 0 {
			t.Fatalf("%s: row %s, %d kill commands", st, st2, len(f.commandsOf(CmdKill)))
		}
	}
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	code, m, _, _ := f.killTarget("air26/" + remoteRef)
	if code != http.StatusOK || m.State != team.MemberKilling || len(f.commandsOf(CmdKill)) != 1 {
		t.Fatalf("kill of an active row = %d %s, %d commands", code, m.State, len(f.commandsOf(CmdKill)))
	}
	if code, m, _, _ := f.killTarget("air26/" + remoteRef); code != http.StatusOK || m.State != team.MemberKilling || len(f.commandsOf(CmdKill)) != 1 {
		t.Fatal("a second kill queued another command")
	}
}

func TestRemoteKill_AReleasedRowIsNotYourMember(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", string(team.MemberReleased))
	if code, _, e, _ := f.killTarget("air26/" + remoteRef); code != http.StatusConflict || e.Error != team.ErrNotYourMember {
		t.Fatalf("kill of a released row = %d %s", code, e.Error)
	}
}

// §3.1 rule 7: an unsupported kind is refused to the lead before anything moves or queues.
func TestRemoteCommands_AnUnsupportedKindIsRefusedAndNothingMoves(t *testing.T) {
	f, fc := remoteFixture(t)
	fc.caps["hostM"] = ipeers.TeamCaps{Kinds: []string{CmdAdopt, CmdRelease}, AllowTeam: true}
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	if code, _, e, _ := f.killTarget("air26/" + remoteRef); code != http.StatusConflict || e.Error != team.ErrRemoteUnsupported {
		t.Fatalf("kill = %d %s, want 409 remote_unsupported", code, e.Error)
	}
	fc.caps["hostM"] = ipeers.TeamCaps{Kinds: allKinds, AllowTeam: false}
	if code, _, e, _ := f.release("air26/" + remoteRef); code != http.StatusConflict || e.Error != "host_not_allowed" {
		t.Fatalf("release = %d %s, want 409 host_not_allowed", code, e.Error)
	}
	if st, _ := f.memberRowState("abc12"); st != rowActive || len(f.commandsOf(CmdKill))+len(f.commandsOf(CmdRelease)) != 0 {
		t.Fatalf("row %s, commands queued", st)
	}
}

func TestRemoteTarget_ByAliasAndHostIdNeverByAliasAlone(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	// a bare ref finds the remote row too
	if code, m, _, _ := f.release(remoteRef); code != http.StatusOK || m.State != team.MemberReleasing {
		t.Fatalf("bare ref = %d %s", code, m.State)
	}
	// an alias that names another host (the ref exists only on hostM) finds nothing
	f2, fc2 := remoteFixture(t)
	f2.remoteRow("abc12", "hostM", "mk1", rowActive)
	if code, _, e, _ := f2.release("other/" + remoteRef); code != http.StatusConflict || e.Error != team.ErrNotYourMember {
		t.Fatalf("wrong host's alias = %d %s", code, e.Error)
	}
	// an unknown alias, and a name (a remote member has none in this registry)
	for _, target := range []string{"nowhere/" + remoteRef, "air26/some-name"} {
		if code, _, e, _ := f2.release(target); code != http.StatusConflict || e.Error != team.ErrNotYourMember {
			t.Fatalf("%q = %d %s", target, code, e.Error)
		}
	}
	// the alias re-created for another host id: the row (host hostM) is no longer found through it
	fc2.aliases["air26"] = "hostN"
	if code, _, e, _ := f2.release("air26/" + remoteRef); code != http.StatusConflict || e.Error != team.ErrNotYourMember {
		t.Fatalf("re-bound alias = %d %s", code, e.Error)
	}
}

func endTeamOf(t *testing.T, f *fixture) team.Team {
	t.Helper()
	tm, ok, err := f.m.store.TeamByID(uid(1))
	if err != nil || !ok {
		t.Fatalf("team: %v %v", ok, err)
	}
	return tm
}

// §3.1 rule 2: the team ends and `end` is enqueued per host with a live remote row, in the same transaction. Mutation gate:
// no end for a host / one end for all hosts → red.
func TestRemoteEnd_OneCommandPerHostWithALiveRowInTheEndingTransaction(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	f.remoteRow("abc13", "hostM", "mk2", rowReleasing)
	f.remoteRow("abc14", "hostN", "mk3", rowJoining)
	f.remoteRow("abc15", "hostO", "mk4", string(team.MemberReleased)) // finished: no end for hostO
	tm := endTeamOf(t, f)
	ended, err := f.m.store.EndTeamWithCommands(tm, team.TeamEndLeadGone, f.clock.Load(), f.m.leadTuple(tm), f.m.newID)
	if err != nil || !ended {
		t.Fatalf("end = %v %v", ended, err)
	}
	ends := f.commandsOf(CmdEnd)
	if len(ends) != 2 || ends[0].HostID != "hostM" || ends[1].HostID != "hostN" || ends[0].MK != "" || ends[1].MK != "" {
		t.Fatalf("ends = %+v, want one team-level end each for hostM and hostN", ends)
	}
	if st, _ := f.memberRowState("abc12"); st != rowActive {
		t.Fatalf("row = %s: the rows keep their states when a team ends (D4)", st)
	}
	// ending it again does nothing and enqueues nothing
	if again, _ := f.m.store.EndTeamWithCommands(tm, team.TeamEndLeadGone, f.clock.Load(), f.m.leadTuple(tm), f.m.newID); again || len(f.commandsOf(CmdEnd)) != 2 {
		t.Fatal("an ended team ended again")
	}
}

func TestRemoteEnd_TheTeamDoesNotEndWithoutItsCommands(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	f.enqueue(f.cmd("dup", CmdRelease, "hostM", "mk9")) // the id the end would use
	tm := endTeamOf(t, f)
	if _, err := f.m.store.EndTeamWithCommands(tm, team.TeamEndLeadGone, f.clock.Load(), f.m.leadTuple(tm), func() string { return "dup" }); err == nil {
		t.Fatal("an end whose command could not be enqueued succeeded")
	}
	if endTeamOf(t, f).EndedAt != 0 {
		t.Fatal("the team ended although its command was not enqueued")
	}
}

func TestRemoteEnd_ATeamWithOnlyLocalMembersQueuesNothing(t *testing.T) {
	f, _ := remoteFixture(t)
	seedMember(t, f.m.store, "op-local", uid(1), "sid-local", f.clock.Load())
	tm := endTeamOf(t, f)
	if ended, err := f.m.store.EndTeamWithCommands(tm, team.TeamEndLeadGone, f.clock.Load(), f.m.leadTuple(tm), f.m.newID); err != nil || !ended || len(f.commandsOf(CmdEnd)) != 0 {
		t.Fatalf("ended=%v err=%v ends=%d", ended, err, len(f.commandsOf(CmdEnd)))
	}
}

// The lead's cleared moves the team and tells each host with a live remote row, in the cleared's transaction.
func TestRemoteLeadMoved_FollowsTheLeadsCleared(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	f.remoteRow("abc14", "hostN", "mk3", string(team.MemberReleased))
	f.relayLeadTo("sid-1b")
	moved := f.commandsOf(CmdLeadMoved)
	if len(moved) != 1 || moved[0].HostID != "hostM" || moved[0].MK != "" {
		t.Fatalf("lead_moved = %+v, want one team-level command for hostM", moved)
	}
	var tc team.TeamCommand
	_ = json.Unmarshal(moved[0].Body, &tc)
	if tc.LeadSessionID != "sid-1b" || tc.LeadRef == "" || tc.Lead.SessionID != "sid-1b" || tc.TeamID != uid(1) {
		t.Fatalf("body = %+v", tc)
	}
}

// A remote member is told by its own host: the lead-handover notice (P6-1′) skips remote rows.
func TestRemoteLeadMoved_NoLocalHandoverNoticeForARemoteRow(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	f.relayLeadTo("sid-1b")
	for _, c := range f.sender.calls() {
		t.Fatalf("a notice was sent to %s for a team with only a remote member", c.To)
	}
}

// codex attack: a cleared that moves NO team (the old session led nothing) enqueues no lead_moved, even if the new session
// already leads one. Mutation gate: look the teams up by the new session → red.
func TestRemoteLeadMoved_NotForATeamTheClearedDidNotMove(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	// an ordinary session's relay whose new session id happens to be the lead's: the old session leads nothing
	out := f.begin("sid-2")
	if code, body := f.decide(out.RequestID, "approve"); code != 200 {
		t.Fatalf("approve: %d %s", code, body)
	}
	for _, st := range []team.RelayState{team.RelayWriting, team.RelayWritten} {
		f.report(out.Op.ID, team.RelayReportRequest{State: st})
	}
	f.origins.markDead("sid-2")
	// at the store (the route refuses a new session under another process before it gets here)
	op, res, err := f.m.store.ReportRelay(out.Op.ID, RelayReport{State: team.RelayCleared, NewSessionID: "sid-1", NewRef: "_new123", At: f.clock.Load()})
	t.Logf("store cleared into a session that already leads: %s %v %v", op.State, res, err)
	if n := len(f.commandsOf(CmdLeadMoved)); n != 0 {
		t.Fatalf("%d lead_moved for a team no cleared moved", n)
	}
}

// codex R1: the host part of a target may be a peer's host id as well as its alias.
func TestRemoteTarget_ByHostId(t *testing.T) {
	f, fc := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	fc.aliases["air26"] = "hostM"
	if code, m, _, _ := f.release("hostM/" + remoteRef); code != http.StatusOK || m.State != team.MemberReleasing {
		t.Fatalf("host-id target = %d %s", code, m.State)
	}
}
