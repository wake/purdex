// internal/module/team/kill_claim_test.go
package teammod

import (
	"errors"
	"net/http"
	"syscall"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// #2152 point 2: a kill used to signal first and mark after, so a release or a relay claim by the same lead landing in
// between made the mark lose AFTER the process was already terminated (and the loser answered relay_open / 200). The kill now
// CLAIMS the member first — one compare-and-set active → killing — and signals only once it holds the claim; release and relay
// find a row that is not active and refuse by their existing rules. A signal that cannot be sent gives the claim back.
//
// Both origins are covered: the adopted member's process signal and the spawned member's tmux kill have the same shape.

func (f *fixture) killRowState(spawnOp string) team.MemberState {
	f.t.Helper()
	return memberBySpawn(f.t, f.m.store, spawnOp).State
}

// What the world looks like at the moment the signal goes out: the row's state, and what a release and a member relay claim by
// the same lead answer meanwhile.
type atSignal struct {
	state          team.MemberState
	releaseCode    int
	releaseState   team.MemberState
	relayErr       error
	rowAfterRaces  team.MemberState
	secondKillCode int
	secondKillErr  string
}

func (f *fixture) observeAt(key, target, teamID, sessionID string, out *atSignal) func() {
	return func() {
		out.state = f.killRowState(key)
		code, m, _, _ := f.release(target)
		out.releaseCode, out.releaseState = code, m.State
		_, _, out.relayErr = f.m.store.CreateMemberRelayOp(team.RelayOp{ID: "relay-at-signal", Kind: team.RelayKindMember, HostID: "h:1",
			SessionID: sessionID, Ref: target, TeamID: teamID, HandoffPath: "/x", CreatedAt: 1, UpdatedAt: 1}, nil)
		c2, _, e2, _ := f.killTarget(target)
		out.secondKillCode, out.secondKillErr = c2, e2.Error
		out.rowAfterRaces = f.killRowState(key)
	}
}

// Mutation gate (the point of the change): claim after the signal, as before → the row is still active at the signal, so the
// release lands, and this goes red.
func TestKillClaim_AdoptedRowIsKillingWhenTheSignalGoesAndReleaseAndRelayAreRefused(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	var seen atSignal
	var signals []int
	observe := f.observeAt(key, "_def456", uid(1), memberBySpawn(t, f.m.store, key).SessionID, &seen)
	f.m.killProcess = func(pid int) error {
		signals = append(signals, pid)
		if len(signals) == 1 {
			observe()
		}
		return nil
	}
	code, m, _, body := f.killTarget("_def456")
	if code != http.StatusOK || m.State != team.MemberKilled {
		t.Fatalf("kill = %d %s", code, body)
	}
	if seen.state != team.MemberKilling {
		t.Fatalf("at the signal the row was %q, want killing: the claim must come first", seen.state)
	}
	if seen.releaseCode != http.StatusOK || seen.releaseState != team.MemberKilling {
		t.Errorf("a release during the kill = %d %s, want the row as it is (killing), not released", seen.releaseCode, seen.releaseState)
	}
	if !errors.Is(seen.relayErr, ErrMemberNotActive) {
		t.Errorf("a relay claim during the kill = %v, want ErrMemberNotActive", seen.relayErr)
	}
	if seen.secondKillCode != http.StatusConflict || seen.secondKillErr != team.ErrCommandPending {
		t.Errorf("a second kill during the kill = %d %s, want 409 command_pending", seen.secondKillCode, seen.secondKillErr)
	}
	if len(signals) != 1 {
		t.Errorf("signals = %v, want exactly one (the second kill must not signal)", signals)
	}
	if got := f.killRowState(key); got != team.MemberKilled {
		t.Errorf("row = %s, want killed", got)
	}
}

func TestKillClaim_SpawnedRowIsKillingWhenTheSessionIsKilled(t *testing.T) {
	f, root := newTeamFixture(t, 4)
	m1 := f.member(1, root, "sid-m1", "w-one", nil)
	var at team.MemberState
	var kills int
	f.m.beforeMemberSignal = func(mr memberRow) {
		at, kills = f.killRowState(mr.SpawnOp), len(f.tmux.KillIfInstanceCalls())
	}
	code, _, e := f.kill("/tmp/10.sock", m1.Ref)
	if code != http.StatusOK {
		t.Fatalf("kill = %d %+v", code, e)
	}
	if at != team.MemberKilling || kills != 0 {
		t.Fatalf("at the kill the row was %q after %d tmux kill(s), want killing before any", at, kills)
	}
	if got := f.killRowState(m1.SpawnOp); got != team.MemberKilled {
		t.Fatalf("row = %s, want killed", got)
	}
}

// A signal that cannot be sent gives the claim back: the member is active again, and the lead gets the original error.
func TestKillClaim_AFailedSignalGivesTheClaimBack(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	var during team.MemberState
	f.m.killProcess = func(int) error { during = f.killRowState(key); return syscall.EPERM }
	code, _, e, body := f.killTarget("_def456")
	if code != http.StatusInternalServerError || e.Error != team.ErrKillFailed {
		t.Fatalf("kill = %d %s, want 500 kill_failed", code, body)
	}
	if during != team.MemberKilling {
		t.Errorf("during the signal the row was %q, want killing", during)
	}
	if got := f.killRowState(key); got != team.MemberActive {
		t.Fatalf("row = %s after the failure, want active again", got)
	}
	// and the member can be released or killed again
	f.m.killProcess = func(int) error { return nil }
	if code, m, _, body := f.killTarget("_def456"); code != http.StatusOK || m.State != team.MemberKilled {
		t.Fatalf("kill again = %d %s", code, body)
	}
}

func TestKillClaim_AnUnverifiableProcessGivesTheClaimBack(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	f.origins.mu.Lock()
	f.origins.procErr = errors.New("ps timed out")
	f.origins.mu.Unlock()
	code, _, e, body := f.killTarget("_def456")
	if code != http.StatusServiceUnavailable || e.Error != team.ErrNotReady {
		t.Fatalf("kill = %d %s, want 503 not_ready", code, body)
	}
	if got := f.killRowState(key); got != team.MemberActive {
		t.Fatalf("row = %s, want active again", got)
	}
}

func TestKillClaim_SpawnedTmuxFailureGivesTheClaimBack(t *testing.T) {
	f, root := newTeamFixture(t, 4)
	m1 := f.member(1, root, "sid-m1", "w-one", nil)
	f.tmux.SetPaneIdentityErr(errors.New("tmux: server busy"))
	code, _, e := f.kill("/tmp/10.sock", m1.Ref)
	if code != http.StatusServiceUnavailable || e.Error != team.ErrNotReady {
		t.Fatalf("kill = %d %+v, want 503 not_ready", code, e)
	}
	if got := f.killRowState(m1.SpawnOp); got != team.MemberActive {
		t.Fatalf("row = %s, want active again", got)
	}
}

// ESRCH / the process is gone: nothing was killed by this call, the claim ends as gone (not killed).
func TestKillClaim_ANothingToSignalEndsGoneFromTheClaim(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	f.m.killProcess = func(int) error { return syscall.ESRCH }
	code, m, _, body := f.killTarget("_def456")
	if code != http.StatusOK || m.State != team.MemberGone {
		t.Fatalf("kill = %d %s, want 200 gone", code, body)
	}
	if got := f.killRowState(key); got != team.MemberGone {
		t.Fatalf("row = %s, want gone", got)
	}
}

// A release that wins the claim: the kill never signals. Mutation gate: signal without holding the claim → red.
func TestKillClaim_ALostClaimNeverSignals(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	var signals []int
	f.m.killProcess = func(pid int) error { signals = append(signals, pid); return nil }
	f.m.beforeKillClaim = func(mr memberRow) { // the lead's release lands between the kill's read and its claim
		if released, err := f.m.store.ReleaseMember(mr.SpawnOp, mr.SessionID, 7); err != nil || !released {
			t.Fatalf("release: %v %v", released, err)
		}
	}
	code, _, e, body := f.killTarget("_def456")
	if code != http.StatusConflict || e.Error != team.ErrNotYourMember {
		t.Fatalf("kill = %d %s, want 409 not_your_member (it was released)", code, body)
	}
	if len(signals) != 0 {
		t.Fatalf("signalled %v although the claim was lost", signals)
	}
	if got := f.killRowState(key); got != team.MemberReleased {
		t.Fatalf("row = %s, want released", got)
	}
}

// A killing member is still a member for the role gates: nothing else may take its session (adopt, lead) while the kill is in
// flight, and the claim can always be given back.
func TestKillClaim_AKillingMemberIsStillAMemberForTheRoleGates(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'killing' WHERE spawn_op = ?`, key); err != nil {
		t.Fatal(err)
	}
	role, err := sessionRoleIn(f.m.store.db, memberBySpawn(t, f.m.store, key).SessionID)
	if err != nil || role != sessionRoleMemberLocal {
		t.Fatalf("role = %q err=%v, want member_local", role, err)
	}
}

// Crash between the claim and the end: at the next boot a killing row whose session the registry still lists goes back to
// active (the lead kills again — nothing is signalled at boot, a pid read then may be stale). One it does NOT list is left for
// the sweeper: at boot a live session may not have registered again yet (codex R1 P1), which is why every other clean-up waits
// out the boot grace.
func TestKillClaim_BootGivesBackWhatIsListedAndLeavesTheRestToTheSweeper(t *testing.T) {
	f := newFixture(t)
	live := f.adoptedMember(t)
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'killing' WHERE spawn_op = ?`, live); err != nil {
		t.Fatal(err)
	}
	f2, root := newTeamFixture(t, 4)
	notYet := f2.member(1, root, "sid-m1", "w-one", nil)
	if _, err := f2.m.store.db.Exec(`UPDATE team_members SET state = 'killing' WHERE spawn_op = ?`, notYet.SpawnOp); err != nil {
		t.Fatal(err)
	}
	f2.so.mu.Lock()
	delete(f2.so.members, "sid-m1") // not registered again (yet)
	f2.so.mu.Unlock()

	f.m.recoverKillingMembers()
	f2.m.recoverKillingMembers()
	if got := f.killRowState(live); got != team.MemberActive {
		t.Errorf("a killing row whose session is listed = %s, want active", got)
	}
	if got := f2.killRowState(notYet.SpawnOp); got != team.MemberKilling {
		t.Errorf("a killing row whose session is not listed at boot = %s, want it left for the sweeper (never gone on the boot's say-so)", got)
	}
	f.m.recoverKillingMembers() // idempotent
	if got := f.killRowState(live); got != team.MemberActive {
		t.Errorf("after a second pass = %s", got)
	}
}

