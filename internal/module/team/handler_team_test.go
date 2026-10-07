package teammod

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// seedLiveTeam makes sid the lead of a live team with id, written straight
// through the store — as a team approved before this request would be —
// so a test can reach the states the handler's own checks keep it from
// producing.
func seedLiveTeam(t *testing.T, f *fixture, id, sid string) {
	t.Helper()
	now := f.clock.Load()
	if _, _, _, err := f.m.store.Create(openApproval(id, sid, now), "seed-"+id); err != nil {
		t.Fatal(err)
	}
	g := team.Grant{MaxMembers: 3, Roots: []string{"/w"}}
	if _, won, err := f.m.store.CloseLeadApproved(id, approveClose(now, g), leadTeam(id, sid, "_abc123", g, now)); err != nil || !won {
		t.Fatalf("seed team %s for %s: won=%v err=%v", id, sid, won, err)
	}
}

func appApprove(g *team.Grant) team.DecideRequest {
	return team.DecideRequest{Decision: "approve", Grant: g, Client: team.Client{Kind: "app", Label: "Purdex.app @ air26"}}
}

// Spec §6.2: approving a lead request creates its team before decide
// answers 200, with the grant as edited in the dialog. A deny makes none.
func TestDecide_ApproveCreatesTheTeamWithTheEditedGrant(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	second := f.createReq(uid(2))
	second.OriginInbox = "/tmp/20.sock"
	if code, body := f.do(http.MethodPost, "/api/team/approvals", second); code != 201 {
		t.Fatalf("second create: %d %s", code, body)
	}
	f.events()
	f.clock.Add(5)

	code, body := f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide", appApprove(&team.Grant{MaxMembers: 2, Roots: []string{"x", "/y/"}}))
	if a := decodeApproval(t, body); code != 200 || a.State != team.StateApproved {
		t.Fatalf("approve: %d %s", code, body)
	}
	got, ok, err := f.m.store.LiveTeamByLead("sid-1")
	want := team.Team{ID: uid(1), HostID: "h:1", LeadSessionID: "sid-1", LeadRef: "_abc123",
		Grant: team.Grant{MaxMembers: 2, Roots: []string{"/w/x", "/y"}}, RequestID: uid(1), CreatedAt: 1_000_005}
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("team after approve = %+v ok=%v err=%v, want %+v", got, ok, err, want)
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d, want 1", n)
	}

	if code, body := f.do(http.MethodPost, "/api/team/approvals/"+uid(2)+"/decide", team.DecideRequest{Decision: "deny", Client: team.Client{Kind: "app", Label: "x"}}); code != 200 {
		t.Fatalf("deny: %d %s", code, body)
	}
	if _, ok, _ := f.m.store.LiveTeamByLead("sid-2"); ok {
		t.Fatal("a denied request created a team")
	}
}

// Decide's floor (spec §6.2 already_lead): an origin that already leads a
// live team gets 409 already_lead, and the row is left open — no close, no
// event, no team. The body carries no approval, so the App does not read it
// as "closed elsewhere" (the row is still open; deny still works). Once
// that team ends, the same row approves.
func TestDecide_AlreadyLeadLeavesTheRowOpen(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	seedLiveTeam(t, f, uid(9), "sid-1")
	f.events()

	code, body := f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide", appApprove(nil))
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrAlreadyLead || e.Approval != nil {
		t.Fatalf("approve while leading: %d %s, want 409 already_lead without an approval", code, body)
	}
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateOpen || a.Grant != nil || a.DecidedBy != nil || a.DecidedAt != 0 {
		t.Fatalf("row after already_lead = %+v, want open and untouched", a)
	}
	if _, ok := getTeam(t, f.m.store, uid(1)); ok {
		t.Fatal("already_lead inserted a team")
	}
	if n := len(f.events()); n != 0 {
		t.Fatalf("%d events after already_lead", n)
	}

	if ended, err := f.m.store.EndTeam(uid(9), "sid-1", team.TeamEndLeadGone, f.clock.Load()); err != nil || !ended {
		t.Fatalf("end the seeded team: ended=%v err=%v", ended, err)
	}
	if code, body := f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide", appApprove(nil)); code != 200 {
		t.Fatalf("approve after the old team ended: %d %s", code, body)
	}
	if got, ok, _ := f.m.store.LiveTeamByLead("sid-1"); !ok || got.ID != uid(1) {
		t.Fatalf("live team of sid-1 = %+v ok=%v, want %s", got, ok, uid(1))
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d, want 1", n)
	}
}

// Create (spec §6.2): a live lead is refused 409 already_lead, after the
// idempotent replay (the approving request still answers 200) and after
// request_open. An ended team does not count.
func TestCreate_AlreadyLeadIs409_EndedTeamDoesNotCount(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	if code, body := f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide", appApprove(nil)); code != 200 {
		t.Fatalf("approve: %d %s", code, body)
	}
	f.events()
	f.clock.Add(1)

	code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(2)))
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrAlreadyLead {
		t.Fatalf("create while leading: %d %s, want 409 already_lead", code, body)
	}
	if _, ok, _ := f.m.store.Get(uid(2)); ok {
		t.Fatal("already_lead stored the request")
	}
	if n := len(f.events()); n != 0 {
		t.Fatalf("%d events after already_lead", n)
	}
	// Replay first: the approving request answers its row, not already_lead.
	code, body = f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1)))
	if a := decodeApproval(t, body); code != 200 || a.ID != uid(1) || a.State != team.StateApproved {
		t.Fatalf("replay of the approving request: %d %s", code, body)
	}

	// The team ends: the session may ask again.
	if ended, err := f.m.store.EndTeam(uid(1), "sid-1", team.TeamEndLeadGone, f.clock.Load()); err != nil || !ended {
		t.Fatalf("end: ended=%v err=%v", ended, err)
	}
	f.create(uid(3))

	// request_open comes before already_lead: sid-2 has an open request and
	// (seeded) a live team; a new id gets request_open carrying the open row.
	open := f.createReq(uid(4))
	open.OriginInbox = "/tmp/20.sock"
	if code, body := f.do(http.MethodPost, "/api/team/approvals", open); code != 201 {
		t.Fatalf("sid-2 create: %d %s", code, body)
	}
	seedLiveTeam(t, f, uid(8), "sid-2")
	next := f.createReq(uid(5))
	next.OriginInbox = "/tmp/20.sock"
	code, body = f.do(http.MethodPost, "/api/team/approvals", next)
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrRequestOpen || e.Approval == nil || e.Approval.ID != uid(4) {
		t.Fatalf("open request and live team: %d %s, want 409 request_open carrying %s", code, body, uid(4))
	}
}
