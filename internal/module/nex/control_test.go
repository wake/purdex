package nex

// D22 (conversation entity spec §4.3, extended by ruling R-PC-1 on
// 2026-10-07): every path in which the daemon takes control itself preempts
// a pdx holder's lease — releases it as the holder, then acquires an
// exclusive one of its own — so the holder cannot write into the worker (a
// send, or an answer to a permission request) before the worker ends.
// Transfers (take-to-terminal, take-back) preempt with preemptPdx; exit,
// and the paths that end a worker through it (Q1, worker-rebuild with a
// replaced row), take control in exit's mode (takeControl): one preempt
// pass more than a transfer, then a fall back to borrowing the holder's
// lease, so an exit never fails on contention (D4). These tests run the
// control helpers over the fake's opt-in lease fence (enforceLease), which
// answers as Nexen's store does.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/store"
)

// tab2 is another Purdex client of this host: a pdx principal the daemon minted.
const tab2 = "pdx:" + testHostID + "/tab2"

// withLease is e with lease l on its row, as the store reports the holder.
func withLease(e store.Execution, l store.Lease) store.Execution {
	e.LeaseID, e.LeasePrincipalID, e.LeaseExpiresAt = l.ID, l.PrincipalID, l.ExpiresAt
	return e
}

func liveLease(id, principal string) store.Lease {
	return store.Lease{ID: id, PrincipalID: principal, ExpiresAt: nowMs() + 60_000}
}

// preemptEnv: tab2 holds L-b on E, the fence is on, and an acquire of ours mints L-own.
func preemptEnv(t *testing.T) (*takebackEnv, store.Lease) {
	t.Helper()
	env := newTakebackEnv(t)
	lb := liveLease("L-b", tab2)
	env.svc.enforceLease = true
	env.svc.heldLease = lb
	env.svc.lease = store.Lease{ID: "L-own"}
	env.store.script(withLease(store.Execution{ID: "E", State: store.StateIdle}, lb))
	return env, lb
}

// handOverOnRelease makes the holder re-attach (a fresh lease id, as
// Nexen's AcquireLease mints one) just before each of the first n of our
// releases lands, so that release finds a different lease id: the race
// between our read of the holder and our release.
func handOverOnRelease(env *takebackEnv, n int) {
	count := 0
	env.svc.onRecord = func(ev string) {
		if ev != "release" || count >= n {
			return
		}
		count++
		env.svc.setHeldLease(liveLease(fmt.Sprintf("L-b%d", count+1), tab2))
	}
}

