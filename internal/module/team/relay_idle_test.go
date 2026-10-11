package teammod

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// Idle relay IR-1a (docs/specs/2026-10-11-idle-relay-spec-plan.md §2, §3.1, §8).

func idleBeginReq(sid string) team.RelayBeginRequest {
	r := beginReq(sid)
	r.Reason, r.IdleMinutes = team.RelayBeginReasonIdle, 40
	return r
}

func (f *fixture) beginIdle(sid string) team.RelayBeginResponse {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/relay/begin", idleBeginReq(sid))
	if code != http.StatusCreated {
		f.t.Fatalf("idle begin %s: %d %s", sid, code, body)
	}
	var out team.RelayBeginResponse
	if err := json.Unmarshal(body, &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) payloadOf(requestID string) team.SelfRelayPayload {
	f.t.Helper()
	row, ok, err := f.m.store.Get(requestID)
	if err != nil || !ok {
		f.t.Fatalf("approval %s: ok=%v err=%v", requestID, ok, err)
	}
	var sp team.SelfRelayPayload
	if err := json.Unmarshal(row.Payload, &sp); err != nil {
		f.t.Fatal(err)
	}
	return sp
}

func TestRelayHello_IdleNumbers(t *testing.T) {
	f := newFixture(t)
	code, body := f.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{SessionID: "sid-1", ModVersion: "1", Agent: "cc"})
	var h team.RelayHelloResponse
	if err := json.Unmarshal(body, &h); code != http.StatusOK || err != nil {
		t.Fatalf("hello: %d %s", code, body)
	}
	if h.IdleMinutes != 40 || h.IdleThreshold != 40 {
		t.Fatalf("hello idle numbers = %d / %d, want 40 / 40", h.IdleMinutes, h.IdleThreshold)
	}
}

func TestRelayBegin_IdleReasonStored(t *testing.T) {
	f := newFixture(t)
	out := f.beginIdle("sid-1")
	if sp := f.payloadOf(out.RequestID); sp.Reason != team.RelayBeginReasonIdle || sp.IdleMinutes != 40 {
		t.Fatalf("payload = %+v, want reason idle, 40 minutes", sp)
	}
	// An ordinary begin stores neither.
	f2 := newFixture(t)
	if sp := f2.payloadOf(f2.begin("sid-1").RequestID); sp.Reason != "" || sp.IdleMinutes != 0 {
		t.Fatalf("ordinary payload = %+v, want no reason", sp)
	}
}

func TestRelayBegin_IdleValidation(t *testing.T) {
	for name, mut := range map[string]func(*team.RelayBeginRequest){
		"idle minutes 0":             func(r *team.RelayBeginRequest) { r.IdleMinutes = 0 },
		"idle minutes 60":            func(r *team.RelayBeginRequest) { r.IdleMinutes = 60 },
		"idle minutes negative":      func(r *team.RelayBeginRequest) { r.IdleMinutes = -1 },
		"unknown reason":             func(r *team.RelayBeginRequest) { r.Reason = "lazy" },
		"idle with manual":           func(r *team.RelayBeginRequest) { r.Manual = true },
		"minutes without a reason":   func(r *team.RelayBeginRequest) { r.Reason = "" },
		"minutes on an unknown only": func(r *team.RelayBeginRequest) { r.Reason = "Idle" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			r := idleBeginReq("sid-1")
			mut(&r)
			code, body := f.do(http.MethodPost, "/api/relay/begin", r)
			if code != http.StatusBadRequest {
				t.Fatalf("%s: %d %s, want 400", name, code, body)
			}
			if ae := decodeErr(t, body); ae.Error != team.ErrBadRequest {
				t.Fatalf("%s: error = %q, want bad_request", name, ae.Error)
			}
			if active, _ := f.m.store.ListActiveRelayOps(); len(active) != 0 {
				t.Fatalf("%s opened an op: %+v", name, active)
			}
		})
	}
	for _, m := range []int{1, 59} {
		f := newFixture(t)
		r := idleBeginReq("sid-1")
		r.IdleMinutes = m
		if code, body := f.do(http.MethodPost, "/api/relay/begin", r); code != http.StatusCreated {
			t.Fatalf("idle minutes %d: %d %s, want 201", m, code, body)
		}
	}
}