// The sweeper settles a claim that lost its end (codex attacker high 1: a store error after the signal, no restart): once it is
// old enough and the boot grace is over, a process the table says is gone → gone; one still there → active again.
// Mutation gate: no settlement → both stay killing for ever → red.
func TestKillClaim_TheSweeperSettlesAStuckClaim(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	sid := memberBySpawn(t, f.m.store, key).SessionID
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'killing', updated_at = ? WHERE spawn_op = ?`, f.m.now(), key); err != nil {
		t.Fatal(err)
	}
	f.m.bootAt = f.m.now() - (team.BootGraceS+1)*1000 // the boot grace is over
	f.m.settleStuckKillingMembers()
	if got := f.killRowState(key); got != team.MemberKilling {
		t.Fatalf("a claim younger than %s was settled: %s", killClaimStuckAfter, got)
	}
	f.clock.Add(killClaimStuckAfter.Milliseconds() + 1)
	f.m.settleStuckKillingMembers() // the process is still there
	if got := f.killRowState(key); got != team.MemberActive {
		t.Fatalf("a stuck claim whose process is alive = %s, want active again", got)
	}
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'killing', updated_at = ? WHERE spawn_op = ?`, f.m.now()-killClaimStuckAfter.Milliseconds()-1, key); err != nil {
		t.Fatal(err)
	}
	f.origins.mu.Lock()
	if f.origins.dead == nil {
		f.origins.dead = map[string]bool{}
	}
	f.origins.dead[sid] = true
	f.origins.mu.Unlock()
	f.m.settleStuckKillingMembers() // the process is gone
	if got := f.killRowState(key); got != team.MemberGone {
		t.Fatalf("a stuck claim whose process is gone = %s, want gone", got)
	}
}

