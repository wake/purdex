package teammod

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/team"
)

func beginReq(sid string) team.RelayBeginRequest {
	return team.RelayBeginRequest{SessionID: sid, Self: true, UsedPercentage: 72.4, Window: 1_000_000}
}

func (f *fixture) begin(sid string) team.RelayBeginResponse {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/relay/begin", beginReq(sid))
	if code != http.StatusCreated {
		f.t.Fatalf("begin %s: %d %s", sid, code, body)
	}
	var out team.RelayBeginResponse
	if err := json.Unmarshal(body, &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) op(id string) team.RelayOp {
	f.t.Helper()
	op, ok, err := f.m.store.GetRelayOp(id)
	if err != nil || !ok {
		f.t.Fatalf("op %s: ok=%v err=%v", id, ok, err)
	}
	return op
}

func (f *fixture) decide(id, decision string) (int, []byte) {
	return f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide",
		team.DecideRequest{Decision: decision, Client: team.Client{Kind: "app", Label: "Purdex.app @ air26"}})
}

// Spec §8.7 (b): begin opens one op in awaiting_approval and one self_relay
// row together (10 min deadline, 30 s lease, payload with op_id and the
// usage), broadcasts opened, and makes the handoff directory. The payload's
// model_id / effort come from the DAEMON's statusline reading for the
// session (agent.ContextUsageReader), not from the request — the request
// has no such fields. Mutation gate: drop the m.usage lookup in
// handleRelayBegin → p.ModelID == "" → red.
func TestRelayBegin_OpensOpAndApprovalTogether(t *testing.T) {
	f := newFixture(t)
	f.usage.set("sid-1", "claude-opus-5-5", "high")
	out := f.begin("sid-1")
	if out.Op.ID != rid(1) || out.RequestID != rid(2) || out.Op.RequestID != rid(2) {
		t.Fatalf("ids: %+v", out)
	}
	if out.Op.State != team.RelayAwaitingApproval || out.Op.Kind != team.RelayKindSelf || out.Op.SessionID != "sid-1" || out.Op.Ref != "_abc123" ||
		out.Op.UsedPercentage == nil || *out.Op.UsedPercentage != 72.4 || out.Op.HostID != "h:1" {
		t.Fatalf("op = %+v", out.Op)
	}
	if want := filepath.Join(f.core.Cfg.DataDir, "relay", rid(1)+".md"); out.Op.HandoffPath != want {
		t.Fatalf("handoff_path = %q, want %q", out.Op.HandoffPath, want)
	}
	if st, err := os.Stat(filepath.Join(f.core.Cfg.DataDir, "relay")); err != nil || !st.IsDir() {
		t.Fatalf("relay dir: %v", err)
	}
	a, ok, err := f.m.store.Get(rid(2))
	if err != nil || !ok || a.Kind != team.KindSelfRelay || a.State != team.StateOpen || a.Origin.SessionID != "sid-1" ||
		a.DeadlineAt != 1_000_000+600_000 || a.LeaseUntil != 1_000_000+30_000 {
		t.Fatalf("approval = %+v ok=%v err=%v", a, ok, err)
	}
	var p team.SelfRelayPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil || p.OpID != rid(1) || p.UsedPercentage != 72.4 || p.Window != 1_000_000 || p.ModelID != "claude-opus-5-5" || p.Effort != "high" {
		t.Fatalf("payload = %+v err=%v", p, err)
	}
	evs := f.events()
	if len(evs) != 1 || evs[0].Op != "opened" || evs[0].Approval.ID != rid(2) || evs[0].Approval.Kind != team.KindSelfRelay {
		t.Fatalf("events = %+v", evs)
	}
	// A second begin while the first is open: 409 relay_open carrying the op.
	code, body := f.do(http.MethodPost, "/api/relay/begin", beginReq("sid-1"))
	ae := decodeErr(t, body)
	if code != http.StatusConflict || ae.Error != team.ErrRelayOpen || ae.Op == nil || ae.Op.ID != rid(1) {
		t.Fatalf("second begin: %d %+v", code, ae)
	}
	// The refusal came from the OpenRelayOpBySession check, before any id
	// was minted or an INSERT attempted: the next begin (after the op ends)
	// gets rid(3)/rid(4). Were the check dropped, the table's index would
	// still answer the same 409 (relay_ops_one_open → ErrRelayOpOpen), but
	// two ids would have been burnt on the refused call. Mutation gate for
	// "drop the OpenRelayOpBySession check".
	if _, res, err := f.m.store.ReportRelay(rid(1), RelayReport{State: team.RelayCancelled, Reason: team.RelayReasonAbandoned, At: f.clock.Load()}); err != nil || res != ReportApplied {
		t.Fatalf("end op: res=%v err=%v", res, err)
	}
	if again := f.begin("sid-1"); again.Op.ID != rid(3) || again.RequestID != rid(4) {
		t.Fatalf("a refused begin must mint no ids: next begin = %+v, want op %s request %s", again, rid(3), rid(4))
	}
	// The generic create route still refuses the kind: begin is the one door.
	code, body = f.do(http.MethodPost, "/api/team/approvals", team.CreateApprovalRequest{ID: uid(9), Kind: team.KindSelfRelay, OriginInbox: "/tmp/20.sock", Reason: "x"})
	if code != http.StatusBadRequest || decodeErr(t, body).Error != team.ErrUnsupportedKind {
		t.Fatalf("generic create self_relay: %d %s", code, body)
	}
}

