package teammod

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// MR-3a-1 (member relay spec §3.4, D5, D10): the lead relays a member that lives on another host. The op is recorded on this
// host as `forwarded` in the same transaction as the `relay` command; only a refusal of that command, a `moved` with the op's
// id, a `relay_failed` or (MR-3a-2) the void / unpair can end it, each as a compare-and-set FROM `forwarded`.

const fwdOp = "11111111-1111-4111-8111-0000000000f1"

// fwdRelayFixture: a team led here (h:1), a remote member abc12 (session sid-abc12, ref _rabc12, mk1) on hostM = alias air26,
// which announces `relay`.
func fwdRelayFixture(t *testing.T) (*fixture, *fakeHostCaller) {
	t.Helper()
	f, fc := remoteFixture(t)
	fc.caps["hostM"] = ipeers.TeamCaps{Kinds: append(append([]string{}, allKinds...), CmdRelay), AllowTeam: true}
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	f.core.CfgMu.Lock()
	f.core.Cfg.Peers.Hosts = []config.PeerHost{{Alias: "air26", URL: "https://air26.example", HostID: "hostM", InboundToken: "i", AllowTeam: true}}
	f.core.CfgMu.Unlock()
	return f, fc
}

func memberPrincipal() *middleware.Principal {
	return &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "air26", HostID: "hostM"}
}

// relayForwarded creates the lead's relay of the remote member and returns the op.
func (f *fixture) relayForwarded(id string) team.RelayOp {
	f.t.Helper()
	code, op, ae := f.createRelay(id, "/tmp/10.sock", "air26/"+remoteRef)
	if code != http.StatusCreated {
		f.t.Fatalf("create relay: %d %+v", code, ae)
	}
	return op
}

func movedOf(opID, fid string) team.TeamFact {
	return team.TeamFact{ID: fid, Kind: team.FactMoved, ToHostID: "h:1", TeamID: uid(1), MK: "mk1", OpID: opID,
		NewSession: "sid-new", NewRef: "_nnn222", PID: 88, ProcStart: "ps2", Pane: "%7", Title: "ios"}
}

func relayFailedOf(opID, fid, state, reason string) team.TeamFact {
	return team.TeamFact{ID: fid, Kind: team.FactRelayFailed, ToHostID: "h:1", TeamID: uid(1), MK: "mk1", OpID: opID, State: state, Reason: reason}
}

func (f *fixture) relayCommands() []commandRow { return f.commandsOf(CmdRelay) }

// ---- create ----

func TestForwarded_CreateRecordsTheOpAndTheCommandTogether(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.relayForwarded(fwdOp)
	if op.State != team.RelayForwarded || op.HostID != "hostM" || op.SessionID != "sid-abc12" || op.Ref != remoteRef || op.TeamID != uid(1) {
		t.Fatalf("op = %+v", op)
	}
	cmds := f.relayCommands()
	if len(cmds) != 1 || cmds[0].HostID != "hostM" || cmds[0].MK != "mk1" || cmds[0].TeamID != uid(1) {
		t.Fatalf("commands = %+v", cmds)
	}
	var body team.TeamCommand
	if err := json.Unmarshal(cmds[0].Body, &body); err != nil || body.Kind != team.CommandRelay || body.OpID != fwdOp || body.ToHostID != "hostM" || body.MK != "mk1" || body.ID == fwdOp {
		t.Fatalf("body = %+v err=%v (op_id must be the op's, the command's own id another)", body, err)
	}
	// the member host refuses a relay command without created_at (command_expired); it is the op's creation time
	if body.CreatedAt != op.CreatedAt || body.CreatedAt == 0 {
		t.Fatalf("created_at = %d, want the op's %d", body.CreatedAt, op.CreatedAt)
	}
	if len(f.sender.calls()) != 0 {
		t.Fatalf("a local control message was sent for a remote member: %+v", f.sender.calls())
	}
}

