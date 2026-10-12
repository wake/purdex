// internal/module/team/pause_member_test.go — a member's relay and a held card meet an empty quota while the host is unattended
// (unattended quota pause spec 2026-10-12 §3.3, UP-2b). Every pause here needs the release gate open (enablePauseEntry).
package teammod

import (
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func (f *fixture) outboxOf(sid string) []outboxRow {
	f.t.Helper()
	var out []outboxRow
	for _, r := range f.outbox() {
		if r.Session == sid {
			out = append(out, r)
		}
	}
	return out
}

func (f *fixture) closeReasonOf(id string) string { return f.closeReason(id) }

// ---- the lead relays a member whose pool is spent out ----

// Pool 0 + unattended + rule on + gate open: no op, no card; the member holds its own `quota` pause (role member, ref / title /
// on_since recorded) and is told once; the lead's command is answered member_paused_no_quota naming the member; no push.
// Mutation gate: the member path ignores the gate → TestMemberPause_GateClosedKeepsTodaysHeldCard red.
func TestMemberPause_PoolZeroPausesTheMemberInsteadOfOpeningACard(t *testing.T) {
	enablePauseEntry(t)
	f := gateFixture(t, true, true, 0)
	f.streamOf()
	code, _, ae := f.createRelay(rid(400), "/tmp/10.sock", "_mem001")
	if code != http.StatusConflict || ae.Error != team.ErrMemberPausedNoQuota || ae.Detail != "worker（_mem001）接力額度用完，已暫停" {
		t.Fatalf("relay: %d %+v", code, ae)
	}
	if n := countRelayOps(t, f); n != 0 {
		t.Fatalf("%d ops for a paused relay", n)
	}
	if f.rowCount("member_relay") != 0 {
		t.Fatal("a card was opened for a paused relay")
	}
	st, _ := f.unatt.Unattended()
	p, ok := f.pauses()["sid-m1/quota"]
	if !ok || len(f.pauses()) != 1 || p.Role != team.PauseRoleMember || p.Ref != "_mem001" || p.Title != "worker" || p.OnSince != st.Since || p.Since == 0 ||
		p.TeamID != uid(1) || p.LeadSessionID != "sid-1" {
		t.Fatalf("pauses = %+v", f.pauses())
	}
	ob := f.outbox()
	if len(ob) != 1 || ob[0].Session != "sid-m1" || ob[0].Kind != team.PauseOutboxNotice || ob[0].Text != team.PauseNoticeMemberQuota {
		t.Fatalf("outbox = %+v (the member is told once; a member never gets a push)", ob)
	}
	if f.poolLeft("sid-1") != 0 {
		t.Fatal("a paused relay spent the pool")
	}
}

// A member that already holds its quota pause: the same answer, nothing new written.
func TestMemberPause_AnAlreadyPausedMemberGetsTheSameAnswerAndNoNewRows(t *testing.T) {
	enablePauseEntry(t)
	f := gateFixture(t, true, true, 0)
	f.streamOf()
	if code, _, _ := f.createRelay(rid(401), "/tmp/10.sock", "_mem001"); code != http.StatusConflict {
		t.Fatalf("first: %d", code)
	}
	f.setPool("sid-1", 5) // even with a pool again, the pause stands until a person releases it
	code, _, ae := f.createRelay(rid(402), "/tmp/10.sock", "_mem001")
	if code != http.StatusConflict || ae.Error != team.ErrMemberPausedNoQuota {
		t.Fatalf("second: %d %+v", code, ae)
	}
	if len(f.pauses()) != 1 || len(f.outbox()) != 1 || countRelayOps(t, f) != 0 || f.poolLeft("sid-1") != 5 {
		t.Fatalf("pauses %d, outbox %d, ops %d, pool %d: want 1, 1, 0, 5", len(f.pauses()), len(f.outbox()), countRelayOps(t, f), f.poolLeft("sid-1"))
	}
}

// The gate closed (pauseEntryEnabled is false): today's held card, no pause row, no outbox row.
// Mutation gate: take the gate out of the member path → red.
func TestMemberPause_GateClosedKeepsTodaysHeldCard(t *testing.T) {
	if pauseEntryEnabled {
		t.Skip("the gate is open in this build")
	}
	f := gateFixture(t, true, true, 0)
	f.streamOf()
	code, op, ae := f.createRelay(rid(403), "/tmp/10.sock", "_mem001")
	if code != http.StatusCreated || op.State != team.RelayAwaitingApproval || ae.Error != "" {
		t.Fatalf("relay: %d %+v %+v", code, op, ae)
	}
	if f.rowCount("member_relay") != 1 || len(f.pauses()) != 0 || len(f.outbox()) != 0 {
		t.Fatalf("cards %d, pauses %d, outbox %d: want 1, 0, 0", f.rowCount("member_relay"), len(f.pauses()), len(f.outbox()))
	}
}

// Unattended off, rule off, or a pool left: today's relay even with the gate open.
func TestMemberPause_OnlyAnEmptyPoolWhileUnattendedPauses(t *testing.T) {
	enablePauseEntry(t)
	for _, c := range []struct {
		name        string
		unatt, rule bool
		pool        int
		want        team.RelayState
	}{
		{"unattended off", false, true, 0, team.RelayRequested},
		{"rule off", true, false, 0, team.RelayRequested},
		{"pool left", true, true, 2, team.RelayRequested},
	} {
		f := gateFixture(t, c.unatt, c.rule, c.pool)
		f.streamOf()
		code, op, ae := f.createRelay(rid(404), "/tmp/10.sock", "_mem001")
		if code != http.StatusCreated || op.State != c.want {
			t.Errorf("%s: %d %+v %+v", c.name, code, op, ae)
		}
		if len(f.pauses()) != 0 || len(f.outbox()) != 0 {
			t.Errorf("%s: pauses %d, outbox %d, want none", c.name, len(f.pauses()), len(f.outbox()))
		}
	}
}

// A member on another host cannot be paused from here (its notice and its message gate are that host's; codex R1 P1): even with the
// gate open the lead's relay of it is today's held card, and the sweep leaves a held remote card for a person.
// Mutation gate: let the remote member through the pause → red.
func TestMemberPause_ARemoteMemberStaysAHeldCardEvenWithTheGateOpen(t *testing.T) {
	enablePauseEntry(t)
	f, _ := fwdRelayFixture(t)
	op := f.heldRemoteRelay(fwdOp)
	if op.State != team.RelayAwaitingApproval || f.rowCount("member_relay") != 1 || len(f.relayCommands()) != 0 {
		t.Fatalf("op %+v, cards %d, commands %d: want the held card", op, f.rowCount("member_relay"), len(f.relayCommands()))
	}
	if len(f.pauses()) != 0 || len(f.outbox()) != 0 {
		t.Fatalf("pauses %d, outbox %d, want none", len(f.pauses()), len(f.outbox()))
	}
	f.m.tick() // the sweep sees the held card and finds no quota
	if st := f.rowState(op.RequestID); st != team.StateOpen || f.op(op.ID).State != team.RelayAwaitingApproval || len(f.pauses()) != 0 || len(f.outbox()) != 0 {
		t.Fatalf("after the sweep: row %s, op %s, pauses %d, outbox %d, want the card untouched", st, f.op(op.ID).State, len(f.pauses()), len(f.outbox()))
	}
}

// The forwarded path with the gate closed is today's held card.
func TestMemberPause_ForwardedGateClosedKeepsTodaysHeldCard(t *testing.T) {
	if pauseEntryEnabled {
		t.Skip("the gate is open in this build")
	}
	f, _ := fwdRelayFixture(t)
	op := f.heldRemoteRelay(fwdOp)
	if op.State != team.RelayAwaitingApproval || len(f.pauses()) != 0 || len(f.outbox()) != 0 {
		t.Fatalf("op %+v, pauses %d, outbox %d", op, len(f.pauses()), len(f.outbox()))
	}
}

// ---- the switch goes on with a held card ----

// heldSelf: an open self_relay row + its op, opened while unattended was off (a person's card), then the host goes unattended with no quota.
func (f *fixture) heldSelf() team.RelayBeginResponse {
	f.t.Helper()
	f.qrule.set(true, nil)
	f.setQuota("sid-1", 0)
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Title: "boss", Address: "self/boss", PID: 10})
	b := f.begin("sid-1")
	f.unatt.set(true)
	return b
}

