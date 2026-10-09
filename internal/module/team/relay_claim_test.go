package teammod

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

func (f *fixture) claim(id, sid string) (int, team.RelayClaimResponse, team.APIError) {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/relay/ops/"+id+"/claim", team.RelayClaimRequest{SessionID: sid})
	var out team.RelayClaimResponse
	var ae team.APIError
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &out); err != nil {
			f.t.Fatal(err)
		}
	} else {
		ae = decodeErr(f.t, body)
	}
	return code, out, ae
}

func (f *fixture) seen(id, sid string) (int, team.RelayOp, team.APIError) {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/relay/ops/"+id+"/seen", team.RelaySeenRequest{SessionID: sid})
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

// requestedOp is a member team with one requested op (rid(1)'s successor).
func (f *fixture) requestedOp() team.RelayOp {
	f.t.Helper()
	f.memberTeam("2")
	code, op, ae := f.createRelay(rid(90), "/tmp/10.sock", "_mem001")
	if code != 201 {
		f.t.Fatalf("create: %d %+v", code, ae)
	}
	return op
}

// Mutation gate (spec §15): drop the session check → the other session claims it (red).
func TestClaim_OnlyTheTargetSession(t *testing.T) {
	f := newFixture(t)
	op := f.requestedOp()
	for _, sid := range []string{"sid-1", "sid-2", "sid-nobody"} {
		if code, _, ae := f.claim(op.ID, sid); code != 409 || ae.Error != team.ErrNotYourOp {
			t.Fatalf("claim by %s: %d %+v", sid, code, ae)
		}
	}
	if got := f.op(op.ID); got.State != team.RelayRequested {
		t.Fatalf("a refused claim moved the op: %+v", got)
	}
	if code, _, ae := f.claim(op.ID, ""); code != 400 {
		t.Fatalf("claim without a session: %d %+v", code, ae)
	}
	if code, _, ae := f.claim(rid(404), "sid-m1"); code != 404 || ae.Error != team.ErrNotFound {
		t.Fatalf("claim of nothing: %d %+v", code, ae)
	}
}

func TestClaim_IsIdempotentAndCarriesTheLead(t *testing.T) {
	f := newFixture(t)
	op := f.requestedOp()
	code, out, ae := f.claim(op.ID, "sid-m1")
	if code != 200 || out.Op.State != team.RelayClaimed || out.Lead == nil || out.Lead.Ref != ipeers.RefID("sid-1") || out.Lead.TeamID != uid(1) || out.Lead.Address == "" {
		t.Fatalf("claim: %d %+v %+v", code, out, ae)
	}
	again, out2, _ := f.claim(op.ID, "sid-m1")
	if again != 200 || out2.Op.State != team.RelayClaimed || out2.Op.UpdatedAt != out.Op.UpdatedAt {
		t.Fatalf("repeat: %d %+v", again, out2)
	}
	// the claim raises no relay flag (plan rule 5: the mod raises it at the write turn)
	if _, err := os.Stat(filepath.Join(f.core.Cfg.DataDir, "hooklocks")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(f.core.Cfg.DataDir, "hooklocks"))
		if len(entries) != 0 {
			t.Fatalf("a claim left lock files: %v", entries)
		}
	}
	// an op that awaits a person (RQ-2) is not claimable by its target; mutation gate: drop the check → 200 (red)
	f.m.store.db.Exec(`UPDATE relay_ops SET state = 'awaiting_approval' WHERE id = ?`, op.ID)
	if code, _, ae := f.claim(op.ID, "sid-m1"); code != 409 || ae.Error != team.ErrBadTransition || f.op(op.ID).State != team.RelayAwaitingApproval {
		t.Fatalf("claim of an awaiting op: %d %+v", code, ae)
	}
	// past claimed, a claim does not step back
	f.m.store.db.Exec(`UPDATE relay_ops SET state = 'written' WHERE id = ?`, op.ID)
	if code, _, ae := f.claim(op.ID, "sid-m1"); code != 409 || ae.Error != team.ErrBadTransition || ae.Op == nil || ae.Op.State != team.RelayWritten {
		t.Fatalf("claim of a written op: %d %+v", code, ae)
	}
}

func TestClaim_SelfOpIsBadTransition(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1")
	if code, _, ae := f.claim(out.Op.ID, "sid-1"); code != 409 || ae.Error != team.ErrBadTransition {
		t.Fatalf("claim of a self op: %d %+v", code, ae)
	}
	if code, _, ae := f.seen(out.Op.ID, "sid-1"); code != 409 || ae.Error != team.ErrBadTransition {
		t.Fatalf("seen of a self op: %d %+v", code, ae)
	}
	if got := f.op(out.Op.ID); got.State != team.RelayAwaitingApproval {
		t.Fatalf("self op moved: %+v", got)
	}
}