// Fault injection between the op's insert and the command's enqueue: neither exists. Mutation: enqueue after the commit → the
// op survives (red).
func TestForwarded_AFailureBetweenTheOpAndTheCommandLeavesNeitherNorASpentPool(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	f.unatt.set(true)
	f.qrule.set(true, nil)
	f.setPool("sid-1", 2)
	f.m.store.afterRelayCommandEnqueue = func() error { return errInjected }
	code, _, _ := f.createRelay(fwdOp, "/tmp/10.sock", "air26/"+remoteRef)
	if code != http.StatusInternalServerError {
		t.Fatalf("create = %d, want 500", code)
	}
	if _, ok, _ := f.m.store.GetRelayOp(fwdOp); ok {
		t.Fatal("the op was left without its command")
	}
	if n := len(f.relayCommands()); n != 0 {
		t.Fatalf("%d commands left", n)
	}
	if left := f.poolLeft("sid-1"); left != 2 {
		t.Fatalf("pool = %d, want 2 (a spend must roll back with the rest)", left)
	}
}

// M that does not announce `relay` (or one that has not allowed this host, or cannot be reached) is told before the gate:
// nothing is spent, nothing is written.
func TestForwarded_RefusedBeforeTheGateWhenTheHostCannotApplyIt(t *testing.T) {
	cases := []struct {
		name   string
		caps   ipeers.TeamCaps
		err    error
		status int
		code   string
	}{
		{"no relay in kinds", ipeers.TeamCaps{Kinds: allKinds, AllowTeam: true}, nil, 409, team.ErrRelayUnsupported},
		{"team not allowed", ipeers.TeamCaps{Kinds: append(append([]string{}, allKinds...), CmdRelay), AllowTeam: false}, nil, 409, "host_not_allowed"},
		{"unreachable", ipeers.TeamCaps{}, errInjected, 503, "remote_unreachable"},
	}
	for _, tc := range cases {
		f, fc := fwdRelayFixture(t)
		f.unatt.set(true)
		f.qrule.set(true, nil)
		f.setPool("sid-1", 2)
		fc.caps["hostM"], fc.capsErr = tc.caps, tc.err
		code, _, ae := f.createRelay(fwdOp, "/tmp/10.sock", "air26/"+remoteRef)
		if code != tc.status || ae.Error != tc.code {
			t.Errorf("%s: %d %s, want %d %s", tc.name, code, ae.Error, tc.status, tc.code)
		}
		if _, ok, _ := f.m.store.GetRelayOp(fwdOp); ok || len(f.relayCommands()) != 0 || f.poolLeft("sid-1") != 2 {
			t.Errorf("%s: something was written or spent", tc.name)
		}
	}
}

// A pool that is spent out would hold the op for a card; the card for a remote member is MR-3a-2, so for now it is refused
// whole (nothing written, nothing spent).
func TestForwarded_ASpentOutPoolIsRefusedUntilTheCardPathLands(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	f.unatt.set(true)
	f.qrule.set(true, nil)
	f.setPool("sid-1", 0)
	code, _, ae := f.createRelay(fwdOp, "/tmp/10.sock", "air26/"+remoteRef)
	if code != http.StatusConflict || ae.Error != team.ErrRelayUnsupported {
		t.Fatalf("= %d %s, want 409 relay_unsupported", code, ae.Error)
	}
	if _, ok, _ := f.m.store.GetRelayOp(fwdOp); ok || len(f.relayCommands()) != 0 {
		t.Fatal("a refused create left an op or a command")
	}
	if open, _ := f.m.store.ListOpen(); len(open) != 0 {
		t.Fatalf("a card was opened: %+v", open)
	}
}

// With the pool on, one unit is spent in the create's transaction and the command is queued.
func TestForwarded_TheGateSpendsOneUnitInTheSameTransaction(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	f.unatt.set(true)
	f.qrule.set(true, nil)
	f.setPool("sid-1", 2)
	f.relayForwarded(fwdOp)
	if left := f.poolLeft("sid-1"); left != 1 {
		t.Fatalf("pool = %d, want 1", left)
	}
}

func TestForwarded_ReplayAndOneOpenAndNotActive(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.relayForwarded(fwdOp)
	code, again, _ := f.createRelay(fwdOp, "/tmp/10.sock", "air26/"+remoteRef)
	if code != http.StatusOK || again.ID != op.ID || len(f.relayCommands()) != 1 {
		t.Fatalf("replay = %d %+v, %d commands", code, again, len(f.relayCommands()))
	}
	code, _, ae := f.createRelay("11111111-1111-4111-8111-0000000000f2", "/tmp/10.sock", "air26/"+remoteRef)
	if code != http.StatusConflict || ae.Error != team.ErrRelayOpen {
		t.Fatalf("second open op = %d %s, want 409 relay_open", code, ae.Error)
	}
	g, _ := fwdRelayFixture(t)
	if _, err := g.m.store.db.Exec(`UPDATE team_members SET state = 'releasing' WHERE spawn_op = 'abc12'`); err != nil {
		t.Fatal(err)
	}
	if code, _, ae := g.createRelay(fwdOp, "/tmp/10.sock", "air26/"+remoteRef); code != http.StatusConflict || ae.Error != team.ErrNotYourMember {
		t.Fatalf("not active = %d %s, want 409 not_your_member", code, ae.Error)
	}
}

