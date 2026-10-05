package nex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/store"
)

// testHostID is the HostID newHandoffEnv configures.
const testHostID = "host1"

const pdxOther = "pdx:" + testHostID + "/other-tab" // testHostID: the HostID newTakebackEnv configures

func TestTakeControl(t *testing.T) {
	t.Run("caller lease is used as is", func(t *testing.T) {
		env := newTakebackEnv(t)
		ctl, herr := env.m.takeControl(context.Background(), "E1", "L-caller", "pdx:"+testHostID)
		if herr != nil || ctl.LeaseID != "L-caller" {
			t.Fatalf("%+v %v", ctl, herr)
		}
		ctl.release()
		if len(env.svc.releases) != 0 {
			t.Fatal("a caller's lease must never be released")
		}
	})
	t.Run("acquires and releases its own", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.lease = store.Lease{ID: "L-own"}
		ctl, herr := env.m.takeControl(context.Background(), "E1", "", "pdx:"+testHostID)
		if herr != nil || ctl.LeaseID != "L-own" {
			t.Fatalf("%+v %v", ctl, herr)
		}
		ctl.release()
		if len(env.svc.releases) != 1 {
			t.Fatal("own lease not released")
		}
	})
	t.Run("borrows a pdx holder's lease", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.acquireErr = store.ErrLeaseHeld
		env.store.script(store.Execution{ID: "E1", State: store.StateIdle, LeaseID: "L-b", LeasePrincipalID: pdxOther, LeaseExpiresAt: nowMs() + 60_000})
		ctl, herr := env.m.takeControl(context.Background(), "E1", "", "pdx:"+testHostID)
		if herr != nil || ctl.LeaseID != "L-b" || ctl.PrincipalID != pdxOther {
			t.Fatalf("%+v %v", ctl, herr)
		}
		ctl.release()
		if len(env.svc.releases) != 0 {
			t.Fatal("a borrowed lease must never be released")
		}
	})
	t.Run("refuses a non-pdx holder", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.acquireErr = store.ErrLeaseHeld
		env.store.script(store.Execution{ID: "E1", State: store.StateIdle, LeaseID: "L-p", LeasePrincipalID: "ploom:agent-7", LeaseExpiresAt: nowMs() + 60_000})
		_, herr := env.m.takeControl(context.Background(), "E1", "", "pdx:"+testHostID)
		if herr == nil || herr.status != http.StatusConflict || herr.code != "held_by" || herr.detail["principal"] != "ploom:agent-7" {
			t.Fatalf("herr = %+v", herr)
		}
	})
	t.Run("renewControl extends the lease under its holder", func(t *testing.T) {
		env := newTakebackEnv(t)
		ctl := control{LeaseID: "L-b", PrincipalID: pdxOther, release: noRelease}
		got, herr := env.m.renewControl(context.Background(), "E1", ctl, "pdx:"+testHostID)
		if herr != nil || got.LeaseID != "L-b" || len(env.svc.renewCalls) != 1 || env.svc.renewCalls[0].PrincipalID != pdxOther {
			t.Fatalf("%+v %v %+v", got, herr, env.svc.renewCalls)
		}
	})
	t.Run("renewControl re-takes an expired lease once", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.renewErr = store.ErrLeaseExpired
		env.svc.lease = store.Lease{ID: "L-new"}
		released := false
		got, herr := env.m.renewControl(context.Background(), "E1", control{LeaseID: "L-old", PrincipalID: "pdx:" + testHostID, release: func() { released = true }}, "pdx:"+testHostID)
		if herr != nil || got.LeaseID != "L-new" || !released {
			t.Fatalf("%+v %v released=%v", got, herr, released)
		}
	})
	t.Run("renewControl: re-take fails with an infra error → release never nil", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.renewErr = store.ErrLeaseExpired
		env.svc.acquireErr = errors.New("db locked")
		releasedOld := 0
		got, herr := env.m.renewControl(context.Background(), "E1", control{LeaseID: "L-old", PrincipalID: "pdx:" + testHostID, release: func() { releasedOld++ }}, "pdx:"+testHostID)
		if herr == nil || herr.code != "lease_error" || releasedOld != 1 {
			t.Fatalf("herr=%+v releasedOld=%d", herr, releasedOld)
		}
		if got.release == nil {
			t.Fatal("control.release must never be nil")
		}
		got.release()
	})
	t.Run("renewControl: re-take sees a non-pdx holder → held_by, release never nil", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.renewErr = store.ErrLeaseExpired
		env.svc.acquireErr = store.ErrLeaseHeld
		env.store.script(store.Execution{ID: "E1", State: store.StateIdle, LeaseID: "L-p", LeasePrincipalID: "ploom:agent-7", LeaseExpiresAt: nowMs() + 60_000})
		got, herr := env.m.renewControl(context.Background(), "E1", control{LeaseID: "L-old", PrincipalID: "pdx:" + testHostID, release: noRelease}, "pdx:"+testHostID)
		if herr == nil || herr.code != "held_by" {
			t.Fatalf("herr=%+v", herr)
		}
		if got.release == nil {
			t.Fatal("control.release must never be nil")
		}
		got.release()
	})
	t.Run("lease that keeps changing hands → lease_contended", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.acquireErr = store.ErrLeaseHeld
		env.store.script(
			store.Execution{ID: "E1", LeaseID: "L-1", LeasePrincipalID: pdxOther, LeaseExpiresAt: 1},
			store.Execution{ID: "E1", LeaseID: "L-2", LeasePrincipalID: pdxOther, LeaseExpiresAt: 1},
		)
		ctl, herr := env.m.takeControl(context.Background(), "E1", "", "pdx:"+testHostID)
		if herr == nil || herr.status != http.StatusConflict || herr.code != "lease_contended" || herr.detail["principal"] != pdxOther {
			t.Fatalf("herr = %+v", herr)
		}
		if ctl.release == nil {
			t.Fatal("control.release must never be nil")
		}
	})
	t.Run("a pdx principal of another host is not ours", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.acquireErr = store.ErrLeaseHeld
		env.store.script(store.Execution{ID: "E1", LeaseID: "L-x", LeasePrincipalID: "pdx:" + testHostID + "x", LeaseExpiresAt: nowMs() + 60_000})
		if _, herr := env.m.takeControl(context.Background(), "E1", "", "pdx:"+testHostID); herr == nil || herr.code != "held_by" {
			t.Fatalf("herr = %+v", herr)
		}
	})
}

