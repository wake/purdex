package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

const memStart = "Sun Sep 13 15:22:36 2026" // the start time the fixture registry shows for every process

// memberTeam makes sid-1 lead a team with one live member sid-m1 (ref _mem001, pid 42) whose mod said hello at ver
// ("" = no hello). It returns the member row.
func (f *fixture) memberTeam(ver string) memberRow {
	f.t.Helper()
	f.approveLead(uid(1))
	row := newMember("op-m1", uid(1), "sid-m1", "_mem001", f.clock.Load())
	row.ProcStart, row.PaneID = memStart, "%2"
	if err := f.m.store.InsertMember(row); err != nil {
		f.t.Fatal(err)
	}
	f.liveMember("sid-m1", "_mem001", "one", "self/w-one", "tm-1")
	if ver != "" {
		if code, body := f.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{SessionID: "sid-m1", ModVersion: ver, Agent: "cc"}); code != http.StatusOK {
			f.t.Fatalf("hello: %d %s", code, body)
		}
	}
	return row
}

func (f *fixture) createRelay(id, inbox, target string) (int, team.RelayOp, team.APIError) {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/team/relays", team.RelayCreateRequest{ID: id, OriginInbox: inbox, Target: target})
	var resp team.RelayCreateResponse
	var ae team.APIError
	if code == http.StatusOK || code == http.StatusCreated {
		if err := json.Unmarshal(body, &resp); err != nil {
			f.t.Fatal(err)
		}
	} else {
		ae = decodeErr(f.t, body)
	}
	return code, resp.Op, ae
}

func TestRelayCreate_Checks(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("2")
	for _, c := range []struct {
		name, id, inbox, target, code string
		status                        int
	}{
		{"id not a UUID", "nope", "/tmp/10.sock", "_mem001", team.ErrBadRequest, 400},
		{"unknown inbox", rid(10), "/tmp/nobody.sock", "_mem001", team.ErrOriginUnknown, 400},
		{"not a lead", rid(11), "/tmp/20.sock", "_mem001", team.ErrNotLead, 409},
		{"not a member", rid(12), "/tmp/10.sock", "_ffffff", team.ErrNotYourMember, 409},
	} {
		if code, _, ae := f.createRelay(c.id, c.inbox, c.target); code != c.status || ae.Error != c.code {
			t.Errorf("%s: %d %+v, want %d %s", c.name, code, ae, c.status, c.code)
		}
	}
	// a member the registry no longer lists
	f.origins.mu.Lock()
	f.origins.hidden = map[string]bool{"sid-m1": true}
	f.origins.mu.Unlock()
	if code, _, ae := f.createRelay(rid(13), "/tmp/10.sock", "_mem001"); code != 404 || ae.Error != team.ErrUnknownSession {
		t.Errorf("not running: %d %+v", code, ae)
	}
	if n := countRelayOps(t, f); n != 0 {
		t.Fatalf("a refused create left %d ops", n)
	}
}

