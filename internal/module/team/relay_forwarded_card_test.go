package teammod

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// MR-3a-2 (member relay spec §3.4 RQ-2 delta, D9, D11): the held card, the 10 minute expiry with its void, the unpair clean-up.

// heldRemoteRelay opens the lead's relay of the remote member with the pool spent out: the op waits for a card.
func (f *fixture) heldRemoteRelay(opID string) team.RelayOp {
	f.t.Helper()
	f.unatt.set(true)
	f.qrule.set(true, nil)
	f.setPool("sid-1", 0)
	return f.relayForwarded(opID)
}

// ---- the held card ----

func TestForwardedCard_APoolSpentOutHoldsTheOpForACardAndSendsNothing(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.heldRemoteRelay(fwdOp)
	if op.State != team.RelayAwaitingApproval || op.HostID != "hostM" || op.RequestID == "" {
		t.Fatalf("op = %+v", op)
	}
	if f.rowCount("member_relay") != 1 || len(f.relayCommands()) != 0 || len(f.sender.calls()) != 0 {
		t.Fatalf("card rows %d, commands %d, control messages %d", f.rowCount("member_relay"), len(f.relayCommands()), len(f.sender.calls()))
	}
}

// The approve moves awaiting_approval → forwarded and queues the command in the same transaction; no local control message.
// Mutation gates: leave the op `requested` (the local path) → red; call the local control → a message is sent (red).
func TestForwardedCard_AnApproveForwardsTheOpAndQueuesTheCommandTogether(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.heldRemoteRelay(fwdOp)
	f.clock.Add(7 * minute)
	if code, body := f.decide(op.RequestID, "approve"); code != http.StatusOK {
		t.Fatalf("approve: %d %s", code, body)
	}
	if got := f.op(op.ID); got.State != team.RelayForwarded {
		t.Fatalf("op = %+v, want forwarded", got)
	}
	cmds := f.relayCommands()
	if len(cmds) != 1 || cmds[0].HostID != "hostM" || cmds[0].MK != "mk1" {
		t.Fatalf("commands = %+v", cmds)
	}
	var body team.TeamCommand
	if err := json.Unmarshal(cmds[0].Body, &body); err != nil || body.OpID != op.ID || body.CreatedAt != f.clock.Load() {
		t.Fatalf("body = %+v err=%v (created_at is the approve's time, not the op's)", body, err)
	}
	time.Sleep(60 * time.Millisecond)
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d control messages for a remote member", n)
	}
}

// A failure between the op's move and the command's enqueue: the row stays open, the op awaits, nothing is queued.
func TestForwardedCard_AFailureDuringTheApproveRollsBackWholly(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.heldRemoteRelay(fwdOp)
	f.m.store.afterRelayCommandEnqueue = func() error { return errInjected }
	if code, _ := f.decide(op.RequestID, "approve"); code == http.StatusOK {
		t.Fatal("the approve succeeded through an injected failure")
	}
	if got := f.op(op.ID); got.State != team.RelayAwaitingApproval || len(f.relayCommands()) != 0 {
		t.Fatalf("op = %+v, %d commands", got, len(f.relayCommands()))
	}
	if st := f.rowState(op.RequestID); st != team.StateOpen {
		t.Fatalf("row = %s, want open", st)
	}
}

func TestForwardedCard_DenyCancelsAndSendsNothing(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.heldRemoteRelay(fwdOp)
	if code, body := f.decide(op.RequestID, "deny"); code != http.StatusOK {
		t.Fatalf("deny: %d %s", code, body)
	}
	if got := f.op(op.ID); got.State != team.RelayCancelled || len(f.relayCommands()) != 0 {
		t.Fatalf("op = %+v, %d commands", got, len(f.relayCommands()))
	}
}

