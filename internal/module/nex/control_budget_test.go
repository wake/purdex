package nex

// D4 under R-PC-1, bounded in time: exit's whole preempt phase — every pass,
// the release and the re-acquire inside them included — shares one budget
// (exitPreemptBudget, 20 s), each engine call capped at what is left of it.
// Once it is spent the exit stops preempting and falls back to the borrow,
// whose calls keep their own timeouts. Without it the per-call timeouts
// stacked: three passes of acquire, re-read, release, re-acquire (10 + 10 +
// 5 + 10 s) and the borrow made ~125 s, all under the exec lock. These tests
// run the engine slow, at 1:50 of production (engine op 10 s → 200 ms,
// release 5 s → 100 ms, the budget 20 s → 400 ms).

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/store"
)

// stall waits d, or answers ctx's error when it ends (or has ended) first:
// a slow engine call that a deadline cuts.
func stall(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// slowLeases makes the fake's lease calls slow: each stalls for its delay
// before the fake sees it, and a context that ends first is the call's
// answer (the call never reaches the fake, so it is not recorded).
// hangReleases makes the first n releases that land hang until their
// context ends: the engine wrote the release, its answer came too late.
// afterRelease runs after a release that landed, before its answer.
type slowLeases struct {
	*fakeNexService
	acquireDelay, releaseDelay time.Duration
	hangReleases               int
	afterRelease               func()
}

func (s *slowLeases) AcquireLease(ctx context.Context, execID, principal string) (store.Lease, error) {
	if err := stall(ctx, s.acquireDelay); err != nil {
		return store.Lease{}, err
	}
	return s.fakeNexService.AcquireLease(ctx, execID, principal)
}

func (s *slowLeases) ReleaseLease(ctx context.Context, execID, leaseID, principal string) error {
	if err := stall(ctx, s.releaseDelay); err != nil {
		return err
	}
	if err := s.fakeNexService.ReleaseLease(ctx, execID, leaseID, principal); err != nil {
		return err
	}
	if s.hangReleases > 0 {
		s.hangReleases--
		<-ctx.Done()
		return ctx.Err()
	}
	if s.afterRelease != nil {
		s.afterRelease()
	}
	return nil
}

// liveRows answers Get with row carrying the fake's current lease, so a
// re-read sees every hand-over, after delay (a context that ends first is
// the answer).
type liveRows struct {
	*fakeNexStore
	svc   *fakeNexService
	row   store.Execution
	delay time.Duration
}

func (s *liveRows) Get(ctx context.Context, id string) (store.Execution, error) {
	if err := stall(ctx, s.delay); err != nil {
		return store.Execution{}, err
	}
	s.svc.mu.Lock()
	l := s.svc.heldLease
	s.svc.mu.Unlock()
	return withLease(s.row, l), nil
}

func setExitPreemptBudget(t *testing.T, d time.Duration) {
	t.Helper()
	old := exitPreemptBudget
	exitPreemptBudget = d
	t.Cleanup(func() { exitPreemptBudget = old })
}

// slowEnv: preemptEnv's tab2 holding L-b on an idle E, timings at 1:50, and
// the engine slow by the given delays (acquire, re-read, release).
func slowEnv(t *testing.T, acquire, get, release time.Duration) (*takebackEnv, *slowLeases) {
	t.Helper()
	env, _ := preemptEnv(t)
	env.m.engineOpTimeout = 200 * time.Millisecond
	env.m.leaseCleanupTimeout = 100 * time.Millisecond
	setExitPreemptBudget(t, 400*time.Millisecond)
	svc := &slowLeases{fakeNexService: env.svc, acquireDelay: acquire, releaseDelay: release}
	env.m.sys.service = svc
	env.m.sys.store = &liveRows{fakeNexStore: env.store, svc: env.svc, row: store.Execution{ID: "E", State: store.StateIdle}, delay: get}
	return env, svc
}

// assertBounded: the take-control phase took at most the budget plus one
// borrow (an acquire and a re-read at their full timeouts), with slack for
// the scheduler — not passes × per-call timeouts.
func assertBounded(t *testing.T, env *takebackEnv, elapsed time.Duration) {
	t.Helper()
	bound := exitPreemptBudget + 2*env.m.engineOpTimeout + 200*time.Millisecond
	assert.Less(t, elapsed, bound, "the preempt phase shares one budget, then borrows")
	t.Logf("take-control phase: %v at 1:50 (≈ %v in production); bound %v", elapsed.Round(time.Millisecond), (elapsed * 50).Round(time.Second), bound)
}

// assertExitedUnderBorrow: the exit completed — terminated and archived —
// under one control, a pdx holder's borrowed lease, never lease_contended.
func assertExitedUnderBorrow(t *testing.T, env *takebackEnv, out exitOutcome, herr *handoffError) {
	t.Helper()
	const self = "pdx:" + testHostID
	require.Nil(t, herr, "D4: an exit never fails merely because another pdx tab holds control")
	assert.True(t, out.Terminated && out.Archived, "%+v", out)
	require.Len(t, env.svc.terminateCalls, 1)
	term := env.svc.terminateCalls[0]
	assert.True(t, env.m.isPdxPrincipal(term.PrincipalID) && term.PrincipalID != self, "borrowed: a holder's lease, not ours: %+v", term)
	reqs := env.svc.ArchiveReqs()
	require.Len(t, reqs, 1)
	assert.Equal(t, term.LeaseID, reqs[0].LeaseID)
	assert.Equal(t, term.PrincipalID, reqs[0].PrincipalID)
}

func TestExitPreempt_OneBudget(t *testing.T) {
	const self = "pdx:" + testHostID
	ctx := context.Background()
	idle := store.Execution{ID: "E", State: store.StateIdle}

	t.Run("every call slow, every pass lost after our release (the worst stacking) → the phase ends with the budget, the exit completes under the borrow", func(t *testing.T) {
		env, _ := slowEnv(t, 190*time.Millisecond, 190*time.Millisecond, 95*time.Millisecond)
		// Another tab takes the lease between each release of ours and our
		// acquire: every pass runs acquire, re-read, release and re-acquire.
		tab3 := "pdx:" + testHostID + "/tab3"
		n, afterRelease := 0, false
		env.svc.onRecord = func(ev string) {
			switch {
			case ev == "release":
				afterRelease = true
			case ev == "acquire" && afterRelease:
				afterRelease = false
				n++
				env.svc.setHeldLease(liveLease(fmt.Sprintf("L-t%d", n), tab3))
			}
		}
		logs := captureLogs(env.handoffEnv)
		start := time.Now()
		out, herr := env.m.exitWorker(ctx, idle, nil, self)
		assertBounded(t, env, time.Since(start))
		assertExitedUnderBorrow(t, env, out, herr)
		assert.Equal(t, 1, logs.count("borrowing"), "the fall back is logged once: %q", logs.lines)
	})

	t.Run("the holder re-attaches before every release of ours → the exit completes under the borrow within the budget", func(t *testing.T) {
		env, _ := slowEnv(t, 150*time.Millisecond, 150*time.Millisecond, 50*time.Millisecond)
		handOverOnRelease(env, 100)
		start := time.Now()
		out, herr := env.m.exitWorker(ctx, idle, nil, self)
		assertBounded(t, env, time.Since(start))
		assertExitedUnderBorrow(t, env, out, herr)
		assert.Equal(t, env.svc.heldLease.ID, env.svc.terminateCalls[0].LeaseID, "the holder's current lease")
	})

	t.Run("budget spent after our release landed, before our acquire → the fall back acquires the freed lease: an own, valid control", func(t *testing.T) {
		env, svc := slowEnv(t, 0, 0, 0)
		setExitPreemptBudget(t, 50*time.Millisecond)
		svc.afterRelease = func() { time.Sleep(80 * time.Millisecond) } // the release landed; its answer comes after the budget
		logs := captureLogs(env.handoffEnv)
		ctl, herr := env.m.takeControl(ctx, "E", "", self)
		require.Nil(t, herr)
		assert.Equal(t, "L-own", ctl.LeaseID)
		assert.Equal(t, self, ctl.PrincipalID, "the lease was free: acquired, not borrowed")
		assert.NoError(t, env.svc.CheckLease("E", ctl.LeaseID, ctl.PrincipalID), "a control the engine honours")
		assert.Equal(t, []string{"acquire", "release", "acquire"}, env.svc.Calls())
		assert.Equal(t, 1, logs.count("budget"), "the fall back on the spent budget is logged: %q", logs.lines)
		ctl.release()
		assert.Equal(t, []releaseCall{{"E", "L-b", tab2}, {"E", "L-own", self}}, env.svc.releases, "ours to release; nothing leaked")
	})

	t.Run("the release lands but answers only at the budget's end → the fall back acquires the freed lease, never borrows the released one", func(t *testing.T) {
		env, svc := slowEnv(t, 0, 0, 0)
		env.m.leaseCleanupTimeout = 300 * time.Millisecond
		setExitPreemptBudget(t, 100*time.Millisecond)
		svc.hangReleases = 1
		ctl, herr := env.m.takeControl(ctx, "E", "", self)
		require.Nil(t, herr)
		assert.Equal(t, "L-own", ctl.LeaseID)
		assert.Equal(t, self, ctl.PrincipalID)
		assert.NoError(t, env.svc.CheckLease("E", ctl.LeaseID, ctl.PrincipalID), "a control the engine honours, not the released L-b")
		ctl.release()
		assert.Equal(t, []releaseCall{{"E", "L-b", tab2}, {"E", "L-own", self}}, env.svc.releases, "ours to release; nothing leaked")
	})

	t.Run("the acquire running when the budget ends is cut there, and the fall back still answers a non-pdx holder held_by, nothing released", func(t *testing.T) {
		env, _ := slowEnv(t, 120*time.Millisecond, 0, 0)
		setExitPreemptBudget(t, 60*time.Millisecond)
		ploom := liveLease("L-p", "ploom:agent-7")
		env.svc.heldLease = ploom
		_, herr := env.m.takeControl(ctx, "E", "", self)
		require.NotNil(t, herr)
		assert.Equal(t, "held_by", herr.code, "a call the budget cut is not a lease_error")
		assert.Equal(t, "ploom:agent-7", herr.detail["principal"])
		assert.Equal(t, []string{"acquire"}, env.svc.Calls(), "the cut acquire never reached the engine; the fall back's did")
		assert.Empty(t, env.svc.releases)
		assert.Equal(t, ploom, env.svc.heldLease)
	})

	t.Run("a non-pdx holder that takes over on a later pass → held_by", func(t *testing.T) {
		env, _ := slowEnv(t, 0, 0, 0)
		ploom := liveLease("L-p", "ploom:agent-7")
		env.svc.onRecord = func(ev string) {
			if ev == "release" {
				env.svc.setHeldLease(ploom)
			}
		}
		_, herr := env.m.takeControl(ctx, "E", "", self)
		require.NotNil(t, herr)
		assert.Equal(t, "held_by", herr.code)
		assert.Equal(t, "ploom:agent-7", herr.detail["principal"])
		assert.Equal(t, []string{"acquire", "release", "acquire"}, env.svc.Calls())
		assert.Equal(t, ploom, env.svc.heldLease)
	})

	t.Run("a budget spent before the first call → no preempt at all, straight to the borrow", func(t *testing.T) {
		env, lb := preemptEnv(t)
		setExitPreemptBudget(t, time.Nanosecond)
		ctl, herr := env.m.takeControl(ctx, "E", "", self)
		require.Nil(t, herr)
		assert.Equal(t, lb.ID, ctl.LeaseID)
		assert.Equal(t, tab2, ctl.PrincipalID)
		assert.Equal(t, []string{"acquire"}, env.svc.Calls(), "the borrow's one acquire")
		assert.Empty(t, env.svc.releases)
	})

	t.Run("transfers (preemptPdx) are not budgeted: two full passes, then lease_contended", func(t *testing.T) {
		env, lb := preemptEnv(t)
		setExitPreemptBudget(t, time.Nanosecond)
		env.store.script(withLease(idle, lb), withLease(idle, liveLease("L-b2", tab2)))
		handOverOnRelease(env, 2)
		_, herr := env.m.takeControlMode(ctx, "E", "", self, preemptPdx)
		require.NotNil(t, herr)
		assert.Equal(t, "lease_contended", herr.code)
		assert.Equal(t, []string{"acquire", "release", "acquire", "release"}, env.svc.Calls())
	})

	t.Run("…and keep their own per-call timeouts", func(t *testing.T) {
		env, _ := slowEnv(t, 150*time.Millisecond, 0, 0)
		setExitPreemptBudget(t, 50*time.Millisecond)
		env.svc.heldLease = store.Lease{}
		ctl, herr := env.m.takeControlMode(ctx, "E", "", self, preemptPdx)
		require.Nil(t, herr, "a 150 ms acquire under a 200 ms timeout is not cut by exit's budget")
		assert.Equal(t, "L-own", ctl.LeaseID)
	})
}

var _ nexService = (*slowLeases)(nil)