// Mutation gates: drop the seen_at = 0 compare → the second seen moves seen_at (red); set updated_at in seen → red.
func TestSeen_OnceOnlyForTheTargetAndNeverMovesUpdatedAt(t *testing.T) {
	f := newFixture(t)
	op := f.requestedOp()
	if code, _, ae := f.seen(op.ID, "sid-1"); code != 409 || ae.Error != team.ErrNotYourOp {
		t.Fatalf("seen by another: %d %+v", code, ae)
	}
	f.clock.Add(1000)
	code, one, _ := f.seen(op.ID, "sid-m1")
	if code != 200 || one.SeenAt == 0 || one.UpdatedAt != op.UpdatedAt || one.State != team.RelayRequested {
		t.Fatalf("seen: %d %+v (created %+v)", code, one, op)
	}
	f.clock.Add(1000)
	if _, two, _ := f.seen(op.ID, "sid-m1"); two.SeenAt != one.SeenAt {
		t.Fatalf("a second seen moved seen_at: %d → %d", one.SeenAt, two.SeenAt)
	}
	// once claimed, seen is a no-op that still answers the op
	f.claim(op.ID, "sid-m1")
	f.m.store.db.Exec(`UPDATE relay_ops SET seen_at = 0 WHERE id = ?`, op.ID)
	if _, three, _ := f.seen(op.ID, "sid-m1"); three.SeenAt != 0 || three.State != team.RelayClaimed {
		t.Fatalf("seen after the claim: %+v", three)
	}
}

// poll starts GET /api/relay/ops/{id}?wait=N and returns a channel of the answered op.
func (f *fixture) poll(id string, wait int) chan team.RelayOp {
	ch := make(chan team.RelayOp, 1)
	go func() {
		_, body := f.do(http.MethodGet, "/api/relay/ops/"+id+"?wait="+string(rune('0'+wait)), nil)
		var op team.RelayOp
		_ = json.Unmarshal(body, &op)
		ch <- op
	}()
	return ch
}

func (f *fixture) waitersOn(id string) int {
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	return len(f.m.waiters[id])
}

// The wake comes from the store's one choke point, so it works for a change that never touched the HTTP report
// handler. Mutation gate: do not set store.opChanged in Init → the store-path cases time out (red).
func TestRelayOp_LongPollWakesOnEveryCommittedChange(t *testing.T) {
	steps := []struct {
		name string
		do   func(f *fixture, op team.RelayOp)
		want team.RelayState
	}{
		{"claim route", func(f *fixture, op team.RelayOp) { f.claim(op.ID, "sid-m1") }, team.RelayClaimed},
		{"a store report, no HTTP", func(f *fixture, op team.RelayOp) {
			if _, res, err := f.m.store.ReportRelay(op.ID, RelayReport{State: team.RelayFailed, Reason: team.RelayReasonMemberGone, At: 5}); err != nil || res != ReportApplied {
				t.Errorf("report: %v %v", res, err)
			}
		}, team.RelayFailed},
		{"the HTTP report route", func(f *fixture, op team.RelayOp) {
			f.m.store.ReportRelay(op.ID, RelayReport{State: team.RelayClaimed, At: 5})
			f.report(op.ID, team.RelayReportRequest{State: team.RelayFailed, Error: team.RelayReasonMemberGone})
		}, team.RelayFailed},
	}
	for _, s := range steps {
		f := newFixture(t)
		op := f.requestedOp()
		got := f.poll(op.ID, 5)
		waitFor(t, func() bool { return f.waitersOn(op.ID) == 1 })
		start := time.Now()
		s.do(f, op)
		select {
		case answered := <-got:
			if answered.State == team.RelayRequested || time.Since(start) > 3*time.Second {
				t.Errorf("%s: answered %+v after %v", s.name, answered, time.Since(start))
			}
			if s.name != "the HTTP report route" && answered.State != s.want {
				t.Errorf("%s: state %s, want %s", s.name, answered.State, s.want)
			}
		case <-time.After(4 * time.Second):
			t.Errorf("%s: the long-poll was not woken", s.name)
		}
	}
}

// An approval's close moves a self op (claimed) with no report route: its waiters wake too (the unattended and the click
// paths end in the store's transaction).
func TestRelayOp_LongPollWakesOnAnApprovalClose(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1")
	got := f.poll(out.Op.ID, 5)
	waitFor(t, func() bool { return f.waitersOn(out.Op.ID) == 1 })
	f.decide(out.RequestID, "approve")
	select {
	case op := <-got:
		if op.State != team.RelayClaimed {
			t.Fatalf("answered %+v", op)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("the approve did not wake the op's long-poll")
	}
}

func TestRelayOp_LongPollTimesOutUnchangedAndAnswersTerminalAtOnce(t *testing.T) {
	f := newFixture(t)
	op := f.requestedOp()
	start := time.Now()
	if got := <-f.poll(op.ID, 1); got.State != team.RelayRequested || time.Since(start) < 900*time.Millisecond {
		t.Fatalf("timeout answer %+v after %v", got, time.Since(start))
	}
	f.m.store.ReportRelay(op.ID, RelayReport{State: team.RelayCancelled, Reason: team.RelayReasonAbandoned, At: 9})
	start = time.Now()
	if got := <-f.poll(op.ID, 5); got.State != team.RelayCancelled || time.Since(start) > time.Second {
		t.Fatalf("terminal answer %+v after %v", got, time.Since(start))
	}
	if code, _ := f.do(http.MethodGet, "/api/relay/ops/"+rid(404)+"?wait=1", nil); code != 404 {
		t.Fatalf("unknown op: %d", code)
	}
	if code, _ := f.do(http.MethodGet, "/api/relay/ops/"+op.ID+"?wait=x", nil); code != 400 {
		t.Fatalf("bad wait: %d", code)
	}
}