// A mod's report can never move a forwarded op: the op is not on this host's mod.
func TestForwarded_AModReportDoesNotMoveIt(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.relayForwarded(fwdOp)
	for _, to := range []team.RelayState{team.RelayClaimed, team.RelayCleared, team.RelayDone, team.RelayFailed, team.RelayCancelled, team.RelayRequested} {
		if relayTransitionOK(team.RelayKindMember, team.RelayForwarded, to) {
			t.Errorf("forwarded → %s is a report transition", to)
		}
	}
	if got := f.op(op.ID); got.State != team.RelayForwarded {
		t.Fatalf("op = %+v", got)
	}
}

// ---- the command's answer ----

func (f *fixture) settleRelayCommand(res func(id string) any, refusedCode string) {
	f.t.Helper()
	cmds := f.relayCommands()
	if len(cmds) != 1 {
		f.t.Fatalf("%d relay commands", len(cmds))
	}
	r := refusedBy(refusedCode)
	if refusedCode == "" {
		r = answerOf(cmds[0].ID, res(cmds[0].ID))
	}
	if _, err := f.m.store.SettleCommand(cmds[0].ID, r, f.clock.Load(), remoteOutcomes{m: f.m}); err != nil {
		f.t.Fatalf("settle: %v", err)
	}
}

func TestForwarded_ARefusedCommandFailsTheOpWithItsCode(t *testing.T) {
	for _, code := range []string{"relay_open", team.ErrRelayUnsupported, "host_not_allowed", team.ErrNotYourMember} {
		f, _ := fwdRelayFixture(t)
		op := f.relayForwarded(fwdOp)
		f.settleRelayCommand(nil, code)
		got := f.op(op.ID)
		if got.State != team.RelayFailed || got.Reason != code {
			t.Errorf("%s: op = %+v, want failed/%s", code, got, code)
		}
	}
}

func TestForwarded_AnAcceptedCommandChangesNothing(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.relayForwarded(fwdOp)
	f.settleRelayCommand(func(string) any { return team.RelayCommandOutcome{State: "accepted"} }, "")
	if got := f.op(op.ID); got.State != team.RelayForwarded {
		t.Fatalf("op = %+v, want forwarded", got)
	}
}

// ---- moved ----

func TestForwarded_MovedWithTheOpsIdEndsItDoneAndMovesTheRow(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.relayForwarded(fwdOp)
	code, body := f.postFact(memberPrincipal(), movedOf(fwdOp, factUUID1))
	if code != 200 || !strings.Contains(string(body), `"applied"`) {
		t.Fatalf("moved = %d %s", code, body)
	}
	got := f.op(op.ID)
	if got.State != team.RelayDone || got.NewSessionID != "sid-new" || got.NewRef != "_nnn222" {
		t.Fatalf("op = %+v, want done with the new session and ref", got)
	}
	if row := f.dumpMember("abc12"); row.Session != "sid-new" || row.Ref != "_nnn222" {
		t.Fatalf("row = %+v", row)
	}
}

// `--wait` (the op's long-poll) returns the done op with its new session and ref.
func TestForwarded_AWaitingPollGetsTheDoneOp(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.relayForwarded(fwdOp)
	var (
		wg   sync.WaitGroup
		got  team.RelayOp
		code int
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, body := f.do(http.MethodGet, "/api/relay/ops/"+op.ID+"?wait=5", nil)
		code = c
		_ = json.Unmarshal(body, &got)
	}()
	waitForWaiter(t, f, op.ID)
	start := time.Now()
	f.postFact(memberPrincipal(), movedOf(fwdOp, factUUID1))
	wg.Wait()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the poll took %s after the fact: nothing woke it", d)
	}
	if code != 200 || got.State != team.RelayDone || got.NewRef != "_nnn222" {
		t.Fatalf("poll = %d %+v", code, got)
	}
}

