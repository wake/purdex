package teammod

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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
	f.switches.set(hostconfig.RelaySwitches{SelfSolo: false, SelfLead: true, MemberAuto: true})
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

// A handoff op (RT-3c) also ends cancelled / half and keeps its file as the session's fresh half (a kill that does not complete is asked
// again on the same file). Mutation gate: only half closes → the handoff stays written (red).
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
	if id, path, ok := f.freshHalf("sid-m1"); !ok || id != "op-h" || path != "/p" {
		t.Fatalf("fresh row = %q %q %v, want the handoff's op and file", id, path, ok)
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
	// the only word to the lead is the handoff's own (the kill here cannot complete: no tmux for this member), never "接力已取消"
	for _, c := range f.sender.calls() {
		if strings.Contains(c.Text, "接力") && strings.Contains(c.Text, "取消") {
			t.Fatalf("a handoff's end told the lead it was cancelled: %+v", c)
		}
	}
}

// ---- T31: reuse and staleness ----

// freshHalfWithFile makes sid's idle half, written, with its file on disk.
func (f *fixture) freshHalfWithFile(sid string) team.RelayOp {
	f.t.Helper()
	op := f.halfOp(sid)
	if err := os.MkdirAll(filepath.Dir(op.HandoffPath), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(op.HandoffPath, []byte("# HANDOFF\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
	if c, got, _ := f.report(op.ID, team.RelayReportRequest{State: team.RelayWritten}); c != http.StatusOK || got.State != team.RelayCancelled {
		f.t.Fatalf("written: %d %+v", c, got)
	}
	return op
}

func reuseBeginReq(sid, op string) team.RelayBeginRequest {
	r := beginReq(sid)
	r.ReuseOp = op
	return r
}

// A valid reuse opens the card with the half's file and keeps the fresh row until that op claims. Mutation gates: ignore ReuseOp → the new
// op has its own path (red); delete the row at begin → it is gone before the claim (red); never delete at claim → it stays (red).
func TestReuse_ValidCarriesTheFileAndTheRowGoesAtTheClaim(t *testing.T) {
	f := newFixture(t)
	half := f.freshHalfWithFile("sid-1")
	code, resp, body := f.beginRaw2(reuseBeginReq("sid-1", half.ID))
	if code != http.StatusCreated || resp.Op.HandoffPath != half.HandoffPath || resp.Op.ID == half.ID {
		t.Fatalf("reuse begin: %d %s, want a new op with path %s", code, body, half.HandoffPath)
	}
	if _, _, ok := f.freshHalf("sid-1"); !ok {
		t.Fatal("the fresh row went at begin, before the op claimed")
	}
	if c, _ := f.decide(resp.RequestID, "approve"); c != http.StatusOK {
		t.Fatalf("approve: %d", c)
	}
	if _, _, ok := f.freshHalf("sid-1"); ok {
		t.Fatal("the fresh row is still there after the reusing op claimed")
	}
}

// Everything else is 409 reuse_invalid, nothing written. Mutation gates: drop each check → its case begins (red).
func TestReuse_InvalidIs409(t *testing.T) {
	f := newFixture(t)
	half := f.freshHalfWithFile("sid-1")
	for name, op := range map[string]string{"an unknown op": "no-such-op", "an empty id that is named": "x"} {
		if code, _, body := f.beginRaw2(reuseBeginReq("sid-1", op)); code != http.StatusConflict || !strings.Contains(string(body), team.ErrReuseInvalid) {
			t.Fatalf("%s: %d %s, want 409 reuse_invalid", name, code, body)
		}
	}
	// another session's half
	f2 := newFixture(t)
	other := f2.freshHalfWithFile("sid-1")
	f2.liveMember("sid-2", "_two002", "two", "self/two", "tm-2")
	if code, _, body := f2.beginRaw2(reuseBeginReq("sid-2", other.ID)); code != http.StatusConflict || !strings.Contains(string(body), team.ErrReuseInvalid) {
		t.Fatalf("another session's op: %d %s", code, body)
	}
	// the file is gone / empty
	if err := os.Truncate(half.HandoffPath, 0); err != nil {
		t.Fatal(err)
	}
	if code, _, body := f.beginRaw2(reuseBeginReq("sid-1", half.ID)); code != http.StatusConflict || !strings.Contains(string(body), team.ErrReuseInvalid) {
		t.Fatalf("an empty file: %d %s", code, body)
	}
	if err := os.Remove(half.HandoffPath); err != nil {
		t.Fatal(err)
	}
	if code, _, body := f.beginRaw2(reuseBeginReq("sid-1", half.ID)); code != http.StatusConflict || !strings.Contains(string(body), team.ErrReuseInvalid) {
		t.Fatalf("a missing file: %d %s", code, body)
	}
	if n := countRelayOps(t, f); n != 1 {
		t.Fatalf("%d relay ops, want only the half: a refused reuse writes nothing", n)
	}
	// a non-half op and no fresh row
	f3 := newFixture(t)
	full := f3.begin("sid-1")
	if code, _, body := f3.beginRaw2(reuseBeginReq("sid-1", full.Op.ID)); code != http.StatusConflict {
		t.Fatalf("a full op (and a relay already open): %d %s", code, body)
	}
}

// Each staleness path deletes the row. Mutation gates: drop each deletion → its case is red.
func TestFresh_EveryStalenessPathDeletesTheRow(t *testing.T) {
	post := func(f *fixture, sid, op string) {
		if code, body := f.do(http.MethodPost, "/api/relay/fresh", team.RelayFreshRequest{SessionID: sid, OpID: op, Stale: true}); code != http.StatusOK {
			t.Fatalf("fresh: %d %s", code, body)
		}
	}
	cases := map[string]func(f *fixture, half team.RelayOp){
		"the mod reports it stale": func(f *fixture, h team.RelayOp) { post(f, "sid-1", h.ID) },
		"a compaction is reported": func(f *fixture, h team.RelayOp) { f.compacted("sid-1", "auto") },
		"a manual compaction too":  func(f *fixture, h team.RelayOp) { f.compacted("sid-1", "manual") },
		"the session is gone":      func(f *fixture, h team.RelayOp) { f.origins.markDead("sid-1"); f.livenessTick() },
		"a relay claims without it": func(f *fixture, h team.RelayOp) {
			r := f.begin("sid-1")
			if c, _ := f.decide(r.RequestID, "approve"); c != http.StatusOK {
				t.Fatalf("approve: %d", c)
			}
		},
	}
	for name, stale := range cases {
		f := newFixture(t)
		half := f.freshHalfWithFile("sid-1")
		if _, _, ok := f.freshHalf("sid-1"); !ok {
			t.Fatalf("%s: setup: no fresh row", name)
		}
		stale(f, half)
		if _, _, ok := f.freshHalf("sid-1"); ok {
			t.Errorf("%s: the fresh row is still there", name)
		}
	}
	// a stale report for another op leaves it
	f := newFixture(t)
	f.freshHalfWithFile("sid-1")
	post(f, "sid-1", "another-op")
	if _, _, ok := f.freshHalf("sid-1"); !ok {
		t.Fatal("a stale report naming another op deleted the row")
	}
}

// Hello carries the fresh half. Mutation gate: drop it → red.
func TestHello_CarriesTheFreshHalf(t *testing.T) {
	f := newFixture(t)
	half := f.freshHalfWithFile("sid-1")
	code, body := f.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{SessionID: "sid-1", ModVersion: "8", Agent: "cc"})
	var h team.RelayHelloResponse
	if err := json.Unmarshal(body, &h); code != http.StatusOK || err != nil || h.FreshHalf == nil || h.FreshHalf.OpID != half.ID || h.FreshHalf.Path != half.HandoffPath || h.FreshHalf.WrittenAt == 0 {
		t.Fatalf("hello: %d %s", code, body)
	}
	f2 := newFixture(t)
	code, body = f2.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{SessionID: "sid-1", ModVersion: "8", Agent: "cc"})
	if err := json.Unmarshal(body, &h); code != http.StatusOK || err != nil {
		t.Fatalf("hello: %d %s", code, body)
	}
}

// The unattended begin (approved at once) carries the reuse too. Mutation gate: build the unattended op without the path → red.
func TestReuse_UnattendedBeginCarriesTheFile(t *testing.T) {
	f := newFixture(t)
	half := f.freshHalfWithFile("sid-1")
	f.unatt.set(true)
	code, resp, body := f.beginRaw2(reuseBeginReq("sid-1", half.ID))
	if code != http.StatusCreated || resp.Op.State != team.RelayClaimed || resp.Op.HandoffPath != half.HandoffPath {
		t.Fatalf("unattended reuse: %d %s", code, body)
	}
}

// Only the CURRENT fresh half can be reused: an older half op of the session, though cancelled / half with its file on disk, is not.
// Mutation gate: drop the `h.OpID != opID` check → the older op's file is reused (red).
func TestReuse_AnOlderHalfIsNotTheFreshOne(t *testing.T) {
	f := newFixture(t)
	older := f.freshHalfWithFile("sid-1")
	newer := f.freshHalfWithFile("sid-1") // a second half replaces the fresh row
	if id, _, ok := f.freshHalf("sid-1"); !ok || id != newer.ID {
		t.Fatalf("setup: fresh row names %q, want the newer half %q", id, newer.ID)
	}
	if code, _, body := f.beginRaw2(reuseBeginReq("sid-1", older.ID)); code != http.StatusConflict || !strings.Contains(string(body), team.ErrReuseInvalid) {
		t.Fatalf("an older half: %d %s, want 409 reuse_invalid", code, body)
	}
	if code, _, body := f.beginRaw2(reuseBeginReq("sid-1", newer.ID)); code != http.StatusCreated {
		t.Fatalf("the current half: %d %s", code, body)
	}
}

// ---- codex: replay identity, half + reask, fresh vs begin ----

// The same request_id with another half / reuse_op is a conflicting reuse, not a replay. Mutation gates: drop the half comparison → the
// first case replays (red); drop the reuse comparison → the second and third replay (red).
func TestBeginReplay_HalfAndReuseAreTheRequestsIdentity(t *testing.T) {
	f := newFixture(t)
	rid1 := "11111111-1111-4111-8111-111111111111"
	r := halfBeginReq("sid-1", true)
	r.RequestID = rid1
	if code, _, body := f.beginRaw2(r); code != http.StatusCreated {
		t.Fatalf("half: %d %s", code, body)
	}
	if code, _, body := f.beginRaw2(r); code != http.StatusCreated { // the same request replays
		t.Fatalf("replay of the half: %d %s", code, body)
	}
	same := halfBeginReq("sid-1", false) // same id, now an ordinary idle begin
	same.RequestID = rid1
	if code, _, body := f.beginRaw2(same); code != http.StatusConflict {
		t.Fatalf("a half replayed as an ordinary begin: %d %s, want 409", code, body)
	}
	// reuse
	f2 := newFixture(t)
	half := f2.freshHalfWithFile("sid-1")
	rid2 := "22222222-2222-4222-8222-222222222222"
	a := reuseBeginReq("sid-1", half.ID)
	a.RequestID = rid2
	if code, _, body := f2.beginRaw2(a); code != http.StatusCreated {
		t.Fatalf("reuse begin: %d %s", code, body)
	}
	if code, _, body := f2.beginRaw2(a); code != http.StatusCreated {
		t.Fatalf("replay of the reuse begin: %d %s", code, body)
	}
	plain := beginReq("sid-1") // same id, no reuse
	plain.RequestID = rid2
	if code, _, body := f2.beginRaw2(plain); code != http.StatusConflict {
		t.Fatalf("a reuse replayed without it: %d %s, want 409", code, body)
	}
	other := reuseBeginReq("sid-1", "another-op") // same id, another reuse_op
	other.RequestID = rid2
	if code, _, body := f2.beginRaw2(other); code != http.StatusConflict {
		t.Fatalf("a reuse replayed with another op: %d %s, want 409", code, body)
	}
}

// A half is never a card: with a reask token it is refused. Mutation gate: drop the check → the reask branch opens a card (red).
func TestHalfBegin_RefusesAReaskToken(t *testing.T) {
	f := newFixture(t)
	r := halfBeginReq("sid-1", true)
	r.ReaskID = "33333333-3333-4333-8333-333333333333"
	if code, _, body := f.beginRaw2(r); code != http.StatusBadRequest {
		t.Fatalf("half + reask_id: %d %s, want 400", code, body)
	}
}

// codex critic: a stale report while a reuse card waits cancels that card, so approving it cannot claim an op that carries the stale file.
// Mutation gate: only delete the row → the card is still open and the approve claims the op (red).
func TestFresh_AStaleReportCancelsTheWaitingReuseCard(t *testing.T) {
	f := newFixture(t)
	half := f.freshHalfWithFile("sid-1")
	code, resp, body := f.beginRaw2(reuseBeginReq("sid-1", half.ID))
	if code != http.StatusCreated {
		t.Fatalf("reuse begin: %d %s", code, body)
	}
	if c, b := f.do(http.MethodPost, "/api/relay/fresh", team.RelayFreshRequest{SessionID: "sid-1", OpID: half.ID, Stale: true}); c != http.StatusOK {
		t.Fatalf("fresh: %d %s", c, b)
	}
	if st := f.rowState(resp.RequestID); st != team.StateCancelled {
		t.Fatalf("the reuse card is %s after the stale report, want cancelled", st)
	}
	if c, _ := f.decide(resp.RequestID, "approve"); c == http.StatusOK {
		t.Fatal("a cancelled reuse card was approved")
	}
	if op, _, _ := f.m.store.GetRelayOp(resp.Op.ID); op.State == team.RelayClaimed {
		t.Fatalf("the reuse op was claimed: %+v", op)
	}
	// a stale report naming another op leaves a reuse card alone
	f2 := newFixture(t)
	h2 := f2.freshHalfWithFile("sid-1")
	_, r2, _ := f2.beginRaw2(reuseBeginReq("sid-1", h2.ID))
	f2.do(http.MethodPost, "/api/relay/fresh", team.RelayFreshRequest{SessionID: "sid-1", OpID: "another", Stale: true})
	if st := f2.rowState(r2.RequestID); st != team.StateOpen {
		t.Fatalf("a stale report for another op closed the card: %s", st)
	}
}

// codex critic: the stale report and an approval race (approve takes no createMu). With the fresh row already gone, the approve's own
// transaction refuses: the op is never claimed with the stale file, whatever the order of the stale handler's card cancel.
// Mutation gate: drop the in-transaction check → the approve claims the op (red).
func TestReuseCard_ApproveRefusesWhenTheFreshHalfIsGone(t *testing.T) {
	f := newFixture(t)
	half := f.freshHalfWithFile("sid-1")
	_, resp, _ := f.beginRaw2(reuseBeginReq("sid-1", half.ID))
	if err := f.m.store.DeleteFreshHalf("sid-1", half.ID, f.m.now()); err != nil { // the stale report's first half; its card cancel has not run yet
		t.Fatal(err)
	}
	code, body := f.decide(resp.RequestID, "approve")
	if code != http.StatusConflict || !strings.Contains(string(body), team.ErrReuseInvalid) {
		t.Fatalf("approve: %d %s, want 409 reuse_invalid", code, body)
	}
	if st := f.rowState(resp.RequestID); st != team.StateOpen {
		t.Fatalf("the refused approve left the card %s, want open (the stale handler cancels it)", st)
	}
	if op, _, _ := f.m.store.GetRelayOp(resp.Op.ID); op.State == team.RelayClaimed {
		t.Fatalf("the op was claimed with a stale file: %+v", op)
	}
	// an ordinary card is unaffected
	f2 := newFixture(t)
	plain := f2.begin("sid-1")
	if c, _ := f2.decide(plain.RequestID, "approve"); c != http.StatusOK {
		t.Fatalf("an ordinary approve: %d", c)
	}
}