// A replay carrying the other reason or minutes is a conflicting reuse (409), as for a different window.
func TestRelayBegin_IdleReplayMismatch409(t *testing.T) {
	f := newFixture(t)
	req := idleBeginReq("sid-1")
	req.RequestID = uid(42)
	if code, body := f.do(http.MethodPost, "/api/relay/begin", req); code != http.StatusCreated {
		t.Fatalf("begin: %d %s", code, body)
	}
	if code, body := f.do(http.MethodPost, "/api/relay/begin", req); code != http.StatusCreated {
		t.Fatalf("identical replay: %d %s", code, body)
	}
	other := req
	other.IdleMinutes = 41
	if code, body := f.do(http.MethodPost, "/api/relay/begin", other); code != http.StatusConflict {
		t.Fatalf("replay with other minutes: %d %s", code, body)
	}
	other = req
	other.Reason, other.IdleMinutes = "", 0
	if code, body := f.do(http.MethodPost, "/api/relay/begin", other); code != http.StatusConflict {
		t.Fatalf("replay of an idle ask as a threshold ask: %d %s", code, body)
	}
	// And the other way round: a threshold ask replayed as idle.
	f2 := newFixture(t)
	plain := beginReq("sid-1")
	plain.RequestID = uid(43)
	if code, body := f2.do(http.MethodPost, "/api/relay/begin", plain); code != http.StatusCreated {
		t.Fatalf("plain begin: %d %s", code, body)
	}
	asIdle := idleBeginReq("sid-1")
	asIdle.RequestID = uid(43)
	if code, body := f2.do(http.MethodPost, "/api/relay/begin", asIdle); code != http.StatusConflict {
		t.Fatalf("replay of a threshold ask as idle: %d %s", code, body)
	}
}

// resumed is the idle ask's withdrawal: any other op is refused 400 bad_transition and stays as it was.
func TestRelayReport_ResumedOnlyForIdleOps(t *testing.T) {
	f := newFixture(t)
	plain := f.begin("sid-1")
	code, _, ae := f.report(plain.Op.ID, team.RelayReportRequest{State: team.RelayCancelled, Error: team.RelayReasonResumed})
	if code != http.StatusBadRequest || ae.Error != team.ErrBadTransition {
		t.Fatalf("resumed on a threshold op: %d %+v, want 400 bad_transition", code, ae)
	}
	if got := f.op(plain.Op.ID); got.State != team.RelayAwaitingApproval {
		t.Fatalf("threshold op after the refused resumed = %s, want awaiting_approval", got.State)
	}
	if row, _, _ := f.m.store.Get(plain.RequestID); row.State != team.StateOpen {
		t.Fatalf("threshold row after the refused resumed = %s, want open", row.State)
	}

	f2 := newFixture(t)
	idle := f2.beginIdle("sid-1")
	code, op, _ := f2.report(idle.Op.ID, team.RelayReportRequest{State: team.RelayCancelled, Error: team.RelayReasonResumed})
	if code != http.StatusOK || op.State != team.RelayCancelled || op.Reason != team.RelayReasonResumed {
		t.Fatalf("resumed on an idle op: %d %+v", code, op)
	}
	// A re-send stays 200 (idempotent).
	if code, _, _ := f2.report(idle.Op.ID, team.RelayReportRequest{State: team.RelayCancelled, Error: team.RelayReasonResumed}); code != http.StatusOK {
		t.Fatalf("resumed re-send: %d", code)
	}
	// An unknown op is still a 404, not a bad_transition.
	if code, _, ae := f2.report("no-such-op", team.RelayReportRequest{State: team.RelayCancelled, Error: team.RelayReasonResumed}); code != http.StatusNotFound {
		t.Fatalf("resumed on an unknown op: %d %+v, want 404", code, ae)
	}
}

