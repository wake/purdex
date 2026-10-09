package teammod

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

var appClientMM = team.Client{Kind: "app", Label: "Purdex.app @ air26"}

func (f *fixture) putMax(teamID string, n int, c team.Client) (int, []byte) {
	f.t.Helper()
	return f.do(http.MethodPut, team.MaxMembersRoute, team.MaxMembersPutRequest{TeamID: teamID, MaxMembers: n, Client: c})
}

func (f *fixture) grantMax() int {
	f.t.Helper()
	code, body := f.do(http.MethodGet, "/api/team?origin_inbox=%2Ftmp%2F10.sock", nil)
	var v team.TeamView
	if code != 200 || json.Unmarshal(body, &v) != nil {
		f.t.Fatalf("GET /api/team = %d %s", code, body)
	}
	return v.Team.Grant.MaxMembers
}

// Mutation gate: accept a client kind other than app -> red.
func TestMaxMembersPut_OnlyTheAppAndInRange(t *testing.T) {
	f, _ := newSpawnFixture(t, 3)
	logs := f.logs()
	for name, c := range map[string]team.Client{
		"terminal": {Kind: "terminal", Label: "x"}, "unattended": {Kind: "unattended", Label: "x"}, "no label": {Kind: "app"}, "none": {},
	} {
		if code, body := f.putMax(uid(1), 4, c); code != 400 || decodeErr(t, body).Error != team.ErrBadRequest {
			t.Errorf("%s: %d %s, want 400 bad_request", name, code, body)
		}
	}
	if code, body := f.putMax("", 4, appClientMM); code != 400 || decodeErr(t, body).Error != team.ErrBadRequest {
		t.Errorf("no team_id: %d %s", code, body)
	}
	// Mutation gate: allow 0 or 9 -> red.
	for _, n := range []int{0, 9, -1} {
		if code, body := f.putMax(uid(1), n, appClientMM); code != 400 || decodeErr(t, body).Error != team.ErrBadRequest {
			t.Errorf("max %d: %d %s, want 400", n, code, body)
		}
	}
	if got := f.grantMax(); got != 3 {
		t.Errorf("refused requests changed max_members to %d", got)
	}
	for _, n := range []int{1, 8} {
		if code, body := f.putMax(uid(1), n, appClientMM); code != 200 {
			t.Errorf("max %d: %d %s, want 200", n, code, body)
		}
	}
	if !strings.Contains(strings.Join(logs(), "\n"), `"Purdex.app @ air26"`) {
		t.Errorf("no audit line naming the app label: %v", logs())
	}
}

func TestMaxMembersPut_UnknownAndEndedTeamAre404(t *testing.T) {
	f, _ := newSpawnFixture(t, 3)
	if code, body := f.putMax("no-such-team", 4, appClientMM); code != 404 || decodeErr(t, body).Error != team.ErrNotFound {
		t.Errorf("unknown: %d %s", code, body)
	}
	if ok, err := f.m.store.EndTeam(uid(1), "sid-1", "lead_gone", 5); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if code, body := f.putMax(uid(1), 4, appClientMM); code != 404 || decodeErr(t, body).Error != team.ErrNotFound {
		t.Errorf("ended: %d %s", code, body)
	}
}

// Mutation gate: skip the in_use check -> red.
func TestMaxMembersPut_BelowInUseIs409AndExactlyInUseIs200(t *testing.T) {
	f, root := newSpawnFixture(t, 4)
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 1)
	f.acceptOp(9, root, nil) // a spawn still starting counts too
	code, body := f.putMax(uid(1), 1, appClientMM)
	var refusal team.MaxMembersRefusal
	_ = json.Unmarshal(body, &refusal)
	if code != 409 || refusal.Error != team.ErrMaxBelowInUse || refusal.InUse != 2 {
		t.Fatalf("below: %d %s, want 409 max_below_in_use with in_use 2", code, body)
	}
	if got := f.grantMax(); got != 4 {
		t.Fatalf("a refused lowering left max_members %d, want 4", got)
	}
	code, body = f.putMax(uid(1), 2, appClientMM)
	var v team.MaxMembersView
	_ = json.Unmarshal(body, &v)
	if code != 200 || v != (team.MaxMembersView{TeamID: uid(1), MaxMembers: 2, InUse: 2}) {
		t.Fatalf("exactly in_use: %d %s", code, body)
	}
	if got := f.grantMax(); got != 2 {
		t.Fatalf("GET /api/team grant.max_members = %d, want 2", got)
	}
}

func TestMaxMembersPut_RaisingLetsATeamFullSpawnPass(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	f.holdRunners()
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 1)
	if code, _, e := f.spawn(1, root, nil); code != 409 || e.Error != team.ErrTeamFull {
		t.Fatalf("before: %d %+v, want team_full", code, e)
	}
	if code, body := f.putMax(uid(1), 2, appClientMM); code != 200 {
		t.Fatalf("raise: %d %s", code, body)
	}
	if code, _, e := f.spawn(1, root, nil); code != 200 {
		t.Fatalf("after: %d %+v, want the cap check to pass", code, e)
	}
}

// Mutation gate: do not signal the roster -> red. The event and GET /api/team/roster carry max_members and in_use.
func TestMaxMembersPut_AnnouncesTheRosterWithTheNewNumbers(t *testing.T) {
	f, _ := newSpawnFixture(t, 3)
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 1)
	f.rosterBaselineNow()
	w := f.watchRoster()
	w.drain() // stale pending signals give false greens
	if code, body := f.putMax(uid(1), 5, appClientMM); code != 200 {
		t.Fatalf("put: %d %s", code, body)
	}
	ev := w.one("max members")
	if len(ev.Teams) != 1 || ev.Teams[0].MaxMembers != 5 || ev.Teams[0].InUse != 1 {
		t.Fatalf("roster event = %+v, want max_members 5 in_use 1", ev.Teams)
	}
	if r := f.getRoster(); len(r.Teams) != 1 || r.Teams[0].MaxMembers != 5 || r.Teams[0].InUse != 1 {
		t.Fatalf("GET roster = %+v", r.Teams)
	}
}