func TestForwarded_TheLeadIsToldWithAddressableRefs(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	f.relayForwarded(fwdOp)
	f.postFact(memberPrincipal(), movedOf(fwdOp, factUUID1))
	want := "[pdx team] member 接力完成：air26/_rabc12 → air26/_nnn222"
	waitFor(t, func() bool { return len(f.leadNotices()) == 1 })
	if got := f.leadNotices()[0]; got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
}

// ---- relay_failed ----

func TestForwarded_RelayFailedEndsTheOpFailedOrCancelled(t *testing.T) {
	for _, state := range []string{"failed", "cancelled"} {
		f, _ := fwdRelayFixture(t)
		op := f.relayForwarded(fwdOp)
		code, body := f.postFact(memberPrincipal(), relayFailedOf(fwdOp, factUUID1, state, "member_unresponsive"))
		if code != 200 || !strings.Contains(string(body), `"applied"`) {
			t.Fatalf("%s: %d %s", state, code, body)
		}
		got := f.op(op.ID)
		if string(got.State) != state || got.Reason != "member_unresponsive" {
			t.Errorf("%s: op = %+v", state, got)
		}
		waitFor(t, func() bool { return len(f.leadNotices()) == 1 })
		if n := f.leadNotices()[0]; !strings.Contains(n, "air26/_rabc12") || !strings.Contains(n, "member_unresponsive") {
			t.Errorf("%s: notice = %q", state, n)
		}
	}
}

func TestForwarded_RelayFailedReplayAnswersTheStoredOutcomeAndTellsOnce(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	f.relayForwarded(fwdOp)
	f.postFact(memberPrincipal(), relayFailedOf(fwdOp, factUUID1, "failed", "member_gone"))
	f.postFact(memberPrincipal(), relayFailedOf(fwdOp, factUUID1, "failed", "member_gone"))
	waitFor(t, func() bool { return len(f.leadNotices()) >= 1 })
	time.Sleep(60 * time.Millisecond)
	if n := len(f.leadNotices()); n != 1 {
		t.Fatalf("%d notices, want 1", n)
	}
}

func TestForwarded_RelayFailedValidation(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	f.relayForwarded(fwdOp)
	for name, fact := range map[string]team.TeamFact{
		"state done":    relayFailedOf(fwdOp, factUUID1, "done", "x"),
		"no state":      relayFailedOf(fwdOp, factUUID1, "", "x"),
		"no op id":      relayFailedOf("", factUUID1, "failed", "x"),
		"control chars": relayFailedOf(fwdOp, factUUID1, "failed", "a\nb"),
	} {
		if code, body := f.postFact(memberPrincipal(), fact); code != 400 {
			t.Errorf("%s: %d %s, want 400", name, code, body)
		}
	}
	if got := f.op(fwdOp); got.State != team.RelayForwarded {
		t.Fatalf("op = %+v", got)
	}
}

// One mutation per binding predicate (host, team, mk, the op's own session): each leaves the op forwarded.
func TestForwarded_RelayFailedBinding(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.relayForwarded(fwdOp)
	f.core.CfgMu.Lock()
	f.core.Cfg.Peers.Hosts = append(f.core.Cfg.Peers.Hosts, config.PeerHost{Alias: "other", URL: "https://o.example", HostID: "hostN", InboundToken: "j", AllowTeam: true})
	f.core.CfgMu.Unlock()
	f.remoteRow("def34", "hostM", "mk2", rowActive) // another member of the same host
	f.remoteRow("ghi56", "hostN", "mk3", rowActive) // a member of another host
	otherHost := &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "other", HostID: "hostN"}
	cases := map[string]struct {
		p    *middleware.Principal
		mut  func(*team.TeamFact)
		want int
	}{
		"another host, its own member (the op is hostM's)": {otherHost, func(x *team.TeamFact) { x.MK = "mk3" }, 200},
		"another host, the mk of hostM's member":           {otherHost, func(x *team.TeamFact) {}, 409},
		"another team":                                     {memberPrincipal(), func(x *team.TeamFact) { x.TeamID = uid(2) }, 409},
		"another member's mk (not the op's)":               {memberPrincipal(), func(x *team.TeamFact) { x.MK = "mk2" }, 200},
		"no such mk":                                       {memberPrincipal(), func(x *team.TeamFact) { x.MK = "nope" }, 409},
		"an op id that is not this host's op":              {memberPrincipal(), func(x *team.TeamFact) { x.OpID = "11111111-1111-4111-8111-0000000000ee" }, 200},
	}
	n := 0
	for name, tc := range cases {
		n++
		fact := relayFailedOf(fwdOp, fmt.Sprintf("b%07d-2222-4222-8222-222222222222", n), "failed", "x")
		tc.mut(&fact)
		code, body := f.postFact(tc.p, fact)
		if code != tc.want {
			t.Errorf("%s: %d %s, want %d", name, code, body, tc.want)
		}
		if tc.want == 200 && !strings.Contains(string(body), `"ignored"`) {
			t.Errorf("%s: %s, want ignored", name, body)
		}
		if got := f.op(op.ID); got.State != team.RelayForwarded {
			t.Fatalf("%s moved the op: %+v", name, got)
		}
	}
}

