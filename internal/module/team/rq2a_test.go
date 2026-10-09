package teammod

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// RQ-2a tests: no path opens a member_relay row yet, so the rows are inserted at the store.

// awaitingMemberRelay makes a member team (sid-1 leads, sid-m1 is a live member whose mod said hello at 2) and, in
// the shape RQ-2b's create will write, an awaiting_approval member op with its open member_relay row.
func (f *fixture) awaitingMemberRelay(opID, rowID string) (team.RelayOp, team.Approval) {
	f.t.Helper()
	mr := f.memberTeam("2")
	now := f.clock.Load()
	op := team.RelayOp{ID: opID, Kind: team.RelayKindMember, HostID: "h:1", SessionID: mr.SessionID, Ref: mr.Ref, TeamID: uid(1),
		RequestID: rowID, State: team.RelayAwaitingApproval, HandoffPath: "/p/" + opID + ".md", PID: mr.PID, PaneID: "%2", ProcStart: memStart,
		CreatedAt: now, UpdatedAt: now}
	row := team.Approval{ID: rowID, Kind: team.KindMemberRelay, HostID: "h:1", Origin: team.Origin{SessionID: "sid-1", Ref: "_abc123", PID: 10},
		Payload: []byte(`{"op_id":"` + opID + `"}`), State: team.StateOpen, CreatedAt: now, DeadlineAt: now + team.SelfRelayDeadlineS*1000, LeaseUntil: now + team.SelfRelayDeadlineS*1000}
	if err := f.m.store.CreateRelayOp(op); err != nil {
		f.t.Fatal(err)
	}
	if _, _, ins, err := f.m.store.Create(row, requestHash(team.KindMemberRelay, "sid-1", team.SelfRelayDeadlineS, row.Payload)); err != nil || !ins {
		f.t.Fatalf("insert row: %v %v", ins, err)
	}
	return op, row
}

func (f *fixture) closeReason(id string) string {
	f.t.Helper()
	var r string
	if err := f.m.store.db.QueryRow(`SELECT close_reason FROM approval_requests WHERE id = ?`, id).Scan(&r); err != nil {
		f.t.Fatal(err)
	}
	return r
}

func (f *fixture) setPool(sid string, pool int) {
	f.t.Helper()
	if _, _, err := f.m.store.SetRelayQuota(sid, nil, &pool, f.clock.Load(), "app"); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) poolLeft(sid string) int {
	f.t.Helper()
	q, _, err := f.m.store.RelayQuotaOf(sid)
	if err != nil {
		f.t.Fatal(err)
	}
	return q.MemberPoolLeft
}

// §4.5, both kinds × every pair. Mutation gates: self op awaiting→requested allowed / member awaiting→claimed allowed → red.
func TestRelayTransitionOK_PerKindMatrix(t *testing.T) {
	states := []team.RelayState{team.RelayAwaitingApproval, team.RelayRequested, team.RelayClaimed, team.RelayWriting, team.RelayWritten,
		team.RelayCleared, team.RelayDone, team.RelayFailed, team.RelayCancelled}
	for _, kind := range []team.RelayKind{team.RelayKindSelf, team.RelayKindMember} {
		for _, from := range states {
			for _, to := range states {
				want := relayTransitions[from][to]
				if from == team.RelayAwaitingApproval {
					want = to == team.RelayCancelled || (kind == team.RelayKindSelf && to == team.RelayClaimed) || (kind == team.RelayKindMember && to == team.RelayRequested)
				}
				if got := relayTransitionOK(kind, from, to); got != want {
					t.Errorf("%s %s → %s = %v, want %v", kind, from, to, got, want)
				}
			}
		}
	}
	if !relayTransitionOK(team.RelayKindMember, team.RelayRequested, team.RelayFailed) {
		t.Error("a member op must still fail from requested")
	}
}

func TestAutoApprovable_IncludesMemberRelay(t *testing.T) {
	if !team.AutoApprovable(team.KindMemberRelay) {
		t.Fatal("member_relay is auto-approvable")
	}
}