func TestKillClaim_TheSweeperWaitsOutTheBootGrace(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'killing', updated_at = 1 WHERE spawn_op = ?`, key); err != nil {
		t.Fatal(err)
	}
	f.m.bootAt = f.m.now() // just booted
	f.m.settleStuckKillingMembers()
	if got := f.killRowState(key); got != team.MemberKilling {
		t.Fatalf("a claim settled within the boot grace: %s", got)
	}
}

// codex attacker high 1, end to end: the signal went out and the terminal write failed — the row must not stay killing until a
// restart. Here the mark fails; the sweeper settles it with no restart.
func TestKillClaim_ATerminalWriteThatFailsConvergesWithoutARestart(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	f.m.killProcess = func(int) error { return nil }
	f.m.beforeKillMark = func(memberRow) { // the store breaks right after the signal
		_, _ = f.m.store.db.Exec(`CREATE TRIGGER fail_mark BEFORE UPDATE ON team_members WHEN NEW.state IN ('killed', 'gone') BEGIN SELECT RAISE(ABORT, 'disk I/O error'); END`)
	}
	code, _, _, _ := f.killTarget("_def456")
	f.m.beforeKillMark = nil
	_, _ = f.m.store.db.Exec(`DROP TRIGGER fail_mark`)
	if code != http.StatusInternalServerError {
		t.Fatalf("kill = %d, want 500 (the mark failed)", code)
	}
	if got := f.killRowState(key); got != team.MemberKilling {
		t.Fatalf("row = %s, want killing (the failure is what this test makes)", got)
	}
	f.m.bootAt = f.m.now() - (team.BootGraceS+1)*1000
	f.clock.Add(killClaimStuckAfter.Milliseconds() + 1)
	f.m.settleStuckKillingMembers()
	if got := f.killRowState(key); got == team.MemberKilling {
		t.Fatal("the row stayed killing although the daemon never restarted")
	}
}

// codex R1 P2: an adopted notice not yet sent survives a claim that is given back.
func TestKillClaim_AnUnsentAdoptNoticeSurvivesAGivenBackClaim(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET notice_pending = ?, notice_since = 5 WHERE spawn_op = ?`, team.NoticeAdopted, key); err != nil {
		t.Fatal(err)
	}
	before, _ := f.noticeOf(t, key)
	if before != team.NoticeAdopted {
		t.Fatalf("test setup: the notice is %q, want it pending", before)
	}
	f.m.killProcess = func(int) error {
		// the notice worker's housekeeping runs while the claim is held (the row is killing), then the signal fails
		if _, err := f.m.store.DropStaleAdoptNotices(); err != nil {
			t.Error(err)
		}
		return syscall.EPERM
	}
	f.killTarget("_def456") // claimed, failed, given back
	if got := f.killRowState(key); got != team.MemberActive {
		t.Fatalf("row = %s, want active again", got)
	}
	if after, _ := f.noticeOf(t, key); after != before {
		t.Fatalf("the adopted notice %q became %q across a given-back claim", before, after)
	}
}

