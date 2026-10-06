package nex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	// Archive (v0.18.0, #113): fenced only when it carries a lease; the
	// lease answer comes before archive_while_running.
	arch := func(f *fakeNexService, lease, principal string) error {
		return f.Archive(ctx, execution.ArchiveRequest{ExecutionID: "E", LeaseID: lease, PrincipalID: principal, Archived: true})
	}
	g := &fakeNexService{enforceLease: true, archiveErr: execution.ErrArchiveWhileRunning}
	if err := arch(g, "L", "p"); !errors.Is(err, store.ErrLeaseRequired) {
		t.Fatalf("fenced archive, no lease: %v", err)
	}
	if err := arch(g, "", "p"); !errors.Is(err, execution.ErrArchiveWhileRunning) {
		t.Fatalf("unfenced archive: %v", err)
	}
	g.heldLease = store.Lease{ID: "L", PrincipalID: "p", ExpiresAt: nowMs() + 60_000}
	if err := arch(g, "L", "q"); !errors.Is(err, store.ErrLeaseMismatch) {
		t.Fatalf("fenced archive, other principal: %v", err)
	}
	g.archiveErr = nil
	if err := arch(g, "L", "p"); err != nil {
		t.Fatalf("fenced archive, holder: %v", err)
	}
}

// D23 (Nexen ≥ v0.18.0, nexen#113): when exitWorker terminated the
// execution under a control, the archive that follows carries that same
// control's lease and principal, so Nexen checks the lease in the archive's
// own UPDATE. Only the D4 "terminate failed, archive anyway" archive carries
// no lease; rows that need no terminate and the ErrExecutionTerminal path
// are fenced too (#1665, TestExitWorker_NoTerminateArchiveUnderControl).
func TestExitWorker_ArchiveUnderTerminateLease(t *testing.T) {
	const self = "pdx:" + testHostID
	ctx := context.Background()
	idle := store.Execution{ID: "E", State: store.StateIdle}
	live := func(id, principal string) store.Lease {
		return store.Lease{ID: id, PrincipalID: principal, ExpiresAt: nowMs() + 60_000}
	}
	rowHeld := func(l store.Lease) store.Execution {
		return store.Execution{ID: "E", State: store.StateIdle, LeaseID: l.ID, LeasePrincipalID: l.PrincipalID, LeaseExpiresAt: l.ExpiresAt}
	}
	// onlyArchive is the single archive request exitWorker made.
	onlyArchive := func(t *testing.T, env *takebackEnv) execution.ArchiveRequest {
		t.Helper()
		reqs := env.svc.ArchiveReqs()
		if len(reqs) != 1 {
			t.Fatalf("archive requests = %+v, want exactly one", reqs)
		}
		if !reqs[0].Archived || reqs[0].ExecutionID != "E" {
			t.Fatalf("archive request = %+v", reqs[0])
		}
		return reqs[0]
	}
	// fencedBy asserts the archive carries the lease and principal of the
	// last (the successful) terminate, and that it is the one named.
	fencedBy := func(t *testing.T, env *takebackEnv, lease, principal string) {
		t.Helper()
		req := onlyArchive(t, env)
		term := env.svc.terminateCalls[len(env.svc.terminateCalls)-1]
		if req.LeaseID != term.LeaseID || req.PrincipalID != term.PrincipalID {
			t.Fatalf("archive under %q/%q, terminate under %q/%q: D23 wants the same control", req.LeaseID, req.PrincipalID, term.LeaseID, term.PrincipalID)
		}
		if req.LeaseID != lease || req.PrincipalID != principal {
			t.Fatalf("archive under %q/%q, want %q/%q", req.LeaseID, req.PrincipalID, lease, principal)
		}
	}
	unfenced := func(t *testing.T, env *takebackEnv, principal string) {
		t.Helper()
		if req := onlyArchive(t, env); req.LeaseID != "" || req.PrincipalID != principal {
			t.Fatalf("archive under %q/%q, want no lease and the caller %q", req.LeaseID, req.PrincipalID, principal)
		}
	}
	exitedArchived := func(t *testing.T, out exitOutcome, herr *handoffError) {
		t.Helper()
		if herr != nil || !out.Terminated || !out.Archived || out.State != store.StateTerminated {
			t.Fatalf("out=%+v herr=%+v", out, herr)
		}
	}

	t.Run("a: idle, no caller control → archived under the lease exitWorker took, still held at archive time", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.enforceLease = true
		env.svc.lease = store.Lease{ID: "L-own"}
		out, herr := env.m.exitWorker(ctx, idle, nil, self)
		exitedArchived(t, out, herr)
		fencedBy(t, env, "L-own", self)
		if got, want := strings.Join(env.svc.Calls(), ","), "acquire,terminate,archive,release"; got != want {
			t.Fatalf("calls = %s, want %s (the own lease is released only after the archive)", got, want)
		}
	})
	t.Run("a': idle, a pdx holder's lease borrowed → archived under the holder's lease and principal", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.enforceLease = true
		borrowed := live("L-b", pdxOther)
		env.svc.heldLease = borrowed
		env.store.script(rowHeld(borrowed))
		out, herr := env.m.exitWorker(ctx, idle, nil, self)
		exitedArchived(t, out, herr)
		fencedBy(t, env, "L-b", pdxOther)
	})
	t.Run("b: transfer's control → archived under ctl's lease and principal", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.enforceLease = true
		env.svc.heldLease = live("L-t", pdxOther)
		ctl := &control{LeaseID: "L-t", PrincipalID: pdxOther, release: noRelease}
		out, herr := env.m.exitWorker(ctx, idle, ctl, self)
		exitedArchived(t, out, herr)
		fencedBy(t, env, "L-t", pdxOther)
		if len(env.svc.acquires) != 0 || len(env.svc.releases) != 0 {
			t.Fatalf("acquires=%v releases=%+v: a transfer's control is used as is", env.svc.acquires, env.svc.releases)
		}
	})
	t.Run("c: lease lost before terminate, re-taken → archived under the re-taken lease", func(t *testing.T) {
		env := newTakebackEnv(t)
		borrowed := live("L-b", pdxOther)
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
		out, herr := env.m.exitWorker(ctx, idle, nil, self)
		exitedArchived(t, out, herr)
		if len(env.svc.terminateCalls) != 2 {
			t.Fatalf("terminate calls = %+v", env.svc.terminateCalls)
		}
		fencedBy(t, env, "L-new", self)
		if got, want := strings.Join(env.svc.Calls(), ","), "acquire,terminate,acquire,terminate,archive,release"; got != want {
			t.Fatalf("calls = %s, want %s", got, want)
		}
	})

	// d: rows that need no terminate, and the ErrExecutionTerminal path, are
	// fenced too (#1665): TestExitWorker_NoTerminateArchiveUnderControl.

	t.Run("e: terminate failed (D4, archive anyway) → unfenced archive", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.enforceLease = true
		env.svc.lease = store.Lease{ID: "L-own"}
		env.svc.terminateErr = execution.ErrTerminateContended
		env.store.script(idle)
		out, herr := env.m.exitWorker(ctx, idle, nil, self)
		if herr != nil || !out.Archived || out.Terminated {
			t.Fatalf("out=%+v herr=%+v", out, herr)
		}
		unfenced(t, env, self)
	})
	t.Run("e: lease lost and the re-take failed (D4, archive anyway) → unfenced archive", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.enforceLease = true
		env.svc.lease = store.Lease{ID: "L-own"}
		env.svc.onTerminate = func(execution.TerminateRequest) {
			env.svc.setHeldLease(store.Lease{})
			env.svc.mu.Lock()
			env.svc.acquireErr = errors.New("db locked")
			env.svc.mu.Unlock()
		}
		out, herr := env.m.exitWorker(ctx, idle, nil, self)
		if herr != nil || !out.Archived || out.Terminated {
			t.Fatalf("out=%+v herr=%+v", out, herr)
		}
		unfenced(t, env, self)
	})

	// f: the lease changes hands between the terminate and the archive (the
	// window #113 closes). onRecord fires inside Archive, before its fence.
	handovers := []struct {
		name string
		to   store.Lease
		want error
	}{
		{"handed to a non-pdx principal", live("L-p", "ploom:agent-7"), store.ErrLeaseMismatch},
		{"expired", store.Lease{ID: "L-own", PrincipalID: self, ExpiresAt: nowMs() - 1}, store.ErrLeaseExpired},
		{"released", store.Lease{}, store.ErrLeaseRequired},
	}
	for _, h := range handovers {
		t.Run("f: terminated, lease "+h.name+" before the archive → fenced archive refused, exited (terminated, not archived)", func(t *testing.T) {
			env := newTakebackEnv(t)
			env.svc.enforceLease = true
			env.svc.lease = store.Lease{ID: "L-own"}
			env.svc.onRecord = func(name string) {
				if name == "archive" {
					env.svc.setHeldLease(h.to)
				}
			}
			var logs []string
			env.m.logf = func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }
			out, herr := env.m.exitWorker(ctx, idle, nil, self)
			if herr != nil {
				t.Fatalf("herr = %+v, want none: the worker is exited", herr)
			}
			if !out.Exited() || !out.Terminated || out.Archived || out.State != store.StateTerminated {
				t.Fatalf("out = %+v, want terminated, not archived", out)
			}
			fencedBy(t, env, "L-own", self)
			// Logged as an archive failure after the terminate, with the fence's lease error.
			logged := false
			for _, l := range logs {
				if strings.Contains(l, "archive after terminate") && strings.Contains(l, h.want.Error()) {
					logged = true
				}
			}
			if !logged {
				t.Fatalf("no 'archive after terminate' log with %q: %q", h.want, logs)
			}
		})
	}

	t.Run("endpoint: the caller's lease fences the archive", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.results = []getResult{{exec: store.Execution{ID: "E", State: store.StateIdle}}}
		env.svc.enforceLease = true
		env.svc.heldLease = live("L-mine", self)
		status, body := exitPost(t, env, "E", `{"lease_id":"L-mine"}`)
		if status != 200 || body["terminated"] != true || body["archived"] != true {
			t.Fatalf("%d %v", status, body)
		}
		fencedBy(t, env, "L-mine", self)
	})
}