func TestExitWorker_ByState(t *testing.T) {
	cases := []struct {
		name               string
		row                store.Execution
		wantTerminateCalls int
		wantArchiveCalls   int
		want               exitOutcome
	}{
		{"idle", store.Execution{ID: "E", State: store.StateIdle}, 1, 1, exitOutcome{true, true, store.StateTerminated}},
		{"running", store.Execution{ID: "E", State: store.StateRunning}, 1, 1, exitOutcome{true, true, store.StateTerminated}},
		{"queued", store.Execution{ID: "E", State: store.StateQueued}, 1, 1, exitOutcome{true, true, store.StateTerminated}},
		{"failed", store.Execution{ID: "E", State: store.StateFailed}, 0, 1, exitOutcome{false, true, store.StateFailed}},
		{"rejected", store.Execution{ID: "E", State: store.StateRejected}, 0, 1, exitOutcome{false, true, store.StateRejected}},
		{"terminated unarchived", store.Execution{ID: "E", State: store.StateTerminated}, 0, 1, exitOutcome{true, true, store.StateTerminated}},
		{"already exited", store.Execution{ID: "E", State: store.StateTerminated, ArchivedAt: 9}, 0, 0, exitOutcome{true, true, store.StateTerminated}},
		{"legacy idle+archived", store.Execution{ID: "E", State: store.StateIdle, ArchivedAt: 9}, 1, 0, exitOutcome{true, true, store.StateTerminated}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newTakebackEnv(t)
			env.svc.lease = store.Lease{ID: "L-own"}
			out, herr := env.m.exitWorker(context.Background(), c.row, nil, "pdx:"+testHostID)
			if herr != nil {
				t.Fatalf("herr = %+v", herr)
			}
			if out != c.want {
				t.Errorf("out = %+v, want %+v", out, c.want)
			}
			if got := len(env.svc.terminateCalls); got != c.wantTerminateCalls {
				t.Errorf("terminate calls = %d", got)
			}
			if got := len(env.svc.ArchiveCalls()); got != c.wantArchiveCalls {
				t.Errorf("archive calls = %d", got)
			}
			if c.wantTerminateCalls == 1 && env.svc.terminateCalls[0].LeaseID != "L-own" {
				t.Errorf("terminate lease = %q", env.svc.terminateCalls[0].LeaseID)
			}
		})
	}
}