// A session whose statusline never reported (or a daemon without the agent
// module, m.usage nil) gets a payload without model_id / effort — the JSON
// omits both (omitempty) and the dialog shows neither; nothing fails.
func TestRelayBegin_NoStatuslineReadingLeavesModelAndEffortEmpty(t *testing.T) {
	f := newFixture(t) // f.usage holds no reading for sid-2
	out := f.begin("sid-2")
	a, _, _ := f.m.store.Get(out.RequestID)
	if strings.Contains(string(a.Payload), "model_id") || strings.Contains(string(a.Payload), "effort") {
		t.Fatalf("payload must omit model_id/effort without a reading: %s", a.Payload)
	}
	g := newFixture(t)
	g.m.usage = nil // a daemon whose agent module is absent
	if out := g.begin("sid-1"); out.Op.SessionID != "sid-1" {
		t.Fatalf("begin without a usage reader: %+v", out)
	}
}

// Spec §8.1: the three 409 refusals and the two 4xx shapes.
func TestRelayBegin_Refusals(t *testing.T) {
	f := newFixture(t)
	check := func(sid string, body any, wantCode int, wantErr string) {
		t.Helper()
		code, raw := f.do(http.MethodPost, "/api/relay/begin", body)
		if code != wantCode || decodeErr(t, raw).Error != wantErr {
			t.Fatalf("%s: %d %s, want %d %s", sid, code, raw, wantCode, wantErr)
		}
	}
	check("unknown", beginReq("sid-99"), http.StatusNotFound, team.ErrUnknownSession)
	notSelf := beginReq("sid-1")
	notSelf.Self = false
	check("not self", notSelf, http.StatusBadRequest, team.ErrBadRequest)
	f.switches.set(hostconfig.RelaySwitches{SelfSolo: false, SelfLead: true})
	check("switch off", beginReq("sid-1"), http.StatusConflict, team.ErrSelfRelayOff)
	f.switches.set(hostconfig.DefaultRelaySwitches)
	if code, _ := f.do(http.MethodPost, "/api/relay/self", team.RelaySelfRequest{SessionID: "sid-1", Action: "off"}); code != http.StatusOK {
		t.Fatalf("self off: %d", code)
	}
	check("paused", beginReq("sid-1"), http.StatusConflict, team.ErrSelfRelayPaused)
	// Nothing opened on any refusal.
	if active, _ := f.m.store.ListActiveRelayOps(); len(active) != 0 {
		t.Fatalf("active ops after refusals = %+v", active)
	}
	if n := len(f.events()); n != 0 {
		t.Fatalf("%d events after refusals", n)
	}
}