func TestTakeControlMode_Preempt(t *testing.T) {
	const self = "pdx:" + testHostID
	ctx := context.Background()

	t.Run("pdx holder: released as the holder, then an exclusive lease of our own", func(t *testing.T) {
		env, lb := preemptEnv(t)
		ctl, herr := env.m.takeControlMode(ctx, "E", "", self, preemptPdx)
		require.Nil(t, herr)
		assert.Equal(t, "L-own", ctl.LeaseID)
		assert.Equal(t, self, ctl.PrincipalID, "our principal, not the holder's")
		assert.Equal(t, []string{"acquire", "release", "acquire"}, env.svc.Calls())
		assert.Equal(t, []releaseCall{{"E", lb.ID, tab2}}, env.svc.releases, "released with the holder's lease id and principal")
		assert.Equal(t, []string{self, self}, env.svc.acquires)
		assert.ErrorIs(t, env.svc.CheckLease("E", lb.ID, tab2), store.ErrLeaseMismatch, "the holder's next send fails")

		ctl.release()
		assert.Equal(t, []releaseCall{{"E", lb.ID, tab2}, {"E", "L-own", self}}, env.svc.releases, "the preempted lease is ours: a real release")
		got, err := env.svc.AcquireLease(ctx, "E", tab2)
		require.NoError(t, err, "the original tab can re-attach once we let go")
		assert.Equal(t, tab2, got.PrincipalID)
	})

	t.Run("exit's mode (takeControl) preempts the same holder (R-PC-1)", func(t *testing.T) {
		env, lb := preemptEnv(t)
		ctl, herr := env.m.takeControl(ctx, "E", "", self)
		require.Nil(t, herr)
		assert.Equal(t, "L-own", ctl.LeaseID)
		assert.Equal(t, self, ctl.PrincipalID, "our principal, not the holder's")
		assert.Equal(t, []string{"acquire", "release", "acquire"}, env.svc.Calls())
		assert.Equal(t, []releaseCall{{"E", lb.ID, tab2}}, env.svc.releases, "released with the holder's lease id and principal")
		assert.ErrorIs(t, env.svc.CheckLease("E", lb.ID, tab2), store.ErrLeaseMismatch, "the holder's next send or answer fails")
		ctl.release()
		assert.Equal(t, []releaseCall{{"E", lb.ID, tab2}, {"E", "L-own", self}}, env.svc.releases, "the preempted lease is ours: a real release")
	})

	t.Run("holder re-attached between our read and our release → retried once", func(t *testing.T) {
		env, lb := preemptEnv(t)
		lb2 := liveLease("L-b2", tab2)
		env.store.script(withLease(store.Execution{ID: "E"}, lb), withLease(store.Execution{ID: "E"}, lb2))
		handOverOnRelease(env, 1)
		ctl, herr := env.m.takeControlMode(ctx, "E", "", self, preemptPdx)
		require.Nil(t, herr)
		assert.Equal(t, "L-own", ctl.LeaseID)
		assert.Equal(t, []string{"acquire", "release", "acquire", "release", "acquire"}, env.svc.Calls())
		assert.Equal(t, []releaseCall{{"E", lb.ID, tab2}, {"E", lb2.ID, tab2}}, env.svc.releases)
	})

	t.Run("…and again on the retry → 409 lease_contended with the last holder", func(t *testing.T) {
		env, lb := preemptEnv(t)
		env.store.script(withLease(store.Execution{ID: "E"}, lb), withLease(store.Execution{ID: "E"}, liveLease("L-b2", tab2)))
		handOverOnRelease(env, 2)
		ctl, herr := env.m.takeControlMode(ctx, "E", "", self, preemptPdx)
		require.NotNil(t, herr)
		assert.Equal(t, http.StatusConflict, herr.status)
		assert.Equal(t, "lease_contended", herr.code)
		assert.Equal(t, tab2, herr.detail["principal"])
		require.NotNil(t, ctl.release, "control.release is never nil")
		ctl.release()
		assert.Equal(t, []string{"acquire", "release", "acquire", "release"}, env.svc.Calls(), "no third attempt")
		assert.Equal(t, tab2, env.svc.heldLease.PrincipalID, "the holder keeps what it re-took")
	})

	t.Run("lease taken by another tab between our release and our acquire → retried once, then lease_contended", func(t *testing.T) {
		env, lb := preemptEnv(t)
		tab3 := "pdx:" + testHostID + "/tab3"
		env.store.script(withLease(store.Execution{ID: "E"}, lb), withLease(store.Execution{ID: "E"}, liveLease("L-3", tab3)))
		acquires := 0
		env.svc.onRecord = func(ev string) {
			if ev != "acquire" {
				return
			}
			acquires++
			if acquires%2 == 0 { // the acquire right after each release
				env.svc.setHeldLease(liveLease(fmt.Sprintf("L-%d", acquires+1), tab3))
			}
		}
		_, herr := env.m.takeControlMode(ctx, "E", "", self, preemptPdx)
		require.NotNil(t, herr)
		assert.Equal(t, "lease_contended", herr.code)
		assert.Equal(t, tab3, herr.detail["principal"])
		assert.Equal(t, []string{"acquire", "release", "acquire", "acquire", "release", "acquire"}, env.svc.Calls())
	})

	t.Run("release fails outright → 500 lease_error, nothing acquired after", func(t *testing.T) {
		env, _ := preemptEnv(t)
		env.svc.releaseErr = errors.New("db locked")
		ctl, herr := env.m.takeControlMode(ctx, "E", "", self, preemptPdx)
		require.NotNil(t, herr)
		assert.Equal(t, http.StatusInternalServerError, herr.status)
		assert.Equal(t, "lease_error", herr.code)
		assert.Equal(t, []string{"acquire", "release"}, env.svc.Calls())
		require.NotNil(t, ctl.release)
		ctl.release()
		assert.Len(t, env.svc.releases, 1, "nothing of ours to release")
	})

	t.Run("acquire after the release fails outright → 500 lease_error", func(t *testing.T) {
		env, _ := preemptEnv(t)
		env.svc.onRecord = func(ev string) {
			if ev == "release" {
				env.svc.mu.Lock()
				env.svc.acquireErr = errors.New("db locked")
				env.svc.mu.Unlock()
			}
		}
		ctl, herr := env.m.takeControlMode(ctx, "E", "", self, preemptPdx)
		require.NotNil(t, herr)
		assert.Equal(t, "lease_error", herr.code)
		assert.Equal(t, []string{"acquire", "release", "acquire"}, env.svc.Calls())
		require.NotNil(t, ctl.release)
	})

	t.Run("non-pdx holder → held_by: nothing released, nothing acquired after", func(t *testing.T) {
		env, _ := preemptEnv(t)
		ploom := liveLease("L-p", "ploom:agent-7")
		env.svc.heldLease = ploom
		env.store.script(withLease(store.Execution{ID: "E"}, ploom))
		ctl, herr := env.m.takeControlMode(ctx, "E", "", self, preemptPdx)
		require.NotNil(t, herr)
		assert.Equal(t, "held_by", herr.code)
		assert.Equal(t, "ploom:agent-7", herr.detail["principal"])
		assert.Equal(t, []string{"acquire"}, env.svc.Calls())
		assert.Equal(t, ploom, env.svc.heldLease)
		require.NotNil(t, ctl.release)
	})

	t.Run("caller lease is used as is", func(t *testing.T) {
		env, _ := preemptEnv(t)
		ctl, herr := env.m.takeControlMode(ctx, "E", "L-caller", self, preemptPdx)
		require.Nil(t, herr)
		assert.Equal(t, "L-caller", ctl.LeaseID)
		ctl.release()
		assert.Empty(t, env.svc.Calls())
	})

	t.Run("renewControlMode re-takes a lost lease by preempting", func(t *testing.T) {
		env, lb := preemptEnv(t)
		releasedOld := 0
		got, herr := env.m.renewControlMode(ctx, "E", control{LeaseID: "L-stale", PrincipalID: self, release: func() { releasedOld++ }}, self, preemptPdx)
		require.Nil(t, herr)
		assert.Equal(t, "L-own", got.LeaseID)
		assert.Equal(t, 1, releasedOld)
		assert.Equal(t, []string{"renew", "acquire", "release", "acquire"}, env.svc.Calls())
		assert.Equal(t, []releaseCall{{"E", lb.ID, tab2}}, env.svc.releases)
		got.release()
		assert.Len(t, env.svc.releases, 2, "the re-taken lease is ours to release")
	})

	t.Run("renewControl (exit) re-takes by preempting (R-PC-1)", func(t *testing.T) {
		env, lb := preemptEnv(t)
		got, herr := env.m.renewControl(ctx, "E", control{LeaseID: "L-stale", PrincipalID: self, release: noRelease}, self)
		require.Nil(t, herr)
		assert.Equal(t, "L-own", got.LeaseID)
		assert.Equal(t, self, got.PrincipalID)
		assert.Equal(t, []string{"renew", "acquire", "release", "acquire"}, env.svc.Calls())
		assert.Equal(t, []releaseCall{{"E", lb.ID, tab2}}, env.svc.releases)
		got.release()
		assert.Len(t, env.svc.releases, 2, "the re-taken lease is ours to release")
	})
}