// ---- reverse arrival (D10): whichever lands first ends it; the others are logged and ignored; moved still moves the row ----

func TestForwarded_ReverseArrival_MovedThenALateRefusal(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.relayForwarded(fwdOp)
	f.postFact(memberPrincipal(), movedOf(fwdOp, factUUID1))
	f.settleRelayCommand(nil, "relay_open")
	if got := f.op(op.ID); got.State != team.RelayDone || got.NewRef != "_nnn222" {
		t.Fatalf("a late refusal changed the done op: %+v", got)
	}
}

func TestForwarded_ReverseArrival_RelayFailedThenALateRefusal(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.relayForwarded(fwdOp)
	f.postFact(memberPrincipal(), relayFailedOf(fwdOp, factUUID1, "failed", "member_gone"))
	f.settleRelayCommand(nil, "relay_open")
	if got := f.op(op.ID); got.State != team.RelayFailed || got.Reason != "member_gone" {
		t.Fatalf("a late refusal rewrote the failure: %+v", got)
	}
}

func TestForwarded_ReverseArrival_ARefusalThenALateMovedKeepsTheOpFailedButMovesTheRow(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.relayForwarded(fwdOp)
	f.settleRelayCommand(nil, "relay_open")
	f.postFact(memberPrincipal(), movedOf(fwdOp, factUUID1))
	got := f.op(op.ID)
	if got.State != team.RelayFailed || got.Reason != "relay_open" || got.NewSessionID != "" {
		t.Fatalf("a late moved rewrote the failed op: %+v", got)
	}
	if row := f.dumpMember("abc12"); row.Session != "sid-new" || row.Ref != "_nnn222" {
		t.Fatalf("the session did move on its host; the row = %+v", row)
	}
}

func TestForwarded_ReverseArrival_RelayFailedThenALateMovedAndMovedThenALateRelayFailed(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.relayForwarded(fwdOp)
	f.postFact(memberPrincipal(), relayFailedOf(fwdOp, factUUID1, "cancelled", "denied"))
	f.postFact(memberPrincipal(), movedOf(fwdOp, factUUID2))
	if got := f.op(op.ID); got.State != team.RelayCancelled || got.Reason != "denied" {
		t.Fatalf("op = %+v, want cancelled/denied kept", got)
	}
	g, _ := fwdRelayFixture(t)
	op2 := g.relayForwarded(fwdOp)
	g.postFact(memberPrincipal(), movedOf(fwdOp, factUUID1))
	g.postFact(memberPrincipal(), relayFailedOf(fwdOp, factUUID2, "failed", "member_gone"))
	if got := g.op(op2.ID); got.State != team.RelayDone {
		t.Fatalf("op = %+v, want done kept", got)
	}
}

// ---- D10: the timers and the boot do not see a forwarded op ----

func TestForwarded_TheStallTimerAndTheBootLeaveItAlone(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.relayForwarded(fwdOp)
	f.clock.Add(3 * 60 * minute)
	f.sweepTimeouts()
	f.m.reconcileRelays()
	f.sweepTimeouts()
	if got := f.op(op.ID); got.State != team.RelayForwarded {
		t.Fatalf("a timer or the boot touched a forwarded op: %+v", got)
	}
}

// moved with no op id (a person's /relay on M) leaves a forwarded op of the same member alone: it is not that op's answer.
func TestForwarded_AMovedWithoutAnOpIdDoesNotEndTheOp(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.relayForwarded(fwdOp)
	f.postFact(memberPrincipal(), movedOf("", factUUID1))
	if got := f.op(op.ID); got.State != team.RelayForwarded {
		t.Fatalf("op = %+v", got)
	}
}