// A held self card: one transaction cancels the row and the op (reason paused_no_quota) and writes the pause and its outbox rows.
// Mutation gate: the sweep cancels only the row → the op is still awaiting (red).
func TestSweepPause_AHeldSelfCardIsCancelledWithItsOpAndThePauseIsWritten(t *testing.T) {
	enablePauseEntry(t)
	f := newFixture(t)
	b := f.heldSelf()
	f.m.tick()
	if st := f.rowState(b.RequestID); st != team.StateCancelled || f.closeReasonOf(b.RequestID) != team.RelayReasonPausedNoQuota {
		t.Fatalf("row = %s (%s)", st, f.closeReasonOf(b.RequestID))
	}
	if op := f.op(b.Op.ID); op.State != team.RelayCancelled || op.Reason != team.RelayReasonPausedNoQuota {
		t.Fatalf("op = %s (%s)", op.State, op.Reason)
	}
	if p, ok := f.pauses()["sid-1/quota"]; !ok || p.Role != team.PauseRoleSolo || p.Ref != "_abc123" || p.Title != "boss" {
		t.Fatalf("pauses = %+v", f.pauses())
	}
	ob := f.outbox()
	if len(ob) != 2 || ob[0].Kind != team.PauseOutboxNotice || ob[0].Text != team.PauseNoticeSelf || ob[1].Kind != team.PauseOutboxPush {
		t.Fatalf("outbox = %+v", ob)
	}
	_, v, _ := f.getUnattended("")
	if len(v.Held) != 0 {
		t.Fatalf("held = %+v, a paused card is not held any more", v.Held)
	}
}

