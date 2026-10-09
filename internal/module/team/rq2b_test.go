package teammod

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// gateFixture: a member team (sid-m1) with the pool at pool, unattended and the quota rule as given.
func gateFixture(t *testing.T, unattended, rule bool, pool int) *fixture {
	t.Helper()
	f := newFixture(t)
	f.memberTeam("2")
	f.setPool("sid-1", pool)
	f.setQuota("sid-1", 5)
	f.unatt.set(unattended)
	f.qrule.set(rule, nil)
	return f
}

func (f *fixture) rowCount(kind string) int {
	f.t.Helper()
	var n int
	f.m.store.db.QueryRow(`SELECT COUNT(*) FROM approval_requests WHERE kind = ?`, kind).Scan(&n)
	return n
}

// The §4.1 table. Mutation gates: spend when unattended is off → the first row red; spend with the rule off → the
// second red; skip the spend → the third red; open the row with a pool left → the third red.
func TestRelayCreate_GateTable(t *testing.T) {
	for _, c := range []struct {
		name            string
		unatt, rule     bool
		pool            int
		wantState       team.RelayState
		wantPool        int
		wantRow, wantCT bool // a member_relay row, a control message
	}{
		{"unattended off", false, true, 3, team.RelayRequested, 3, false, true},
		{"rule off", true, false, 3, team.RelayRequested, 3, false, true},
		{"pool left", true, true, 3, team.RelayRequested, 2, false, true},
		{"pool zero", true, true, 0, team.RelayAwaitingApproval, 0, true, false},
	} {
		f := gateFixture(t, c.unatt, c.rule, c.pool)
		f.streamOf()
		q0, _, _ := f.m.store.RelayQuotaOf("sid-1")
		code, op, ae := f.createRelay(rid(300), "/tmp/10.sock", "_mem001")
		if code != 201 || op.State != c.wantState {
			t.Fatalf("%s: %d %+v %+v", c.name, code, op, ae)
		}
		q1, _, _ := f.m.store.RelayQuotaOf("sid-1")
		if q1.MemberPoolLeft != c.wantPool || q1.SelfLeft != 5 {
			t.Errorf("%s: pool %d, self %d", c.name, q1.MemberPoolLeft, q1.SelfLeft)
		}
		if spent := c.pool != c.wantPool; spent && q1.Rev != q0.Rev+1 {
			t.Errorf("%s: rev %d → %d", c.name, q0.Rev, q1.Rev)
		}
		if (f.rowCount("member_relay") == 1) != c.wantRow {
			t.Errorf("%s: member_relay rows = %d", c.name, f.rowCount("member_relay"))
		}
		if c.wantRow {
			row, ok, _ := f.m.store.Get(op.RequestID)
			if !ok || row.Kind != team.KindMemberRelay || row.State != team.StateOpen || row.Origin.SessionID != "sid-1" ||
				row.DeadlineAt != row.CreatedAt+600_000 || row.LeaseUntil != row.DeadlineAt || !strings.Contains(string(row.Payload), op.ID) {
				t.Errorf("%s: row %+v ok=%v", c.name, row, ok)
			}
			f.m.heldMu.Lock()
			_, held := f.m.heldQuota[op.RequestID]
			f.m.heldMu.Unlock()
			if !held {
				t.Errorf("%s: the new row must be held at once", c.name)
			}
		} else if op.RequestID != "" {
			t.Errorf("%s: a requested op has request id %q", c.name, op.RequestID)
		}
		time.Sleep(120 * time.Millisecond)
		if got := len(f.sender.calls()) == 1; got != c.wantCT {
			t.Errorf("%s: control sent = %v, want %v", c.name, got, c.wantCT)
		}
		ops, _ := f.streamOf()
		has := func(s string) bool {
			for _, o := range ops {
				if strings.Contains(o, s) {
					return true
				}
			}
			return false
		}
		if has("opened") != c.wantRow || has(team.RelayQuotaEventType) != (c.pool != c.wantPool) {
			t.Errorf("%s: events %v", c.name, ops)
		}
	}
}

