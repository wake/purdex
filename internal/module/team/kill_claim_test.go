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
// active (the lead kills again — nothing is signalled at boot, a pid read then may be stale); one it no longer lists is gone.
func TestKillClaim_BootGivesBackOrEndsAKillingClaim(t *testing.T) {
	f := newFixture(t)
	live := f.adoptedMember(t) // sid-10, listed by the registry
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'killing' WHERE spawn_op = ?`, live); err != nil {
		t.Fatal(err)
	}
	f2, root := newTeamFixture(t, 4)
	dead := f2.member(1, root, "sid-m1", "w-one", nil)
	if _, err := f2.m.store.db.Exec(`UPDATE team_members SET state = 'killing' WHERE spawn_op = ?`, dead.SpawnOp); err != nil {
		t.Fatal(err)
	}
	f2.so.mu.Lock()
	delete(f2.so.members, "sid-m1") // the registry no longer lists it
	f2.so.mu.Unlock()

	f.m.recoverKillingMembers()
	f2.m.recoverKillingMembers()
	if got := f.killRowState(live); got != team.MemberActive {
		t.Errorf("a killing row whose session is still listed = %s, want active", got)
	}
	if got := f2.killRowState(dead.SpawnOp); got != team.MemberGone {
		t.Errorf("a killing row whose session is gone = %s, want gone", got)
	}
	f.m.recoverKillingMembers() // idempotent
	if got := f.killRowState(live); got != team.MemberActive {
		t.Errorf("after a second pass = %s", got)
	}
}