// PR P5a-1a codex R1: the table holds "one open op per session" too
// (relay_ops_one_open). A creator that slips past the check-then-create —
// here, an op inserted in the window between OpenRelayOpBySession and
// CreateRelayOp — makes CreateRelayOp return ErrRelayOpOpen, and begin
// answers it as the same 409 relay_open carrying the op that is open, not
// a 500. Mutation gate: map ErrRelayOpOpen to 500 → red.
func TestRelayBegin_TableConflictIsRelayOpenToo(t *testing.T) {
	f := newFixture(t)
	other := team.RelayOp{
		ID: uid(7), Kind: team.RelayKindSelf, HostID: "h:1", SessionID: "sid-1", Ref: "_abc123",
		State: team.RelayAwaitingApproval, HandoffPath: "/x/" + uid(7) + ".md", CreatedAt: 1, UpdatedAt: 1,
	}
	f.m.afterOpenCheck = func(sid string) {
		if sid != "sid-1" {
			t.Fatalf("afterOpenCheck for %s", sid)
		}
		if err := f.m.store.CreateRelayOp(other); err != nil {
			t.Fatal(err)
		}
	}
	code, body := f.do(http.MethodPost, "/api/relay/begin", beginReq("sid-1"))
	ae := decodeErr(t, body)
	if code != http.StatusConflict || ae.Error != team.ErrRelayOpen || ae.Op == nil || ae.Op.ID != uid(7) {
		t.Fatalf("begin over a table conflict: %d %s", code, body)
	}
	// Nothing of this begin was left behind: no second op, no approval row, no event.
	if active, _ := f.m.store.ListActiveRelayOps(); len(active) != 1 || active[0].ID != uid(7) {
		t.Fatalf("active ops = %+v", active)
	}
	if _, ok, _ := f.m.store.Get(rid(2)); ok {
		t.Fatal("an approval row was inserted although the op was not")
	}
	if n := len(f.events()); n != 0 {
		t.Fatalf("%d events after a refused begin", n)
	}
}

// Spec §8.3 hello and §8.7 (a) the pause: hello reports role none (P4 fills
// it), the thresholds and the effective state; self off/on/status narrows
// the session only — never lifts a host switch that is off.
func TestRelayHelloAndSelf(t *testing.T) {
	f := newFixture(t)
	code, body := f.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{SessionID: "sid-1", ModVersion: "1", Agent: "cc"})
	var h team.RelayHelloResponse
	if err := json.Unmarshal(body, &h); code != http.StatusOK || err != nil || !h.OK || h.Role != "none" || h.SelfRelay != "on" || h.Threshold != 70 || h.MinGrowth != 20000 {
		t.Fatalf("hello: %d %s", code, body)
	}
	self := func(action string) team.RelaySelfResponse {
		t.Helper()
		code, body := f.do(http.MethodPost, "/api/relay/self", team.RelaySelfRequest{SessionID: "sid-1", Action: action})
		var r team.RelaySelfResponse
		if err := json.Unmarshal(body, &r); code != http.StatusOK || err != nil {
			t.Fatalf("self %s: %d %s", action, code, body)
		}
		return r
	}
	if r := self("status"); r.SelfRelay != "on" || !r.HostSwitch || r.Member {
		t.Fatalf("status = %+v", r)
	}
	if r := self("off"); r.SelfRelay != "paused" || !r.HostSwitch {
		t.Fatalf("off = %+v", r)
	}
	f.switches.set(hostconfig.RelaySwitches{SelfSolo: false, SelfLead: true})
	if r := self("on"); r.SelfRelay != "off" || r.HostSwitch {
		t.Fatalf("on under a host switch that is off = %+v (a session switch only narrows)", r)
	}
	f.switches.set(hostconfig.DefaultRelaySwitches)
	if r := self("status"); r.SelfRelay != "on" {
		t.Fatalf("after on + switch restored = %+v", r)
	}
	for _, bad := range []team.RelaySelfRequest{{SessionID: "", Action: "on"}, {SessionID: "sid-1", Action: "maybe"}} {
		if code, _ := f.do(http.MethodPost, "/api/relay/self", bad); code != http.StatusBadRequest {
			t.Fatalf("%+v: %d", bad, code)
		}
	}
	if code, _ := f.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{}); code != http.StatusBadRequest {
		t.Fatalf("hello without session_id: %d", code)
	}
}

// Decision 6: modSeen is bounded (modSeenCap, oldest evicted) and a hello
// from a session already in it is an update, not a new entry. Mutation
// gate: drop the cap → len(modSeen) == modSeenCap+1 → red.
func TestRelayHello_ModSeenIsCappedOldestFirst(t *testing.T) {
	f := newFixture(t)
	hello := func(sid string) {
		t.Helper()
		if code, body := f.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{SessionID: sid, ModVersion: "1", Agent: "cc"}); code != http.StatusOK {
			t.Fatalf("hello %s: %d %s", sid, code, body)
		}
	}
	for i := 0; i < modSeenCap; i++ {
		f.clock.Add(1) // distinct At per session: "oldest" is well-defined
		hello(fmt.Sprintf("s-%d", i))
	}
	f.clock.Add(1)
	hello("s-0") // an update of the oldest entry: it is now the newest
	f.clock.Add(1)
	hello("s-new") // one over the cap: the oldest (now s-1) goes
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	if len(f.m.modSeen) != modSeenCap {
		t.Fatalf("len(modSeen) = %d, want %d", len(f.m.modSeen), modSeenCap)
	}
	if _, ok := f.m.modSeen["s-1"]; ok {
		t.Fatal("s-1 (the oldest) must have been evicted")
	}
	for _, sid := range []string{"s-0", "s-new", "s-2"} {
		if _, ok := f.m.modSeen[sid]; !ok {
			t.Fatalf("%s must still be in modSeen", sid)
		}
	}
	if f.m.modSeen["s-new"].ModVersion != "1" || f.m.modSeen["s-new"].Agent != "cc" {
		t.Fatalf("s-new = %+v", f.m.modSeen["s-new"])
	}
}