func TestExitWorker_Failures(t *testing.T) {
	t.Run("idle: terminate fails, archive still blocks writes", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.terminateErr = errors.New("boom")
		out, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateIdle}, nil, "pdx:"+testHostID)
		if herr != nil || !out.Exited() || out.Terminated || !out.Archived {
			t.Fatalf("out=%+v herr=%+v", out, herr)
		}
	})
	t.Run("running: terminate fails, archive still attempted; refused while running → the terminate error", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.terminateErr = execution.ErrInterruptUnconfirmed
		env.svc.archiveErr = execution.ErrArchiveWhileRunning
		_, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateRunning}, nil, "pdx:"+testHostID)
		if herr == nil || herr.status != http.StatusGatewayTimeout || herr.code != "interrupt_unconfirmed" {
			t.Fatalf("herr = %+v", herr)
		}
		if len(env.svc.ArchiveCalls()) != 1 {
			t.Fatal("D4: a failed terminate must not skip the archive attempt")
		}
	})
	t.Run("running: terminate fails but the turn ended meanwhile → archived, exited", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.terminateErr = execution.ErrTerminateContended
		out, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateRunning}, nil, "pdx:"+testHostID)
		if herr != nil || !out.Exited() || !out.Archived || out.Terminated {
			t.Fatalf("out=%+v herr=%+v", out, herr)
		}
	})
	t.Run("terminated but archive fails → still exited", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.archiveErr = errors.New("db busy")
		out, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateIdle}, nil, "pdx:"+testHostID)
		if herr != nil || !out.Exited() || out.Archived {
			t.Fatalf("out=%+v herr=%+v", out, herr)
		}
	})
	t.Run("failed row, archive fails → archive_failed", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.archiveErr = errors.New("db busy")
		_, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateFailed}, nil, "pdx:"+testHostID)
		if herr == nil || herr.code != "archive_failed" {
			t.Fatalf("herr = %+v", herr)
		}
	})
	t.Run("non-pdx holder → held_by, nothing changed", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.acquireErr = store.ErrLeaseHeld
		env.store.script(store.Execution{ID: "E", State: store.StateIdle, LeaseID: "L-p", LeasePrincipalID: "ploom:agent-7", LeaseExpiresAt: nowMs() + 60_000})
		_, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateIdle}, nil, "pdx:"+testHostID)
		if herr == nil || herr.code != "held_by" || len(env.svc.terminateCalls) != 0 || len(env.svc.ArchiveCalls()) != 0 {
			t.Fatalf("herr=%+v terminate=%d archive=%d", herr, len(env.svc.terminateCalls), len(env.svc.ArchiveCalls()))
		}
	})
	t.Run("ended on its own meanwhile → re-read and archive", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.terminateErr = store.ErrExecutionTerminal
		env.store.script(store.Execution{ID: "E", State: store.StateFailed})
		out, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateIdle}, nil, "pdx:"+testHostID)
		if herr != nil || out.State != store.StateFailed || !out.Archived || out.Terminated {
			t.Fatalf("out=%+v herr=%+v", out, herr)
		}
	})
	t.Run("nil ctl: releases the lease it acquired itself", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.lease = store.Lease{ID: "L-own"}
		if _, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateIdle}, nil, "pdx:"+testHostID); herr != nil {
			t.Fatal(herr)
		}
		if len(env.svc.releases) != 1 || env.svc.releases[0].LeaseID != "L-own" {
			t.Fatalf("releases = %+v", env.svc.releases)
		}
	})
	t.Run("nil ctl: a borrowed pdx lease is never released", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.acquireErr = store.ErrLeaseHeld
		env.store.script(store.Execution{ID: "E", State: store.StateIdle, LeaseID: "L-b", LeasePrincipalID: pdxOther, LeaseExpiresAt: nowMs() + 60_000})
		if _, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateIdle}, nil, "pdx:"+testHostID); herr != nil {
			t.Fatal(herr)
		}
		if len(env.svc.releases) != 0 || len(env.svc.terminateCalls) != 1 || env.svc.terminateCalls[0].LeaseID != "L-b" {
			t.Fatalf("releases=%+v terminate=%+v", env.svc.releases, env.svc.terminateCalls)
		}
	})
	t.Run("given control is used and not released", func(t *testing.T) {
		env := newTakebackEnv(t)
		released := false
		ctl := &control{LeaseID: "L-t", PrincipalID: "pdx:" + testHostID, release: func() { released = true }}
		if _, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateIdle}, ctl, "pdx:"+testHostID); herr != nil {
			t.Fatal(herr)
		}
		if env.svc.terminateCalls[0].LeaseID != "L-t" || released || len(env.svc.acquires) != 0 {
			t.Fatal("exitWorker must act under the given control without acquiring or releasing")
		}
	})
}