// resumed races an approve exactly as compacted does (TestRelayReport_TerminalReportClosesTheRowFirst): same three
// outcomes — report first, an approve first (op claimed, then cancelled), a deny first (the person's verdict stands).
func TestRelayReport_ResumedRacesApproveLikeCompacted(t *testing.T) {
	for _, reason := range []string{team.RelayReasonCompacted, team.RelayReasonResumed} {
		f := newFixture(t)
		// (1) the report wins
		out := f.beginIdle("sid-1")
		_ = f.events()
		code, op, _ := f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayCancelled, Error: reason})
		if code != http.StatusOK || op.State != team.RelayCancelled || op.Reason != reason {
			t.Fatalf("%s (1) report: %d %+v", reason, code, op)
		}
		if row, _, _ := f.m.store.Get(out.RequestID); row.State != team.StateCancelled || row.DecidedBy != nil {
			t.Fatalf("%s (1) row = %+v, want cancelled with no decided_by", reason, row)
		}
		closed := 0
		for _, ev := range f.events() {
			if ev.Op == "closed" && ev.Approval.ID == out.RequestID {
				closed++
			}
		}
		if closed != 1 {
			t.Fatalf("%s (1) closed events = %d, want 1", reason, closed)
		}

		// (2) an approve wins the CAS first, its afterClose not yet run: row approved, op claimed → cancelled{reason}
		out2 := f.beginIdle("sid-1")
		f.m.beforeTerminalClose = func(string) {
			if _, won, err := f.m.store.CloseIfOpen(out2.RequestID, Close{State: team.StateApproved, DecidedAt: f.clock.Load()}); err != nil || !won {
				t.Fatalf("%s racing approve CAS: won=%v err=%v", reason, won, err)
			}
		}
		code, op, _ = f.report(out2.Op.ID, team.RelayReportRequest{State: team.RelayCancelled, Error: reason})
		f.m.beforeTerminalClose = nil
		if code != http.StatusOK || op.State != team.RelayCancelled || op.Reason != reason {
			t.Fatalf("%s (2) report after a racing approve: %d %+v", reason, code, op)
		}
		if row, _, _ := f.m.store.Get(out2.RequestID); row.State != team.StateApproved {
			t.Fatalf("%s (2) row = %s, want approved", reason, row.State)
		}
		row2, _, _ := f.m.store.Get(out2.RequestID)
		f.m.afterClose(row2, nil)
		if got := f.op(out2.Op.ID); got.State != team.RelayCancelled || got.Reason != reason {
			t.Fatalf("%s (2) op after the late afterClose = %+v", reason, got)
		}

		// (3) a deny wins first: the person's verdict stands
		out3 := f.beginIdle("sid-1")
		f.m.beforeTerminalClose = func(string) {
			if _, won, err := f.m.store.CloseIfOpen(out3.RequestID, Close{State: team.StateDenied, DecidedAt: f.clock.Load()}); err != nil || !won {
				t.Fatalf("%s racing deny CAS: won=%v err=%v", reason, won, err)
			}
		}
		code, op, _ = f.report(out3.Op.ID, team.RelayReportRequest{State: team.RelayCancelled, Error: reason})
		f.m.beforeTerminalClose = nil
		if code != http.StatusOK || op.State != team.RelayCancelled || op.Reason != team.RelayReasonDenied {
			t.Fatalf("%s (3) report after a racing deny: %d %+v, want cancelled (denied)", reason, code, op)
		}
	}
}

// Codex attack: resumed is the cancelled reason of an idle ask only; any other state carrying it is refused 400 and the
// op is untouched. Mutation gate: drop the state check → failed{resumed} lands (red).
func TestRelayReport_ResumedIsOnlyACancelledReason(t *testing.T) {
	f := newFixture(t)
	out := f.beginIdle("sid-1")
	for _, st := range []team.RelayState{team.RelayFailed, team.RelayWriting, team.RelayDone} {
		code, _, ae := f.report(out.Op.ID, team.RelayReportRequest{State: st, Error: team.RelayReasonResumed})
		if code != http.StatusBadRequest || ae.Error != team.ErrBadRequest {
			t.Fatalf("%s{resumed}: %d %+v, want 400 bad_request", st, code, ae)
		}
	}
	if got := f.op(out.Op.ID); got.State != team.RelayAwaitingApproval {
		t.Fatalf("op after the refused reports = %s, want awaiting_approval", got.State)
	}
}
