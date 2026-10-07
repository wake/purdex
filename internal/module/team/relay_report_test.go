package teammod

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/team"
)

func (f *fixture) report(id string, req team.RelayReportRequest) (int, team.RelayOp, team.APIError) {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/relay/ops/"+id+"/report", req)
	var op team.RelayOp
	var ae team.APIError
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &op); err != nil {
			f.t.Fatal(err)
		}
	} else {
		ae = decodeErr(f.t, body)
	}
	return code, op, ae
}

func decodeRelayOp(t *testing.T, body []byte) team.RelayOp {
	t.Helper()
	var op team.RelayOp
	if err := json.Unmarshal(body, &op); err != nil {
		t.Fatalf("decode op: %v; body=%s", err, body)
	}
	return op
}

// Spec §8.3 reports and §8.4 lineage: the forward path from the mod, the
// lineage row and the title move at cleared, new_ref derived from the new
// session id, idempotent re-sends, 409 bad_transition carrying the op,
// and relays_active in inflight.
func TestRelayReport_ForwardPathLineageAndTitle(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1")
	f.decide(out.RequestID, "approve")
	id := out.Op.ID
	inflight := func() team.InflightResponse {
		t.Helper()
		_, body := f.do(http.MethodGet, "/api/team/inflight", nil)
		var r team.InflightResponse
		_ = json.Unmarshal(body, &r)
		return r
	}
	if r := inflight(); r.RelaysActive != 1 || r.ApprovalsOpen != 0 {
		t.Fatalf("inflight = %+v", r)
	}
	for _, st := range []team.RelayState{team.RelayWriting, team.RelayWritten} {
		if code, op, ae := f.report(id, team.RelayReportRequest{State: st}); code != http.StatusOK || op.State != st {
			t.Fatalf("report %s: %d %+v %+v", st, code, op, ae)
		}
	}
	// Re-sending the same state is 200 with the same row.
	if code, op, _ := f.report(id, team.RelayReportRequest{State: team.RelayWritten}); code != http.StatusOK || op.State != team.RelayWritten {
		t.Fatalf("written twice: %d %+v", code, op)
	}
	// cleared needs new_session_id — refused by the handler itself (its
	// detail), before any store transaction. The store's checkLineage
	// would refuse an empty id too (ErrBadRelayReport → 400, PR P5a-1a
	// codex R2), so the detail is what tells the two apart: mutation gate
	// "drop the handler's new_session_id requirement" → red here.
	if code, _, ae := f.report(id, team.RelayReportRequest{State: team.RelayCleared}); code != http.StatusBadRequest || ae.Error != team.ErrBadRequest || !strings.Contains(ae.Detail, "new_session_id is required") {
		t.Fatalf("cleared without new_session_id: %d %+v", code, ae)
	}
	code, op, _ := f.report(id, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-1b"})
	if code != http.StatusOK || op.State != team.RelayCleared || op.NewSessionID != "sid-1b" || op.NewRef != ipeers.RefID("sid-1b") {
		t.Fatalf("cleared: %d %+v", code, op)
	}
	refs, err := f.m.store.PreviousRefs()
	if err != nil || len(refs["sid-1b"]) != 1 || refs["sid-1b"][0] != "_abc123" {
		t.Fatalf("lineage = %v err=%v", refs, err)
	}
	if len(f.titles.moves) != 1 || f.titles.moves[0] != [2]string{"sid-1", "sid-1b"} {
		t.Fatalf("title moves = %v", f.titles.moves)
	}
	// A stale earlier state: 409 bad_transition with the op as it is.
	code, _, ae := f.report(id, team.RelayReportRequest{State: team.RelayWriting})
	if code != http.StatusConflict || ae.Error != team.ErrBadTransition || ae.Op == nil || ae.Op.State != team.RelayCleared {
		t.Fatalf("stale report: %d %+v", code, ae)
	}
	if code, op, _ := f.report(id, team.RelayReportRequest{State: team.RelayDone}); code != http.StatusOK || op.State != team.RelayDone {
		t.Fatalf("done: %d %+v", code, op)
	}
	if r := inflight(); r.RelaysActive != 0 {
		t.Fatalf("inflight after done = %+v", r)
	}
	// failed/cancelled need a reason; unknown state and unknown op are 400/404.
	if code, _, _ := f.report(id, team.RelayReportRequest{State: team.RelayFailed}); code != http.StatusBadRequest {
		t.Fatalf("failed without error: %d", code)
	}
	if code, _, _ := f.report(id, team.RelayReportRequest{State: "flying"}); code != http.StatusBadRequest {
		t.Fatalf("unknown state: %d", code)
	}
	if code, _, _ := f.report("nope", team.RelayReportRequest{State: team.RelayDone}); code != http.StatusNotFound {
		t.Fatalf("unknown op: %d", code)
	}
	code, body := f.do(http.MethodGet, "/api/relay/ops/"+id, nil)
	if code != http.StatusOK || decodeRelayOp(t, body).State != team.RelayDone {
		t.Fatalf("get op: %d %s", code, body)
	}
}

// PR P5a-1a codex R2: a `cleared` that would corrupt the lineage (here:
// the new session is the old one; also a session already heading another
// op's lineage, or a cycle) is refused by the store with ErrBadRelayReport
// and must surface as 400 bad_request carrying the store's reason — not as
// a 500 storage_error — with the op left as it was. Mutation gate: map
// ErrBadRelayReport to 500 → red.
func TestRelayReport_CorruptLineageIs400NotStorageError(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1")
	f.decide(out.RequestID, "approve")
	code, _, ae := f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-1"})
	if code != http.StatusBadRequest || ae.Error != team.ErrBadRequest || !strings.Contains(ae.Detail, "new session equals the old one") {
		t.Fatalf("cleared into itself: %d %+v, want 400 bad_request with the store's reason", code, ae)
	}
	if op := f.op(out.Op.ID); op.State != team.RelayClaimed || op.NewSessionID != "" {
		t.Fatalf("op after a refused cleared = %+v, want untouched (claimed)", op)
	}
	if len(f.titles.moves) != 0 {
		t.Fatalf("a refused cleared must move no title: %v", f.titles.moves)
	}
	// A second op clearing into a session that already heads a lineage row
	// — within the same process (pid 10), so the handler's process check
	// passes and it is the store's lineage guard that answers.
	if code, _, _ := f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-1c"}); code != http.StatusOK {
		t.Fatalf("cleared #1: %d", code)
	}
	o2 := f.begin("sid-1b") // another live session of pid 10
	f.decide(o2.RequestID, "approve")
	code, _, ae = f.report(o2.Op.ID, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-1c"})
	if code != http.StatusBadRequest || ae.Error != team.ErrBadRequest || !strings.Contains(ae.Detail, "already heads the lineage") {
		t.Fatalf("cleared into a taken head: %d %+v", code, ae)
	}
}

// Spec §8.7 (c): the mod reports `cancelled --error compacted` while the
// request is still open (auto-compact mid-wait). The report moves the op
// AND closes the approval row through the same CAS as a cancel, so every
// client's dialog goes away now (one `closed` event), `pdx relay wait`
// exits 12, and afterClose — which runs on that close — is a no-op on the
// already-cancelled op (no bad_transition logged). A report on an op whose
// row is already closed closes nothing more.
func TestRelayReport_CancelledClosesTheOpenApprovalOnce(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1")
	f.events()
	code, op, _ := f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayCancelled, Error: team.RelayReasonCompacted})
	if code != http.StatusOK || op.State != team.RelayCancelled || op.Reason != team.RelayReasonCompacted {
		t.Fatalf("report cancelled: %d %+v", code, op)
	}
	a, _, _ := f.m.store.Get(out.RequestID)
	if a.State != team.StateCancelled || a.DecidedBy != nil {
		t.Fatalf("approval after compacted = %+v, want cancelled with no decided_by", a)
	}
	closed := 0
	for _, ev := range f.events() {
		if ev.Op == "closed" && ev.Approval.ID == out.RequestID {
			closed++
		}
	}
	if closed != 1 {
		t.Fatalf("closed events = %d, want exactly 1", closed)
	}
	// The op keeps the report's reason: afterClose's cancelled{abandoned}
	// must not overwrite compacted (ReportRelay is a no-op on the same state).
	if again, _, _ := f.m.store.RelayOpByRequest(out.RequestID); again.Reason != team.RelayReasonCompacted {
		t.Fatalf("reason after afterClose = %q, want compacted", again.Reason)
	}
	// Idempotent re-send: 200, nothing closes again.
	if code, _, _ := f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayCancelled, Error: team.RelayReasonCompacted}); code != http.StatusOK {
		t.Fatalf("re-send: %d", code)
	}
	if evs := f.events(); len(evs) != 0 {
		t.Fatalf("re-send must broadcast nothing, got %+v", evs)
	}
	// failed on a claimed op (approval already closed by the approve): the op moves, no second close.
	o2 := f.begin("sid-2")
	f.decide(o2.RequestID, "approve")
	f.events()
	if code, op, _ := f.report(o2.Op.ID, team.RelayReportRequest{State: team.RelayFailed, Error: team.RelayReasonHandoffIncomplete}); code != http.StatusOK || op.State != team.RelayFailed {
		t.Fatalf("failed: %d %+v", code, op)
	}
	if evs := f.events(); len(evs) != 0 {
		t.Fatalf("a closed row is not closed again: %+v", evs)
	}
}

