// internal/module/team/facts_store_test.go
package teammod

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func factsOf(t *testing.T, s *Store, hostID string) []factRow {
	t.Helper()
	f, err := s.FactsOfHost(hostID)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func decodeFact(t *testing.T, f factRow) team.TeamFact {
	t.Helper()
	var out team.TeamFact
	if err := json.Unmarshal([]byte(f.BodyJSON), &out); err != nil {
		t.Fatalf("fact body %q: %v", f.BodyJSON, err)
	}
	return out
}

// §5.2: an active remote member whose session is gone → gone, with the `ended` fact for the lead host in the same
// transaction (§3.1 rule 2); no notice (nobody is there to read it).
func TestMarkRemoteMemberGone_WritesTheEndedFactWithIt(t *testing.T) {
	s := openTestStore(t)
	seedRemote(t, s, "mk-1", "sid-1", 1000)
	gone, err := s.MarkRemoteMemberGone("mk-1", "sid-1", "fact-1", 2000)
	if err != nil || !gone {
		t.Fatalf("gone=%v err=%v", gone, err)
	}
	row, _, _ := s.RemoteMember("mk-1")
	if row.State != remoteGone || row.UpdatedAt != 2000 {
		t.Fatalf("row = %+v", row)
	}
	facts := factsOf(t, s, "host-L")
	if len(facts) != 1 || facts[0].ID != "fact-1" || facts[0].State != factPending || facts[0].Kind != team.FactEnded || facts[0].MK != "mk-1" {
		t.Fatalf("facts = %+v", facts)
	}
	f := decodeFact(t, facts[0])
	if f.ID != "fact-1" || f.Kind != team.FactEnded || f.ToHostID != "host-L" || f.TeamID != "team-L" || f.MK != "mk-1" || f.Reason != team.FactReasonSessionGone {
		t.Fatalf("fact = %+v", f)
	}
	if n := noticesOf(t, s, "mk-1"); len(n) != 0 {
		t.Fatalf("a gone member owes a notice: %+v", n)
	}
	// Monotonic: a second sighting, or another session id, changes nothing and writes no second fact.
	if again, _ := s.MarkRemoteMemberGone("mk-1", "sid-1", "fact-2", 3000); again {
		t.Fatal("gone twice")
	}
	if len(factsOf(t, s, "host-L")) != 1 {
		t.Fatal("a second ended fact was written")
	}
}

func TestMarkRemoteMemberGone_OnlyTheSessionTheRowNames(t *testing.T) {
	s := openTestStore(t)
	seedRemote(t, s, "mk-1", "sid-1", 1000)
	if gone, _ := s.MarkRemoteMemberGone("mk-1", "sid-other", "f", 2000); gone {
		t.Fatal("marked gone on another session's absence")
	}
	if row, _, _ := s.RemoteMember("mk-1"); row.State != remoteActive || len(factsOf(t, s, "host-L")) != 0 {
		t.Fatalf("row = %+v", row)
	}
}

// The operator ends a remote member here: ended, the `ended{local_end}` fact for the lead host, and a notice
// for the member — one transaction. The role is gone with it (self relay is on again).
func TestEndRemoteMemberLocally(t *testing.T) {
	s := openTestStore(t)
	seedRemote(t, s, "mk-1", "sid-1", 1000)
	res, err := s.EndRemoteMemberLocally("mk-1", "fact-1", "cause-1", 2000)
	if err != nil || res != remoteEndEnded {
		t.Fatalf("res=%v err=%v", res, err)
	}
	if row, _, _ := s.RemoteMember("mk-1"); row.State != remoteEnded {
		t.Fatalf("row = %+v", row)
	}
	if role, _ := s.SessionRole("sid-1"); role != sessionRoleNone {
		t.Fatalf("role = %s", role)
	}
	facts := factsOf(t, s, "host-L")
	if len(facts) != 1 || decodeFact(t, facts[0]).Reason != team.FactReasonLocalEnd {
		t.Fatalf("facts = %+v", facts)
	}
	n := noticesOf(t, s, "mk-1")
	if len(n) != 1 || n[0].Kind != noticeLocalEnd || n[0].CauseID != "cause-1" || n[0].LeadAddress != "lead/x [lead01]" {
		t.Fatalf("notices = %+v", n)
	}
	if res, _ := s.EndRemoteMemberLocally("mk-1", "fact-2", "cause-2", 3000); res != remoteEndNotLive {
		t.Fatalf("second end = %v, want not-live", res)
	}
	if res, _ := s.EndRemoteMemberLocally("mk-nope", "fact-3", "cause-3", 3000); res != remoteEndNotFound {
		t.Fatalf("unknown mk = %v, want not-found", res)
	}
	if len(factsOf(t, s, "host-L")) != 1 {
		t.Fatal("a refused end wrote a fact")
	}
}

// §3.1 rule 2 / §11 crash cut: the state change, the fact and the notice are one transaction.
func TestEndRemoteMemberLocally_IsOneTransaction(t *testing.T) {
	s := openTestStore(t)
	seedRemote(t, s, "mk-1", "sid-1", 1000)
	s.failAfterFactInsert = func() error { return errors.New("injected crash") }
	if _, err := s.EndRemoteMemberLocally("mk-1", "fact-1", "cause-1", 2000); err == nil {
		t.Fatal("injected failure was swallowed")
	}
	if row, _, _ := s.RemoteMember("mk-1"); row.State != remoteActive {
		t.Fatalf("the end survived a rolled-back transaction: %+v", row)
	}
	if len(factsOf(t, s, "host-L")) != 0 || len(noticesOf(t, s, "mk-1")) != 0 {
		t.Fatal("a fact or notice survived the rollback")
	}
	s.failAfterFactInsert = nil
	if res, err := s.EndRemoteMemberLocally("mk-1", "fact-1", "cause-1", 2000); err != nil || res != remoteEndEnded {
		t.Fatalf("retry = %v %v", res, err)
	}
}

// §3.2 unpairing on M: every live row whose lead host is no longer paired → ended, with NO notice and NO fact (the
// lead host is not bound, there is nobody to tell and nobody to hear it); that host's queued facts are dropped.
// Terminal rows stay as they ended. (Mutation: ending rows of still-paired hosts, or keeping the facts → red.)
func TestEndUnpairedRemoteMembers(t *testing.T) {
	s := openTestStore(t)
	for i, host := range []string{"host-A", "host-B", "host-C"} {
		r := newRemote("mk-"+host, "sid-"+host, host, int64(1000+i))
		if err := s.InsertRemoteMember(r); err != nil {
			t.Fatal(err)
		}
	}
	// host-A's member had gone earlier: its ended fact is still queued; host-B's rows are live.
	if _, err := s.MarkRemoteMemberGone("mk-host-A", "sid-host-A", "fact-A", 1500); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkRemoteMemberGone("mk-host-C", "sid-host-C", "fact-C", 1500); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRemoteMemberState("mk-host-B", []string{remoteActive}, remoteReleased, 1600); err != nil {
		t.Fatal(err)
	}
	seedRemote(t, s, "mk-live", "sid-live", 1700) // host-L
	seedRemote(t, s, "mk-keep", "sid-keep", 1700)
	keep := newRemote("mk-b2", "sid-b2", "host-B", 1700)
	if err := s.InsertRemoteMember(keep); err != nil {
		t.Fatal(err)
	}

	// Paired now: host-L only. host-A, host-B and host-C are gone from the config.
	n, err := s.EndUnpairedRemoteMembers([]string{"host-L"}, 5000)
	if err != nil || n != 1 {
		t.Fatalf("ended %d err=%v, want 1 (host-B's live row)", n, err)
	}
	for mk, want := range map[string]string{"mk-b2": remoteEnded, "mk-host-A": remoteGone, "mk-host-B": remoteReleased, "mk-live": remoteActive, "mk-keep": remoteActive} {
		if row, _, _ := s.RemoteMember(mk); row.State != want {
			t.Fatalf("%s = %s, want %s", mk, row.State, want)
		}
	}
	if len(noticesOf(t, s, "mk-b2")) != 0 {
		t.Fatal("an unpaired end owes a notice")
	}
	for _, host := range []string{"host-A", "host-C"} {
		for _, f := range factsOf(t, s, host) {
			if f.State != factDropped {
				t.Fatalf("%s fact %s = %s, want dropped", host, f.ID, f.State)
			}
		}
	}
	if len(factsOf(t, s, "host-B")) != 0 {
		t.Fatal("an unpaired end wrote an ended fact")
	}

	// Nothing paired at all: every live row ends.
	if n, err := s.EndUnpairedRemoteMembers(nil, 6000); err != nil || n != 2 {
		t.Fatalf("ended %d err=%v, want the 2 left", n, err)
	}
}

// A live row's facts are kept while its host is paired; dropped facts stay dropped.
func TestEndUnpairedRemoteMembers_KeepsThePairedHostsFacts(t *testing.T) {
	s := openTestStore(t)
	seedRemote(t, s, "mk-1", "sid-1", 1000)
	seedRemote(t, s, "mk-2", "sid-2", 1000)
	if _, err := s.MarkRemoteMemberGone("mk-1", "sid-1", "fact-1", 1500); err != nil {
		t.Fatal(err)
	}
	if n, err := s.EndUnpairedRemoteMembers([]string{"host-L"}, 2000); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if f := factsOf(t, s, "host-L"); len(f) != 1 || f[0].State != factPending {
		t.Fatalf("facts = %+v", f)
	}
}
