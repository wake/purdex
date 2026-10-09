package teammod

import (
	"context"
	"database/sql"
	"net/http"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// Adopt over HTTP (adopt plan PL-1c). sid-1 (_abc123) leads; sid-2 (_def456) is the target.

func (f *fixture) adoptReq(id, target string) team.CreateApprovalRequest {
	return team.CreateApprovalRequest{ID: id, Kind: team.KindAdopt, OriginInbox: "/tmp/10.sock", Target: target}
}

func (f *fixture) adopt(id, target string) (int, []byte) {
	f.t.Helper()
	return f.do(http.MethodPost, "/api/team/approvals", f.adoptReq(id, target))
}

// adoptOK opens adopt request id for target and fails unless it answers 201.
func (f *fixture) adoptOK(id, target string) team.Approval {
	f.t.Helper()
	code, body := f.adopt(id, target)
	if code != http.StatusCreated {
		f.t.Fatalf("adopt %s %q: %d %s", id, target, code, body)
	}
	return decodeApproval(f.t, body)
}

// wantRefusal fails unless the answer is 409/400/503 with code, and no approval row was written for id.
func (f *fixture) wantAdoptRefusal(id, target string, status int, code string) team.APIError {
	f.t.Helper()
	got, body := f.adopt(id, target)
	e := decodeErr(f.t, body)
	if got != status || e.Error != code {
		f.t.Fatalf("adopt %q = %d %s, want %d %s", target, got, body, status, code)
	}
	if _, ok, _ := f.m.store.Get(id); ok {
		f.t.Fatalf("a refused adopt %s left a row", id)
	}
	return e
}

func TestAdoptCreate_RefusalsBeforeOpening(t *testing.T) {
	f := newFixture(t)
	f.wantAdoptRefusal(uid(10), "_def456", http.StatusConflict, team.ErrNotLead)
	f.approveLead(uid(1))
	f.events() // drain
	f.wantAdoptRefusal(uid(11), "other/_def456", http.StatusConflict, team.ErrRemoteUnsupported)
	f.wantAdoptRefusal(uid(12), "_zzzzzz", http.StatusConflict, team.ErrAdoptTargetNotFound)
	f.wantAdoptRefusal(uid(13), "_abc123", http.StatusConflict, team.ErrAdoptSelf)
	f.origins.setRefAmbiguous("_def456")
	f.wantAdoptRefusal(uid(14), "_def456", http.StatusConflict, team.ErrAdoptTargetAmbiguous)
	f.origins.mu.Lock()
	delete(f.origins.ambiguousRef, "_def456")
	f.origins.mu.Unlock()
	// a member target
	f.makeMemberOfLead("sid-2")
	f.wantAdoptRefusal(uid(15), "_def456", http.StatusConflict, team.ErrAdoptAlreadyMember)
	for _, bad := range []string{"", "name-only", "host/name-only", "_ab"} {
		if code, body := f.adopt(uid(16), bad); code != http.StatusBadRequest {
			t.Errorf("target %q = %d %s, want 400", bad, code, body)
		}
	}
	if ops := f.opsOf(); len(ops) != 0 {
		t.Errorf("events = %v, want none for any refusal", ops)
	}
}

// makeMemberOfLead seeds sid as an active member of the lead's team uid(1).
func (f *fixture) makeMemberOfLead(sid string) {
	f.t.Helper()
	seedMember(f.t, f.m.store, "op-"+sid, uid(1), sid, f.clock.Load())
}

func TestAdoptCreate_TargetThatLeadsAndFullTeamAndSecondOpen(t *testing.T) {
	f := newFixture(t)
	f.createReqEdit = func(r *team.CreateApprovalRequest) { r.MaxMembers = 1 }
	f.approveLead(uid(1))
	// sid-2 leads a team of its own
	seedTeam(t, f.m.store, uid(9), "sid-2", f.clock.Load())
	f.wantAdoptRefusal(uid(10), "_def456", http.StatusConflict, team.ErrAdoptTargetIsLead)
	f.origins.show(team.Origin{SessionID: "sid-3", Ref: "_ghi789", PID: 30, Cwd: "/w3"})
	opened := f.adoptOK(uid(11), "_ghi789")
	e := f.wantAdoptRefusal(uid(12), "_ghi789", http.StatusConflict, team.ErrRequestOpen)
	if e.Approval == nil || e.Approval.ID != opened.ID {
		t.Fatalf("request_open carries %+v, want the open row %s", e.Approval, opened.ID)
	}
	if code, body := f.do(http.MethodDelete, "/api/team/approvals/"+opened.ID, nil); code != http.StatusOK {
		t.Fatalf("cancel: %d %s", code, body)
	}
	f.makeMemberOfLead("sid-9") // the single place is taken
	f.wantAdoptRefusal(uid(13), "_ghi789", http.StatusConflict, team.ErrTeamFull)
}

func TestAdoptCreate_OpensWithThePayloadAndBroadcastsOpened(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	f.events()
	a := f.adoptOK(uid(10), "h:1/n20 [def456]")
	if a.State != team.StateOpen || a.Kind != team.KindAdopt || a.Origin.SessionID != "sid-1" {
		t.Fatalf("row = %+v", a)
	}
	p, err := team.AdoptPayloadOf(a)
	if err != nil || p.TeamID != uid(1) || p.LeadSessionID != "sid-1" || p.TargetSessionID != "sid-2" || p.TargetRef != "_def456" || p.TargetCwd != "/w2" {
		t.Fatalf("payload = %+v (%v)", p, err)
	}
	if got, want := a.DeadlineAt-a.CreatedAt, int64(team.DefaultWaitS)*1000; got != want {
		t.Errorf("deadline = +%d ms, want %d", got, want)
	}
	if ops := f.opsOf(); !reflect.DeepEqual(ops, []string{"opened"}) {
		t.Errorf("events = %v, want [opened]", ops)
	}
	// every accepted spelling names the same target
	for i, tgt := range []string{"_def456", "def456", "h:1/_def456", "H:1/def456"} {
		id := uid(20 + i)
		f.wantAdoptRefusalOrReplay(id, tgt)
	}
}

// wantAdoptRefusalOrReplay: while uid(10) is open for sid-2, any spelling of it is request_open.
func (f *fixture) wantAdoptRefusalOrReplay(id, target string) {
	f.t.Helper()
	code, body := f.adopt(id, target)
	if e := decodeErr(f.t, body); code != http.StatusConflict || e.Error != team.ErrRequestOpen {
		f.t.Fatalf("target %q = %d %s, want request_open", target, code, body)
	}
}

func TestAdoptCreate_ReplayIsIdempotentOtherTargetConflicts(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	first := f.adoptOK(uid(10), "_def456")
	code, body := f.adopt(uid(10), "_def456")
	if again := decodeApproval(t, body); code != http.StatusOK || again.ID != first.ID {
		t.Fatalf("replay = %d %s", code, body)
	}
	f.origins.show(team.Origin{SessionID: "sid-3", Ref: "_ghi789", PID: 30, Cwd: "/w3"})
	code, body = f.adopt(uid(10), "_ghi789")
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrIDConflict {
		t.Fatalf("other target = %d %s, want id_conflict", code, body)
	}
}

func TestAdoptCreate_ByOldRefThroughTheLineage(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	if _, err := f.m.store.db.Exec(`INSERT INTO session_lineage (session_id, predecessor_session_id, predecessor_ref, op_id, at)
		VALUES ('sid-2', 'sid-old', '_old111', 'op-l', 1)`); err != nil {
		t.Fatal(err)
	}
	a := f.adoptOK(uid(10), "_old111")
	if p, _ := team.AdoptPayloadOf(a); p.TargetSessionID != "sid-2" || p.TargetRef != "_def456" {
		t.Fatalf("payload = %+v, want the successor sid-2 under its current ref", p)
	}
	// a predecessor whose successor is not live is not found
	f.origins.hide("sid-2")
	f.wantAdoptRefusal(uid(11), "_old111", http.StatusConflict, team.ErrAdoptTargetNotFound)
}

func TestAdoptCreate_RegistryErrorIs503NotNotFound(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	f.origins.setReadErr(true)
	// origin resolution reads the registry first: 503
	if code, body := f.adopt(uid(10), "_def456"); code != http.StatusServiceUnavailable {
		t.Fatalf("= %d %s, want 503", code, body)
	}
}

func TestAdoptCreate_UnattendedIsApprovedNeverOpen(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	f.unatt.set(true)
	f.events()
	seen := false
	f.m.store.afterApprovedInsert = func(tx *sql.Tx) error {
		seen = true
		assertInvisible(t, f.m.store, uid(10), "sid-1", "")
		return nil
	}
	a := f.adoptOK(uid(10), "_def456")
	if !seen {
		t.Fatal("the create did not go through CreateApproved")
	}
	assertDecidedByUnattended(t, a, f.clock.Load())
	if ops := f.opsOf(); !reflect.DeepEqual(ops, []string{"closed"}) {
		t.Errorf("events = %v, want [closed]", ops)
	}
	mem := memberBySpawn(t, f.m.store, uid(10))
	if mem.SessionID != "sid-2" || mem.Origin != team.MemberOriginAdopted || mem.State != team.MemberActive || mem.NoticePending != team.NoticeAdopted || mem.TeamID != uid(1) {
		t.Fatalf("member = %+v", mem)
	}
}

func TestAdoptCreate_UnattendedRefusalInTheTransactionIs409(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	f.unatt.set(true)
	f.events()
	// the target becomes a member between the create's checks and its transaction
	f.m.store.afterApprovedInsert = func(tx *sql.Tx) error {
		_, err := insertMemberRowIn(context.Background(), tx, newMember("op-race", uid(1), "sid-2", "_race11", f.clock.Load()))
		return err
	}
	f.wantAdoptRefusal(uid(10), "_def456", http.StatusConflict, team.ErrAdoptAlreadyMember)
	if ops := f.opsOf(); len(ops) != 0 {
		t.Errorf("events = %v, want none", ops)
	}
}

// A ref two live sessions share is ambiguous (409 adopt_target_ambiguous, no row); the session id, bare or
// behind the host, names exactly one of them and opens the request.
func TestAdoptCreate_AmbiguousRefIsNamedBySessionID(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	f.origins.setRefAmbiguous("_def456")
	f.wantAdoptRefusal(uid(10), "_def456", http.StatusConflict, team.ErrAdoptTargetAmbiguous)
	f.origins.show(team.Origin{SessionID: uid(77), Ref: "_def456", PID: 77, Cwd: "/w7"})
	a := f.adoptOK(uid(11), "h:1/"+uid(77))
	if p, _ := team.AdoptPayloadOf(a); p.TargetSessionID != uid(77) {
		t.Fatalf("payload = %+v, want the session named by id", p)
	}
	f.wantAdoptRefusal(uid(12), uid(78), http.StatusConflict, team.ErrAdoptTargetNotFound)
}