// D4 under R-PC-1: exit's preempt is bounded — the transfer's two passes
// plus one — and then falls back to borrowing the holder's current lease,
// logged, so a pdx tab that keeps re-attaching never makes an exit fail
// with lease_contended (plan Task 5a (f), unit level).
func TestTakeControl_ExitPreemptFallsBackToBorrow(t *testing.T) {
	const self = "pdx:" + testHostID
	ctx := context.Background()
	// heldRows scripts the row reads: tab2 holding each lease id in turn.
	heldRows := func(env *takebackEnv, row store.Execution, ids ...string) {
		var rs []store.Execution
		for _, id := range ids {
			rs = append(rs, withLease(row, liveLease(id, tab2)))
		}
		env.store.script(rs...)
	}
	idle := store.Execution{ID: "E", State: store.StateIdle}

	t.Run("two passes lost, the third preempts", func(t *testing.T) {
		env, _ := preemptEnv(t)
		heldRows(env, idle, "L-b", "L-b2", "L-b3")
		handOverOnRelease(env, 2)
		ctl, herr := env.m.takeControl(ctx, "E", "", self)
		require.Nil(t, herr)
		assert.Equal(t, "L-own", ctl.LeaseID, "a transfer would have answered lease_contended here")
		assert.Equal(t, self, ctl.PrincipalID)
		assert.Equal(t, []string{"acquire", "release", "acquire", "release", "acquire", "release", "acquire"}, env.svc.Calls())
		assert.Equal(t, []releaseCall{{"E", "L-b", tab2}, {"E", "L-b2", tab2}, {"E", "L-b3", tab2}}, env.svc.releases)
		assert.ErrorIs(t, env.svc.CheckLease("E", "L-b3", tab2), store.ErrLeaseMismatch)
	})
	t.Run("every pass lost → the holder's current lease borrowed, logged, never lease_contended", func(t *testing.T) {
		env, _ := preemptEnv(t)
		heldRows(env, idle, "L-b", "L-b2", "L-b3", "L-b4")
		handOverOnRelease(env, 3)
		logs := captureLogs(env.handoffEnv)
		ctl, herr := env.m.takeControl(ctx, "E", "", self)
		require.Nil(t, herr, "an exit never fails merely because another pdx tab holds control (D4)")
		assert.Equal(t, "L-b4", ctl.LeaseID)
		assert.Equal(t, tab2, ctl.PrincipalID, "borrowed: the holder's current lease and principal")
		assert.Equal(t, []string{"acquire", "release", "acquire", "release", "acquire", "release", "acquire"}, env.svc.Calls(),
			"three preempt passes, then the borrow's one acquire")
		ctl.release()
		assert.Len(t, env.svc.releases, 3, "a borrowed lease is never released")
		assert.Equal(t, "L-b4", env.svc.heldLease.ID, "the holder keeps what it re-took")
		assert.Equal(t, 1, logs.count("borrowing"), "the fall back is logged: %q", logs.lines)
	})
	t.Run("renewControl's re-take falls back the same way", func(t *testing.T) {
		env, _ := preemptEnv(t)
		heldRows(env, idle, "L-b", "L-b2", "L-b3", "L-b4")
		handOverOnRelease(env, 3)
		got, herr := env.m.renewControl(ctx, "E", control{LeaseID: "L-stale", PrincipalID: self, release: noRelease}, self)
		require.Nil(t, herr)
		assert.Equal(t, "L-b4", got.LeaseID)
		assert.Equal(t, tab2, got.PrincipalID)
	})
	t.Run("transfers keep two passes and lease_contended", func(t *testing.T) {
		env, _ := preemptEnv(t)
		heldRows(env, idle, "L-b", "L-b2", "L-b3")
		handOverOnRelease(env, 2)
		_, herr := env.m.takeControlMode(ctx, "E", "", self, preemptPdx)
		require.NotNil(t, herr)
		assert.Equal(t, "lease_contended", herr.code)
	})
}