// Spec §9.3 (P5a's share): at boot, an awaiting_approval op whose row
// already closed takes the verdict; one whose session is gone is abandoned
// through the row (one closed broadcast); a cleared op re-runs the title
// move, which is a no-op when it already happened.
func TestStart_ReconcilesSelfRelayOps(t *testing.T) {
	f := newFixture(t)
	// (1) approved while the daemon was down: close the row directly, bypassing afterClose.
	o1 := f.begin("sid-1")
	if _, won, err := f.m.store.CloseIfOpen(o1.RequestID, Close{State: team.StateApproved, DecidedAt: f.clock.Load()}); err != nil || !won {
		t.Fatal(err)
	}
	// (2) still open, but its session is gone.
	o2 := f.begin("sid-2")
	f.origins.markDead("sid-2")
	f.events()

	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if op := f.op(o1.Op.ID); op.State != team.RelayClaimed {
		t.Fatalf("(1) after boot: %+v", op)
	}
	if op := f.op(o2.Op.ID); op.State != team.RelayCancelled || op.Reason != team.RelayReasonAbandoned {
		t.Fatalf("(2) after boot: %+v", op)
	}
	if a, _, _ := f.m.store.Get(o2.RequestID); a.State != team.StateAbandoned {
		t.Fatalf("(2) approval after boot: %+v", a)
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events at boot = %d, want 1 (the abandoned row)", n)
	}
	// (3): clear o1 now, then a second reconcile must not move the title twice.
	f.report(o1.Op.ID, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-1c"})
	before := len(f.titles.moves)
	f.m.reconcileRelays()
	if len(f.titles.moves) != before {
		t.Fatalf("title moved again at reconcile: %v", f.titles.moves)
	}
}

// reboot is "the daemon restarted": the first Module is stopped and closed,
// and a SECOND Module is built over the same core (same data dir ⇒ same
// team.db) with the given title mover (the same meta.db), its routes on a
// fresh mux, Start run (boot reconciliation included).
func (f *fixture) reboot(titles TitleMover) *fixture {
	f.t.Helper()
	_ = f.m.Stop(context.Background())
	_ = f.m.Close()
	g := &fixture{t: f.t, core: f.core, origins: f.origins, switches: f.switches, usage: f.usage}
	g.clock.Store(f.clock.Load())
	g.m = New().WithTitles(titles)
	g.m.logf = func(string, ...any) {}
	g.m.now = func() int64 { return g.clock.Load() }
	g.m.newID = sequentialIDs()
	g.m.clearedWait, g.m.clearedPoll = 200*time.Millisecond, 10*time.Millisecond
	if err := g.m.Init(f.core); err != nil {
		f.t.Fatal(err)
	}
	g.mux = http.NewServeMux()
	g.m.RegisterRoutes(g.mux)
	g.sub = f.core.Events.AddTestSubscriber()
	if err := g.m.Start(context.Background()); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() {
		f.core.Events.RemoveTestSubscriber(g.sub)
		_ = g.m.Stop(context.Background())
		_ = g.m.Close()
	})
	return g
}