// A held member card: the member is paused (not the lead), no push.
func TestSweepPause_AHeldMemberCardPausesTheMember(t *testing.T) {
	enablePauseEntry(t)
	f := newFixture(t)
	op, row := f.awaitingMemberRelay(rid(410), rid(411))
	f.setPool("sid-1", 0)
	f.unatt.set(true)
	f.qrule.set(true, nil)
	f.m.tick()
	if st := f.rowState(row.ID); st != team.StateCancelled || f.closeReasonOf(row.ID) != team.RelayReasonPausedNoQuota {
		t.Fatalf("row = %s (%s)", st, f.closeReasonOf(row.ID))
	}
	if got := f.op(op.ID); got.State != team.RelayCancelled || got.Reason != team.RelayReasonPausedNoQuota {
		t.Fatalf("op = %s (%s)", got.State, got.Reason)
	}
	p, ok := f.pauses()["sid-m1/quota"]
	if !ok || len(f.pauses()) != 1 || p.Role != team.PauseRoleMember || p.Ref != "_mem001" {
		t.Fatalf("pauses = %+v", f.pauses())
	}
	ob := f.outbox()
	if len(ob) != 1 || ob[0].Session != "sid-m1" || ob[0].Text != team.PauseNoticeMemberQuota {
		t.Fatalf("outbox = %+v", ob)
	}
}

// A decide that got there first wins: the sweep writes no pause. (The other order: the sweep first, the decide finds the row closed.)
func TestSweepPause_ADecideThatCameFirstWinsAndNothingIsPaused(t *testing.T) {
	enablePauseEntry(t)
	f := newFixture(t)
	b := f.heldSelf()
	if code, body := f.decide(b.RequestID, "deny"); code != http.StatusOK {
		t.Fatalf("deny: %d %s", code, body)
	}
	f.m.tick()
	if len(f.pauses()) != 0 || len(f.outbox()) != 0 || f.rowState(b.RequestID) != team.StateDenied {
		t.Fatalf("pauses %d, outbox %d, row %s", len(f.pauses()), len(f.outbox()), f.rowState(b.RequestID))
	}
}

func TestSweepPause_ADecideAfterThePauseFindsTheRowClosed(t *testing.T) {
	enablePauseEntry(t)
	f := newFixture(t)
	b := f.heldSelf()
	f.m.tick()
	if code, _ := f.decide(b.RequestID, "approve"); code == http.StatusOK {
		t.Fatal("an approve of a row the pause cancelled succeeded")
	}
	if op := f.op(b.Op.ID); op.State != team.RelayCancelled {
		t.Fatalf("op = %s, an approve must not revive it", op.State)
	}
	if len(f.pauses()) != 1 {
		t.Fatalf("pauses = %+v", f.pauses())
	}
}

// An overdue row loses the CAS: nothing is paused, the row is the timeout sweeper's.
func TestSweepPause_AnOverdueRowIsNotPaused(t *testing.T) {
	enablePauseEntry(t)
	f := newFixture(t)
	b := f.heldSelf()
	row, _, _ := f.m.store.Get(b.RequestID)
	f.clock.Store(row.DeadlineAt + 1)
	f.m.createMu.Lock()
	f.m.autoApprove(row)
	f.m.createMu.Unlock()
	if len(f.pauses()) != 0 || len(f.outbox()) != 0 || f.rowState(b.RequestID) != team.StateOpen {
		t.Fatalf("pauses %d, outbox %d, row %s: an overdue row is left to the timeout", len(f.pauses()), len(f.outbox()), f.rowState(b.RequestID))
	}
}

