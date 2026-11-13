package codexbroker

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Auto sweep tests (spec 2026-11-13).

func autoHandler(rec BrokerRecord, d DecisionResult) (*SweepHandler, *fakeKiller, *fakeScanner) {
	scan := &fakeScanner{brokers: []BrokerRecord{rec}}
	killer := &fakeKiller{}
	eval := &stubDecisionEvaluator{by: map[string]DecisionResult{rec.Key: d}}
	return newSweepHandlerForTest(scan, killer, eval, emptyRegistry{}, &QuarantineFile{Version: 1}), killer, scan
}

func TestEvaluate_Auto_Matrix(t *testing.T) {
	foreign := "foreign_quarantine"
	cases := []struct {
		name     string
		cwd      string
		d        DecisionResult
		auto     bool
		wantKill bool
	}{
		{"auto abs cwd e1", "/gone/x", DecisionResult{Kill: true, OverrideE1: true, Reason: foreign}, true, true},
		{"auto relative cwd", "worktrees/x", DecisionResult{Kill: true, OverrideE1: true, Reason: foreign}, true, false},
		{"auto no e1", "/gone/x", DecisionResult{Kill: true, Reason: foreign}, true, false},
		{"auto alive", "/gone/x", DecisionResult{Kill: false, OverrideE1: true, Reason: foreign}, true, false},
		{"manual unfiltered", "/gone/x", DecisionResult{Kill: true, OverrideE1: true, Reason: foreign}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := BrokerRecord{Key: "k1", Cwd: c.cwd}
			h, killer, _ := autoHandler(rec, c.d)
			resp := h.evaluate(context.Background(), []BrokerRecord{rec}, "apply", "", c.auto)
			invoked, _ := killer.snapshot()
			if (invoked == 1) != c.wantKill {
				t.Fatalf("killed=%d want %v", invoked, c.wantKill)
			}
			if c.wantKill && (len(resp.Applied) != 1 || resp.Evaluated[0].Decision.Reason != "workspace_gone") {
				t.Errorf("resp = %+v", resp)
			}
		})
	}
}

func TestEvaluate_CtxCancelled(t *testing.T) {
	recs := []BrokerRecord{{Key: "a", Cwd: "/x"}, {Key: "b", Cwd: "/x"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, auto := range []bool{true, false} {
		evals := 0
		h, killer, _ := autoHandler(recs[0], DecisionResult{})
		h.EvalFn = func(context.Context, BrokerRecord, DecisionOpts) DecisionResult { evals++; return DecisionResult{} }
		resp := h.evaluate(ctx, recs, "apply", "", auto)
		if invoked, _ := killer.snapshot(); invoked != 0 {
			t.Errorf("auto=%v killed", auto)
		}
		if auto {
			if evals != 0 || len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], "2 brokers not evaluated") {
				t.Errorf("auto: evals=%d errors=%v", evals, resp.Errors)
			}
		} else if evals != 2 || len(resp.Errors) != 0 {
			t.Errorf("manual: evals=%d errors=%v", evals, resp.Errors)
		}
	}
}

// Wiring: real EvalDecision + E1Tracker; the first observation is seeded by hand.
func TestEvaluate_Auto_RealEvalDecision_WorkspaceGone(t *testing.T) {
	for _, seeded := range []bool{true, false} {
		t0 := time.Now().Add(-2 * time.Hour)
		rec := BrokerRecord{
			Key: "abcdef0123456789", PID: 4321, Cwd: "/missing/ws", StateDir: "/state",
			Endpoint:         "unix:/tmp/cxc-XX/broker.sock",
			LastJobUpdatedAt: &t0,
			Anomalies:        []Anomaly{{Code: AnomalyCwdMissing}},
		}
		scan := &fakeScanner{brokers: []BrokerRecord{rec}}
		killer := &fakeKiller{}
		h := newSweepHandlerForTest(scan, killer, &stubDecisionEvaluator{}, emptyRegistry{}, &QuarantineFile{Version: 1})
		h.EvalFn = EvalDecision
		h.BaseDecisionOpts = decisionOptsAllFalse(false)
		h.BaseDecisionOpts.Registry = nil
		if seeded {
			h.E1Tracker.Observe(rec, time.Now().Add(-2*time.Minute))
		}
		resp := h.evaluate(context.Background(), []BrokerRecord{rec}, "apply", "", true)
		invoked, _ := killer.snapshot()
		if seeded != (invoked == 1) {
			t.Fatalf("seeded=%v killed=%d resp=%+v", seeded, invoked, resp)
		}
		if seeded && resp.Evaluated[0].Decision.Reason != "workspace_gone" {
			t.Errorf("reason = %q", resp.Evaluated[0].Decision.Reason)
		}
	}
}
