package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/team"
)

// fakeUnattended is the host config's unattended switch in these tests.
type fakeUnattended struct {
	mu  sync.Mutex
	on  bool
	err error
}

func (f *fakeUnattended) Unattended() (team.UnattendedState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return team.UnattendedState{}, f.err
	}
	return team.UnattendedState{On: f.on, Since: 1}, nil
}

func (f *fakeUnattended) SetUnattended(on bool, by team.Client, now int64) (team.UnattendedState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	changed := f.on != on
	f.on = on
	return team.UnattendedState{On: on, Since: 1, ChangedAt: now, ChangedBy: &by}, changed, nil
}

func (f *fakeUnattended) set(on bool) { _, _, _ = f.SetUnattended(on, appClient(), 1) }

// opsOf is the ops of the events drained so far.
func (f *fixture) opsOf() []string {
	var out []string
	for _, ev := range f.events() {
		out = append(out, ev.Op)
	}
	return out
}

// assertTeamGrant fails unless sid leads a live team with grant max and roots.
func (f *fixture) assertTeamGrant(sid string, max int, roots []string) {
	f.t.Helper()
	tm, ok, err := f.m.store.LiveTeamByLead(sid)
	if err != nil || !ok || tm.Grant.MaxMembers != max || !reflect.DeepEqual(tm.Grant.Roots, roots) {
		f.t.Fatalf("team of %s = %+v ok=%v err=%v, want max_members %d roots %v", sid, tm, ok, err, max, roots)
	}
}

// Rule 1 / decision 1: while the switch is on, a lead request is written
// already approved. At the seam between the insert and the approve neither
// ListOpen nor a snapshot to a new subscriber sees it; the 201 is the
// approved row decided by unattended (no addr), its team exists, and the
// only event is closed. Mutation gate: insert open, broadcast opened, then
// approve (today's order) → red.
func TestCreate_UnattendedOnIsApprovedNeverOpen(t *testing.T) {
	f := newFixture(t)
	f.unatt.set(true)
	second := f.core.Events.AddTestSubscriber()
	defer f.core.Events.RemoveTestSubscriber(second)
	ran := false
	f.m.store.afterApprovedInsert = func(*sql.Tx) error {
		ran = true
		assertInvisible(t, f.m.store, uid(1), "sid-1", "")
		if why := f.m.snapshotUnderLock(second); why != "" {
			t.Errorf("snapshot: %s", why)
		}
		var ev struct{ Value string }
		_ = json.Unmarshal(<-second.SendCh(), &ev)
		var v team.EventValue
		if err := json.Unmarshal([]byte(ev.Value), &v); err != nil || v.Op != "snapshot" || len(v.Approvals) != 0 {
			t.Errorf("snapshot at the seam = %+v (%v), want an empty one", v, err)
		}
		return nil
	}
	a := f.create(uid(1))
	if !ran {
		t.Error("the create did not go through CreateApproved")
	}
	if ops := f.opsOf(); !reflect.DeepEqual(ops, []string{"closed"}) {
		t.Errorf("events = %v, want [closed]", ops)
	}
	assertDecidedByUnattended(t, a, f.clock.Load())
	f.assertTeamGrant("sid-1", 3, []string{"/w"})
}

// Switch off → today's path exactly: opened, and the row stays open.
func TestCreate_UnattendedOffOpensAsToday(t *testing.T) {
	f := newFixture(t)
	if a := f.create(uid(1)); a.State != team.StateOpen {
		t.Fatalf("state %s, want open", a.State)
	}
	if ops := f.opsOf(); !reflect.DeepEqual(ops, []string{"opened"}) {
		t.Fatalf("events = %v, want [opened]", ops)
	}
}

// Fail closed (PU-1b2 rule 1): a switch that cannot be read is off.
func TestCreate_UnattendedReadErrorIsOff(t *testing.T) {
	f := newFixture(t)
	f.unatt.on, f.unatt.err = true, errors.New("stored unattended value has no since")
	if a := f.create(uid(1)); a.State != team.StateOpen {
		t.Fatalf("state %s, want open", a.State)
	}
	if ops := f.opsOf(); !reflect.DeepEqual(ops, []string{"opened"}) {
		t.Fatalf("events = %v, want [opened]", ops)
	}
}

// Rule 3: a replay of the same id answers the stored, approved row.
func TestCreate_UnattendedReplayAnswersTheApprovedRow(t *testing.T) {
	f := newFixture(t)
	f.unatt.set(true)
	first := f.create(uid(1))
	code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1)))
	if again := decodeApproval(t, body); code != http.StatusOK || !reflect.DeepEqual(again, first) {
		t.Fatalf("replay: %d %s, want 200 with %+v", code, body, first)
	}
	if ops := f.opsOf(); !reflect.DeepEqual(ops, []string{"closed"}) {
		t.Fatalf("events = %v, want [closed]", ops)
	}
}