// holderProbe records what a preempted holder's next write meets — Nexen
// checks the lease before every send and every permission answer — probed
// from a fake hook while the ending path holds control and the worker has
// not ended yet. Only the first probe counts.
type holderProbe struct {
	mu  sync.Mutex
	err error
	ran bool
}

func (p *holderProbe) check(svc *fakeNexService, execID string, l store.Lease) {
	err := svc.CheckLease(execID, l.ID, l.PrincipalID)
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.ran {
		p.err, p.ran = err, true
	}
}

// assertRefused: the holder's write was refused as someone else's lease.
func (p *holderProbe) assertRefused(t *testing.T) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	require.True(t, p.ran, "the holder was never probed")
	assert.ErrorIs(t, p.err, store.ErrLeaseMismatch, "the preempted holder's next send or answer is refused")
}

// probeAtTerminate probes l's holder when the terminate is entered.
func probeAtTerminate(svc *fakeNexService, execID string, l store.Lease) *holderProbe {
	p := &holderProbe{}
	svc.onTerminate = func(execution.TerminateRequest) { p.check(svc, execID, l) }
	return p
}

// probeAt probes l's holder when the fake records call (for a row that needs
// no terminate: "renew" or "archive").
func probeAt(svc *fakeNexService, call, execID string, l store.Lease) *holderProbe {
	p := &holderProbe{}
	svc.onRecord = func(name string) {
		if name == call {
			p.check(svc, execID, l)
		}
	}
	return p
}