// A click approves: op → requested in the same transaction, one control message from the lead, nothing spent.
// Mutation gates: spend on a click → the pool drops (red); afterClose reports claimed → the op is claimed (red).
func TestMemberRelay_ClickApprovesOpRequestedAndSendsOneControl(t *testing.T) {
	f := newFixture(t)
	op, row := f.awaitingMemberRelay(rid(100), rid(101))
	f.setPool("sid-1", 3)
	got := f.poll(op.ID, 5)
	waitFor(t, func() bool { return f.waitersOn(op.ID) == 1 })
	if code, body := f.decide(row.ID, "approve"); code != http.StatusOK {
		t.Fatalf("approve: %d %s", code, body)
	}
	cur := f.op(op.ID)
	if cur.State != team.RelayRequested || cur.UpdatedAt != f.clock.Load() {
		t.Fatalf("op = %+v", cur)
	}
	select {
	case a := <-got:
		if a.State != team.RelayRequested {
			t.Fatalf("poll answered %+v", a)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the approve did not wake the op's long-poll")
	}
	waitFor(t, func() bool { return len(f.sender.calls()) == 1 })
	c := f.sender.calls()[0]
	if c.Text != "[pdx-relay:control] op="+op.ID || c.OriginInbox != "/tmp/10.sock" {
		t.Fatalf("control = %+v", c)
	}
	if f.poolLeft("sid-1") != 3 {
		t.Fatalf("a click spent: pool %d", f.poolLeft("sid-1"))
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d control messages", n)
	}
}

// The daemon's approve spends the lead's POOL (never self_left), keyed by the chain root, rev + 1, in the approve's
// transaction. Mutation gates: spend self_left → red; key by the session id (a relayed lead) → red.
func TestMemberRelay_AutoApproveSpendsThePoolOfTheCurrentLeadsChain(t *testing.T) {
	f := newFixture(t)
	op, row := f.awaitingMemberRelay(rid(110), rid(111))
	f.setPool("sid-1", 2)
	f.setQuota("sid-1", 5)
	f.unatt.set(true)
	f.qrule.set(true, nil)
	q0, _, _ := f.m.store.RelayQuotaOf("sid-1")
	f.m.createMu.Lock()
	ok, open := f.m.autoApprove(row)
	f.m.createMu.Unlock()
	if !ok || open {
		t.Fatalf("auto approve: %v %v", ok, open)
	}
	q1, _, _ := f.m.store.RelayQuotaOf("sid-1")
	if q1.MemberPoolLeft != 1 || q1.SelfLeft != 5 || q1.Rev != q0.Rev+1 {
		t.Fatalf("quota %+v → %+v", q0, q1)
	}
	if f.op(op.ID).State != team.RelayRequested {
		t.Fatalf("op = %+v", f.op(op.ID))
	}
	// the lead relayed: the quota is read at the chain root of the team's CURRENT lead
	f.m.store.db.Exec(`UPDATE teams SET lead_session_id = 'sid-1b' WHERE id = ?`, uid(1))
	f.m.store.db.Exec(`INSERT INTO session_lineage (session_id, predecessor_session_id, predecessor_ref, op_id, at) VALUES ('sid-1b', 'sid-1', '_abc123', 'x', 1)`)
	op2, row2 := f.awaitingAgain(rid(112), rid(113), op.ID)
	_ = op2
	f.m.createMu.Lock()
	ok, _ = f.m.autoApprove(row2)
	f.m.createMu.Unlock()
	if !ok || f.poolLeft("sid-1") != 0 {
		t.Fatalf("spend after the lead relayed: ok=%v pool %d", ok, f.poolLeft("sid-1"))
	}
}

// awaitingAgain: a second awaiting op for the same member after the first reached a terminal state.
func (f *fixture) awaitingAgain(opID, rowID, prev string) (team.RelayOp, team.Approval) {
	f.t.Helper()
	f.m.store.db.Exec(`UPDATE relay_ops SET state = 'cancelled' WHERE id = ?`, prev)
	now := f.clock.Load()
	op := team.RelayOp{ID: opID, Kind: team.RelayKindMember, HostID: "h:1", SessionID: "sid-m1", Ref: "_mem001", TeamID: uid(1), RequestID: rowID,
		State: team.RelayAwaitingApproval, HandoffPath: "/p", PID: 42, ProcStart: memStart, CreatedAt: now, UpdatedAt: now}
	row := team.Approval{ID: rowID, Kind: team.KindMemberRelay, HostID: "h:1", Origin: team.Origin{SessionID: "sid-1"}, Payload: []byte(`{}`), State: team.StateOpen,
		CreatedAt: now, DeadlineAt: now + 600_000, LeaseUntil: now + 600_000}
	if err := f.m.store.CreateRelayOp(op); err != nil {
		f.t.Fatal(err)
	}
	if _, _, ins, err := f.m.store.Create(row, "h"+rowID); err != nil || !ins {
		f.t.Fatal(err)
	}
	return op, row
}

// Pool 0: held; nothing is approved or spent. Rule off (unattended on): approved, nothing spent.
func TestMemberRelay_PoolZeroIsHeldAndRuleOffSpendsNothing(t *testing.T) {
	f := newFixture(t)
	op, row := f.awaitingMemberRelay(rid(120), rid(121))
	f.unatt.set(true)
	f.qrule.set(true, nil)
	f.m.createMu.Lock()
	ok, open := f.m.autoApprove(row)
	f.m.createMu.Unlock()
	if ok || !open || f.op(op.ID).State != team.RelayAwaitingApproval || f.rowState(row.ID) != team.StateOpen {
		t.Fatalf("pool 0: ok=%v open=%v op=%s", ok, open, f.op(op.ID).State)
	}
	f.m.heldMu.Lock()
	_, held := f.m.heldQuota[row.ID]
	f.m.heldMu.Unlock()
	if !held {
		t.Fatal("a row refused for quota is held")
	}
	f.qrule.set(false, nil)
	f.setPool("sid-1", 0)
	f.m.createMu.Lock()
	ok, _ = f.m.autoApprove(row)
	f.m.createMu.Unlock()
	if !ok || f.op(op.ID).State != team.RelayRequested || f.poolLeft("sid-1") != 0 {
		t.Fatalf("rule off: ok=%v op=%s", ok, f.op(op.ID).State)
	}
	f.m.heldMu.Lock()
	_, held = f.m.heldQuota[row.ID]
	f.m.heldMu.Unlock()
	if held {
		t.Fatal("an approved row must leave the held set")
	}
}

// Fault injection: the op's move fails → the row is still open, the pool unchanged, no control, op awaiting.
// Mutation gate: move the op in a second transaction (outside) → the row stays closed (red).
func TestMemberRelay_ApproveRollsBackWhenTheOpMoveFails(t *testing.T) {
	f := newFixture(t)
	op, row := f.awaitingMemberRelay(rid(130), rid(131))
	f.setPool("sid-1", 2)
	f.unatt.set(true)
	f.qrule.set(true, nil)
	f.m.store.beforeMemberOpMove = func() error { return errors.New("boom") }
	f.m.createMu.Lock()
	ok, _ := f.m.autoApprove(row)
	f.m.createMu.Unlock()
	f.m.store.beforeMemberOpMove = nil
	if ok || f.rowState(row.ID) != team.StateOpen || f.op(op.ID).State != team.RelayAwaitingApproval || f.poolLeft("sid-1") != 2 {
		t.Fatalf("after a failed move: ok=%v row=%s op=%s pool=%d", ok, f.rowState(row.ID), f.op(op.ID).State, f.poolLeft("sid-1"))
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("a control went out for a rolled-back approve: %d", n)
	}
}

// Deny, the deadline, and an ended team: the op follows in the close's own transaction; a failing op move keeps the
// row open. Waiters wake. The lease equals the deadline, so an unrenewed row closes timeout, never abandoned.
func TestMemberRelay_ClosesMoveTheOpInTheSameTransaction(t *testing.T) {
	for _, c := range []struct {
		name   string
		close  func(f *fixture, row team.Approval)
		state  team.State
		reason string
	}{
		{"deny", func(f *fixture, row team.Approval) { f.decide(row.ID, "deny") }, team.StateDenied, team.RelayReasonDenied},
		{"timeout", func(f *fixture, row team.Approval) { f.clock.Add(600_001); f.m.tick() }, team.StateTimeout, team.RelayReasonTimeout},
		{"team ended", func(f *fixture, row team.Approval) {
			f.m.store.db.Exec(`UPDATE teams SET ended_at = 5 WHERE id = ?`, uid(1))
			f.m.tick()
		}, team.StateAbandoned, team.RelayReasonAbandoned},
	} {
		f := newFixture(t)
		op, row := f.awaitingMemberRelay(rid(140), rid(141))
		got := f.poll(op.ID, 5)
		waitFor(t, func() bool { return f.waitersOn(op.ID) == 1 })
		f.m.heldMu.Lock()
		f.m.heldQuota = map[string]struct{}{row.ID: {}}
		f.m.heldMu.Unlock()
		c.close(f, row)
		if f.rowState(row.ID) != c.state {
			t.Errorf("%s: row %s, want %s", c.name, f.rowState(row.ID), c.state)
		}
		if cur := f.op(op.ID); cur.State != team.RelayCancelled || cur.Reason != c.reason {
			t.Errorf("%s: op %+v", c.name, cur)
		}
		select {
		case <-got:
		case <-time.After(3 * time.Second):
			t.Errorf("%s: the close did not wake the op's long-poll", c.name)
		}
		f.m.heldMu.Lock()
		_, held := f.m.heldQuota[row.ID]
		f.m.heldMu.Unlock()
		if held {
			t.Errorf("%s: the closed row is still held", c.name)
		}
		if n := len(f.sender.calls()); n != 0 {
			t.Errorf("%s: %d control messages", c.name, n)
		}
	}
	// the op's move fails → the close rolls back, the row stays open
	f := newFixture(t)
	op, row := f.awaitingMemberRelay(rid(142), rid(143))
	f.m.store.beforeMemberOpMove = func() error { return errors.New("boom") }
	if code, _ := f.decide(row.ID, "deny"); code == http.StatusOK {
		t.Fatal("a deny whose op move failed answered 200")
	}
	f.m.store.beforeMemberOpMove = nil
	if f.rowState(row.ID) != team.StateOpen || f.op(op.ID).State != team.RelayAwaitingApproval {
		t.Fatalf("row %s, op %s", f.rowState(row.ID), f.op(op.ID).State)
	}
}

// The lead's own relay ends its old session while the row is rightly open: no origin liveness for this kind.
// Mutation gate: keep the origin-liveness branch → the row is abandoned (red).
func TestMemberRelay_SurvivesTheLeadsSessionLeaving(t *testing.T) {
	f := newFixture(t)
	_, row := f.awaitingMemberRelay(rid(150), rid(151))
	f.m.store.db.Exec(`UPDATE teams SET lead_session_id = 'sid-1b' WHERE id = ?`, uid(1)) // the relay moved the lead
	f.origins.markDead("sid-1")
	for i := 0; i < 10; i++ {
		f.m.tick()
	}
	if f.rowState(row.ID) != team.StateOpen {
		t.Fatalf("row %s after ten ticks with the lead's old session gone", f.rowState(row.ID))
	}
}

// The approve's re-check: a gone member or an ended team cancels the row and the op in the same transaction, nothing
// spent, no control. Mutation gate: skip the re-check → approved for a gone member (red).
func TestMemberRelay_ApproveRecheckCancelsForAGoneMemberOrEndedTeam(t *testing.T) {
	for name, gone := range map[string]struct {
		do     func(f *fixture)
		reason string
	}{
		"member killed": {func(f *fixture) { f.m.store.db.Exec(`UPDATE team_members SET state = 'killed'`) }, memberRelayMemberGone},
		"team ended":    {func(f *fixture) { f.m.store.db.Exec(`UPDATE teams SET ended_at = 5`) }, memberRelayTeamEnded},
	} {
		f := newFixture(t)
		op, row := f.awaitingMemberRelay(rid(160), rid(161))
		f.setPool("sid-1", 2)
		gone.do(f)
		code, body := f.decide(row.ID, "approve")
		if code != http.StatusConflict {
			t.Errorf("%s: approve %d %s", name, code, body)
		}
		if f.rowState(row.ID) != team.StateCancelled || f.closeReason(row.ID) != gone.reason {
			t.Errorf("%s: row %s reason %q", name, f.rowState(row.ID), f.closeReason(row.ID))
		}
		if cur := f.op(op.ID); cur.State != team.RelayCancelled || cur.Reason != team.RelayReasonAbandoned {
			t.Errorf("%s: op %+v", name, cur)
		}
		if f.poolLeft("sid-1") != 2 || len(f.sender.calls()) != 0 {
			t.Errorf("%s: spent or sent: pool %d, sends %d", name, f.poolLeft("sid-1"), len(f.sender.calls()))
		}
	}
}

// Only the row moves an awaiting member op: any report is 409 bad_transition, the row still open, the op unchanged.
// Mutation gate: let a failed/cancelled report through → the row closes (red).
func TestMemberRelay_AReportOnAnAwaitingOpIsRefused(t *testing.T) {
	f := newFixture(t)
	op, row := f.awaitingMemberRelay(rid(170), rid(171))
	for _, rep := range []team.RelayReportRequest{
		{State: team.RelayFailed, Error: "x"}, {State: team.RelayCancelled, Error: "x"}, {State: team.RelayWriting}, {State: team.RelayDone},
	} {
		if code, _, ae := f.report(op.ID, rep); code != http.StatusConflict || ae.Error != team.ErrBadTransition {
			t.Errorf("report %s: %d %+v", rep.State, code, ae)
		}
	}
	if f.rowState(row.ID) != team.StateOpen || f.op(op.ID).State != team.RelayAwaitingApproval {
		t.Fatalf("row %s, op %s", f.rowState(row.ID), f.op(op.ID).State)
	}
}

// Boot reconciliation (§7): the three items.
func TestMemberRelay_BootRepairsAndResendsControl(t *testing.T) {
	// 2: an open row whose member is gone is cancelled
	f := newFixture(t)
	op, row := f.awaitingMemberRelay(rid(180), rid(181))
	f.m.store.db.Exec(`UPDATE team_members SET state = 'gone'`)
	f.m.reconcileRelays()
	if f.rowState(row.ID) != team.StateCancelled || f.op(op.ID).State != team.RelayCancelled {
		t.Fatalf("gone member: row %s op %s", f.rowState(row.ID), f.op(op.ID).State)
	}
	// 1: the op awaiting while its row was closed approved → the op follows (requested)
	f = newFixture(t)
	op, row = f.awaitingMemberRelay(rid(182), rid(183))
	f.m.store.db.Exec(`UPDATE approval_requests SET state = 'approved' WHERE id = ?`, row.ID)
	f.m.reconcileRelays()
	if f.op(op.ID).State != team.RelayRequested {
		t.Fatalf("approved row: op %s", f.op(op.ID).State)
	}
	// 1: an open row whose op is terminal → the row is closed
	f = newFixture(t)
	op, row = f.awaitingMemberRelay(rid(184), rid(185))
	f.m.store.db.Exec(`UPDATE relay_ops SET state = 'cancelled' WHERE id = ?`, op.ID)
	f.m.reconcileRelays()
	if f.rowState(row.ID) != team.StateAbandoned {
		t.Fatalf("terminal op: row %s", f.rowState(row.ID))
	}
	// 3: a requested member op is sent its control again (the lead is live)
	f = newFixture(t)
	f.memberTeam("2")
	_, c, _ := f.createRelay(rid(186), "/tmp/10.sock", "_mem001")
	waitFor(t, func() bool { return len(f.sender.calls()) == 1 })
	f.m.reconcileRelays()
	waitFor(t, func() bool { return len(f.sender.calls()) == 2 })
	_ = c
}

// The store honours SpendQuota only for the daemon's own (Auto) close: a person's click spends nothing even if a
// caller asked. Mutation gate: drop the Auto condition → the pool drops (red).
func TestCloseMemberRelayApproved_ASpendIsNeverAClicks(t *testing.T) {
	f := newFixture(t)
	_, row := f.awaitingMemberRelay(rid(190), rid(191))
	f.setPool("sid-1", 2)
	spent := false
	c := Close{State: team.StateApproved, DecidedAt: f.clock.Load(), SpendQuota: true, SpentOut: &spent}
	if _, won, refused, err := f.m.store.CloseMemberRelayApproved(row.ID, c); err != nil || !won || refused != "" {
		t.Fatalf("approve: won=%v refused=%q err=%v", won, refused, err)
	}
	if spent || f.poolLeft("sid-1") != 2 {
		t.Fatalf("a non-Auto close spent: spent=%v pool=%d", spent, f.poolLeft("sid-1"))
	}
}

// A deny that committed before the approve's transaction: the approve loses the CAS (won=false, no error), nothing is
// spent, the op stays cancelled. Mutation gate: drop the row-state read → an error (red).
func TestCloseMemberRelayApproved_AfterADenyIsALostCASNotAnError(t *testing.T) {
	f := newFixture(t)
	op, row := f.awaitingMemberRelay(rid(200), rid(201))
	f.setPool("sid-1", 2)
	f.decide(row.ID, "deny")
	spent := false
	c := Close{State: team.StateApproved, DecidedAt: f.clock.Load(), Auto: true, SpendQuota: true, SpentOut: &spent}
	a, won, refused, err := f.m.store.CloseMemberRelayApproved(row.ID, c)
	if err != nil || won || refused != "" || a.State != team.StateDenied || spent || f.poolLeft("sid-1") != 2 || f.op(op.ID).State != team.RelayCancelled {
		t.Fatalf("approve after a deny: row=%s won=%v refused=%q err=%v spent=%v op=%s", a.State, won, refused, err, spent, f.op(op.ID).State)
	}
}
