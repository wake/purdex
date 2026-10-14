package teammod

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/team"
)

// RT-3a1 T28: the mode column and relay_fresh_half on a database from before. Mutation gate: skip the ensureColumn → the old table has
// no mode column and a read of an op fails (red).
func TestRelayOps_ModeColumnOnAnOldTable(t *testing.T) {
	s := openTestStore(t)
	op := team.RelayOp{ID: "op-1", Kind: team.RelayKindSelf, HostID: "h", SessionID: "s", Ref: "_abc123", State: team.RelayRequested, HandoffPath: "/p", CreatedAt: 1, UpdatedAt: 1}
	if err := s.CreateRelayOp(op); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE relay_ops DROP COLUMN mode`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := migrateRelayOpBinding(s.db); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetRelayOp("op-1")
	if err != nil || !ok || got.Mode != "" || got.ID != "op-1" {
		t.Fatalf("old row = %+v ok=%v err=%v, want it kept with mode ''", got, ok, err)
	}
	half := op
	half.ID, half.SessionID, half.Mode = "op-2", "s2", team.RelayModeHalf
	if err := s.CreateRelayOp(half); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.GetRelayOp("op-2"); got.Mode != team.RelayModeHalf {
		t.Fatalf("mode = %q, want half", got.Mode)
	}
}

func TestRelayFreshHalf_TableExists(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec(`INSERT INTO relay_fresh_half (session_id, op_id, path, written_at) VALUES ('s', 'o', '/p', 1)`); err != nil {
		t.Fatalf("relay_fresh_half: %v", err)
	}
}

// ---- T29: begin half, idle in unattended, the idle card's life, the switch-on split ----

func halfBeginReq(sid string, half bool) team.RelayBeginRequest {
	r := beginReq(sid)
	r.Reason, r.IdleMinutes, r.Half = team.RelayBeginReasonIdle, 40, half
	return r
}

func (f *fixture) beginRaw2(r team.RelayBeginRequest) (int, team.RelayBeginResponse, []byte) {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/relay/begin", r)
	var out team.RelayBeginResponse
	if code == http.StatusCreated {
		if err := json.Unmarshal(body, &out); err != nil {
			f.t.Fatal(err)
		}
	}
	return code, out, body
}

// A half begin is claimed at once, mode half, approved by the daemon's own client, and spends nothing - even with no quota left.
// Mutation gates: spend on a half (Auto) → the quota drops / the begin is paused (red); leave the mode ” → red.
func TestHalfBegin_ClaimedAtOnceNothingSpent(t *testing.T) {
	f := newFixture(t)
	f.quotaWorld()
	f.setQuota("sid-1", 0)
	code, resp, body := f.beginRaw2(halfBeginReq("sid-1", true))
	if code != http.StatusCreated {
		t.Fatalf("half begin: %d %s", code, body)
	}
	if resp.Op.State != team.RelayClaimed || resp.Op.Mode != team.RelayModeHalf {
		t.Fatalf("op = %+v, want claimed, mode half", resp.Op)
	}
	row := f.row(resp.RequestID)
	if row.State != team.StateApproved || row.DecidedBy == nil || row.DecidedBy.Kind != "daemon" || row.DecidedBy.Label != "閒置寫接力檔" {
		t.Fatalf("row = %s by %+v, want approved by the daemon's half client", row.State, row.DecidedBy)
	}
	if f.selfLeft("sid-1") != 0 || len(f.pauses()) != 0 {
		t.Fatalf("self_left %d, pauses %+v: a half spends nothing and pauses nobody", f.selfLeft("sid-1"), f.pauses())
	}
}

func TestHalfBegin_OnlyWithReasonIdle(t *testing.T) {
	f := newFixture(t)
	r := beginReq("sid-1")
	r.Half = true
	if code, _, body := f.beginRaw2(r); code != http.StatusBadRequest {
		t.Fatalf("half without idle: %d %s, want 400", code, body)
	}
}

// The switch refusals still apply to a half. Mutation gate: skip them for a half → the off host opens one (red).
func TestHalfBegin_TheSwitchRefusalsApply(t *testing.T) {
	f := newFixture(t)
	f.switches.set(hostconfig.RelaySwitches{SelfSolo: false, SelfLead: true})
	code, _, body := f.beginRaw2(halfBeginReq("sid-1", true))
	if code != http.StatusConflict || !strings.Contains(string(body), team.ErrSelfRelayOff) {
		t.Fatalf("half on an off host: %d %s, want 409 self_relay_off", code, body)
	}
}

// Unattended + idle: a mod that speaks the half (protocol ≥ 8) gets a half; an older one gets today's approval. Mutation gates: ignore the
// version → mod 7 gets a half (red); never half → mod 8 spends and is approved (red).
func TestIdleBegin_UnattendedSplitsOnTheModVersion(t *testing.T) {
	for _, tc := range []struct {
		ver      string
		wantMode string
		wantLeft int
	}{{"8", team.RelayModeHalf, 2}, {"7", "", 1}} {
		f := newFixture(t)
		f.quotaWorld()
		f.setQuota("sid-1", 2)
		f.modHello("sid-1", tc.ver)
		code, resp, body := f.beginRaw2(halfBeginReq("sid-1", false))
		if code != http.StatusCreated || resp.Op.State != team.RelayClaimed || resp.Op.Mode != tc.wantMode {
			t.Fatalf("mod %s: %d %s, want claimed with mode %q", tc.ver, code, body, tc.wantMode)
		}
		if got := f.selfLeft("sid-1"); got != tc.wantLeft {
			t.Fatalf("mod %s: self_left %d, want %d", tc.ver, got, tc.wantLeft)
		}
	}
}

// An idle card (the switch off) lives five minutes, a threshold card ten. Mutation gate: the same deadline for both → red.
func TestIdleCard_FiveMinutes(t *testing.T) {
	f := newFixture(t)
	_, resp, _ := f.beginRaw2(halfBeginReq("sid-1", false))
	if row := f.row(resp.RequestID); row.DeadlineAt-row.CreatedAt != team.SelfRelayIdleDeadlineS*1000 {
		t.Fatalf("idle card life = %d ms, want %d", row.DeadlineAt-row.CreatedAt, team.SelfRelayIdleDeadlineS*1000)
	}
	f2 := newFixture(t)
	_, resp2, _ := f2.beginRaw2(beginReq("sid-1"))
	if row := f2.row(resp2.RequestID); row.DeadlineAt-row.CreatedAt != team.SelfRelayDeadlineS*1000 {
		t.Fatalf("threshold card life = %d ms, want %d", row.DeadlineAt-row.CreatedAt, team.SelfRelayDeadlineS*1000)
	}
}

// The switch goes on with an idle card open: a mod ≥ 8 runs the half itself (the card times out, close_reason unattended); an older mod's
// card is approved as today. Mutation gates: approve both → the mod 8 case is red; time both out → the mod 7 case is red.
func TestIdleCard_SwitchOnSplitsOnTheModVersion(t *testing.T) {
	for _, tc := range []struct {
		ver  string
		want team.State
	}{{"8", team.StateTimeout}, {"7", team.StateApproved}} {
		f := newFixture(t)
		f.qrule.set(false, nil)
		f.modHello("sid-1", tc.ver)
		_, resp, _ := f.beginRaw2(halfBeginReq("sid-1", false)) // the switch is off: a card
		if st := f.row(resp.RequestID).State; st != team.StateOpen {
			t.Fatalf("mod %s: card = %s, want open", tc.ver, st)
		}
		f.switchTo(true)
		row := f.row(resp.RequestID)
		if row.State != tc.want {
			t.Fatalf("mod %s: card = %s after switch-on, want %s", tc.ver, row.State, tc.want)
		}
		if tc.want == team.StateTimeout {
			var reason string
			_ = f.m.store.db.QueryRow(`SELECT close_reason FROM approval_requests WHERE id = ?`, resp.RequestID).Scan(&reason)
			if reason != "unattended" {
				t.Fatalf("close_reason = %q, want unattended", reason)
			}
		}
	}
}

// ---- T30: `written` closes a half or a handoff ----

// halfOp opens a half op (claimed, mode half) and walks it to writing.
func (f *fixture) halfOp(sid string) team.RelayOp {
	f.t.Helper()
	code, resp, body := f.beginRaw2(halfBeginReq(sid, true))
	if code != http.StatusCreated {
		f.t.Fatalf("half begin: %d %s", code, body)
	}
	if c, _, ae := f.report(resp.Op.ID, team.RelayReportRequest{State: team.RelayWriting}); c != http.StatusOK {
		f.t.Fatalf("writing: %d %+v", c, ae)
	}
	return resp.Op
}

func (f *fixture) freshHalf(sid string) (opID, path string, ok bool) {
	f.t.Helper()
	err := f.m.store.db.QueryRow(`SELECT op_id, path FROM relay_fresh_half WHERE session_id = ?`, sid).Scan(&opID, &path)
	return opID, path, err == nil
}

// Mutation gates: leave the half `written` → red; skip the fresh upsert → red.
func TestWritten_ClosesAHalfAndKeepsItsFile(t *testing.T) {
	f := newFixture(t)
	op := f.halfOp("sid-1")
	code, got, ae := f.report(op.ID, team.RelayReportRequest{State: team.RelayWritten})
	if code != http.StatusOK || got.State != team.RelayCancelled || got.Reason != team.RelayReasonHalf || got.Mode != team.RelayModeHalf {
		t.Fatalf("written on a half: %d %+v %+v, want cancelled / half", code, got, ae)
	}
	if id, path, ok := f.freshHalf("sid-1"); !ok || id != op.ID || path != op.HandoffPath {
		t.Fatalf("fresh row = %q %q %v, want the half's op and file", id, path, ok)
	}
	// a re-send is the same answer
	if code, again, _ := f.report(op.ID, team.RelayReportRequest{State: team.RelayWritten}); code != http.StatusOK || again.State != team.RelayCancelled {
		t.Fatalf("re-send: %d %+v", code, again)
	}
}

// A handoff op (RT-3c) also ends cancelled / half, with no fresh row. Mutation gate: only half closes → the handoff stays written (red).
func TestWritten_ClosesAHandoffToo(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("2")
	row := team.RelayOp{ID: "op-h", Kind: team.RelayKindMember, HostID: f.m.hostID(), SessionID: "sid-m1", Ref: "_mem001", TeamID: uid(1), State: team.RelayClaimed,
		Mode: team.RelayModeHandoff, HandoffPath: "/p", CreatedAt: 1, UpdatedAt: 1}
	if err := f.m.store.CreateRelayOp(row); err != nil {
		t.Fatal(err)
	}
	code, got, ae := f.report("op-h", team.RelayReportRequest{State: team.RelayWritten})
	if code != http.StatusOK || got.State != team.RelayCancelled || got.Reason != team.RelayReasonHalf {
		t.Fatalf("written on a handoff: %d %+v %+v, want cancelled / half", code, got, ae)
	}
	if _, _, ok := f.freshHalf("sid-m1"); ok {
		t.Fatal("a handoff left a fresh-half row")
	}
}

// A full relay's `written` is as ever. Mutation gate: close every written → the full op is cancelled (red).
func TestWritten_OnAFullOpStaysWritten(t *testing.T) {
	f := newFixture(t)
	resp := f.begin("sid-1")
	if c, _ := f.decide(resp.RequestID, "approve"); c != http.StatusOK {
		t.Fatalf("approve: %d", c)
	}
	if c, _, ae := f.report(resp.Op.ID, team.RelayReportRequest{State: team.RelayWriting}); c != http.StatusOK {
		t.Fatalf("writing: %d %+v", c, ae)
	}
	code, got, _ := f.report(resp.Op.ID, team.RelayReportRequest{State: team.RelayWritten})
	if code != http.StatusOK || got.State != team.RelayWritten {
		t.Fatalf("written on a full op: %d %+v, want written", code, got)
	}
	if _, _, ok := f.freshHalf("sid-1"); ok {
		t.Fatal("a full op left a fresh-half row")
	}
}

// No "接力已取消" notice for either (C#20). Mutation gate: let the outcome notice run for cancelled / half → the lead is told (red).
func TestWritten_HalfAndHandoffSendNoCancelNotice(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("2")
	row := team.RelayOp{ID: "op-h", Kind: team.RelayKindMember, HostID: f.m.hostID(), SessionID: "sid-m1", Ref: "_mem001", TeamID: uid(1), State: team.RelayClaimed,
		Mode: team.RelayModeHandoff, HandoffPath: "/p", CreatedAt: 1, UpdatedAt: 1}
	if err := f.m.store.CreateRelayOp(row); err != nil {
		t.Fatal(err)
	}
	f.forgetSent()
	f.report("op-h", team.RelayReportRequest{State: team.RelayWritten})
	time.Sleep(100 * time.Millisecond)
	if calls := f.sender.calls(); len(calls) != 0 {
		t.Fatalf("a handoff's end told someone: %+v", calls)
	}
}
