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

// pdxOther is another Purdex client of this host: a pdx principal the daemon minted.
const pdxOther = "pdx:" + testHostID + "/other-tab"

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

// TestFakeLeaseFence pins the fake's opt-in fence to store.CheckLease's three answers.
func TestFakeLeaseFence(t *testing.T) {
	ctx := context.Background()
	term := func(f *fakeNexService, lease, principal string) error {
		return f.Terminate(ctx, execution.TerminateRequest{ExecutionID: "E", LeaseID: lease, PrincipalID: principal})
	}
	f := &fakeNexService{enforceLease: true}
	if err := term(f, "L", "p"); !errors.Is(err, store.ErrLeaseRequired) {
		t.Fatalf("no lease: %v", err)
	}
	f.heldLease = store.Lease{ID: "L", PrincipalID: "p", ExpiresAt: nowMs() - 1}
	if err := term(f, "L", "p"); !errors.Is(err, store.ErrLeaseExpired) {
		t.Fatalf("expired: %v", err)
	}
	f.heldLease.ExpiresAt = nowMs() + 60_000
	if err := term(f, "L", "q"); !errors.Is(err, store.ErrLeaseMismatch) {
		t.Fatalf("other principal: %v", err)
	}
	if err := term(f, "M", "p"); !errors.Is(err, store.ErrLeaseMismatch) {
		t.Fatalf("other id: %v", err)
	}
	if _, err := f.RenewLease(ctx, "E", "M", "p"); !errors.Is(err, store.ErrLeaseMismatch) {
		t.Fatalf("renew other id: %v", err)
	}
	before := f.heldLease.ExpiresAt
	if l, err := f.RenewLease(ctx, "E", "L", "p"); err != nil || l.ExpiresAt <= before {
		t.Fatalf("renew: %+v %v", l, err)
	}
	if err := term(f, "L", "p"); err != nil {
		t.Fatalf("holder: %v", err)
	}
	if _, err := f.AcquireLease(ctx, "E", "q"); !errors.Is(err, store.ErrLeaseHeld) {
		t.Fatalf("acquire over a live lease: %v", err)
	}
}

