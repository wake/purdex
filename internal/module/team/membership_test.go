package teammod

import (
	"testing"

	"github.com/wake/purdex/internal/team"
)

// The membership read the push module's badge rules use (badge spec §3.1): who counts as a "team member".
var _ team.MembershipReader = (*Store)(nil)

func isMembership(t *testing.T, s *Store, sid string) bool {
	t.Helper()
	got, err := s.Membership(sid)
	if err != nil {
		t.Fatalf("Membership(%q): %v", sid, err)
	}
	return got
}

// Each row state is pinned: only `active` is a member (a kill in flight, a finished seat and an unanswered adopt are not).
// Mutation gate: widen the state set (e.g. add 'killing') / drop the state filter → red.
func TestMembership_OnlyActiveLocalRowsAreMembers(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, "team-1", "lead-1", 1000)
	for _, st := range []team.MemberState{team.MemberActive, team.MemberKilled, team.MemberGone, team.MemberReleased,
		team.MemberJoining, team.MemberReleasing, team.MemberKilling, team.MemberFailed} {
		sid := "sid-" + string(st)
		seedMember(t, s, "op-"+string(st), "team-1", sid, 2000)
		if err := s.SetMemberState("op-"+string(st), st, 2001); err != nil {
			t.Fatalf("%s: %v", st, err)
		}
		if got, want := isMembership(t, s, sid), st == team.MemberActive; got != want {
			t.Errorf("state %s: member = %v, want %v", st, got, want)
		}
	}
}

// #2757: after a team ends its member rows stay `active`, but those sessions are no longer members.
// Mutation gate: drop the `ended_at = 0` join condition → red.
func TestMembership_MemberOfAnEndedTeamIsNot(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, "team-1", "lead-1", 1000)
	seedMember(t, s, "op-1", "team-1", "sid-m", 2000)
	if !isMembership(t, s, "sid-m") {
		t.Fatal("an active member of a live team is a member")
	}
	if ended, err := s.EndTeam("team-1", "lead-1", team.TeamEndLeadGone, 3000); err != nil || !ended {
		t.Fatalf("end team: %v %v", ended, err)
	}
	if isMembership(t, s, "sid-m") {
		t.Fatal("the row is still active, but its team ended: not a member")
	}
}

func TestMembership_ALeadIsNotAMember(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, "team-1", "lead-1", 1000)
	seedMember(t, s, "op-1", "team-1", "sid-m", 2000)
	if isMembership(t, s, "lead-1") {
		t.Fatal("the lead of a live team is not a member")
	}
	// even with a member row of its own (a lead that was a member of another team)
	seedTeam(t, s, "team-2", "lead-2", 1000)
	seedMember(t, s, "op-2", "team-2", "lead-1", 2000)
	if isMembership(t, s, "lead-1") {
		t.Fatal("a live lead with a member row elsewhere is still not a member")
	}
}

func TestMembership_NoSessionAndUnknownSessionAreNotMembers(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, "team-1", "lead-1", 1000)
	seedMember(t, s, "op-1", "team-1", "sid-m", 2000)
	if isMembership(t, s, "") {
		t.Fatal("an event with no session id is not a member")
	}
	if isMembership(t, s, "nobody") {
		t.Fatal("an unknown session is not a member")
	}
}

// A member row of a seat on another host (this host leads the team) describes a session running elsewhere: its events reach
// its own host's daemon, so this host does not call it a member. Mutation gate: drop the local-host filter → red.
func TestMembership_RemoteSeatOnALeadHostIsNotConsulted(t *testing.T) {
	s := openTestStore(t)
	s.localHostID = "h:1"
	seedTeam(t, s, "team-1", "lead-1", 1000)
	seedMember(t, s, "op-1", "team-1", "sid-local", 2000) // HostID h:1 = this host
	far := newMember("op-2", "team-1", "sid-far", "_far", 2000)
	far.HostID = "h:2"
	if err := s.InsertMember(far); err != nil {
		t.Fatal(err)
	}
	if !isMembership(t, s, "sid-local") {
		t.Fatal("a member running here is a member")
	}
	if isMembership(t, s, "sid-far") {
		t.Fatal("a seat that runs on another host is not consulted here")
	}
}

// (b): a session adopted by a lead elsewhere and running here. Only an `active` row counts.
// Mutation gate: drop the remote branch / accept a non-active state → red.
func TestMembership_ActiveRemoteMemberRunningHereIsAMember(t *testing.T) {
	s := openTestStore(t)
	for i, st := range []string{remoteActive, remoteReleased, remoteKilled, remoteGone, remoteEnded} {
		sid := "sid-r-" + st
		r := newRemote("mk-"+st, sid, "host-L", int64(1000+i))
		r.State = st
		if err := s.InsertRemoteMember(r); err != nil {
			t.Fatal(err)
		}
		if got, want := isMembership(t, s, sid), st == remoteActive; got != want {
			t.Errorf("remote state %s: member = %v, want %v", st, got, want)
		}
	}
}

// A read error is an error, never "not a member": the caller decides how to treat the unknown.
func TestMembership_StoreErrorIsReturned(t *testing.T) {
	s := openTestStore(t)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Membership("sid-m"); err == nil || got {
		t.Fatalf("a closed store: member = %v, err = %v; want an error", got, err)
	}
}

// The module publishes the reader under its key, so the push module can find it; it answers from the module's own store.
// Mutation gate: drop the Register line → red.
func TestMembership_RegisteredForThePushModule(t *testing.T) {
	f := newFixture(t)
	svc, ok := f.core.Registry.Get(team.MembershipReaderKey)
	r, isReader := svc.(team.MembershipReader)
	if !ok || !isReader {
		t.Fatalf("registry has no MembershipReader under %q (%T)", team.MembershipReaderKey, svc)
	}
	f.makeMember("sid-m")
	if got, err := r.Membership("sid-m"); err != nil || !got {
		t.Fatalf("Membership(sid-m) = %v, %v; want true", got, err)
	}
}