// codex attacker high 2: while a row is killing the unique index on active rows does not cover it, so the paths that put a
// session in a seat refuse a session that is killing. And if one slips through anyway, the give-back ends the row gone instead of
// leaving it killing.
func TestKillClaim_ASessionThatIsKillingIsNotAdoptedOrRegisteredAgain(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	sid := memberBySpawn(t, f.m.store, key).SessionID
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'killing' WHERE spawn_op = ?`, key); err != nil {
		t.Fatal(err)
	}
	if _, held, err := f.m.store.LiveMemberSeat(sid); err != nil || !held {
		t.Fatalf("LiveMemberSeat = %v %v, want the seat held while killing", held, err)
	}
	var one int
	err := f.m.store.db.QueryRow(`SELECT 1 FROM team_members WHERE session_id = ? AND state IN ('active', 'killing')`, sid).Scan(&one)
	if err != nil {
		t.Fatalf("the conflict check does not see a killing row: %v", err)
	}
}

func TestKillClaim_AGiveBackThatMeetsTheUniqueIndexEndsTheRowGone(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	mr := memberBySpawn(t, f.m.store, key)
	if claimed, err := f.m.store.ClaimMemberKilling(mr.SpawnOp, mr.SessionID, 5); err != nil || !claimed {
		t.Fatalf("claim = %v %v", claimed, err)
	}
	// a newer membership of the same session slips in while the row is killing (the index covers active rows only)
	other := newMember("dup1", mr.TeamID, mr.SessionID, mr.Ref, 6)
	if err := f.m.store.InsertMember(other); err != nil {
		t.Fatalf("the competing membership could not be inserted: %v", err)
	}
	given, err := f.m.store.GiveBackMemberKilling(mr.SpawnOp, mr.SessionID, 7)
	if err != nil || given {
		t.Fatalf("give back = %v %v, want not given and no error", given, err)
	}
	if got := f.killRowState(key); got != team.MemberGone {
		t.Fatalf("row = %s, want gone (not stuck killing)", got)
	}
}

// The sweeper's tick runs the settlement (the unit tests above call it directly). Mutation gate: drop it from tick → red.
func TestKillClaim_TheSweeperTickSettlesAStuckClaim(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	f.m.bootAt = f.m.now() - (team.BootGraceS+1)*1000
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'killing', updated_at = ? WHERE spawn_op = ?`, f.m.now()-killClaimStuckAfter.Milliseconds()-1, key); err != nil {
		t.Fatal(err)
	}
	f.m.tickN = livenessEvery - 1 // the next tick is a liveness tick
	f.m.tick()
	if got := f.killRowState(key); got != team.MemberActive {
		t.Fatalf("after a liveness tick the stuck claim is %s, want active again", got)
	}
}