// Fault injection after the op insert, after the row insert and after the spend: nothing persisted, the pool unchanged.
// Mutation gate: spend outside the transaction → the pool drops (red).
func TestRelayCreate_AFailureRollsBackOpRowAndSpend(t *testing.T) {
	boom := errors.New("boom")
	for name, inject := range map[string]func(f *fixture, pool int){
		"after the spend":      func(f *fixture, pool int) { f.m.afterPoolSpend = func() error { return boom } },
		"after the row insert": func(f *fixture, pool int) { f.m.afterMemberRowInsert = func() error { return boom } },
		"after the op insert":  func(f *fixture, pool int) { f.m.store.afterMemberOpInsert = func() error { return boom } },
	} {
		for _, pool := range []int{2, 0} { // a spend path and a row path
			if (name == "after the spend" && pool == 0) || (name == "after the row insert" && pool == 2) {
				continue // that point is not on this path
			}
			f := gateFixture(t, true, true, pool)
			inject(f, pool)
			code, _, _ := f.createRelay(rid(310), "/tmp/10.sock", "_mem001")
			if code != 500 || countRelayOps(t, f) != 0 || f.rowCount("member_relay") != 0 || f.poolLeft("sid-1") != pool {
				t.Errorf("%s (pool %d): %d, ops %d, rows %d, pool %d", name, pool, code, countRelayOps(t, f), f.rowCount("member_relay"), f.poolLeft("sid-1"))
			}
		}
	}
}

// Replay of the same id and member is 200 with the same op in each state; another member is id_conflict.
func TestRelayCreate_ReplayInEveryState(t *testing.T) {
	for _, pool := range []int{2, 0} {
		f := gateFixture(t, true, true, pool)
		_, op, _ := f.createRelay(rid(320), "/tmp/10.sock", "_mem001")
		code, again, _ := f.createRelay(rid(320), "/tmp/10.sock", "_mem001")
		if code != 200 || again.ID != op.ID || again.State != op.State || f.poolLeft("sid-1") != pool-btoi(pool > 0) {
			t.Fatalf("pool %d replay: %d %+v pool %d", pool, code, again, f.poolLeft("sid-1"))
		}
		if code, _, ae := f.createRelay(rid(320), "/tmp/10.sock", "_ffffff"); code != 409 || ae.Error != team.ErrIDConflict {
			t.Fatalf("other member: %d %+v", code, ae)
		}
		if f.rowCount("member_relay") > 1 {
			t.Fatalf("a replay opened a second row")
		}
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Two members race for the last unit: exactly one is spent and requested, the other waits on a row.
func TestRelayCreate_TwoMembersOneLastUnit(t *testing.T) {
	f := gateFixture(t, true, true, 1)
	row2 := newMember("op-m2", uid(1), "sid-m2", "_mem002", f.clock.Load())
	row2.ProcStart = memStart
	if err := f.m.store.InsertMember(row2); err != nil {
		t.Fatal(err)
	}
	f.liveMember("sid-m2", "_mem002", "two", "self/w-two", "tm-2")
	f.do("POST", "/api/relay/hello", team.RelayHelloRequest{SessionID: "sid-m2", ModVersion: "2", Agent: "cc"})
	_, a, _ := f.createRelay(rid(330), "/tmp/10.sock", "_mem001")
	_, b, _ := f.createRelay(rid(331), "/tmp/10.sock", "_mem002")
	if a.State != team.RelayRequested || b.State != team.RelayAwaitingApproval || f.poolLeft("sid-1") != 0 || f.rowCount("member_relay") != 1 {
		t.Fatalf("a=%s b=%s pool=%d rows=%d", a.State, b.State, f.poolLeft("sid-1"), f.rowCount("member_relay"))
	}
}

// The held view lists a held member_relay row beside the self_relay ones. Mutation gate: filter to self_relay → red.
func TestUnattendedView_HeldListsTheMemberRelayRow(t *testing.T) {
	f := gateFixture(t, true, true, 0)
	f.realUnattended()
	f.switchTo(true)
	_, op, _ := f.createRelay(rid(340), "/tmp/10.sock", "_mem001")
	_, v, _ := f.getUnattended("")
	if len(v.Held) != 1 || v.Held[0].ID != op.RequestID || v.Held[0].Kind != team.KindMemberRelay {
		t.Fatalf("held = %+v", v.Held)
	}
}

// End to end: pool 0 → a person approves → the op is requested and the control goes out; the click spent nothing.
func TestRelayCreate_HeldRowApprovedByAClickSendsTheControl(t *testing.T) {
	f := gateFixture(t, true, true, 0)
	_, op, _ := f.createRelay(rid(350), "/tmp/10.sock", "_mem001")
	if code, body := f.decide(op.RequestID, "approve"); code != 200 {
		t.Fatalf("approve: %d %s", code, body)
	}
	waitFor(t, func() bool { return len(f.sender.calls()) == 1 })
	if f.op(op.ID).State != team.RelayRequested || f.poolLeft("sid-1") != 0 {
		t.Fatalf("op %s pool %d", f.op(op.ID).State, f.poolLeft("sid-1"))
	}
}