// Review Focus 3 across the layers (codex round): the mod reports cleared,
// the daemon restarts, and the mod re-sends the same cleared (P5b-2 re-sends
// a failed report at the next turn.complete). Over a REAL meta.db
// (store.OpenMeta + PeerLabels, the production TitleMover) and the same
// team.db: exactly one lineage row, the title moved once — the new session
// id holds it, the old row is released (label "", kept: P5a-1a's Move) —
// and the old ref resolves to the new row through LineageReader → Build →
// Resolve. Mutation gates: drop the no-op branch of ReportRelay's cleared
// transition (apply it again) → two lineage rows → red; make Move not NULL
// the old row → two rows with the label → red.
func TestRelayReport_ClearedAcrossRestartIsIdempotentEndToEnd(t *testing.T) {
	ms, err := store.OpenMeta(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	labels := ms.PeerLabels()
	if _, err := labels.Claim("sid-1", "purdex-tester", time.UnixMilli(1000)); err != nil {
		t.Fatal(err)
	}

	f := newFixture(t)
	f.m.titles = labels // the real mover for this test, over the fake-titles default
	out := f.begin("sid-1")
	f.decide(out.RequestID, "approve")
	id := out.Op.ID
	for _, st := range []team.RelayState{team.RelayWriting, team.RelayWritten} {
		f.report(id, team.RelayReportRequest{State: st})
	}
	if code, op, ae := f.report(id, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-1b"}); code != http.StatusOK || op.State != team.RelayCleared {
		t.Fatalf("cleared #1: %d %+v %+v", code, op, ae)
	}

	g := f.reboot(labels) // the daemon came back; Start ran reconcileRelays over the op still in cleared
	if code, op, ae := g.report(id, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-1b"}); code != http.StatusOK || op.State != team.RelayCleared || op.NewSessionID != "sid-1b" {
		t.Fatalf("cleared #2 after restart: %d %+v %+v", code, op, ae)
	}

	// One lineage row.
	refs, err := g.m.store.PreviousRefs()
	if err != nil || len(refs) != 1 || len(refs["sid-1b"]) != 1 || refs["sid-1b"][0] != "_abc123" {
		t.Fatalf("lineage after restart = %v err=%v (want one row sid-1b ← _abc123)", refs, err)
	}
	var n int
	if err := g.m.store.db.QueryRow(`SELECT COUNT(*) FROM session_lineage`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("session_lineage rows = %d err=%v, want 1", n, err)
	}
	// The title moved once: the new id holds it, the old row is released.
	rows, err := labels.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]store.PeerLabel{}
	for _, r := range rows {
		byID[r.SessionID] = r
	}
	if len(byID) != 2 || byID["sid-1b"].Label != "purdex-tester" || byID["sid-1b"].Rev != 2 || byID["sid-1"].Label != "" {
		t.Fatalf("peer_labels after restart = %+v (want the label on sid-1b at rev 2 once, sid-1 released)", byID)
	}
	// The old ref resolves to the new row through the whole path.
	records := ipeers.Build(ipeers.BuildInput{
		HostID: "h:1", Alias: "mlab",
		Entries:      []ipeers.Entry{{PID: 4242, SessionID: "sid-1b", Name: "purdex-tester", Inbox: "/s/4242", ProcStart: "Sun Sep 13 15:22:36 2026"}},
		PreviousRefs: refs,
	})
	rec, err := ipeers.Resolve(records, "_abc123", ipeers.ResolveSnapshot{})
	if err != nil || rec.Agent == nil || rec.Agent.SessionID != "sid-1b" {
		t.Fatalf("Resolve(old ref) after restart = %+v err=%v; want the sid-1b row", rec, err)
	}
}