// Rule 1: a refusal inside the create's transaction (the origin became a
// member between the create's check and the approve's re-check) is the
// create's own 409, with nothing written and nothing broadcast.
func TestCreate_UnattendedRefusalInTheTransactionIs409NothingWritten(t *testing.T) {
	f := newFixture(t)
	f.unatt.set(true)
	f.m.store.afterApprovedInsert = func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO teams (` + teamCols + `) VALUES ('team-x', 'h:1', 'sid-2', '_def456', '{}', 'team-x', 1, 0, '')`); err != nil {
			return err
		}
		m := newMember("op-x", "team-x", "sid-1", "_abc123", 1)
		_, err := tx.Exec(`INSERT INTO team_members (`+memberCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, m.dest()...)
		return err
	}
	code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1)))
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrMemberCannotLead {
		t.Fatalf("create: %d %s, want 409 %s", code, body, team.ErrMemberCannotLead)
	}
	if _, ok, err := f.m.store.Get(uid(1)); err != nil || ok {
		t.Fatalf("row: ok=%v err=%v, want none", ok, err)
	}
	if _, ok, _ := f.m.store.TeamByID("team-x"); ok {
		t.Fatal("the seam's team survived the rollback")
	}
	if ops := f.opsOf(); len(ops) != 0 {
		t.Fatalf("events = %v, want none", ops)
	}
}

// U25 caps only the daemon's approvals: a click's grant is the person's.
// Mutation gate: cap the click's grant too → red.
func TestDecide_ClickGrantIsNotCapped(t *testing.T) {
	f := newFixture(t)
	f.createReqEdit = func(r *team.CreateApprovalRequest) { r.MaxMembers = 8 }
	f.create(uid(1))
	if code, body := f.decide(uid(1), "approve"); code != http.StatusOK {
		t.Fatalf("decide: %d %s", code, body)
	}
	f.assertTeamGrant("sid-1", 8, []string{"/w"})
}

// Rule 2: begin while the switch is on writes the op claimed and the row
// approved in one transaction (the seam sees neither); 201 carries the
// claimed op, the wait answers approved at once, and the only event is
// closed.
func TestRelayBegin_UnattendedOnClaimedNeverOpen(t *testing.T) {
	f := newFixture(t)
	f.unatt.set(true)
	ran := false
	f.m.store.afterApprovedInsert = func(*sql.Tx) error {
		ran = true
		assertInvisible(t, f.m.store, rid(2), "sid-1", rid(1))
		return nil
	}
	out := f.begin("sid-1")
	if !ran || out.Op.State != team.RelayClaimed || out.RequestID != rid(2) || f.op(rid(1)).State != team.RelayClaimed {
		t.Fatalf("begin = %+v (seam ran %v), want op %s claimed for request %s", out, ran, rid(1), rid(2))
	}
	code, body := f.do(http.MethodGet, "/api/relay/wait/"+rid(2), nil)
	if a := decodeApproval(t, body); code != http.StatusOK {
		t.Fatalf("wait: %d %s", code, body)
	} else {
		assertDecidedByUnattended(t, a, f.clock.Load())
	}
	if ops := f.opsOf(); !reflect.DeepEqual(ops, []string{"closed"}) {
		t.Fatalf("events = %v, want [closed]", ops)
	}
}

// Rule 4 / D-U23-4: a member, a host switch off and a paused session are
// refused before any row, switch on or not: nothing to auto-approve.
func TestRelayBegin_UnattendedMemberPausedOrOffRaisesNothing(t *testing.T) {
	f := newFixture(t)
	f.unatt.set(true)
	f.makeMember("sid-1")
	if code, body := f.do(http.MethodPost, "/api/relay/begin", beginReq("sid-1")); code != http.StatusConflict || decodeErr(t, body).Error != team.ErrMemberRelayIsLeads {
		t.Fatalf("member: %d %s", code, body)
	}
	f.self("sid-2", "off")
	if code, body := f.do(http.MethodPost, "/api/relay/begin", beginReq("sid-2")); code != http.StatusConflict || decodeErr(t, body).Error != team.ErrSelfRelayPaused {
		t.Fatalf("paused: %d %s", code, body)
	}
	f.switches.set(hostconfig.RelaySwitches{})
	if code, body := f.do(http.MethodPost, "/api/relay/begin", beginReq("sid-2")); code != http.StatusConflict || decodeErr(t, body).Error != team.ErrSelfRelayOff {
		t.Fatalf("off: %d %s", code, body)
	}
	if open, _ := f.m.store.ListOpen(); len(open) != 0 {
		t.Fatalf("open = %+v, want none", open)
	}
	if ops := f.opsOf(); len(ops) != 0 {
		t.Fatalf("events = %v, want none", ops)
	}
}