// D22 under ruling R-PC-1 (replaces D4's "exit borrows"): an exit that
// takes control itself preempts a pdx holder's lease, so from the preempt
// on the holder's next send or permission answer is refused
// (lease_mismatch). A non-pdx holder is refused (held_by) and a caller's or
// a transfer's lease is used as is. Plan Task 5a cases (a), (c)–(h); (b) is
// TestManualResume_PreemptsAPdxHoldersLease and TestWorkerRebuild's
// replace case, (a) over HTTP on the real engine is
// TestExit_RealEngine_PreemptsAnotherTabsLease.
func TestExit_PreemptsPdxHolderLease(t *testing.T) {
	const self = "pdx:" + testHostID
	ctx := context.Background()
	idle := store.Execution{ID: "E", State: store.StateIdle}
	calls := func(t *testing.T, env *takebackEnv, want ...string) {
		t.Helper()
		assert.Equal(t, want, env.svc.Calls())
	}
	archivedUnder := func(t *testing.T, env *takebackEnv, lease, principal string) {
		t.Helper()
		reqs := env.svc.ArchiveReqs()
		require.Len(t, reqs, 1)
		assert.Equal(t, lease, reqs[0].LeaseID)
		assert.Equal(t, principal, reqs[0].PrincipalID)
	}

	t.Run("a: no caller lease → holder's lease released, own acquired, terminate + archive under it, released after; the holder refused from the preempt on", func(t *testing.T) {
		env, lb := preemptEnv(t)
		probe := probeAtTerminate(env.svc, "E", lb)
		out, herr := env.m.exitWorker(ctx, idle, nil, self)
		require.Nil(t, herr)
		assert.True(t, out.Terminated && out.Archived, "%+v", out)
		calls(t, env, "acquire", "release", "acquire", "terminate", "archive", "release")
		assert.Equal(t, []releaseCall{{"E", lb.ID, tab2}, {"E", "L-own", self}}, env.svc.releases)
		assert.Equal(t, []execution.TerminateRequest{{ExecutionID: "E", LeaseID: "L-own", PrincipalID: self}}, env.svc.terminateCalls)
		archivedUnder(t, env, "L-own", self)
		probe.assertRefused(t)
	})
	t.Run("a: the endpoint with no lease preempts the same way", func(t *testing.T) {
		env, lb := preemptEnv(t)
		probe := probeAtTerminate(env.svc, "E", lb)
		status, body := exitPost(t, env, "E", ``)
		require.Equal(t, http.StatusOK, status, "%v", body)
		assert.Equal(t, true, body["exited"])
		calls(t, env, "acquire", "release", "acquire", "terminate", "archive", "release")
		assert.Equal(t, []releaseCall{{"E", lb.ID, tab2}, {"E", "L-own", self}}, env.svc.releases)
		probe.assertRefused(t)
	})
	t.Run("a: the preempted lease lost before the terminate and the holder re-attached → the re-take preempts again", func(t *testing.T) {
		env, lb := preemptEnv(t)
		lb2 := liveLease("L-b2", tab2)
		env.svc.onTerminate = func(req execution.TerminateRequest) {
			if req.LeaseID == "L-own" { // our lease lapsed, the tab re-attached
				env.svc.setHeldLease(lb2)
				env.store.script(withLease(idle, lb2))
				env.svc.mu.Lock()
				env.svc.lease = store.Lease{ID: "L-own2"}
				env.svc.mu.Unlock()
			}
		}
		out, herr := env.m.exitWorker(ctx, idle, nil, self)
		require.Nil(t, herr)
		assert.True(t, out.Terminated && out.Archived, "%+v", out)
		calls(t, env, "acquire", "release", "acquire", "terminate", "release", "acquire", "release", "acquire", "terminate", "archive", "release")
		assert.Equal(t, []releaseCall{{"E", lb.ID, tab2}, {"E", "L-own", self}, {"E", lb2.ID, tab2}, {"E", "L-own2", self}}, env.svc.releases)
		require.Len(t, env.svc.terminateCalls, 2)
		assert.Equal(t, execution.TerminateRequest{ExecutionID: "E", LeaseID: "L-own2", PrincipalID: self}, env.svc.terminateCalls[1])
		archivedUnder(t, env, "L-own2", self)
	})
	t.Run("a: the endpoint re-takes a stale caller lease by preempting", func(t *testing.T) {
		env, lb := preemptEnv(t)
		probe := probeAtTerminate(env.svc, "E", lb)
		status, body := exitPost(t, env, "E", `{"lease_id":"L-stale"}`)
		require.Equal(t, http.StatusOK, status, "%v", body)
		assert.Equal(t, []renewCall{{"E", "L-stale", self}}, env.svc.renewCalls)
		calls(t, env, "renew", "acquire", "release", "acquire", "terminate", "archive", "release")
		assert.Equal(t, []releaseCall{{"E", lb.ID, tab2}, {"E", "L-own", self}}, env.svc.releases)
		assert.Equal(t, []execution.TerminateRequest{{ExecutionID: "E", LeaseID: "L-own", PrincipalID: self}}, env.svc.terminateCalls)
		probe.assertRefused(t)
	})
	t.Run("c: non-pdx holder → held_by, nothing released, nothing else called", func(t *testing.T) {
		env, _ := preemptEnv(t)
		ploom := liveLease("L-p", "ploom:agent-7")
		env.svc.heldLease = ploom
		env.store.script(withLease(idle, ploom))
		out, herr := env.m.exitWorker(ctx, idle, nil, self)
		require.NotNil(t, herr)
		assert.Equal(t, "held_by", herr.code)
		assert.Equal(t, "ploom:agent-7", herr.detail["principal"])
		assert.False(t, out.Exited())
		calls(t, env, "acquire")
		assert.Empty(t, env.svc.releases)
		assert.Equal(t, ploom, env.svc.heldLease)
	})
	t.Run("d: the caller's own live lease → used as is, no acquire, no preempt, never released", func(t *testing.T) {
		env, _ := preemptEnv(t)
		mine := liveLease("L-mine", self)
		env.svc.heldLease = mine
		env.store.script(withLease(idle, mine))
		status, body := exitPost(t, env, "E", `{"lease_id":"L-mine"}`)
		require.Equal(t, http.StatusOK, status, "%v", body)
		calls(t, env, "renew", "terminate", "archive")
		assert.Empty(t, env.svc.releases)
		archivedUnder(t, env, "L-mine", self)
	})

	noTerminate := []struct {
		name string
		row  store.Execution
	}{
		{"terminated, unarchived (the D16 retry)", store.Execution{ID: "E", State: store.StateTerminated}},
		{"failed", store.Execution{ID: "E", State: store.StateFailed}},
		{"rejected", store.Execution{ID: "E", State: store.StateRejected}},
	}
	for _, c := range noTerminate {
		t.Run("e: "+c.name+" → preempted, renewed, archived under the own lease, released after; the holder refused", func(t *testing.T) {
			env, lb := preemptEnv(t)
			env.store.script(withLease(c.row, lb))
			probe := probeAt(env.svc, "archive", "E", lb)
			out, herr := env.m.exitWorker(ctx, c.row, nil, self)
			require.Nil(t, herr)
			assert.True(t, out.Archived, "%+v", out)
			calls(t, env, "acquire", "release", "acquire", "renew", "archive", "release")
			assert.Equal(t, []renewCall{{"E", "L-own", self}}, env.svc.renewCalls)
			assert.Equal(t, []releaseCall{{"E", lb.ID, tab2}, {"E", "L-own", self}}, env.svc.releases)
			archivedUnder(t, env, "L-own", self)
			probe.assertRefused(t)
		})
		t.Run("e: "+c.name+", the preempted lease lost before the renew and the holder re-attached → the re-take preempts again", func(t *testing.T) {
			env, lb := preemptEnv(t)
			env.store.script(withLease(c.row, lb))
			lb2 := liveLease("L-b2", tab2)
			env.svc.onRecord = func(name string) {
				if name == "renew" {
					env.svc.setHeldLease(lb2)
					env.store.script(withLease(c.row, lb2))
					env.svc.mu.Lock()
					env.svc.lease = store.Lease{ID: "L-own2"}
					env.svc.mu.Unlock()
				}
			}
			out, herr := env.m.exitWorker(ctx, c.row, nil, self)
			require.Nil(t, herr)
			assert.True(t, out.Archived, "%+v", out)
			calls(t, env, "acquire", "release", "acquire", "renew", "release", "acquire", "release", "acquire", "archive", "release")
			assert.Equal(t, []releaseCall{{"E", lb.ID, tab2}, {"E", "L-own", self}, {"E", lb2.ID, tab2}, {"E", "L-own2", self}}, env.svc.releases)
			archivedUnder(t, env, "L-own2", self)
		})
	}

	// f: a holder that re-attaches between our read and our release on every
	// pass. heldRows scripts what each read sees.
	heldRows := func(env *takebackEnv, row store.Execution, ids ...string) {
		var rs []store.Execution
		for _, id := range ids {
			rs = append(rs, withLease(row, liveLease(id, tab2)))
		}
		env.store.script(rs...)
	}
	t.Run("f: preempt loses every pass (idle) → exit still completes under the borrowed lease, never lease_contended", func(t *testing.T) {
		env, _ := preemptEnv(t)
		heldRows(env, idle, "L-b", "L-b2", "L-b3", "L-b4")
		handOverOnRelease(env, 3)
		logs := captureLogs(env.handoffEnv)
		out, herr := env.m.exitWorker(ctx, idle, nil, self)
		require.Nil(t, herr, "D4: an exit never fails merely because another pdx tab holds control")
		assert.True(t, out.Terminated && out.Archived, "%+v", out)
		calls(t, env, "acquire", "release", "acquire", "release", "acquire", "release", "acquire", "terminate", "archive")
		assert.Equal(t, []execution.TerminateRequest{{ExecutionID: "E", LeaseID: "L-b4", PrincipalID: tab2}}, env.svc.terminateCalls)
		archivedUnder(t, env, "L-b4", tab2)
		assert.Len(t, env.svc.releases, 3, "the three lost preempts; the borrowed lease is never released")
		assert.Equal(t, 1, logs.count("borrowing"), "%q", logs.lines)
	})
	t.Run("f: preempt loses every pass (failed row, #1665) → archived under the borrowed lease, never lease_contended", func(t *testing.T) {
		env, _ := preemptEnv(t)
		failed := store.Execution{ID: "E", State: store.StateFailed}
		heldRows(env, failed, "L-b", "L-b2", "L-b3", "L-b4")
		handOverOnRelease(env, 3)
		out, herr := env.m.exitWorker(ctx, failed, nil, self)
		require.Nil(t, herr)
		assert.True(t, out.Archived, "%+v", out)
		calls(t, env, "acquire", "release", "acquire", "release", "acquire", "release", "acquire", "renew", "archive")
		assert.Equal(t, []renewCall{{"E", "L-b4", tab2}}, env.svc.renewCalls)
		archivedUnder(t, env, "L-b4", tab2)
	})
	t.Run("g: preempt succeeded but the terminate fails → the taken lease is released, the holder can re-attach, the worker is still live", func(t *testing.T) {
		env, lb := preemptEnv(t)
		running := store.Execution{ID: "E", State: store.StateRunning}
		env.store.script(withLease(running, lb))
		env.svc.terminateErr = execution.ErrInterruptUnconfirmed
		env.svc.archiveErr = execution.ErrArchiveWhileRunning
		out, herr := env.m.exitWorker(ctx, running, nil, self)
		require.NotNil(t, herr)
		assert.Equal(t, "interrupt_unconfirmed", herr.code)
		assert.False(t, out.Exited(), "the worker is still live: %+v", out)
		calls(t, env, "acquire", "release", "acquire", "terminate", "archive", "release")
		assert.Equal(t, []releaseCall{{"E", lb.ID, tab2}, {"E", "L-own", self}}, env.svc.releases, "the lease the exit took is released")
		got, err := env.svc.AcquireLease(ctx, "E", tab2)
		require.NoError(t, err, "the old holder re-attaches")
		assert.Equal(t, tab2, got.PrincipalID)
	})
	t.Run("h: preempt succeeded and the archive is lease-refused (D23) → terminated, not archived; the next exit retries the archive", func(t *testing.T) {
		env, lb := preemptEnv(t)
		lb2 := liveLease("L-b2", tab2)
		first := true
		env.svc.onRecord = func(name string) {
			if name == "archive" && first { // our lease lapsed after the terminate, the tab re-attached
				first = false
				env.svc.setHeldLease(lb2)
			}
		}
		out, herr := env.m.exitWorker(ctx, idle, nil, self)
		require.Nil(t, herr, "the worker is exited")
		assert.True(t, out.Terminated && !out.Archived, "%+v", out)
		archivedUnder(t, env, "L-own", self)

		// The next exit (D16) preempts the re-attached tab and archives.
		terminated := store.Execution{ID: "E", State: store.StateTerminated}
		env.store.script(withLease(terminated, lb2))
		out, herr = env.m.exitWorker(ctx, terminated, nil, self)
		require.Nil(t, herr)
		assert.True(t, out.Terminated && out.Archived, "%+v", out)
		reqs := env.svc.ArchiveReqs()
		require.Len(t, reqs, 2)
		assert.Equal(t, "L-own", reqs[1].LeaseID)
		assert.Equal(t, self, reqs[1].PrincipalID)
		assert.Contains(t, env.svc.releases, releaseCall{"E", lb.ID, tab2})
		assert.Contains(t, env.svc.releases, releaseCall{"E", lb2.ID, tab2})
	})
}

