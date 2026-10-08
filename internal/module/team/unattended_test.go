package teammod

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
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

// logs captures the module's log lines.
func (f *fixture) logs() func() []string {
	var mu sync.Mutex
	var lines []string
	f.m.logf = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), lines...) }
}

// countLines is how many lines hold sub.
func countLines(lines []string, sub string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

// sweep runs the switch-on sweep as its caller does: under createMu.
func (f *fixture) sweep() (int, int) {
	f.m.createMu.Lock()
	defer f.m.createMu.Unlock()
	return f.m.sweepUnattended("switch on")
}

// Rule 1 / Review focus 4: the switch is read under createMu. A create
// paused just before createMu while a switch-on (fake store on + the sweep
// under createMu: the PUT route is PU-1c's) completes sees the switch on
// when it goes on: never committed open, no opened. Mutation gate: read
// the switch before taking createMu → red.
func TestCreate_SwitchOnRacesCreateNeverOpen(t *testing.T) {
	f := newFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	f.m.beforeCreateLock = func() { close(entered); <-release }
	done := make(chan team.Approval)
	go func() {
		_, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1)))
		var a team.Approval
		_ = json.Unmarshal(body, &a)
		done <- a
	}()
	<-entered
	f.m.createMu.Lock()
	f.unatt.set(true)
	f.m.sweepUnattended("switch on")
	f.m.createMu.Unlock()
	close(release)
	if a := <-done; a.State != team.StateApproved {
		t.Fatalf("state %s, want approved", a.State)
	}
	if ops := f.opsOf(); !reflect.DeepEqual(ops, []string{"closed"}) {
		t.Fatalf("events = %v, want [closed]", ops)
	}
}

// U25 / D-U24-7 (decision 22): every daemon approval of a lead request
// grants min(requested, 3) members — unspecified counts as 3 — with the
// requested roots; at create, through the switch-on sweep and through the
// tick share unattendedGrant. Mutation gates: return the payload's
// max_members uncapped → red (rows 5 and 8); leave the tick's grant
// uncapped → the tick rows 5 and 8 red.
func TestUnattendedLeadGrant_IsMinOfRequestAndThree(t *testing.T) {
	paths := map[string]func(f *fixture){
		"create": func(f *fixture) { f.unatt.set(true); f.create(uid(1)) },
		"sweep":  func(f *fixture) { f.create(uid(1)); f.unatt.set(true); f.sweep() },
		"tick":   func(f *fixture) { f.create(uid(1)); f.unatt.set(true); f.m.tick() },
	}
	for _, c := range []struct{ asked, want int }{{0, 3}, {1, 1}, {2, 2}, {3, 3}, {5, 3}, {8, 3}} {
		for name, run := range paths {
			t.Run(fmt.Sprintf("%s/%d", name, c.asked), func(t *testing.T) {
				f := newFixture(t)
				f.createReqEdit = func(r *team.CreateApprovalRequest) { r.MaxMembers, r.Roots = c.asked, []string{"/w/a"} }
				run(f)
				f.assertTeamGrant("sid-1", c.want, []string{"/w/a"})
			})
		}
	}
}

// D-U23-3, rule 5: the switch-on sweep approves the open lead and
// self_relay rows by unattended and leaves both hook kinds open. Mutation
// gate: AutoApprovable includes hook_ask → red.
func TestSweepUnattended_ApprovesOpenLeadAndSelfRelayLeavesHookKinds(t *testing.T) {
	f := newFixture(t)
	lead := f.create(uid(1))
	relay := f.begin("sid-2")
	ask, perm := f.askBegin("tu-1"), f.askBeginPermission("tu-2")
	f.unatt.set(true)
	if approved, pending := f.sweep(); approved != 2 || pending != 0 {
		t.Fatalf("sweep = (%d, %d), want (2, 0)", approved, pending)
	}
	for _, id := range []string{lead.ID, relay.RequestID} {
		a, _, _ := f.m.store.Get(id)
		assertDecidedByUnattended(t, a, f.clock.Load())
	}
	for _, id := range []string{ask, perm} {
		if a, _, _ := f.m.store.Get(id); a.State != team.StateOpen {
			t.Fatalf("hook row %s is %s, want open", id, a.State)
		}
	}
	if f.op(relay.Op.ID).State != team.RelayClaimed {
		t.Fatal("the self relay's op was not claimed")
	}
}

