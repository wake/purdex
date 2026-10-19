package peers

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The rebuild reads the team module's open ops with the pool lock held (1f's review of A2). A reader that calls back into the name
// service waits for that same lock, so the read is bounded: past reservationReadTimeout it is given up on and the lock is
// released, whatever the reader does with its context.

type reentrantReader struct {
	call  func() // runs inside the first OpenReservations only (a later rebuild attempt finds it done)
	once  sync.Once
	done  chan struct{}
	calls atomic.Int32
}

func (r *reentrantReader) OpenReservations(context.Context) ([]ipeers.Reservation, error) {
	r.calls.Add(1)
	r.once.Do(func() {
		defer close(r.done)
		r.call()
	})
	return nil, nil
}

func reentrantEnv(t *testing.T, call func(e *poolEnv)) (*poolEnv, *reentrantReader) {
	t.Helper()
	e := reserveEnv(t)
	e.m.reservationReadTimeout = 50 * time.Millisecond
	rd := &reentrantReader{done: make(chan struct{})}
	rd.call = func() { call(e) }
	c := newTestCoreWithHosts(t, "host-mlab", "mlab", nil)
	c.Registry.Register(team.SpawnReservationsKey, rd)
	e.m.core = c
	return e, rd
}

// finishesIn fails the test, not the whole run, when f does not return in time.
func finishesIn(t *testing.T, d time.Duration, what string, f func()) {
	t.Helper()
	fin := make(chan struct{})
	go func() { f(); close(fin) }()
	select {
	case <-fin:
	case <-time.After(d):
		t.Fatalf("%s did not return within %v (the pool lock is held by a reader)", what, d)
	}
}

func TestReservations_AReaderThatCallsTheNameServiceDoesNotHoldThePoolLock(t *testing.T) {
	// the reader ignores its context (Background): only the caller's give-up frees the lock
	e, rd := reentrantEnv(t, func(e *poolEnv) {
		_, _ = e.m.ReserveMember(context.Background(), poolSidA, "op-inner", "tm-inner", tNow)
	})
	var code string
	finishesIn(t, 5*time.Second, "ReserveMember", func() {
		var err error
		code, err = e.m.ReserveMember(context.Background(), poolSidA, "op-1", "tm-1", tNow)
		require.NoError(t, err)
	})
	assert.NotEmpty(t, code, "the reservation itself goes on without the rebuild")
	assert.True(t, e.logged("did not finish"), "the give-up is logged")
	select {
	case <-rd.done: // the abandoned reader got the lock after the caller let it go and ended
	case <-time.After(5 * time.Second):
		t.Fatal("the abandoned reader never ended")
	}
}

func TestReservations_AReaderThatNeverReturnsIsGivenUpAndTriedAgainAtTheNextUse(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	e, rd := reentrantEnv(t, func(e *poolEnv) {})
	rd.call = func() { <-block }
	finishesIn(t, 5*time.Second, "ReserveExact", func() {
		assert.NoError(t, e.m.ReserveExact(context.Background(), "mia", "op-1", "tm-1", tNow))
	})
	e.m.reservations.mu.Lock()
	rebuilt := e.m.reservations.rebuilt
	e.m.reservations.mu.Unlock()
	assert.False(t, rebuilt, "a rebuild that timed out is tried again at the next use")
}

// codex R2 on #3524: a read given up on is not started again while it is still running, or a reader that never returns would
// leave one goroutine per naming. Mutation gate: no in-flight guard → red.
func TestReservations_AReadStillRunningIsNotStartedTwice(t *testing.T) {
	block := make(chan struct{})
	e, rd := reentrantEnv(t, func(e *poolEnv) {})
	rd.call = func() { <-block }
	for i, id := range []string{"op-1", "op-2", "op-1"} {
		finishesIn(t, 5*time.Second, "ReserveExact", func() {
			assert.NoError(t, e.m.ReserveExact(context.Background(), []string{"mia", "anna", "mia"}[i], id, "tm-"+id, tNow))
		})
	}
	assert.EqualValues(t, 1, rd.calls.Load(), "the reader was started once, not once per naming")
	close(block)
	select {
	case <-rd.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the reader never ended")
	}
	// the stuck read ended: the next naming reads again
	deadline := time.After(5 * time.Second)
	for e.m.reservations.reading() {
		select {
		case <-deadline:
			t.Fatal("the in-flight mark was never cleared")
		case <-time.After(5 * time.Millisecond):
		}
	}
	assert.NoError(t, e.m.ReserveExact(context.Background(), "mia", "op-1", "tm-op-1", tNow), "the same op again: a success")
	assert.EqualValues(t, 2, rd.calls.Load())
}