// A refused command ends the op: the long-poll is woken and the lead is told once, with the member's address.
func TestForwarded_ARefusalWakesThePollAndTellsTheLead(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	op := f.relayForwarded(fwdOp)
	var (
		wg  sync.WaitGroup
		got team.RelayOp
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, body := f.do(http.MethodGet, "/api/relay/ops/"+op.ID+"?wait=5", nil)
		_ = json.Unmarshal(body, &got)
	}()
	waitForWaiter(t, f, op.ID)
	start := time.Now()
	f.settleRelayCommand(nil, "relay_open")
	wg.Wait()
	if d := time.Since(start); d > 2*time.Second || got.State != team.RelayFailed {
		t.Fatalf("poll after %s: %+v", d, got)
	}
	waitFor(t, func() bool { return len(f.leadNotices()) == 1 })
	if n := f.leadNotices()[0]; n != "[pdx team] member 接力失敗：air26/_rabc12（relay_open）" {
		t.Fatalf("notice = %q", n)
	}
}

// codex R1: what a settle noted for after its commit dies with a settle that did not commit — a later settle must not tell the
// lead about an op that another cause ended in between. Mutation: one shared list of noted ids → two notices (red).
type failAfterApply struct{ m *Module }

func (o failAfterApply) ApplyOutcome(tx *sql.Tx, c commandRow, res peersmod.CallResult) error {
	if err := (remoteOutcomes{m: o.m}).ApplyOutcome(tx, c, res); err != nil {
		return err
	}
	return errInjected
}

func TestForwarded_ASettleThatDidNotCommitLeavesNothingToTellLater(t *testing.T) {
	f, _ := fwdRelayFixture(t)
	f.relayForwarded(fwdOp)
	cmds := f.relayCommands()
	if _, err := f.m.store.SettleCommand(cmds[0].ID, refusedBy("relay_open"), f.clock.Load(), failAfterApply{f.m}); err == nil {
		t.Fatal("the injected failure did not surface")
	}
	if got := f.op(fwdOp); got.State != team.RelayForwarded {
		t.Fatalf("a rolled back settle ended the op: %+v", got)
	}
	f.postFact(memberPrincipal(), relayFailedOf(fwdOp, factUUID1, "failed", "member_gone")) // another cause ends it, and tells once
	waitFor(t, func() bool { return len(f.leadNotices()) == 1 })
	f.settleRemote(CmdRelease, "r9", "mk1", answerOf("r9", map[string]string{"state": "ok"})) // an unrelated settle commits
	time.Sleep(80 * time.Millisecond)
	if n := len(f.leadNotices()); n != 1 {
		t.Fatalf("%d notices, want 1", n)
	}
}

// codex attack: only an explicit `accepted` for THIS command settles it. A 2xx with another command's id, no outcome or an
// unknown state is a broken or newer peer: the command stays pending (sent again) and the op stays forwarded, never done with
// nothing behind it.
func TestForwarded_OnlyAnAcceptedAnswerSettlesTheCommand(t *testing.T) {
	bad := map[string]func(id string) peersmod.CallResult{
		"another command's id": func(string) peersmod.CallResult {
			return answerOf("someone-else", team.RelayCommandOutcome{State: "accepted"})
		},
		"no outcome":    func(id string) peersmod.CallResult { return answerOf(id, nil) },
		"unknown state": func(id string) peersmod.CallResult { return answerOf(id, team.RelayCommandOutcome{State: "maybe"}) },
		"applied (an adopt's word)": func(id string) peersmod.CallResult {
			return answerOf(id, map[string]string{"state": "applied"})
		},
	}
	for name, mk := range bad {
		f, _ := fwdRelayFixture(t)
		f.relayForwarded(fwdOp)
		c := f.relayCommands()[0]
		if settled, err := f.m.store.SettleCommand(c.ID, mk(c.ID), f.clock.Load(), remoteOutcomes{m: f.m}); err == nil || settled {
			t.Errorf("%s: settled=%v err=%v, want an error and nothing settled", name, settled, err)
		}
		if got := f.cmdState(c.ID); got.State != cmdPending {
			t.Errorf("%s: command is %s, want pending", name, got.State)
		}
		if got := f.op(fwdOp); got.State != team.RelayForwarded {
			t.Errorf("%s: op = %+v", name, got)
		}
	}
}