// Spec §8.7 (b): approved → the op is claimed; denied → cancelled{denied};
// the sweeper's timeout → cancelled{timeout}; a vanished origin →
// cancelled{abandoned}. Every path goes through the same CAS and one
// closed broadcast, and /api/relay/wait is the row's long-poll.
func TestRelayApprovalCloseMovesTheOp(t *testing.T) {
	f := newFixture(t)
	// approve
	out := f.begin("sid-1")
	code, body := f.decide(out.RequestID, "approve")
	if code != http.StatusOK {
		t.Fatalf("approve: %d %s", code, body)
	}
	// A self_relay approval carries no grant: handleDecide skips the lead
	// grant branch for it (a SelfRelayPayload decodes into a LeadPayload as
	// all zeroes, so without the guard the row would carry grant
	// {max_members:0, roots:null} — a lead-shaped field on a relay row).
	if a := decodeApproval(t, body); a.State != team.StateApproved || a.Grant != nil {
		t.Fatalf("approved self_relay row = %+v (grant %+v), want approved with no grant", a, a.Grant)
	}
	if op := f.op(out.Op.ID); op.State != team.RelayClaimed {
		t.Fatalf("after approve: %+v", op)
	}
	f.events()
	// A claimed op is still "open" for the session: a new begin is relay_open until it ends.
	if code, _ := f.do(http.MethodPost, "/api/relay/begin", beginReq("sid-1")); code != http.StatusConflict {
		t.Fatalf("begin while claimed: %d", code)
	}
	// End it through the store (the report route is P5a-2b).
	if _, res, err := f.m.store.ReportRelay(out.Op.ID, RelayReport{State: team.RelayFailed, Reason: team.RelayReasonHandoffIncomplete, At: f.clock.Load()}); err != nil || res != ReportApplied {
		t.Fatalf("end op: res=%v err=%v", res, err)
	}

	// deny
	out = f.begin("sid-1")
	if code, _ := f.decide(out.RequestID, "deny"); code != http.StatusOK {
		t.Fatalf("deny: %d", code)
	}
	if op := f.op(out.Op.ID); op.State != team.RelayCancelled || op.Reason != team.RelayReasonDenied {
		t.Fatalf("after deny: %+v", op)
	}

	// timeout (sweeper): the wait route renews the lease first, as the mod does.
	out = f.begin("sid-1")
	code, body = f.do(http.MethodGet, "/api/relay/wait/"+out.RequestID+"?wait=0", nil)
	if code != http.StatusOK || decodeApproval(t, body).ID != out.RequestID {
		t.Fatalf("wait: %d %s", code, body)
	}
	f.clock.Add(600_001)
	f.m.tick()
	if a, _, _ := f.m.store.Get(out.RequestID); a.State != team.StateTimeout {
		t.Fatalf("approval after deadline: %+v", a)
	}
	if op := f.op(out.Op.ID); op.State != team.RelayCancelled || op.Reason != team.RelayReasonTimeout {
		t.Fatalf("after timeout: %+v", op)
	}

	// abandoned: the origin session is gone (10th tick liveness check).
	out = f.begin("sid-2")
	f.origins.markDead("sid-2")
	f.m.tickN = livenessEvery - 1
	f.m.tick()
	if op := f.op(out.Op.ID); op.State != team.RelayCancelled || op.Reason != team.RelayReasonAbandoned {
		t.Fatalf("after origin gone: %+v", op)
	}
	if n := f.countOps("closed"); n != 3 { // deny + timeout + abandoned since the drain after approve
		t.Fatalf("closed events = %d, want 3 (one per close, whoever closed)", n)
	}
}