func countRelayOps(t *testing.T, f *fixture) int {
	t.Helper()
	var n int
	if err := f.m.store.db.QueryRow(`SELECT COUNT(*) FROM relay_ops`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Mutation gate: drop the version comparison → the version-1 case red; drop the presence check → the no-hello case red.
func TestRelayCreate_NeedsAProtocol2Mod(t *testing.T) {
	for _, ver := range []string{"", "1", "x"} {
		f := newFixture(t)
		f.memberTeam(ver)
		code, _, ae := f.createRelay(rid(20), "/tmp/10.sock", "_mem001")
		if code != 409 || ae.Error != team.ErrRelayUnsupported || !strings.Contains(ae.Detail, "_mem001 沒有載入 Purdex mod") {
			t.Errorf("mod %q: %d %+v", ver, code, ae)
		}
		if n := countRelayOps(t, f); n != 0 {
			t.Errorf("mod %q: %d ops", ver, n)
		}
	}
}

func TestRelayCreate_OpensARequestedOpBoundToTheMember(t *testing.T) {
	f := newFixture(t)
	mr := f.memberTeam("2")
	code, op, ae := f.createRelay(rid(30), "/tmp/10.sock", "_mem001")
	if code != 201 {
		t.Fatalf("create: %d %+v", code, ae)
	}
	if op.ID != rid(30) || op.Kind != team.RelayKindMember || op.State != team.RelayRequested || op.SessionID != "sid-m1" || op.Ref != "_mem001" ||
		op.TeamID != uid(1) || op.RequestID != "" || op.PID != mr.PID || op.PaneID != "%2" || op.ProcStart != memStart {
		t.Fatalf("op = %+v", op)
	}
	if got := f.op(rid(30)); got.State != team.RelayRequested || got.ProcStart != memStart {
		t.Fatalf("stored = %+v", got)
	}
	// the replay: the same id for the same member is the same op (200); for another target, id_conflict
	if code, again, _ := f.createRelay(rid(30), "/tmp/10.sock", "_mem001"); code != 200 || again.ID != op.ID {
		t.Fatalf("replay: %d %+v", code, again)
	}
	if code, _, ae := f.createRelay(rid(30), "/tmp/10.sock", "_ffffff"); code != 409 || ae.Error != team.ErrIDConflict {
		t.Fatalf("same id, other target: %d %+v", code, ae)
	}
	// a second id while one is open: relay_open with the open op
	if code, _, ae := f.createRelay(rid(31), "/tmp/10.sock", "_mem001"); code != 409 || ae.Error != team.ErrRelayOpen || ae.Op == nil || ae.Op.ID != rid(30) {
		t.Fatalf("second op: %d %+v", code, ae)
	}
}

func TestRelayCreate_SendsTheControlMessageFromTheLeadsInbox(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("2")
	f.createRelay(rid(40), "/tmp/10.sock", "_mem001")
	waitFor(t, func() bool { return len(f.sender.calls()) == 1 })
	alias, _ := f.m.selfHost()
	c := f.sender.calls()[0]
	if c.To != alias+"/_mem001" || c.Text != "[pdx-relay:control] op="+rid(40) || c.OriginInbox != "/tmp/10.sock" {
		t.Fatalf("control = %+v", c)
	}
}

// The sender is the team's CURRENT lead, and nothing is sent for an op that is no longer requested.
func TestSendMemberControl_FromTheCurrentLeadAndOnlyWhileRequested(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("2")
	_, op, _ := f.createRelay(rid(41), "/tmp/10.sock", "_mem001")
	waitFor(t, func() bool { return len(f.sender.calls()) == 1 })
	if _, err := f.m.store.db.Exec(`UPDATE teams SET lead_session_id = 'sid-2' WHERE id = ?`, uid(1)); err != nil { // the lead relayed
		t.Fatal(err)
	}
	f.m.sendMemberControl(op)
	if c := f.sender.calls(); len(c) != 2 || c[1].OriginInbox != "/tmp/20.sock" {
		t.Fatalf("after the lead changed: %+v", c)
	}
	if _, err := f.m.store.db.Exec(`UPDATE relay_ops SET state = 'claimed' WHERE id = ?`, op.ID); err != nil {
		t.Fatal(err)
	}
	f.m.sendMemberControl(op)
	if n := len(f.sender.calls()); n != 2 {
		t.Fatalf("a claimed op was sent a control: %d sends", n)
	}
}

// The core of the PR. A release between the create's checks and its transaction wins: the create answers
// not_your_member and no op exists. The other order: the op first, so the release finds it and refuses.
// Mutation gate: confirm membership outside the insert's transaction (drop the SELECT in CreateMemberRelayOp) → the
// first half goes red (an op is inserted for a released member).
func TestRelayCreate_RacesReleaseOneWins(t *testing.T) {
	f := newFixture(t)
	mr := f.memberTeam("2")
	released := false
	f.m.beforeMemberRelayInsert = func(row memberRow) {
		ok, err := f.m.store.ReleaseMember(row.SpawnOp, row.SessionID, f.clock.Load())
		if err != nil {
			t.Error(err)
		}
		released = ok
	}
	code, _, ae := f.createRelay(rid(50), "/tmp/10.sock", "_mem001")
	if !released || code != 409 || ae.Error != team.ErrNotYourMember {
		t.Fatalf("release first: released=%v, create %d %+v", released, code, ae)
	}
	if n := countRelayOps(t, f); n != 0 {
		t.Fatalf("an op was inserted for a released member: %d", n)
	}
	// the reverse: a fresh active member, the op first
	f2 := newFixture(t)
	mr = f2.memberTeam("2")
	if code, _, ae := f2.createRelay(rid(51), "/tmp/10.sock", "_mem001"); code != 201 {
		t.Fatalf("create: %d %+v", code, ae)
	}
	if ok, err := f2.m.store.ReleaseMember(mr.SpawnOp, mr.SessionID, f2.clock.Load()); err != nil || ok {
		t.Fatalf("a release over an open op: released=%v err=%v, want refused", ok, err)
	}
}

// Fault injection: a failure inside the transaction (the gate's own write, then its error) leaves no op and nothing the
// gate wrote. Mutation gate: run the gate's write outside the transaction → the row survives (red).
func TestCreateMemberRelayOp_AFailureRollsEverythingBack(t *testing.T) {
	f := newFixture(t)
	mr := f.memberTeam("2")
	op := team.RelayOp{ID: rid(60), Kind: team.RelayKindMember, HostID: "h:1", SessionID: mr.SessionID, Ref: mr.Ref, TeamID: uid(1),
		State: team.RelayRequested, HandoffPath: "/p", CreatedAt: 1, UpdatedAt: 1}
	boom := errors.New("boom")
	_, _, err := f.m.store.CreateMemberRelayOp(op, func(tx *sql.Tx, _ *team.RelayOp) (bool, error) {
		if _, err := tx.Exec(`INSERT INTO session_prefs (session_id, self_relay_paused, updated_at) VALUES ('gate-wrote', 1, 1)`); err != nil {
			t.Fatal(err)
		}
		return false, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	var n int
	f.m.store.db.QueryRow(`SELECT (SELECT COUNT(*) FROM relay_ops) + (SELECT COUNT(*) FROM session_prefs WHERE session_id = 'gate-wrote')`).Scan(&n)
	if n != 0 {
		t.Fatalf("a failed create left %d rows", n)
	}
	// a gate that succeeds is committed with the op, and may change what is inserted
	got, _, err := f.m.store.CreateMemberRelayOp(op, func(_ *sql.Tx, o *team.RelayOp) (bool, error) { o.Reason = "gate"; return false, nil })
	if err != nil || got.Reason != "gate" || f.op(op.ID).Reason != "gate" {
		t.Fatalf("with a gate: %+v err=%v", got, err)
	}
}

// pid + proc_start is the identity (P6-2a attacker #1). Mutation gate: drop the proc_start comparison → red.
func TestCleared_MemberOpBindsToItsProcStart(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("2")
	f.origins.cleared = map[string]int{"sid-new": 42}
	f.createRelay(rid(70), "/tmp/10.sock", "_mem001")
	set := func(start string) {
		t.Helper()
		if _, err := f.m.store.db.Exec(`UPDATE relay_ops SET state = 'written', proc_start = ? WHERE id = ?`, start, rid(70)); err != nil {
			t.Fatal(err)
		}
	}
	set("Mon Jan  1 00:00:00 2001") // the pid matches (42) but the process is another one
	if code, _, ae := f.report(rid(70), team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-new"}); code != 400 || !strings.Contains(ae.Detail, "started at") {
		t.Fatalf("reused pid: %d %+v", code, ae)
	}
	set(memStart)
	if code, _, ae := f.report(rid(70), team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-new"}); code != 200 {
		t.Fatalf("same process: %d %+v", code, ae)
	}
}

func TestRelayBegin_RecordsProcStartAndTheApprovalFallbackBindsIt(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1")
	if out.Op.ProcStart != memStart {
		t.Fatalf("begin op proc_start = %q", out.Op.ProcStart)
	}
	f.decide(out.RequestID, "approve")
	// an op from before pid/proc_start: bound through its approval row's origin, whose start differs from the target's
	f.m.store.db.Exec(`UPDATE relay_ops SET pid = 0, proc_start = '' WHERE id = ?`, out.Op.ID)
	row, _, _ := f.m.store.Get(out.RequestID)
	row.Origin.ProcStart = "Mon Jan  1 00:00:00 2001"
	raw, _ := json.Marshal(row.Origin)
	f.m.store.db.Exec(`UPDATE approval_requests SET origin_json = ? WHERE id = ?`, string(raw), out.RequestID)
	f.origins.cleared = map[string]int{"sid-new": 10}
	if code, _, ae := f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-new"}); code != 400 || !strings.Contains(ae.Detail, "started at") {
		t.Fatalf("fallback proc_start: %d %+v", code, ae)
	}
}