// Decision 5 / rule 6: what the switch-on sweep could not approve (a
// transient failure) the next tick approves. Mutation gate: drop
// reconcileUnattended from tick → red.
func TestTick_ApprovesWhatTheSwitchOnSweepLeftOpen(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	relay := f.begin("sid-2")
	f.unatt.set(true)
	failed := false
	f.m.beforeAutoApprove = func(a team.Approval) error {
		if a.ID == relay.RequestID && !failed {
			failed = true
			return errors.New("database is locked")
		}
		return nil
	}
	if approved, pending := f.sweep(); approved != 1 || pending != 1 {
		t.Fatalf("sweep = (%d, %d), want (1, 1)", approved, pending)
	}
	f.m.tick()
	a, _, _ := f.m.store.Get(relay.RequestID)
	assertDecidedByUnattended(t, a, f.clock.Load())
	if f.op(relay.Op.ID).State != team.RelayClaimed {
		t.Fatal("the self relay's op was not claimed")
	}
}

// Rule 6's other half: with the switch off the tick approves nothing.
func TestTick_UnattendedOffApprovesNothing(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	f.m.tick()
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateOpen {
		t.Fatalf("state %s, want open", a.State)
	}
}

// Rule 6: a row a rule refuses (its origin became a member) stays open,
// and the refusal is logged once, not once per tick; a tick that approved
// nothing logs no summary. Once the row is approved and closed its entry
// goes, so a later refusal of the same id would log again. Mutation
// gates: drop the per-row set → red (three lines); log the tick's summary
// always → red.
func TestTick_RuleRefusalLogsOnce(t *testing.T) {
	f := newFixture(t)
	logs := f.logs()
	f.create(uid(1))
	f.makeMember("sid-1")
	f.unatt.set(true)
	for range 3 {
		f.m.tick()
	}
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateOpen {
		t.Fatalf("state %s, want open", a.State)
	}
	if n := countLines(logs(), "not auto-approved"); n != 1 {
		t.Fatalf("%d refusal lines in %q, want 1", n, logs())
	}
	if n := countLines(logs(), "unattended sweep (tick)"); n != 0 {
		t.Fatalf("%d tick summaries in %q, want 0", n, logs())
	}
}

// The per-row set forgets a row once it is no longer open (here it was
// cancelled), at the next sweep: it does not grow with every refused row
// of the daemon's life.
func TestTick_RefusalSetForgetsClosedRows(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	f.makeMember("sid-1")
	f.unatt.set(true)
	f.m.tick()
	if _, ok := f.m.notAutoApproved[uid(1)]; !ok {
		t.Fatal("the refusal was not remembered")
	}
	f.unatt.set(false)
	relay := f.begin("sid-2") // an open row that keeps the next tick sweeping
	if code, body := f.do(http.MethodDelete, "/api/team/approvals/"+uid(1), nil); code != http.StatusOK {
		t.Fatalf("cancel: %d %s", code, body)
	}
	f.unatt.set(true)
	f.m.tick()
	if a, _, _ := f.m.store.Get(relay.RequestID); a.State != team.StateApproved {
		t.Fatalf("relay row %s, want approved", a.State)
	}
	if len(f.m.notAutoApproved) != 0 {
		t.Fatalf("set = %v, want empty", f.m.notAutoApproved)
	}
}

// Rule 7: Start sweeps when the switch is on (the daemon restarted while
// requests were open), before its sweeper's first tick. Mutation gate:
// drop the boot sweep from Start → red.
func TestStart_UnattendedOnSweepsAtBoot(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	f.unatt.set(true)
	if err := f.m.Start(context.Background()); err != nil { // newFixture's Cleanup stops it
		t.Fatal(err)
	}
	a, _, _ := f.m.store.Get(uid(1))
	assertDecidedByUnattended(t, a, f.clock.Load())
}

// Rule 7: with the switch off, Start approves nothing.
func TestStart_UnattendedOffLeavesRowsOpen(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateOpen {
		t.Fatalf("state %s, want open", a.State)
	}
}

// Rule 9: a click and the daemon race on the CAS; exactly one wins and one
// closed is broadcast. A losing click answers 409 already_decided carrying
// the row decided by unattended.
func TestDecide_RacesUnattendedOneWins(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	f.events()
	f.unatt.set(true)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); f.sweep() }()
	code, body := f.decide(uid(1), "approve")
	wg.Wait()
	a, _, _ := f.m.store.Get(uid(1))
	switch code {
	case http.StatusOK:
		if a.DecidedBy == nil || a.DecidedBy.Kind != "app" {
			t.Fatalf("click won but the row is decided by %+v", a.DecidedBy)
		}
	case http.StatusConflict:
		if e := decodeErr(t, body); e.Error != team.ErrAlreadyDecided || e.Approval == nil || e.Approval.DecidedBy.Kind != team.ClientKindUnattended {
			t.Fatalf("click lost: %s", body)
		}
	default:
		t.Fatalf("decide: %d %s", code, body)
	}
	if ops := f.opsOf(); !reflect.DeepEqual(ops, []string{"closed"}) {
		t.Fatalf("events = %v, want one closed", ops)
	}
}