// codex attacker high 2, the facts route: a registered fact whose session is killing here is a session_conflict (the op fails),
// as for an active one. Mutation gate: look at active rows only → red.
func TestKillClaim_ARegisteredFactForAKillingSessionFailsTheOp(t *testing.T) {
	f := factFixture(t)
	f.remoteRow("abc12", "hostN", "mkx", string(team.MemberKilling)) // session "sid-abc12", a kill in flight
	f.remoteSpawn("op1", "lead:1", "")
	fact := registeredFact(factUUID1, "op1")
	fact.MemberSession = "sid-abc12"
	if code, body := f.postFact(leadPrincipal(), fact); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if st, reason := f.spawnState("op1"); st != "failed" || reason != "session_conflict" {
		t.Fatalf("op = %s{%s}, want failed{session_conflict}", st, reason)
	}
}

// codex re-review P2: a claim that got stuck and whose team then ended is settled too — EndTeam leaves member rows as they are,
// so nothing else would, and the row would sit in killing until a restart. Mutation gate: only live teams → red.
func TestKillClaim_TheSweeperSettlesAStuckClaimOfAnEndedTeam(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	mr := memberBySpawn(t, f.m.store, key)
	f.m.bootAt = f.m.now() - (team.BootGraceS+1)*1000
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'killing', updated_at = ? WHERE spawn_op = ?`, f.m.now()-killClaimStuckAfter.Milliseconds()-1, key); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.store.db.Exec(`UPDATE teams SET ended_at = 5 WHERE id = ?`, mr.TeamID); err != nil {
		t.Fatal(err)
	}
	f.m.settleStuckKillingMembers()
	if got := f.killRowState(key); got == team.MemberKilling {
		t.Fatal("a stuck claim of an ended team stayed killing")
	}
}