// D4 under R-PC-1, the infra half of the fall back: when releasing a pdx
// holder's lease fails outright (not a lease-class refusal — the store is
// locked, the call timed out), exit's mode borrows that holder's lease and
// logs it, as when the preempt stays contended: an exit never fails because
// of another tab, and the holder's lease is still held, so the borrow can
// act under it. A transfer (preemptPdx) still surfaces the error as 500
// lease_error.
func TestTakeControl_ExitBorrowsWhenReleasingTheHolderFails(t *testing.T) {
	const self = "pdx:" + testHostID
	ctx := context.Background()
	idle := store.Execution{ID: "E", State: store.StateIdle}
	releaseFails := func(t *testing.T) (*takebackEnv, store.Lease, *logSink) {
		t.Helper()
		env, lb := preemptEnv(t)
		env.svc.releaseErr = errors.New("db locked")
		return env, lb, captureLogs(env.handoffEnv)
	}

	t.Run("takeControl → the holder's lease borrowed, logged, never lease_error", func(t *testing.T) {
		env, lb, logs := releaseFails(t)
		ctl, herr := env.m.takeControl(ctx, "E", "", self)
		require.Nil(t, herr, "an exit never fails because releasing another tab's lease failed (D4)")
		assert.Equal(t, lb.ID, ctl.LeaseID)
		assert.Equal(t, tab2, ctl.PrincipalID, "borrowed: the holder's lease and principal")
		assert.Equal(t, []string{"acquire", "release"}, env.svc.Calls(), "no acquire after the failed release")
		ctl.release()
		assert.Len(t, env.svc.releases, 1, "a borrowed lease is never released")
		assert.Equal(t, 1, logs.count("borrowing"), "the fall back is logged: %q", logs.lines)
	})
	t.Run("renewControl's re-take borrows the same way", func(t *testing.T) {
		env, lb, _ := releaseFails(t)
		got, herr := env.m.renewControl(ctx, "E", control{LeaseID: "L-stale", PrincipalID: self, release: noRelease}, self)
		require.Nil(t, herr)
		assert.Equal(t, lb.ID, got.LeaseID)
		assert.Equal(t, tab2, got.PrincipalID)
	})
	t.Run("exitWorker (idle) → terminated and archived under the borrowed lease", func(t *testing.T) {
		env, lb, _ := releaseFails(t)
		out, herr := env.m.exitWorker(ctx, idle, nil, self)
		require.Nil(t, herr)
		assert.True(t, out.Terminated && out.Archived, "%+v", out)
		assert.Equal(t, []string{"acquire", "release", "terminate", "archive"}, env.svc.Calls())
		assert.Equal(t, []execution.TerminateRequest{{ExecutionID: "E", LeaseID: lb.ID, PrincipalID: tab2}}, env.svc.terminateCalls)
		reqs := env.svc.ArchiveReqs()
		require.Len(t, reqs, 1)
		assert.Equal(t, lb.ID, reqs[0].LeaseID)
		assert.Equal(t, tab2, reqs[0].PrincipalID)
	})
	t.Run("exitWorker (failed row, #1665) → renewed, then archived under the borrowed lease", func(t *testing.T) {
		env, lb, _ := releaseFails(t)
		failed := store.Execution{ID: "E", State: store.StateFailed}
		env.store.script(withLease(failed, lb))
		out, herr := env.m.exitWorker(ctx, failed, nil, self)
		require.Nil(t, herr)
		assert.True(t, out.Archived, "%+v", out)
		assert.Equal(t, []string{"acquire", "release", "renew", "archive"}, env.svc.Calls())
		assert.Equal(t, []renewCall{{"E", lb.ID, tab2}}, env.svc.renewCalls)
	})
	t.Run("the exit endpoint with no lease → 200, terminated under the borrowed lease", func(t *testing.T) {
		env, lb, _ := releaseFails(t)
		status, body := exitPost(t, env, "E", ``)
		require.Equal(t, http.StatusOK, status, "%v", body)
		assert.Equal(t, true, body["exited"])
		assert.Equal(t, true, body["terminated"], "not the archive-only path")
		assert.Equal(t, []execution.TerminateRequest{{ExecutionID: "E", LeaseID: lb.ID, PrincipalID: tab2}}, env.svc.terminateCalls)
	})

	// The transfers keep surfacing it (preemptPdx; the unit case is
	// TestTakeControlMode_Preempt's "release fails outright").
	t.Run("take-to-terminal → 500 lease_error, nothing done", func(t *testing.T) {
		env := newTTEnv(t)
		lb := liveLease("L-b", tab2)
		env.svc.enforceLease = true
		env.svc.heldLease = lb
		env.svc.lease = store.Lease{ID: "L-own"}
		env.svc.releaseErr = errors.New("db locked")
		env.store.script(withLease(ttExec(store.StateRunning), lb))
		status, body := env.post(t, tbExecID, ttBody())
		assert.Equal(t, http.StatusInternalServerError, status, "%v", body)
		assert.Equal(t, "lease_error", body["code"])
		assert.Equal(t, []string{"acquire", "release"}, env.timeline(t))
		env.assertNoSession(t)
		assert.Equal(t, lb, env.svc.heldLease, "the holder keeps its lease")
	})
	t.Run("take-back → 500 lease_error, nothing done", func(t *testing.T) {
		env := newTakebackEnv(t)
		tl := env.tbTimeline()
		lb := liveLease("L-b", tab2)
		env.svc.enforceLease = true
		env.svc.heldLease = lb
		env.svc.lease = store.Lease{ID: "L-own"}
		env.svc.releaseErr = errors.New("db locked")
		env.store.script(withLease(runningExec(), lb))
		status, body := env.post(t, hoCode, takebackBody())
		assert.Equal(t, http.StatusInternalServerError, status, "%v", body)
		assert.Equal(t, "lease_error", body["code"])
		assert.Equal(t, []string{"acquire", "release"}, tl.snapshot())
		assert.Empty(t, env.tmux.RawKeysSent())
		assert.Equal(t, lb, env.svc.heldLease, "the holder keeps its lease")
	})
}
