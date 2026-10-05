package nex

// D22 (conversation entity spec §4.3): a transfer (take-to-terminal,
// take-back) preempts a pdx holder's lease — releases it as the holder,
// then acquires an exclusive one of its own — so the holder cannot send
// into the worker between the resume and the exit. Exit (D4) keeps
// borrowing. These tests run the control helpers over the fake's opt-in
// lease fence (enforceLease), which answers as Nexen's store does.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

	t.Run("borrow mode (exit, D4) still borrows the same holder", func(t *testing.T) {
		env, lb := preemptEnv(t)
		ctl, herr := env.m.takeControl(ctx, "E", "", self)
		require.Nil(t, herr)
		assert.Equal(t, lb.ID, ctl.LeaseID)
		assert.Equal(t, tab2, ctl.PrincipalID)
		ctl.release()
		assert.Empty(t, env.svc.releases, "a borrowed lease is never released")
		assert.Equal(t, lb, env.svc.heldLease)
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

	t.Run("renewControl (exit) re-takes by borrowing", func(t *testing.T) {
		env, lb := preemptEnv(t)
		got, herr := env.m.renewControl(ctx, "E", control{LeaseID: "L-stale", PrincipalID: self, release: noRelease}, self)
		require.Nil(t, herr)
		assert.Equal(t, lb.ID, got.LeaseID)
		assert.Equal(t, tab2, got.PrincipalID)
		got.release()
		assert.Empty(t, env.svc.releases)
	})
}

// D4, unchanged by D22: exit terminates under a pdx holder's lease and never
// releases it — neither exitWorker taking control itself nor the endpoint
// re-taking a stale caller lease.
func TestExit_BorrowsPdxHolderLeaseNeverReleasesIt(t *testing.T) {
	const self = "pdx:" + testHostID
	t.Run("exitWorker without a control", func(t *testing.T) {
		env, lb := preemptEnv(t)
		out, herr := env.m.exitWorker(context.Background(), store.Execution{ID: "E", State: store.StateIdle}, nil, self)
		require.Nil(t, herr)
		assert.True(t, out.Exited())
		assert.Equal(t, []execution.TerminateRequest{{ExecutionID: "E", LeaseID: lb.ID, PrincipalID: tab2}}, env.svc.terminateCalls)
		assert.Empty(t, env.svc.releases, "exit never calls ReleaseLease on a holder's lease")
		assert.Equal(t, lb, env.svc.heldLease)
	})
	t.Run("endpoint with a stale caller lease", func(t *testing.T) {
		env, lb := preemptEnv(t)
		status, body := exitPost(t, env, "E", `{"lease_id":"L-stale"}`)
		require.Equal(t, http.StatusOK, status, "%v", body)
		assert.Equal(t, []renewCall{{"E", "L-stale", self}}, env.svc.renewCalls)
		assert.Equal(t, []execution.TerminateRequest{{ExecutionID: "E", LeaseID: lb.ID, PrincipalID: tab2}}, env.svc.terminateCalls)
		assert.Empty(t, env.svc.releases, "exit never calls ReleaseLease on a holder's lease")
		assert.Equal(t, []string{self}, env.svc.acquires, "one acquire (refused), no preempt")
	})
}