// PR #1716 codex R1: `claimed` is not a reportable state. Reporting it on
// an awaiting_approval self op would step past the person's approval
// (the store's awaiting_approval → claimed transition exists for
// afterClose); the handler refuses it with 400 and the op stays awaiting
// with its row open.
func TestRelayReport_ClaimedIsNotReportable(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1")
	code, _, ae := f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayClaimed})
	if code != http.StatusBadRequest || ae.Error != team.ErrBadRequest {
		t.Fatalf("report claimed: %d %+v", code, ae)
	}
	if got := f.op(out.Op.ID); got.State != team.RelayAwaitingApproval {
		t.Fatalf("op after a refused claimed report = %s, want awaiting_approval", got.State)
	}
	row, ok, _ := f.m.store.Get(out.RequestID)
	if !ok || row.State != team.StateOpen {
		t.Fatalf("approval row = %+v ok=%v, want still open", row, ok)
	}
	// The approval still claims it the legitimate way.
	f.decide(out.RequestID, "approve")
	if got := f.op(out.Op.ID); got.State != team.RelayClaimed {
		t.Fatalf("op after approve = %s, want claimed", got.State)
	}
}

// PR #1716 attacker A-1: a cleared whose new_session_id is live under
// another process is a hijack of that conversation's title and ref — 400,
// op unchanged, no lineage, no title move. A new id the registry does not
// know yet (the normal case at SessionStart) is still accepted.
func TestRelayReport_ClearedRefusesAnotherLiveConversation(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1") // origin pid 10
	f.decide(out.RequestID, "approve")
	f.titles.has["sid-2"] = true // sid-2 is live under pid 20 and has a title
	code, _, ae := f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-2"})
	if code != http.StatusBadRequest || ae.Error != team.ErrBadRequest || !strings.Contains(ae.Detail, "another process") {
		t.Fatalf("cleared into another live conversation: %d %+v", code, ae)
	}
	if got := f.op(out.Op.ID); got.State != team.RelayClaimed || got.NewSessionID != "" {
		t.Fatalf("op after the refused cleared = %+v", got)
	}
	if refs, _ := f.m.store.PreviousRefs(); len(refs) != 0 {
		t.Fatalf("lineage written: %v", refs)
	}
	if !f.titles.has["sid-2"] || !f.titles.has["sid-1"] || len(f.titles.moves) != 0 {
		t.Fatalf("title touched: has=%v moves=%v", f.titles.has, f.titles.moves)
	}
	// A new id the registry does not show (yet): 503 not_ready after the
	// bounded wait, nothing written — the mod retries at its next turn.
	code, _, ae = f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-unregistered"})
	if code != http.StatusServiceUnavailable || ae.Error != team.ErrNotReady {
		t.Fatalf("cleared into an unregistered id: %d %+v", code, ae)
	}
	if got := f.op(out.Op.ID); got.State != team.RelayClaimed || got.NewSessionID != "" {
		t.Fatalf("op after the not-ready cleared = %+v", got)
	}
	// The registry catches up while the handler waits: accepted.
	go func() {
		time.Sleep(50 * time.Millisecond)
		f.origins.mu.Lock()
		f.origins.cleared["sid-late"] = 10
		f.origins.mu.Unlock()
	}()
	if code, op, _ := f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-late"}); code != http.StatusOK || op.NewSessionID != "sid-late" {
		t.Fatalf("cleared into an id the registry showed during the wait: %d %+v", code, op)
	}
}