func exitPost(t *testing.T, env *takebackEnv, id, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(env.srv.URL+"/api/nex/executions/"+id+"/exit", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	return resp.StatusCode, out
}

func TestExitEndpoint(t *testing.T) {
	t.Run("idle, empty body -> 200 exited", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.results = []getResult{{exec: store.Execution{ID: "E1", State: store.StateIdle}}}
		env.svc.lease = store.Lease{ID: "L-own"}
		status, body := exitPost(t, env, "E1", ``)
		if status != 200 || body["exited"] != true || body["terminated"] != true || body["archived"] != true || body["state"] != "terminated" {
			t.Fatalf("%d %v", status, body)
		}
	})
	t.Run("caller lease is used", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.results = []getResult{{exec: store.Execution{ID: "E1", State: store.StateIdle}}}
		status, body := exitPost(t, env, "E1", `{"lease_id":"L-mine"}`)
		if status != 200 || len(env.svc.terminateCalls) != 1 || env.svc.terminateCalls[0].LeaseID != "L-mine" || len(env.svc.acquires) != 0 {
			t.Fatalf("%d %v calls=%+v acquires=%v", status, body, env.svc.terminateCalls, env.svc.acquires)
		}
	})
	t.Run("malformed body -> 400", func(t *testing.T) {
		env := newTakebackEnv(t)
		status, body := exitPost(t, env, "E1", `{nope`)
		if status != 400 || body["code"] != "malformed_body" {
			t.Fatalf("%d %v", status, body)
		}
	})
	t.Run("lock held -> 409 transfer_in_progress", func(t *testing.T) {
		env := newTakebackEnv(t)
		if !env.m.locks.TryLock(takeToTerminalLockKey("E1")) {
			t.Fatal("lock")
		}
		defer env.m.locks.Unlock(takeToTerminalLockKey("E1"))
		status, body := exitPost(t, env, "E1", ``)
		if status != 409 || body["code"] != "transfer_in_progress" {
			t.Fatalf("%d %v", status, body)
		}
	})
	t.Run("missing -> 404", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.results = []getResult{{err: store.ErrNotFound}}
		status, body := exitPost(t, env, "NOPE", ``)
		if status != 404 || body["code"] != "execution_not_found" {
			t.Fatalf("%d %v", status, body)
		}
	})
	t.Run("store error -> 500", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.results = []getResult{{err: errors.New("disk")}}
		status, body := exitPost(t, env, "E1", ``)
		if status != 500 || body["code"] != "store_error" {
			t.Fatalf("%d %v", status, body)
		}
	})
	t.Run("idempotent second call makes no engine call", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.results = []getResult{{exec: store.Execution{ID: "E1", State: store.StateTerminated, ArchivedAt: 5}}}
		status, body := exitPost(t, env, "E1", ``)
		if status != 200 || body["exited"] != true || len(env.svc.terminateCalls)+len(env.svc.ArchiveCalls()) != 0 {
			t.Fatalf("%d %v", status, body)
		}
	})
	t.Run("engine unavailable -> 503", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.m.sys.service = nil
		status, body := exitPost(t, env, "E1", ``)
		if status != 503 || body["code"] != "nex_unavailable" {
			t.Fatalf("%d %v", status, body)
		}
	})
}