// The approve's re-check (§4.3): a member that left meanwhile cancels the op and queues nothing.
func TestForwardedCard_ARecheckThatFindsTheMemberGoneQueuesNothing(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.heldRemoteRelay(fwdOp)
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'released' WHERE spawn_op = 'abc12'`); err != nil {
		t.Fatal(err)
	}
	f.decide(op.RequestID, "approve")
	if got := f.op(op.ID); got.State != team.RelayCancelled || len(f.relayCommands()) != 0 {
		t.Fatalf("op = %+v, %d commands", got, len(f.relayCommands()))
	}
}

// The boot repairs an op whose approved row did not carry it along: toward the row, as a forward with its command.
func TestForwardedCard_TheBootForwardsAnOpWhoseRowWasApproved(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.heldRemoteRelay(fwdOp)
	if _, won, err := f.m.store.CloseIfOpen(op.RequestID, Close{State: team.StateApproved, DecidedAt: f.clock.Load()}); err != nil || !won {
		t.Fatalf("close the row by hand: won=%v err=%v", won, err)
	}
	f.m.reconcileRelays()
	if got := f.op(op.ID); got.State != team.RelayForwarded || len(f.relayCommands()) != 1 {
		t.Fatalf("op = %+v, %d commands", got, len(f.relayCommands()))
	}
}

// ---- D9: the command expires; its void ends the op ----

func (f *fixture) voidsOf() []commandRow { return f.commandsOf(CmdVoid) }

func TestForwardedVoid_ARelayCommandNotDoneIn10MinutesIsVoided(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.relayForwarded(fwdOp)
	cmd := f.relayCommands()[0]
	f.clock.Add(commandExpiryMS - 1_000)
	f.m.expireCommands()
	if len(f.voidsOf()) != 0 {
		t.Fatal("voided early")
	}
	f.clock.Add(1_000)
	f.m.expireCommands()
	if st := f.cmdState(cmd.ID).State; st != cmdVoid {
		t.Fatalf("command = %s, want void", st)
	}
	voids := f.voidsOf()
	if len(voids) != 1 {
		t.Fatalf("%d voids", len(voids))
	}
	var body team.TeamCommand
	if err := json.Unmarshal(voids[0].Body, &body); err != nil || body.CommandID != cmd.ID || body.TeamID != uid(1) || body.MK != "mk1" {
		t.Fatalf("void = %+v err=%v", body, err)
	}
	if got := f.op(op.ID); got.State != team.RelayForwarded {
		t.Fatalf("the op ends by the void's outcome, not by the expiry: %+v", got)
	}
}

// An expired relay command is never sent (the pump's head skips it, as for an adopt).
func TestForwardedVoid_AnExpiredRelayCommandIsNeverSent(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	f.relayForwarded(fwdOp)
	f.clock.Add(commandExpiryMS)
	ob := &commandOutbox{s: f.m.store, out: remoteOutcomes{m: f.m}, now: f.m.now}
	if _, _, ok, err := ob.Head("hostM"); err != nil || ok {
		t.Fatalf("head ok=%v err=%v: an expired relay command is still offered", ok, err)
	}
}

// The void's outcome: not_applied and undone end the op failed{remote_unreachable}; too_late leaves it for the fact.
// Mutation gate: end it on too_late too → red.
func TestForwardedVoid_TheOutcomeEndsTheOpOnlyWhenTheCommandDidNotRun(t *testing.T) {
	for state, want := range map[string]team.RelayState{team.VoidNotApplied: team.RelayFailed, team.VoidUndone: team.RelayFailed, team.VoidTooLate: team.RelayForwarded} {
		f, _ := fwdRelayFixture(t)
		op := f.relayForwarded(fwdOp)
		f.clock.Add(commandExpiryMS)
		f.m.expireCommands()
		void := f.voidsOf()[0]
		if _, err := f.m.store.SettleCommand(void.ID, answerOf(void.ID, team.VoidOutcome{State: state}), f.clock.Load(), remoteOutcomes{m: f.m}); err != nil {
			t.Fatalf("%s: settle: %v", state, err)
		}
		got := f.op(op.ID)
		if got.State != want || (want == team.RelayFailed && got.Reason != "remote_unreachable") {
			t.Errorf("%s: op = %+v, want %s", state, got, want)
		}
	}
}

// Void and fact in either order: the first to land ends the op, the other changes nothing (D10).
func TestForwardedVoid_ReverseArrivalWithTheFacts(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.relayForwarded(fwdOp)
	f.clock.Add(commandExpiryMS)
	f.m.expireCommands()
	void := f.voidsOf()[0]
	f.postFact(memberPrincipal(), movedOf(fwdOp, factUUID1)) // the command had run after all
	if _, err := f.m.store.SettleCommand(void.ID, answerOf(void.ID, team.VoidOutcome{State: team.VoidUndone}), f.clock.Load(), remoteOutcomes{m: f.m}); err != nil {
		t.Fatal(err)
	}
	if got := f.op(op.ID); got.State != team.RelayDone {
		t.Fatalf("a late void rewrote the done op: %+v", got)
	}
	g, _ := fwdRelayFixture(t)
	op2 := g.relayForwarded(fwdOp)
	g.clock.Add(commandExpiryMS)
	g.m.expireCommands()
	void2 := g.voidsOf()[0]
	if _, err := g.m.store.SettleCommand(void2.ID, answerOf(void2.ID, team.VoidOutcome{State: team.VoidNotApplied}), g.clock.Load(), remoteOutcomes{m: g.m}); err != nil {
		t.Fatal(err)
	}
	g.postFact(memberPrincipal(), movedOf(fwdOp, factUUID1))
	if got := g.op(op2.ID); got.State != team.RelayFailed || got.Reason != "remote_unreachable" {
		t.Fatalf("a late moved rewrote the failed op: %+v", got)
	}
	if row := g.dumpMember("abc12"); row.Session != "sid-new" {
		t.Fatalf("the row must still move: %+v", row)
	}
}

// A void of an adopt or a spawn is not about a relay op: unchanged.
func TestForwardedVoid_AMalformedOutcomeSettlesNothing(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	f.relayForwarded(fwdOp)
	f.clock.Add(commandExpiryMS)
	f.m.expireCommands()
	void := f.voidsOf()[0]
	for _, res := range []peersmod.CallResult{answerOf(void.ID, nil), answerOf(void.ID, team.VoidOutcome{State: "maybe"}), answerOf("other", team.VoidOutcome{State: team.VoidUndone})} {
		if settled, err := f.m.store.SettleCommand(void.ID, res, f.clock.Load(), remoteOutcomes{m: f.m}); err == nil || settled {
			t.Errorf("settled=%v err=%v for a malformed void answer", settled, err)
		}
	}
	if got := f.op(fwdOp); got.State != team.RelayForwarded {
		t.Fatalf("op = %+v", got)
	}
}

func TestForwardedVoid_TheEndWakesThePollAndTellsTheLeadOnce(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	f.relayForwarded(fwdOp)
	f.clock.Add(commandExpiryMS)
	f.m.expireCommands()
	void := f.voidsOf()[0]
	if _, err := f.m.store.SettleCommand(void.ID, answerOf(void.ID, team.VoidOutcome{State: team.VoidUndone}), f.clock.Load(), remoteOutcomes{m: f.m}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(f.leadNotices()) == 1 })
	if n := f.leadNotices()[0]; n != "[pdx team] member 接力失敗：air26/_rabc12（remote_unreachable）" {
		t.Fatalf("notice = %q", n)
	}
}

// ---- D11: unpairing ends the host's forwarded ops ----

// Mutation gate: leave them → the op stays forwarded and its session cannot be relayed again (red).
func TestForwardedUnpair_EndsTheHostsForwardedOpsAndOnlyThose(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.relayForwarded(fwdOp)
	f.remoteRow("ghi56", "hostN", "mk3", rowActive)
	other := team.RelayOp{ID: "11111111-1111-4111-8111-0000000000aa", Kind: team.RelayKindMember, HostID: "hostN", SessionID: "sid-ghi56", Ref: "_rghi56", TeamID: uid(1),
		State: team.RelayForwarded, CreatedAt: f.clock.Load(), UpdatedAt: f.clock.Load()}
	if err := f.m.store.CreateRelayOp(other); err != nil {
		t.Fatal(err)
	}
	if err := f.m.unpairHost("hostM", "unpaired_by_peer"); err != nil {
		t.Fatal(err)
	}
	if got := f.op(op.ID); got.State != team.RelayFailed || got.Reason != "unpaired" {
		t.Fatalf("op = %+v, want failed/unpaired", got)
	}
	if got := f.op(other.ID); got.State != team.RelayForwarded {
		t.Fatalf("another host's op = %+v", got)
	}
	if open, found, _ := f.m.store.OpenRelayOpBySession("sid-abc12"); found {
		t.Fatalf("the session still has an open op: %+v", open)
	}
}

func TestForwardedUnpair_WakesThePollAndTellsTheLeadOnce(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	f.relayForwarded(fwdOp)
	if err := f.m.unpairHost("hostM", "unpaired"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(f.leadNotices()) == 1 })
	if err := f.m.unpairHost("hostM", "unpaired"); err != nil { // a second clean-up finds nothing to end
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if n := len(f.leadNotices()); n != 1 {
		t.Fatalf("%d notices, want 1", n)
	}
}

// A command in flight when the host is unpaired is dropped, and the facts that follow (the member host may still send them
// until it learns of the unpairing) cannot bring the op back.
func TestForwardedUnpair_ALateMovedDoesNotReviveTheOp(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.relayForwarded(fwdOp)
	if err := f.m.unpairHost("hostM", "unpaired"); err != nil {
		t.Fatal(err)
	}
	if st := f.cmdState(f.relayCommands()[0].ID).State; st != cmdDropped {
		t.Fatalf("command = %s, want dropped", st)
	}
	f.postFact(memberPrincipal(), movedOf(fwdOp, factUUID1))
	if got := f.op(op.ID); got.State != team.RelayFailed || got.Reason != "unpaired" {
		t.Fatalf("op = %+v", got)
	}
}