// PR #1716 critic on A-4: a terminal report on an op still awaiting
// approval closes the row FIRST through the same CAS an approve uses, so
// the op is never cancelled underneath an approve that then succeeds.
// (1) The report wins: row cancelled, op cancelled{compacted} (the report's
// reason, not the mapping's "abandoned"), one closed event. (2) An approve
// slips in just before the report's close: the row stays approved, the op
// goes claimed → cancelled{compacted} — approved first, given up after.
func TestRelayReport_TerminalReportClosesTheRowFirst(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1")
	_ = f.events()
	code, op, _ := f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayCancelled, Error: team.RelayReasonCompacted})
	if code != http.StatusOK || op.State != team.RelayCancelled || op.Reason != team.RelayReasonCompacted {
		t.Fatalf("(1) report: %d %+v", code, op)
	}
	if row, _, _ := f.m.store.Get(out.RequestID); row.State != team.StateCancelled {
		t.Fatalf("(1) row = %s, want cancelled", row.State)
	}
	closed := 0
	for _, ev := range f.events() {
		if ev.Op == "closed" {
			closed++
		}
	}
	if closed != 1 {
		t.Fatalf("(1) closed events = %d, want 1", closed)
	}

	out2 := f.begin("sid-1")
	f.m.beforeTerminalClose = func(id string) {
		if id != out2.Op.ID {
			t.Fatalf("seam for %s", id)
		}
		if code, body := f.decide(out2.RequestID, "approve"); code != http.StatusOK {
			t.Fatalf("racing approve: %d %s", code, body)
		}
	}
	code, op, _ = f.report(out2.Op.ID, team.RelayReportRequest{State: team.RelayCancelled, Error: team.RelayReasonCompacted})
	f.m.beforeTerminalClose = nil
	if code != http.StatusOK || op.State != team.RelayCancelled || op.Reason != team.RelayReasonCompacted {
		t.Fatalf("(2) report after a racing approve: %d %+v", code, op)
	}
	if row, _, _ := f.m.store.Get(out2.RequestID); row.State != team.StateApproved {
		t.Fatalf("(2) row = %s, want approved (the approve won the CAS)", row.State)
	}
}