// Coordinator decision: <data_dir>/relay/ is made by the daemon at Start
// and again in begin; the mod never creates it. Start on a fresh data dir
// leaves the directory in place (0700) before any begin.
func TestStart_MakesTheRelayDir(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(f.core.Cfg.DataDir, team.RelayDir)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("precondition: relay dir must not exist before Start (err=%v)", err)
	}
	if err := f.m.Start(context.Background()); err != nil { // newFixture's Cleanup stops it
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		t.Fatalf("relay dir after Start: err=%v mode=%v", err, st)
	}
	// begin after an operator removed it: re-created, the handoff path is under it.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	out := f.begin("sid-1")
	if _, err := os.Stat(dir); err != nil || filepath.Dir(out.Op.HandoffPath) != dir {
		t.Fatalf("begin must re-create the dir: err=%v path=%s", err, out.Op.HandoffPath)
	}
}

// The op and its approval row are two writes (Decision 2). When a begin
// meets an awaiting_approval op whose row is gone (a crash between the two
// writes) or already closed (afterClose missed it or failed), the op is
// re-derived from the row and the begin goes through — the session is not
// held at 409 until the next daemon boot (PR #1708 attacker A-1 / A-2).
func TestRelayBegin_ReconcilesAnAwaitingOpWhoseRowIsGoneOrClosed(t *testing.T) {
	f := newFixture(t)

	// (1) Orphan: an op with no approval row at all.
	orphan := team.RelayOp{
		ID: uid(7), Kind: team.RelayKindSelf, HostID: "h:1", SessionID: "sid-1", Ref: "_abc123", RequestID: uid(8),
		State: team.RelayAwaitingApproval, HandoffPath: "/x/" + uid(7) + ".md", CreatedAt: 1, UpdatedAt: 1,
	}
	if err := f.m.store.CreateRelayOp(orphan); err != nil {
		t.Fatal(err)
	}
	out := f.begin("sid-1") // 201, not 409
	if got := f.op(uid(7)); got.State != team.RelayCancelled || got.Reason != team.RelayReasonAbandoned {
		t.Fatalf("orphan op after begin = %s (%s), want cancelled (abandoned)", got.State, got.Reason)
	}
	if f.op(out.Op.ID).State != team.RelayAwaitingApproval {
		t.Fatalf("the new op is not open: %+v", f.op(out.Op.ID))
	}

	// (2) Row closed but the op never moved (afterClose failed): approve
	// through the API, then force the op back to awaiting_approval as a
	// failed second write would have left it.
	if code, body := f.decide(out.RequestID, "approve"); code != http.StatusOK {
		t.Fatalf("approve: %d %s", code, body)
	}
	if f.op(out.Op.ID).State != team.RelayClaimed {
		t.Fatalf("sanity: approve moved the op to %s", f.op(out.Op.ID).State)
	}
	if _, err := f.m.store.db.Exec(`UPDATE relay_ops SET state = ? WHERE id = ?`, string(team.RelayAwaitingApproval), out.Op.ID); err != nil {
		t.Fatal(err)
	}
	// The row says approved, so the op must become claimed — and claimed IS
	// an open op: this begin is 409 relay_open carrying the reconciled op.
	code, body := f.do(http.MethodPost, "/api/relay/begin", beginReq("sid-1"))
	ae := decodeErr(t, body)
	if code != http.StatusConflict || ae.Error != team.ErrRelayOpen || ae.Op == nil || ae.Op.ID != out.Op.ID {
		t.Fatalf("begin over a claimed op: %d %s", code, body)
	}
	if f.op(out.Op.ID).State != team.RelayClaimed {
		t.Fatalf("op after reconcile = %s, want claimed (the row is approved)", f.op(out.Op.ID).State)
	}

	// (3) Row denied, op stuck awaiting: reconciled to cancelled{denied}
	// and the begin goes through.
	if _, err := f.m.store.db.Exec(`UPDATE relay_ops SET state = ? WHERE id = ?`, string(team.RelayAwaitingApproval), out.Op.ID); err != nil {
		t.Fatal(err)
	}
	// The row is already closed (approved); write the denied state directly
	// to model "closed as denied, op never moved".
	if _, err := f.m.store.db.Exec(`UPDATE approval_requests SET state = ? WHERE id = ?`, string(team.StateDenied), out.RequestID); err != nil {
		t.Fatal(err)
	}
	third := f.begin("sid-1")
	if got := f.op(out.Op.ID); got.State != team.RelayCancelled || got.Reason != team.RelayReasonDenied {
		t.Fatalf("op after a denied row = %s (%s), want cancelled (denied)", got.State, got.Reason)
	}
	if third.Op.ID == out.Op.ID {
		t.Fatal("begin did not open a new op")
	}
}