// The store's own CAS: a row past its deadline is not cancelled by the pause, whatever the caller thinks.
// Mutation gate: drop UnexpiredAt from PauseHeldRow's close → the overdue row is paused (red).
func TestSweepPause_TheStoreCASRefusesAnOverdueRow(t *testing.T) {
	f := newFixture(t)
	b := f.heldSelf()
	row, _, _ := f.m.store.Get(b.RequestID)
	_, res, err := f.m.store.PauseHeldRow(b.RequestID, 1, row.DeadlineAt+1)
	if err != nil || res != PauseHeldLost {
		t.Fatalf("PauseHeldRow past the deadline = %v, %v", res, err)
	}
	if len(f.pauses()) != 0 || len(f.outbox()) != 0 || f.rowState(b.RequestID) != team.StateOpen || f.op(b.Op.ID).State != team.RelayAwaitingApproval {
		t.Fatalf("pauses %d, outbox %d, row %s, op %s", len(f.pauses()), len(f.outbox()), f.rowState(b.RequestID), f.op(b.Op.ID).State)
	}
}

// The gate closed: the sweep holds the card as it always did.
func TestSweepPause_GateClosedHoldsTheCardAsToday(t *testing.T) {
	if pauseEntryEnabled {
		t.Skip("the gate is open in this build")
	}
	f := newFixture(t)
	b := f.heldSelf()
	f.m.tick()
	_, v, _ := f.getUnattended("")
	if len(v.Held) != 1 || v.Held[0].ID != b.RequestID || f.rowState(b.RequestID) != team.StateOpen || len(f.pauses()) != 0 || len(f.outbox()) != 0 {
		t.Fatalf("held %+v, row %s, pauses %d, outbox %d", v.Held, f.rowState(b.RequestID), len(f.pauses()), len(f.outbox()))
	}
}

// ---- both causes at once ----

// The member's own quota first, then the lead is paused: the member holds `quota` and `lead`, and is told each time.
func TestPauseCauses_MemberQuotaThenLeadPause(t *testing.T) {
	enablePauseEntry(t)
	f := gateFixture(t, true, true, 0)
	f.streamOf()
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Title: "boss", Address: "self/boss", PID: 10})
	if code, _, _ := f.createRelay(rid(420), "/tmp/10.sock", "_mem001"); code != http.StatusConflict {
		t.Fatalf("member relay: %d", code)
	}
	f.setQuota("sid-1", 0)
	if code, body := f.beginRaw("sid-1"); code != http.StatusConflict {
		t.Fatalf("lead begin: %d %s", code, body)
	}
	ps := f.pauses()
	for _, k := range []string{"sid-m1/quota", "sid-m1/lead", "sid-1/quota"} {
		if _, ok := ps[k]; !ok {
			t.Fatalf("missing %s in %+v", k, ps)
		}
	}
	got := f.outboxOf("sid-m1")
	if len(got) != 2 || got[0].Text != team.PauseNoticeMemberQuota || got[1].Text != team.PauseNoticeLeadPaused {
		t.Fatalf("member notices = %+v", got)
	}
}

// The lead is paused first (its members take a `lead` row), then the lead's relay of a member finds no pool: the member adds `quota`.
func TestPauseCauses_LeadPauseThenMemberQuota(t *testing.T) {
	enablePauseEntry(t)
	f := gateFixture(t, true, true, 0)
	f.streamOf()
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Title: "boss", Address: "self/boss", PID: 10})
	f.setQuota("sid-1", 0)
	if code, body := f.beginRaw("sid-1"); code != http.StatusConflict {
		t.Fatalf("lead begin: %d %s", code, body)
	}
	if code, _, ae := f.createRelay(rid(421), "/tmp/10.sock", "_mem001"); code != http.StatusConflict || ae.Error != team.ErrMemberPausedNoQuota {
		t.Fatalf("member relay: %d %+v", code, ae)
	}
	ps := f.pauses()
	for _, k := range []string{"sid-m1/lead", "sid-m1/quota", "sid-1/quota"} {
		if _, ok := ps[k]; !ok {
			t.Fatalf("missing %s in %+v", k, ps)
		}
	}
	got := f.outboxOf("sid-m1")
	if len(got) != 2 || got[0].Text != team.PauseNoticeLeadPaused || got[1].Text != team.PauseNoticeMemberQuota {
		t.Fatalf("member notices = %+v", got)
	}
}