// PR #1716 attacker A-2 / A-3: the follow-ups of a report (title move,
// closing the open row) can fail after the op committed. They are retried
// on the idempotent re-send of the same state, a done report moves the
// title too, and the boot reconciliation closes an open row whose op is
// already terminal.
func TestRelayReport_FollowUpsAreRetriedOnResendAndAtBoot(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1")
	f.decide(out.RequestID, "approve")

	// (1) The title move fails on the first cleared; the re-send retries it.
	f.titles.fail = true
	if code, _, _ := f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-1b"}); code != http.StatusOK {
		t.Fatalf("cleared: %d", code)
	}
	if len(f.titles.moves) != 0 || f.titles.has["sid-1b"] {
		t.Fatalf("a failed move must not have moved: %v", f.titles.moves)
	}
	f.titles.fail = false
	if code, _, _ := f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-1b"}); code != http.StatusOK {
		t.Fatalf("cleared re-send: %d", code)
	}
	if !f.titles.has["sid-1b"] || len(f.titles.moves) != 1 {
		t.Fatalf("the re-send must have moved the title: has=%v moves=%v", f.titles.has, f.titles.moves)
	}

	// (2) done also moves the title (idempotent: already moved → no-op).
	if code, _, _ := f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayDone}); code != http.StatusOK {
		t.Fatalf("done: %d", code)
	}
	if len(f.titles.moves) != 1 {
		t.Fatalf("done re-moved an already moved title: %v", f.titles.moves)
	}

	// (3) A fresh op: its row is left open although the op is terminal
	// (model: the close after the op's commit failed) — the re-send of the
	// terminal state closes it, and so does the boot reconciliation.
	out2 := f.begin("sid-2")
	if _, err := f.m.store.db.Exec(`UPDATE relay_ops SET state = ?, reason = ? WHERE id = ?`, string(team.RelayCancelled), team.RelayReasonCompacted, out2.Op.ID); err != nil {
		t.Fatal(err)
	}
	if row, _, _ := f.m.store.Get(out2.RequestID); row.State != team.StateOpen {
		t.Fatalf("sanity: row = %s", row.State)
	}
	_ = f.events() // drain what begin broadcast
	if code, _, _ := f.report(out2.Op.ID, team.RelayReportRequest{State: team.RelayCancelled, Error: team.RelayReasonCompacted}); code != http.StatusOK {
		t.Fatalf("cancelled re-send: %d", code)
	}
	if row, _, _ := f.m.store.Get(out2.RequestID); row.State != team.StateCancelled {
		t.Fatalf("row after the re-send = %s, want cancelled", row.State)
	}
	closed := 0
	for _, ev := range f.events() {
		if ev.Op == "closed" {
			closed++
		}
	}
	if closed != 1 {
		t.Fatalf("closed events on the re-send = %d, want 1", closed)
	}

	out3 := f.begin("sid-2") // sid-2's op is terminal now, so a new one may open
	if _, err := f.m.store.db.Exec(`UPDATE relay_ops SET state = ?, reason = ? WHERE id = ?`, string(team.RelayFailed), team.RelayReasonHandoffIncomplete, out3.Op.ID); err != nil {
		t.Fatal(err)
	}
	f2 := f.reboot(f.titles)
	if row, _, _ := f2.m.store.Get(out3.RequestID); row.State != team.StateAbandoned {
		t.Fatalf("row of a terminal op after boot = %s, want abandoned", row.State)
	}
}
