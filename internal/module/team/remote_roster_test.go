// internal/module/team/remote_roster_test.go
package teammod

import (
	"context"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

func rosterMemberOf(t *testing.T, r team.Roster, sessionID string) (team.RosterMember, bool) {
	t.Helper()
	for _, tr := range r.Teams {
		for _, m := range tr.Members {
			if m.SessionID == sessionID {
				return m, true
			}
		}
	}
	return team.RosterMember{}, false
}

// X5: the roster shows a remote member while it is in play — joining / active / releasing / killing, as the lead host
// holds them — addressed by its host's alias; one that left (released, killed, gone, failed) is out as before.
func TestRemoteRoster_RemoteMembersShowTheirStateAndHost(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("j1", "hostM", "mk1", rowJoining)
	f.remoteRow("a1", "hostM", "mk2", rowActive)
	f.remoteRow("r1", "hostM", "mk3", string(team.MemberReleasing))
	f.remoteRow("k1", "hostM", "mk4", string(team.MemberKilling))
	f.remoteRow("x1", "hostM", "mk5", string(team.MemberReleased))
	f.remoteRow("g1", "hostM", "mk6", string(team.MemberGone))
	f.remoteRow("f1", "hostM", "mk7", string(team.MemberFailed))
	f.m.peerRecords = func(context.Context, string) ([]ipeers.PeerRecord, error) {
		return []ipeers.PeerRecord{remoteRecord("sid-a1", 30, "claude-sonnet-5-5", "")}, nil
	}
	f.m.readRemoteHost(context.Background(), "hostM") // what the background refresh stores

	r, err := f.m.buildRoster()
	if err != nil {
		t.Fatal(err)
	}
	for sid, want := range map[string]team.MemberState{"sid-j1": team.MemberJoining, "sid-a1": team.MemberActive, "sid-r1": team.MemberReleasing, "sid-k1": team.MemberKilling} {
		m, ok := rosterMemberOf(t, r, sid)
		if !ok || m.State != want || m.HostID != "hostM" || m.HostAlias != "air26" || m.Address != "air26/_r"+sid[len("sid-"):] {
			t.Fatalf("%s = %+v ok=%v", sid, m, ok)
		}
	}
	for _, sid := range []string{"sid-x1", "sid-g1", "sid-f1"} {
		if _, ok := rosterMemberOf(t, r, sid); ok {
			t.Fatalf("%s is on the roster though it left", sid)
		}
	}
	a, _ := rosterMemberOf(t, r, "sid-a1")
	// Model / Effort keep their meaning on the roster (what the row was made with: newMember's "sonnet"/"high"); what the
	// member host reports it runs is in context.model_id, as for a local member.
	if a.Context == nil || a.Context.ModelID != "claude-sonnet-5-5" || a.Model != "sonnet" || !a.Live || a.ContextUnavailable {
		t.Fatalf("a1 = %+v", a)
	}
	j, _ := rosterMemberOf(t, r, "sid-j1")
	if j.Context != nil || j.Live {
		t.Fatalf("j1 (not in the host's list) = %+v", j)
	}
}

func TestRemoteRoster_FailedHostIsFlagged(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("a1", "hostM", "mk2", rowActive)
	f.m.peerRecords = func(context.Context, string) ([]ipeers.PeerRecord, error) { return nil, context.DeadlineExceeded }
	f.m.readRemoteHost(context.Background(), "hostM")
	r, err := f.m.buildRoster()
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := rosterMemberOf(t, r, "sid-a1"); !m.ContextUnavailable || m.Context != nil {
		t.Fatalf("a1 = %+v", m)
	}
}

// The lead and local members carry no host fields (an App that does not know them sees no change).
func TestRemoteRoster_LocalSessionsHaveNoHostFields(t *testing.T) {
	f, _ := remoteFixture(t)
	r, err := f.m.buildRoster()
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range r.Teams {
		if tr.Lead.HostID != "" || tr.Lead.HostAlias != "" {
			t.Fatalf("lead = %+v", tr.Lead)
		}
		for _, m := range tr.Members {
			if m.HostID != "" || m.HostAlias != "" {
				t.Fatalf("member = %+v", m)
			}
		}
	}
}
