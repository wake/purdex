package teammod

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// A mod that reloads between written and cleared (pdx setup, #2441) asks its daemon what relay it was in the middle of:
// hello carries the session's open op when it is one the mod can pick up (claimed / writing / written), and the lead
// of a member op. Earlier states keep today's paths (the control message, the wait), terminal ops are nobody's.
// Mutation gates: no active_relay in hello → red; any non-terminal state answered → the earlier-state case red.
func TestRelayHello_CarriesTheOpTheModMustPickUp(t *testing.T) {
	for _, tc := range []struct {
		state team.RelayState
		want  bool
	}{
		{team.RelayAwaitingApproval, false}, {team.RelayRequested, false},
		{team.RelayClaimed, true}, {team.RelayWriting, true}, {team.RelayWritten, true},
		{team.RelayCleared, false}, {team.RelayDone, false}, {team.RelayFailed, false}, {team.RelayCancelled, false},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			f := newFixture(t)
			if err := f.m.store.CreateRelayOp(team.RelayOp{ID: "op-1", Kind: team.RelayKindSelf, HostID: "h:1", SessionID: "sid-1", Ref: "_abc123",
				State: tc.state, HandoffPath: "/d/op-1.md", CreatedAt: 1, UpdatedAt: 1}); err != nil {
				t.Fatal(err)
			}
			code, h, body := f.hello("sid-1")
			if code != http.StatusOK {
				t.Fatalf("hello: %d %s", code, body)
			}
			if !tc.want {
				if h.ActiveRelay != nil {
					t.Fatalf("a %s op was handed to the mod: %+v", tc.state, h.ActiveRelay)
				}
				return
			}
			if h.ActiveRelay == nil || h.ActiveRelay.Op.ID != "op-1" || h.ActiveRelay.Op.State != tc.state || h.ActiveRelay.Op.HandoffPath != "/d/op-1.md" || h.ActiveRelay.Lead != nil {
				t.Fatalf("active_relay = %+v, want op-1 in %s and no lead (a self op)", h.ActiveRelay, tc.state)
			}
		})
	}
}

// Another session's op is nobody else's: the answer is for the asking session only.
func TestRelayHello_ActiveRelayIsTheSessionsOwn(t *testing.T) {
	f := newFixture(t)
	if err := f.m.store.CreateRelayOp(team.RelayOp{ID: "op-1", Kind: team.RelayKindSelf, HostID: "h:1", SessionID: "sid-1", Ref: "_abc123",
		State: team.RelayWritten, HandoffPath: "/d/op-1.md", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if _, h, body := f.hello("sid-other"); h.ActiveRelay != nil {
		t.Fatalf("sid-other got %s", body)
	}
}

// A member op carries the lead the member reports to (the seed names it), as claim does.
func TestRelayHello_MemberOpCarriesItsLead(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1")
	if err := f.m.store.CreateRelayOp(team.RelayOp{ID: "op-m", Kind: team.RelayKindMember, HostID: "h:1", SessionID: "sid-1", Ref: "_mem123", TeamID: uid(9),
		State: team.RelayWritten, HandoffPath: "/d/op-m.md", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	_, h, body := f.hello("sid-1")
	if h.ActiveRelay == nil || h.ActiveRelay.Op.ID != "op-m" || h.ActiveRelay.Lead == nil || h.ActiveRelay.Lead.TeamID != uid(9) {
		t.Fatalf("hello = %s, want op-m with the team's lead", body)
	}
}

// pdx setup names the ops it would strand (#2441): inflight lists the active relays, not only counts them.
func TestInflight_NamesTheActiveRelays(t *testing.T) {
	f := newFixture(t)
	if err := f.m.store.CreateRelayOp(team.RelayOp{ID: "op-1", Kind: team.RelayKindSelf, HostID: "h:1", SessionID: "sid-1", Ref: "_abc123",
		State: team.RelayWritten, HandoffPath: "/d/op-1.md", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := f.m.store.CreateRelayOp(team.RelayOp{ID: "op-2", Kind: team.RelayKindSelf, HostID: "h:1", SessionID: "sid-2", Ref: "_def456",
		State: team.RelayDone, HandoffPath: "/d/op-2.md", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	var inf team.InflightResponse
	if code, body := f.do(http.MethodGet, "/api/team/inflight", nil); code != http.StatusOK || json.Unmarshal(body, &inf) != nil {
		t.Fatalf("inflight: %d %s", code, body)
	}
	if inf.RelaysActive != 1 || len(inf.Relays) != 1 || inf.Relays[0] != (team.InflightRelay{ID: "op-1", Kind: team.RelayKindSelf, State: team.RelayWritten, Ref: "_abc123"}) {
		t.Fatalf("inflight = %+v", inf)
	}
}