// #1665 (extends D23): every exit archive runs under a control. A row that
// needs no terminate — terminated but unarchived (the D16 retry), failed,
// rejected — takes control before the archive: the caller's control, else
// takeControl (a pdx holder's lease borrowed, a non-pdx holder refused with
// held_by and nothing changed, any other error fails closed). The archive
// carries that control's lease and principal; a lease exitWorker acquired
// itself is released after it. The ErrExecutionTerminal path archives under
// the control the terminate ran under. Only the D4 "terminate failed,
// archive anyway" path stays unfenced (the e: cases above).
func TestExitWorker_NoTerminateArchiveUnderControl(t *testing.T) {
	const self = "pdx:" + testHostID
	ctx := context.Background()
	live := func(id, principal string) store.Lease {
		return store.Lease{ID: id, PrincipalID: principal, ExpiresAt: nowMs() + 60_000}
	}
	withLease := func(e store.Execution, l store.Lease) store.Execution {
		e.LeaseID, e.LeasePrincipalID, e.LeaseExpiresAt = l.ID, l.PrincipalID, l.ExpiresAt
		return e
	}
	ploom := live("L-p", "ploom:agent-7")
	// archivedUnder asserts exitWorker made exactly one archive request, under
	// the named lease and principal.
	archivedUnder := func(t *testing.T, env *takebackEnv, lease, principal string) {
		t.Helper()
		reqs := env.svc.ArchiveReqs()
		if len(reqs) != 1 || !reqs[0].Archived || reqs[0].ExecutionID != "E" {
			t.Fatalf("archive requests = %+v, want exactly one archive of E", reqs)
		}
		if reqs[0].LeaseID != lease || reqs[0].PrincipalID != principal {
			t.Fatalf("archive under %q/%q, want %q/%q", reqs[0].LeaseID, reqs[0].PrincipalID, lease, principal)
		}
	}
	calls := func(t *testing.T, env *takebackEnv, want string) {
		t.Helper()
		if got := strings.Join(env.svc.Calls(), ","); got != want {
			t.Fatalf("calls = %s, want %s", got, want)
		}
	}

	rows := []struct {
		name string
		row  store.Execution
	}{
		{"terminated, unarchived (the D16 retry)", store.Execution{ID: "E", State: store.StateTerminated}},
		{"failed", store.Execution{ID: "E", State: store.StateFailed}},
		{"rejected", store.Execution{ID: "E", State: store.StateRejected}},
	}
	// renewedUnder asserts exitWorker renewed exactly once, under the named
	// lease and principal (#1665: the taken control is renewed before the
	// archive, so a borrowed lease near its end cannot expire under it).
	renewedUnder := func(t *testing.T, env *takebackEnv, lease, principal string) {
		t.Helper()
		if len(env.svc.renewCalls) != 1 || env.svc.renewCalls[0].LeaseID != lease || env.svc.renewCalls[0].PrincipalID != principal {
			t.Fatalf("renews = %+v, want exactly one under %q/%q", env.svc.renewCalls, lease, principal)
		}
	}
	for _, c := range rows {
		terminated := c.row.State == store.StateTerminated
		t.Run(c.name+": no holder → own lease acquired, renewed, archived under it, released after the archive", func(t *testing.T) {
			env := newTakebackEnv(t)
			env.svc.enforceLease = true
			env.svc.lease = store.Lease{ID: "L-own"}
			out, herr := env.m.exitWorker(ctx, c.row, nil, self)
			if herr != nil || !out.Archived || out.Terminated != terminated || out.State != c.row.State {
				t.Fatalf("out=%+v herr=%+v", out, herr)
			}
			archivedUnder(t, env, "L-own", self)
			renewedUnder(t, env, "L-own", self)
			calls(t, env, "acquire,renew,archive,release")
			if r := env.svc.releases[0]; r.LeaseID != "L-own" || r.PrincipalID != self {
				t.Fatalf("released %+v, want the own lease L-own", r)
			}
		})
		t.Run(c.name+": pdx holder → its lease borrowed and renewed, archived under the holder's lease and principal, never released", func(t *testing.T) {
			env := newTakebackEnv(t)
			env.svc.enforceLease = true
			borrowed := live("L-b", pdxOther)
			env.svc.heldLease = borrowed
			env.store.script(withLease(c.row, borrowed))
			out, herr := env.m.exitWorker(ctx, c.row, nil, self)
			if herr != nil || !out.Archived {
				t.Fatalf("out=%+v herr=%+v", out, herr)
			}
			archivedUnder(t, env, "L-b", pdxOther)
			renewedUnder(t, env, "L-b", pdxOther)
			calls(t, env, "acquire,renew,archive")
		})
		t.Run(c.name+": borrowed pdx lease expires between the take and the archive → re-taken as our own, archived under it, released", func(t *testing.T) {
			env := newTakebackEnv(t)
			env.svc.enforceLease = true
			env.svc.lease = store.Lease{ID: "L-own"}
			borrowed := live("L-b", pdxOther)
			env.svc.heldLease = borrowed
			env.store.script(withLease(c.row, borrowed))
			env.svc.onRecord = func(name string) {
				if name == "renew" {
					expired := borrowed
					expired.ExpiresAt = nowMs() - 1
					env.svc.setHeldLease(expired) // the other tab stopped renewing
				}
			}
			out, herr := env.m.exitWorker(ctx, c.row, nil, self)
			if herr != nil || !out.Archived || out.Terminated != terminated {
				t.Fatalf("out=%+v herr=%+v", out, herr)
			}
			renewedUnder(t, env, "L-b", pdxOther)
			archivedUnder(t, env, "L-own", self)
			calls(t, env, "acquire,renew,acquire,archive,release")
			if len(env.svc.releases) != 1 || env.svc.releases[0].LeaseID != "L-own" {
				t.Fatalf("releases = %+v, want only the re-taken own lease", env.svc.releases)
			}
		})
		t.Run(c.name+": a non-pdx principal takes the lease between the take and the renew → held_by, nothing archived", func(t *testing.T) {
			env := newTakebackEnv(t)
			env.svc.enforceLease = true
			borrowed := live("L-b", pdxOther)
			env.svc.heldLease = borrowed
			env.store.script(withLease(c.row, borrowed))
			env.svc.onRecord = func(name string) {
				if name == "renew" {
					env.svc.setHeldLease(ploom)
					env.store.script(withLease(c.row, ploom))
				}
			}
			out, herr := env.m.exitWorker(ctx, c.row, nil, self)
			if herr == nil || herr.status != http.StatusConflict || herr.code != "held_by" || herr.detail["principal"] != "ploom:agent-7" {
				t.Fatalf("herr = %+v", herr)
			}
			if out.Archived {
				t.Fatalf("out = %+v, want not archived", out)
			}
			calls(t, env, "acquire,renew,acquire")
		})
		t.Run(c.name+": renew fails with an infra error → lease_error, nothing archived, own lease released", func(t *testing.T) {
			env := newTakebackEnv(t)
			env.svc.enforceLease = true
			env.svc.lease = store.Lease{ID: "L-own"}
			env.svc.renewErr = errors.New("db locked")
			out, herr := env.m.exitWorker(ctx, c.row, nil, self)
			if herr == nil || herr.code != "lease_error" || out.Archived {
				t.Fatalf("out=%+v herr=%+v", out, herr)
			}
			calls(t, env, "acquire,renew,release")
		})
		t.Run(c.name+": non-pdx holder → held_by, nothing archived (D4)", func(t *testing.T) {
			env := newTakebackEnv(t)
			env.svc.enforceLease = true
			env.svc.heldLease = ploom
			env.store.script(withLease(c.row, ploom))
			out, herr := env.m.exitWorker(ctx, c.row, nil, self)
			if herr == nil || herr.status != http.StatusConflict || herr.code != "held_by" || herr.detail["principal"] != "ploom:agent-7" {
				t.Fatalf("herr = %+v", herr)
			}
			if out.Archived || out.State != c.row.State {
				t.Fatalf("out = %+v, want unchanged", out)
			}
			calls(t, env, "acquire")
		})
		// A fenced archive refused with a lease error: a terminated row is
		// still exited (the next exit retries the archive, D16); a failed or
		// rejected row answers 409 — the holder's held_by when the re-read
		// finds a live non-pdx holder, else lease_contended — never a 500.
		handedOver := []struct {
			name   string
			reread func(env *takebackEnv) // the row heldByOther re-reads after the refusal
			code   string
		}{
			{"to a non-pdx principal → 409 held_by", func(env *takebackEnv) { env.store.script(withLease(c.row, ploom)) }, "held_by"},
			{"to another pdx client → 409 lease_contended", func(env *takebackEnv) { env.store.script(withLease(c.row, live("L-o", pdxOther))) }, "lease_contended"},
			{"and the re-read fails → 409 lease_contended", func(env *takebackEnv) {
				env.store.mu.Lock()
				env.store.results, env.store.calls = []getResult{{err: errors.New("disk")}}, 0
				env.store.mu.Unlock()
			}, "lease_contended"},
		}
		for _, h := range handedOver {
			t.Run(c.name+": lease handed over between the renew and the archive "+h.name+", nothing archived", func(t *testing.T) {
				env := newTakebackEnv(t)
				env.svc.enforceLease = true
				env.svc.lease = store.Lease{ID: "L-own"}
				env.svc.onRecord = func(name string) {
					if name == "archive" {
						if h.code == "held_by" {
							env.svc.setHeldLease(ploom)
						} else {
							env.svc.setHeldLease(live("L-o", pdxOther))
						}
						h.reread(env)
					}
				}
				out, herr := env.m.exitWorker(ctx, c.row, nil, self)
				if out.Archived {
					t.Fatalf("out = %+v, want not archived", out)
				}
				if terminated {
					// Still exited (terminated); the next exit retries the archive (D16).
					if herr != nil || !out.Exited() || !out.Terminated {
						t.Fatalf("out=%+v herr=%+v, want exited, not archived", out, herr)
					}
				} else {
					if herr == nil || herr.status != http.StatusConflict || herr.code != h.code {
						t.Fatalf("herr = %+v, want 409 %s", herr, h.code)
					}
					if h.code == "held_by" && herr.detail["principal"] != "ploom:agent-7" {
						t.Fatalf("held_by detail = %+v", herr.detail)
					}
					if h.code == "lease_contended" && herr.detail["execution_id"] != "E" {
						t.Fatalf("lease_contended detail = %+v", herr.detail)
					}
					if out.Exited() {
						t.Fatalf("out = %+v, want not exited", out)
					}
				}
				archivedUnder(t, env, "L-own", self)
				calls(t, env, "acquire,renew,archive,release")
			})
		}
		t.Run(c.name+": take fails with an infra error → that error, nothing archived (fail closed)", func(t *testing.T) {
			env := newTakebackEnv(t)
			env.svc.enforceLease = true
			env.svc.acquireErr = errors.New("db locked")
			out, herr := env.m.exitWorker(ctx, c.row, nil, self)
			if herr == nil || herr.code != "lease_error" || out.Archived {
				t.Fatalf("out=%+v herr=%+v", out, herr)
			}
			calls(t, env, "acquire")
		})
	}

	t.Run("a transfer's control: archived under it, nothing acquired, never renewed or released", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.enforceLease = true
		env.svc.heldLease = live("L-t", self)
		released := false
		ctl := &control{LeaseID: "L-t", PrincipalID: self, release: func() { released = true }}
		out, herr := env.m.exitWorker(ctx, store.Execution{ID: "E", State: store.StateFailed}, ctl, self)
		if herr != nil || !out.Archived {
			t.Fatalf("out=%+v herr=%+v", out, herr)
		}
		archivedUnder(t, env, "L-t", self)
		calls(t, env, "archive")
		if released {
			t.Fatal("a transfer's control is the transfer's to release")
		}
	})
	t.Run("already archived → no control taken, no engine call", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.enforceLease = true
		out, herr := env.m.exitWorker(ctx, store.Execution{ID: "E", State: store.StateFailed, ArchivedAt: 9}, nil, self)
		if herr != nil || !out.Archived {
			t.Fatalf("out=%+v herr=%+v", out, herr)
		}
		calls(t, env, "")
	})

	idle := store.Execution{ID: "E", State: store.StateIdle}
	t.Run("ErrExecutionTerminal → archived under the control the terminate ran under", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.enforceLease = true
		env.svc.lease = store.Lease{ID: "L-own"}
		env.svc.terminateErr = store.ErrExecutionTerminal
		env.store.script(store.Execution{ID: "E", State: store.StateFailed})
		out, herr := env.m.exitWorker(ctx, idle, nil, self)
		if herr != nil || !out.Archived || out.Terminated || out.State != store.StateFailed {
			t.Fatalf("out=%+v herr=%+v", out, herr)
		}
		archivedUnder(t, env, "L-own", self)
		calls(t, env, "acquire,terminate,archive,release")
	})
	t.Run("ErrExecutionTerminal, lease handed to a non-pdx principal before the archive → 409 held_by, nothing archived", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.svc.enforceLease = true
		env.svc.lease = store.Lease{ID: "L-own"}
		env.svc.terminateErr = store.ErrExecutionTerminal
		failed := store.Execution{ID: "E", State: store.StateFailed}
		env.store.script(failed)
		env.svc.onRecord = func(name string) {
			if name == "archive" {
				env.svc.setHeldLease(ploom)
				env.store.script(withLease(failed, ploom))
			}
		}
		out, herr := env.m.exitWorker(ctx, idle, nil, self)
		if herr == nil || herr.status != http.StatusConflict || herr.code != "held_by" || herr.detail["principal"] != "ploom:agent-7" || out.Archived {
			t.Fatalf("out=%+v herr=%+v", out, herr)
		}
		archivedUnder(t, env, "L-own", self)
	})
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
	// #1665: the D16 retry (a terminated, unarchived row) takes control
	// before its archive, so a non-pdx holder is refused like on any exit.
	t.Run("terminated, unarchived row held by a non-pdx principal → 409 held_by, nothing archived", func(t *testing.T) {
		env := newTakebackEnv(t)
		ploom := store.Lease{ID: "L-p", PrincipalID: "ploom:agent-7", ExpiresAt: nowMs() + 60_000}
		env.store.script(store.Execution{ID: "E1", State: store.StateTerminated, LeaseID: ploom.ID, LeasePrincipalID: ploom.PrincipalID, LeaseExpiresAt: ploom.ExpiresAt})
		env.svc.enforceLease = true
		env.svc.heldLease = ploom
		status, body := exitPost(t, env, "E1", ``)
		if status != 409 || body["code"] != "held_by" || body["principal"] != "ploom:agent-7" {
			t.Fatalf("%d %v", status, body)
		}
		if len(env.svc.ArchiveCalls()) != 0 || len(env.svc.terminateCalls) != 0 || len(env.svc.releases) != 0 {
			t.Fatalf("archive=%d terminate=%d releases=%+v, want nothing changed", len(env.svc.ArchiveCalls()), len(env.svc.terminateCalls), env.svc.releases)
		}
	})
	// #1665: a failed row whose fenced archive loses a lease race answers
	// 409 (retryable), never the generic 500 archive_failed.
	t.Run("failed row, lease changes hands at the archive → 409 lease_contended, nothing archived", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.results = []getResult{{exec: store.Execution{ID: "E1", State: store.StateFailed}}}
		env.svc.enforceLease = true
		env.svc.onRecord = func(name string) {
			if name == "archive" {
				env.svc.setHeldLease(store.Lease{ID: "L-o", PrincipalID: pdxOther, ExpiresAt: nowMs() + 60_000})
			}
		}
		status, body := exitPost(t, env, "E1", ``)
		if status != 409 || body["code"] != "lease_contended" || body["execution_id"] != "E1" {
			t.Fatalf("%d %v", status, body)
		}
	})
	t.Run("B2-c: failed row with a caller lease → the caller's lease is not used, archived under the exit's own, 200", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.store.results = []getResult{{exec: store.Execution{ID: "E1", State: store.StateFailed}}}
		status, body := exitPost(t, env, "E1", `{"lease_id":"L-mine"}`)
		if status != 200 || body["exited"] != true || body["archived"] != true || body["state"] != "failed" {
			t.Fatalf("%d %v", status, body)
		}
		if len(env.svc.terminateCalls) != 0 || len(env.svc.ArchiveCalls()) != 1 {
			t.Fatalf("terminate=%d archive=%d", len(env.svc.terminateCalls), len(env.svc.ArchiveCalls()))
		}
		// #1665: exitWorker takes (and renews) its own control for this
		// archive; the caller's lease is neither renewed nor archived under.
		for _, r := range env.svc.renewCalls {
			if r.LeaseID == "L-mine" {
				t.Fatalf("renews = %+v, the caller's lease must not be renewed", env.svc.renewCalls)
			}
		}
		if reqs := env.svc.ArchiveReqs(); reqs[0].LeaseID != tbLeaseID {
			t.Fatalf("archive under %q, want the exit's own %q", reqs[0].LeaseID, tbLeaseID)
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