// PR #1578 B1: a lease that changes hands between takeControl's read and the
// terminate must never end in archiving a non-pdx holder's execution (D4).
func TestExitWorker_LeaseRace(t *testing.T) {
	const self = "pdx:" + testHostID
	ploom := store.Lease{ID: "L-p", PrincipalID: "ploom:agent-7", ExpiresAt: nowMs() + 60_000}
	rowHeld := func(l store.Lease) store.Execution {
		return store.Execution{ID: "E", State: store.StateIdle, LeaseID: l.ID, LeasePrincipalID: l.PrincipalID, LeaseExpiresAt: l.ExpiresAt}
	}
	idle := store.Execution{ID: "E", State: store.StateIdle}

	t.Run("B1-a: borrowed pdx lease handed to a non-pdx holder → held_by, nothing archived", func(t *testing.T) {
		env := newTakebackEnv(t)
		borrowed := store.Lease{ID: "L-b", PrincipalID: pdxOther, ExpiresAt: nowMs() + 60_000}
		env.svc.enforceLease = true
		env.svc.heldLease = borrowed
		env.store.script(rowHeld(borrowed), rowHeld(ploom))
		env.svc.onTerminate = func(execution.TerminateRequest) { env.svc.setHeldLease(ploom) }
		_, herr := env.m.exitWorker(context.Background(), idle, nil, self)
		if herr == nil || herr.status != http.StatusConflict || herr.code != "held_by" || herr.detail["principal"] != "ploom:agent-7" {
			t.Fatalf("herr = %+v", herr)
		}
		if n := len(env.svc.ArchiveCalls()); n != 0 {
			t.Fatalf("archive calls = %d, want 0", n)
		}
		if len(env.svc.terminateCalls) != 1 || len(env.svc.acquires) != 2 || len(env.svc.releases) != 0 {
			t.Fatalf("terminate=%+v acquires=%v releases=%+v", env.svc.terminateCalls, env.svc.acquires, env.svc.releases)
		}
	})
	t.Run("B1-b: lease expired before terminate → re-taken once, retried, archived", func(t *testing.T) {
		env := newTakebackEnv(t)
		borrowed := store.Lease{ID: "L-b", PrincipalID: pdxOther, ExpiresAt: nowMs() + 60_000}
		expired := borrowed
		expired.ExpiresAt = nowMs() - 1
		env.svc.enforceLease = true
		env.svc.heldLease = borrowed
		env.svc.lease = store.Lease{ID: "L-new"}
		env.store.script(rowHeld(borrowed), rowHeld(expired))
		env.svc.onTerminate = func(req execution.TerminateRequest) {
			if req.LeaseID == "L-b" {
				env.svc.setHeldLease(expired) // the other tab stopped renewing
			}
		}
		out, herr := env.m.exitWorker(context.Background(), idle, nil, self)
		if herr != nil || !out.Exited() || !out.Terminated || !out.Archived || out.State != store.StateTerminated {
			t.Fatalf("out=%+v herr=%+v", out, herr)
		}
		calls := env.svc.terminateCalls
		if len(calls) != 2 || calls[1].LeaseID != "L-new" || calls[1].PrincipalID != self {
			t.Fatalf("terminate calls = %+v", calls)
		}
		if n := len(env.svc.ArchiveCalls()); n != 1 {
			t.Fatalf("archive calls = %d", n)
		}
		if len(env.svc.releases) != 1 || env.svc.releases[0].LeaseID != "L-new" {
			t.Fatalf("releases = %+v (own re-taken lease must be released)", env.svc.releases)
		}
	})
	t.Run("own lease lost before terminate: released before the re-take", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.enforceLease = true
		env.svc.lease = store.Lease{ID: "L-own"}
		env.svc.onTerminate = func(req execution.TerminateRequest) {
			if len(env.svc.terminateCalls) == 1 {
				env.svc.setHeldLease(store.Lease{ID: "L-own", PrincipalID: self, ExpiresAt: nowMs() - 1})
				env.svc.mu.Lock()
				env.svc.lease = store.Lease{ID: "L-own2"}
				env.svc.mu.Unlock()
			}
		}
		out, herr := env.m.exitWorker(context.Background(), idle, nil, self)
		if herr != nil || !out.Terminated || !out.Archived {
			t.Fatalf("out=%+v herr=%+v", out, herr)
		}
		got := env.svc.Calls()
		want := []string{"acquire", "terminate", "release", "acquire", "terminate", "archive", "release"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("calls = %v, want %v", got, want)
		}
		if env.svc.releases[0].LeaseID != "L-own" || env.svc.releases[1].LeaseID != "L-own2" {
			t.Fatalf("releases = %+v", env.svc.releases)
		}
	})
	t.Run("re-take fails with an infra error → existing failure rules (archive)", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.enforceLease = true
		env.svc.lease = store.Lease{ID: "L-own"}
		env.svc.onTerminate = func(execution.TerminateRequest) {
			env.svc.setHeldLease(store.Lease{})
			env.svc.mu.Lock()
			env.svc.acquireErr = errors.New("db locked")
			env.svc.mu.Unlock()
		}
		out, herr := env.m.exitWorker(context.Background(), idle, nil, self)
		if herr != nil || !out.Exited() || out.Terminated || !out.Archived {
			t.Fatalf("out=%+v herr=%+v", out, herr)
		}
		if len(env.svc.terminateCalls) != 1 || len(env.svc.ArchiveCalls()) != 1 {
			t.Fatalf("terminate=%d archive=%d", len(env.svc.terminateCalls), len(env.svc.ArchiveCalls()))
		}
	})
	t.Run("B1-c: transfer's ctl, lease now held by a non-pdx principal → held_by, no re-take, no archive", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.enforceLease = true
		env.svc.heldLease = ploom
		env.store.script(rowHeld(ploom))
		released := false
		ctl := &control{LeaseID: "L-t", PrincipalID: self, release: func() { released = true }}
		_, herr := env.m.exitWorker(context.Background(), idle, ctl, self)
		if herr == nil || herr.status != http.StatusConflict || herr.code != "held_by" || herr.detail["principal"] != "ploom:agent-7" {
			t.Fatalf("herr = %+v", herr)
		}
		if len(env.svc.ArchiveCalls()) != 0 || len(env.svc.acquires) != 0 || len(env.svc.terminateCalls) != 1 || released {
			t.Fatalf("archive=%d acquires=%v terminate=%d released=%v", len(env.svc.ArchiveCalls()), env.svc.acquires, len(env.svc.terminateCalls), released)
		}
	})
	t.Run("B1-c': transfer's ctl, lease merely expired → terminate failure, archived", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.enforceLease = true
		env.svc.heldLease = store.Lease{ID: "L-t", PrincipalID: self, ExpiresAt: nowMs() - 1}
		env.store.script(idle)
		ctl := &control{LeaseID: "L-t", PrincipalID: self, release: noRelease}
		out, herr := env.m.exitWorker(context.Background(), idle, ctl, self)
		if herr != nil || !out.Archived || out.Terminated || len(env.svc.acquires) != 0 {
			t.Fatalf("out=%+v herr=%+v acquires=%v", out, herr, env.svc.acquires)
		}
	})

	// B1-d: the guard before an archive that follows a failed (non-lease) terminate.
	guard := []struct {
		name        string
		reread      store.Execution
		wantHeldBy  bool
		wantArchive int
	}{
		{"re-read shows a non-pdx holder → held_by, no archive", rowHeld(ploom), true, 0},
		{"re-read shows no holder → archived", idle, false, 1},
		{"re-read shows a pdx holder → archived", rowHeld(store.Lease{ID: "L-b", PrincipalID: pdxOther, ExpiresAt: nowMs() + 60_000}), false, 1},
		{"re-read shows an expired non-pdx lease → archived", rowHeld(store.Lease{ID: "L-p", PrincipalID: "ploom:agent-7", ExpiresAt: nowMs() - 1}), false, 1},
	}
	for _, c := range guard {
		t.Run("B1-d: "+c.name, func(t *testing.T) {
			env := newTakebackEnv(t)
			env.svc.lease = store.Lease{ID: "L-own"}
			env.svc.terminateErr = execution.ErrInterruptUnconfirmed
			env.store.script(c.reread)
			out, herr := env.m.exitWorker(context.Background(), idle, nil, self)
			if c.wantHeldBy {
				if herr == nil || herr.status != http.StatusConflict || herr.code != "held_by" || herr.detail["principal"] != "ploom:agent-7" {
					t.Fatalf("herr = %+v", herr)
				}
			} else if herr != nil || !out.Exited() || !out.Archived || out.Terminated {
				t.Fatalf("out=%+v herr=%+v", out, herr)
			}
			if n := len(env.svc.ArchiveCalls()); n != c.wantArchive {
				t.Fatalf("archive calls = %d, want %d", n, c.wantArchive)
			}
			if len(env.svc.releases) != 1 || env.svc.releases[0].LeaseID != "L-own" {
				t.Fatalf("releases = %+v", env.svc.releases)
			}
		})
	}
	t.Run("B1-d: guard re-read fails → no archive, the terminate error", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.lease = store.Lease{ID: "L-own"}
		env.svc.terminateErr = errors.New("boom")
		env.store.results = []getResult{{err: errors.New("disk")}}
		_, herr := env.m.exitWorker(context.Background(), idle, nil, self)
		if herr == nil || herr.code != "terminate_failed" {
			t.Fatalf("herr = %+v", herr)
		}
		if n := len(env.svc.ArchiveCalls()); n != 0 {
			t.Fatalf("archive calls = %d, want 0", n)
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
		if len(env.svc.renewCalls) != 1 || env.svc.renewCalls[0].LeaseID != "L-mine" || len(env.svc.releases) != 0 {
			t.Fatalf("caller lease must be validated by a renew and never released: renew=%+v releases=%+v", env.svc.renewCalls, env.svc.releases)
		}
	})
	t.Run("B2-a: stale caller lease on a running row → re-acquired, 200, own lease released", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.results = []getResult{{exec: store.Execution{ID: "E1", State: store.StateRunning}}}
		env.svc.enforceLease = true
		env.svc.heldLease = store.Lease{ID: "L-stale", PrincipalID: "pdx:" + testHostID, ExpiresAt: nowMs() - 1}
		env.svc.lease = store.Lease{ID: "L-new"}
		status, body := exitPost(t, env, "E1", `{"lease_id":"L-stale"}`)
		if status != 200 || body["exited"] != true || body["terminated"] != true || body["archived"] != true {
			t.Fatalf("%d %v", status, body)
		}
		if len(env.svc.renewCalls) != 1 || env.svc.renewCalls[0].LeaseID != "L-stale" {
			t.Fatalf("renew calls = %+v", env.svc.renewCalls)
		}
		if len(env.svc.terminateCalls) != 1 || env.svc.terminateCalls[0].LeaseID != "L-new" {
			t.Fatalf("terminate calls = %+v", env.svc.terminateCalls)
		}
		if len(env.svc.releases) != 1 || env.svc.releases[0].LeaseID != "L-new" {
			t.Fatalf("releases = %+v (the re-acquired lease must be released)", env.svc.releases)
		}
	})
	t.Run("B2-b: idle row held by a non-pdx principal, any caller lease → 409 held_by, nothing changed", func(t *testing.T) {
		env := newTakebackEnv(t)
		ploom := store.Lease{ID: "L-p", PrincipalID: "ploom:agent-7", ExpiresAt: nowMs() + 60_000}
		env.store.results = []getResult{{exec: store.Execution{ID: "E1", State: store.StateIdle, LeaseID: ploom.ID, LeasePrincipalID: ploom.PrincipalID, LeaseExpiresAt: ploom.ExpiresAt}}}
		env.svc.enforceLease = true
		env.svc.heldLease = ploom
		status, body := exitPost(t, env, "E1", `{"lease_id":"L-any"}`)
		if status != 409 || body["code"] != "held_by" || body["principal"] != "ploom:agent-7" {
			t.Fatalf("%d %v", status, body)
		}
		if len(env.svc.terminateCalls) != 0 || len(env.svc.ArchiveCalls()) != 0 {
			t.Fatalf("terminate=%d archive=%d, want 0/0", len(env.svc.terminateCalls), len(env.svc.ArchiveCalls()))
		}
	})
	// #1624 Task 6: exitWorker's own held_by (no caller lease, so the handler
	// renews nothing and exitWorker's takeControl meets the holder) reaches
	// the caller as 409 held_by with the holder.
	t.Run("idle row held by a non-pdx principal, no caller lease → exitWorker's 409 held_by, nothing changed", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.script(store.Execution{ID: "E1", State: store.StateIdle, LeaseID: "L-p", LeasePrincipalID: "ploom:agent-7", LeaseExpiresAt: nowMs() + 60_000})
		env.svc.acquireErr = store.ErrLeaseHeld
		status, body := exitPost(t, env, "E1", ``)
		if status != 409 || body["code"] != "held_by" || body["principal"] != "ploom:agent-7" {
			t.Fatalf("%d %v", status, body)
		}
		if len(env.svc.renewCalls) != 0 || len(env.svc.acquires) != 1 {
			t.Fatalf("renew=%+v acquires=%v, want no renew and exitWorker's one acquire", env.svc.renewCalls, env.svc.acquires)
		}
		if len(env.svc.terminateCalls) != 0 || len(env.svc.ArchiveCalls()) != 0 || len(env.svc.releases) != 0 {
			t.Fatalf("terminate=%d archive=%d releases=%+v, want nothing changed", len(env.svc.terminateCalls), len(env.svc.ArchiveCalls()), env.svc.releases)
		}
	})
	t.Run("B2-c: failed row with a caller lease → no renew, archived, 200", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.results = []getResult{{exec: store.Execution{ID: "E1", State: store.StateFailed}}}
		status, body := exitPost(t, env, "E1", `{"lease_id":"L-mine"}`)
		if status != 200 || body["exited"] != true || body["archived"] != true || body["state"] != "failed" {
			t.Fatalf("%d %v", status, body)
		}
		if len(env.svc.renewCalls) != 0 || len(env.svc.terminateCalls) != 0 || len(env.svc.ArchiveCalls()) != 1 {
			t.Fatalf("renew=%d terminate=%d archive=%d", len(env.svc.renewCalls), len(env.svc.terminateCalls), len(env.svc.ArchiveCalls()))
		}
	})
	t.Run("caller lease renew fails with an infra error → 500 lease_error, nothing changed", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.results = []getResult{{exec: store.Execution{ID: "E1", State: store.StateIdle}}}
		env.svc.renewErr = errors.New("db locked")
		status, body := exitPost(t, env, "E1", `{"lease_id":"L-mine"}`)
		if status != 500 || body["code"] != "lease_error" {
			t.Fatalf("%d %v", status, body)
		}
		if len(env.svc.terminateCalls) != 0 || len(env.svc.ArchiveCalls()) != 0 {
			t.Fatalf("terminate=%d archive=%d", len(env.svc.terminateCalls), len(env.svc.ArchiveCalls()))
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
