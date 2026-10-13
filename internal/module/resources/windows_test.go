package resourcesmod

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	iagent "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/resources"
)

const window = resources.PositionWindow

func (f *posFix) now() time.Time { return f.clock.now() }

func (f *posFix) sweep() { f.m.sweepOnce(context.Background()) }

func (f *posFix) deliver(posID string, at time.Time) {
	f.t.Helper()
	p := f.position(posID)
	ok, err := f.m.store.MarkDelivered(posID, p.OfferedAt, at.UnixMilli())
	require.NoError(f.t, err)
	require.True(f.t, ok)
}

// A offered, B waiting behind it (held back by a rising trend, so B is not
// offered at once). Returns both position ids.
func (f *posFix) offeredWithAWaiter() (posA, posB string) {
	f.t.Helper()
	f.setZone("danger", "flat")
	a := decodeLease(f.t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	f.advance(time.Second)
	b := decodeLease(f.t, f.postAs(newCID(), pidB, resources.CallQueue, "go test"))
	f.setZone("normal", "flat")
	f.pass()
	require.Equal(f.t, resources.PosOffered, f.position(a.PositionID).State)
	f.setZone("normal", "rising") // B waits behind a reserved host
	f.pass()
	require.Equal(f.t, resources.PosQueued, f.position(b.PositionID).State)
	return a.PositionID, b.PositionID
}

// The window counts from delivery: offered t0, delivered t0+1:30 → still offered
// at t0+4:29.999, no_answer at t0+4:30; the next head is offered.
// (M: start the window at offered_at.)
func TestWindow_OfferWindowCountsFromDelivery(t *testing.T) {
	f := newPosFix(t)
	posA, posB := f.offeredWithAWaiter()
	f.advance(90 * time.Second)
	f.deliver(posA, f.now())
	f.advance(2*time.Minute + 59*time.Second + 999*time.Millisecond) // t0 + 4:29.999
	f.sweep()
	assert.Equal(t, resources.PosOffered, f.position(posA).State)
	f.advance(time.Millisecond)
	f.sweep()
	a := f.position(posA)
	assert.Equal(t, resources.PosEnded, a.State)
	assert.Equal(t, resources.EndNoAnswer, a.EndReason)
	rows, err := f.m.store.db.Query(`SELECT end_reason FROM resource_leases WHERE position_id = ?`, posA)
	require.NoError(t, err)
	defer rows.Close()
	n := 0
	for rows.Next() {
		var reason string
		require.NoError(t, rows.Scan(&reason))
		assert.Equal(t, resources.EndNoAnswer, reason)
		n++
	}
	assert.Equal(t, 1, n)
	f.pass()
	assert.Equal(t, resources.PosOffered, f.position(posB).State, "the next head is offered")
}

// An offer that was never delivered passes after 3 minutes: the position goes back
// to queued, first in line, and the next head is offered in that pass.
// (M: end it no_answer instead.)
func TestWindow_UndeliveredOfferPasses(t *testing.T) {
	f := newPosFix(t)
	posA, posB := f.offeredWithAWaiter()
	f.advance(window - time.Millisecond)
	f.sweep()
	assert.Equal(t, resources.PosOffered, f.position(posA).State, "2:59.999")
	f.advance(time.Millisecond)
	f.sweep()
	a := f.position(posA)
	assert.Equal(t, resources.PosQueued, a.State, "3:00.000: back in the queue, not ended")
	assert.Equal(t, 1, a.OfferPasses)
	assert.Equal(t, f.nowMS(), a.PassedAt)
	f.pass()
	assert.Equal(t, resources.PosOffered, f.position(posB).State, "the next head is offered in that pass")
	assert.Equal(t, resources.PosQueued, f.position(posA).State, "the passed position waits for its turn")
}

// A passed position is an entry again only when its session can take a notice and
// the next waiter has had its turn. (M: ignore deliverable.)
func TestWindow_PassedPositionWaitsForItsSession(t *testing.T) {
	f := newPosFix(t)
	posA, posB := f.offeredWithAWaiter()
	f.m.deliverable = func(sid string) bool { return sid != "sid-agent" }
	f.advance(window)
	f.sweep()
	f.pass() // B is offered; A is passed and its session is mid-turn
	require.Equal(t, resources.PosOffered, f.position(posB).State)
	f.setZone("normal", "flat")
	f.pass()
	assert.Equal(t, resources.PosQueued, f.position(posA).State, "its session cannot take a notice")
	f.m.deliverable = nil
	f.pass()
	assert.Equal(t, resources.PosOffered, f.position(posA).State, "deliverable, and B had its turn after A passed")
}

// Alone in the queue and deliverable, a passed position is offered again at the next pass.
func TestWindow_PassedPositionAloneIsOfferedAgain(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	a := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	f.setZone("normal", "flat")
	f.pass()
	require.Equal(t, resources.PosOffered, f.position(a.PositionID).State)
	f.advance(window)
	f.sweep()
	require.Equal(t, resources.PosQueued, f.position(a.PositionID).State)
	f.m.deliverable = func(string) bool { return false }
	f.pass()
	assert.Equal(t, resources.PosQueued, f.position(a.PositionID).State, "not deliverable: wait")
	f.m.deliverable = nil
	f.pass()
	p := f.position(a.PositionID)
	assert.Equal(t, resources.PosOffered, p.State)
	assert.Equal(t, 1, p.OfferPasses)
}

// The idle window restarts at the command end: offer t0, delivered t0+0:10, the
// command starts t0+2:00 and ends t0+10:00 → idle at t0+12:59.999, idle_timeout at
// t0+13:00. (M: keep window_until from the offer.)
func TestWindow_IdleWindowRestartsAtTheCommandEnd(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	a := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	f.setZone("normal", "flat")
	f.pass()
	f.advance(10 * time.Second)
	f.deliver(a.PositionID, f.now())
	f.advance(110 * time.Second) // t0 + 2:00
	run := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	require.Equal(t, resources.StateHeld, run.State)
	f.advance(8 * time.Minute) // t0 + 10:00
	f.release(run.ID, true)
	require.Equal(t, resources.PosIdle, f.position(a.PositionID).State)
	f.advance(window - time.Millisecond)
	f.sweep()
	assert.Equal(t, resources.PosIdle, f.position(a.PositionID).State, "2:59.999 after the command end")
	f.advance(time.Millisecond)
	f.sweep()
	p := f.position(a.PositionID)
	assert.Equal(t, resources.PosEnded, p.State)
	assert.Equal(t, resources.EndIdleTimeout, p.EndReason)
}

// At the idle timeout an ask row that was asked ends no_answer, a queue row
// idle_timeout.
func TestWindow_IdleLapsesToIdleTimeout(t *testing.T) {
	f := newPosFix(t)
	posID := f.idlePosition()
	require.NoError(t, createRow(f.m.store, withPos(txRow("ask", "sid-agent", posID, resources.CallAsk, "bg", "/w", 10), 77)))
	require.NoError(t, createRow(f.m.store, withPos(txRow("q", "sid-agent", posID, resources.CallQueue, "fg", "/w", 11), 77)))
	f.name("ask")
	f.name("q")
	f.advance(window)
	f.sweep()
	assert.Equal(t, resources.EndNoAnswer, f.row("ask").EndReason)
	assert.Equal(t, resources.EndIdleTimeout, f.row("q").EndReason)
}

// withPos makes an ask row that is polled (far in the future for the test).
func withPos(r leaseRow, _ int64) leaseRow {
	r.LeaseUntil = 1 << 60
	return r
}

// name marks a row as named in a notice (offered_at), as the pass does.
func (f *posFix) name(id string) {
	f.t.Helper()
	_, err := f.m.store.db.Exec(`UPDATE resource_leases SET offered_at = ? WHERE id = ?`, f.nowMS(), id)
	require.NoError(f.t, err)
}

// idlePosition makes an idle position of session A whose window ends one window from now.
func (f *posFix) idlePosition() string {
	f.t.Helper()
	got := decodeLease(f.t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	require.Equal(f.t, resources.StateHeld, got.State)
	f.release(got.ID, true)
	require.Equal(f.t, resources.PosIdle, f.position(got.PositionID).State)
	return got.PositionID
}

// A queued position has no window (V16): 2 hours later it is still queued.
// (M: give queued a window.)
func TestWindow_QueuedNeverLapses(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	a := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	for i := 0; i < 4; i++ {
		f.advance(30 * time.Minute)
		f.sweep()
	}
	assert.Equal(t, resources.PosQueued, f.position(a.PositionID).State)
	assert.Equal(t, resources.StateWaiting, f.state(a.ID))
}

// A start in the same millisecond as the window ends wins: the position runs.
func TestWindow_StartWinsOverALapseInTheSameMillisecond(t *testing.T) {
	f := newPosFix(t)
	posID := f.idlePosition()
	f.advance(window)
	got := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go test")) // at window_until exactly
	require.Equal(t, resources.StateHeld, got.State)
	f.sweep()
	assert.Equal(t, resources.PosRunning, f.position(posID).State)
}

// The cancelled notice is armed only when something is pending (Q7).
// (M: always arm it.)
func TestWindow_CancelNoticeOnlyWhenSomethingIsPending(t *testing.T) {
	// idle with a named ask row → cancelled
	f := newPosFix(t)
	posID := f.idlePosition()
	require.NoError(t, createRow(f.m.store, withPos(txRow("ask", "sid-agent", posID, resources.CallAsk, "bg", "/w", 10), 77)))
	f.name("ask")
	f.advance(window)
	f.sweep()
	assert.Equal(t, "cancelled", f.position(posID).NoticeKind)

	// idle with nothing named → empty
	f = newPosFix(t)
	posID = f.idlePosition()
	f.advance(window)
	f.sweep()
	assert.Equal(t, resources.PosEnded, f.position(posID).State)
	assert.Empty(t, f.position(posID).NoticeKind)

	// offered (delivered) with a waiting queue row → cancelled
	f = newPosFix(t)
	posA, _ := f.offeredWithAWaiter()
	f.deliver(posA, f.now())
	f.advance(window)
	f.sweep()
	assert.Equal(t, resources.PosEnded, f.position(posA).State)
	assert.Equal(t, "cancelled", f.position(posA).NoticeKind)
}

// The cost of the windows while someone waits (#10): offer → start, and the idle
// window; none while nobody waits. (M: count only idle windows.)
func TestCost_OfferAndIdleWindowsWhileSomeoneWaits(t *testing.T) {
	f := newPosFix(t)
	posA, _ := f.offeredWithAWaiter()
	f.advance(10 * time.Second)
	f.sweep() // tick 1: 10 s since the offer
	f.advance(10 * time.Second)
	f.sweep() // tick 2
	run := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	require.Equal(t, resources.StateHeld, run.State)
	f.advance(time.Second)
	f.sweep() // the position left the offered state: the cost is written
	p := f.position(posA)
	assert.Equal(t, int64(20_000), p.OfferWaitMS)
	assert.Zero(t, p.IdleWaitMS)

	// The command ends: the idle window while B waits.
	f.release(run.ID, true)
	f.advance(30 * time.Second)
	f.sweep()
	f.advance(window)
	f.sweep() // the window lapses, the cost is written
	p = f.position(posA)
	assert.Equal(t, int64(20_000), p.OfferWaitMS)
	assert.Equal(t, int64(window.Milliseconds()), p.IdleWaitMS, "the whole idle window held the host while B waited")
}

func TestCost_NothingWhileNobodyWaits(t *testing.T) {
	f := newPosFix(t)
	posID := f.idlePosition()
	f.advance(time.Minute)
	f.sweep()
	f.advance(window)
	f.sweep()
	assert.Zero(t, f.position(posID).IdleWaitMS)
	assert.Zero(t, f.position(posID).OfferWaitMS)
}

// ---- liveness ----

// A position whose agent is gone ends abandoned, in each live state but running.
func TestLiveness_AgentGoneAbandons(t *testing.T) {
	// queued
	f := newPosFix(t)
	f.setZone("danger", "flat")
	q := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	delete(f.view.procs, 800)
	f.advance(time.Millisecond) // the view is read after the position was made
	f.sweep()
	assert.Equal(t, resources.EndAbandoned, f.position(q.PositionID).EndReason)
	assert.Equal(t, resources.EndAbandoned, f.row(q.ID).EndReason, "its rows end with it")

	// offered
	f = newPosFix(t)
	posA, _ := f.offeredWithAWaiter()
	delete(f.view.procs, 800)
	f.advance(time.Millisecond)
	f.sweep()
	assert.Equal(t, resources.EndAbandoned, f.position(posA).EndReason)

	// idle
	f = newPosFix(t)
	idle := f.idlePosition()
	delete(f.view.procs, 800)
	f.advance(time.Millisecond)
	f.sweep()
	assert.Equal(t, resources.EndAbandoned, f.position(idle).EndReason)
}

// An agent pid listed under another session in two consecutive snapshots is a
// /clear: the position ends. Once is kept. (M: once is enough.)
func TestLiveness_PidUnderAnotherSessionTwice(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	q := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	snap := func(at time.Time, session string) {
		f.m.publish(&resources.Snapshot{Available: true, SampledAt: at, Capacity: resources.Capacity, Mode: resources.ModeLease,
			Host:     resources.HostUse{Measured: 10, NCPU: 10, Load1: 1, Zone: "danger", Trend: "flat"},
			Sessions: []resources.SessionUse{{SessionID: session, PID: 800}}})
	}
	snap(f.now(), "sid-new")
	f.sweep()
	assert.Equal(t, resources.PosQueued, f.position(q.PositionID).State, "once is kept")
	f.sweep()
	assert.Equal(t, resources.PosQueued, f.position(q.PositionID).State, "the same snapshot is not counted twice")
	snap(f.now().Add(2*time.Second), "sid-new")
	f.sweep()
	assert.Equal(t, resources.EndAbandoned, f.position(q.PositionID).EndReason)
}

// An unreadable process table counts every agent as alive.
func TestLiveness_UnreadableViewIsAlive(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	q := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	delete(f.view.procs, 800)
	f.m.sweepView = func(context.Context) (procView, error) { return nil, errors.New("no process table") }
	f.sweep()
	assert.Equal(t, resources.PosQueued, f.position(q.PositionID).State)
	_ = iagent.ProcessSnapshot{}
}

// The agent dies while its command runs: the held row ends holder_gone and the
// position ends abandoned with it, not idle.
func TestLiveness_RunningEndsAfterItsCommand(t *testing.T) {
	f := newPosFix(t)
	run := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	require.Equal(t, resources.StateHeld, run.State)
	delete(f.view.procs, 800)
	delete(f.view.procs, 1000)
	f.advance(time.Second)
	f.sweep()
	assert.Equal(t, resources.EndHolderGone, f.row(run.ID).EndReason)
	p := f.position(run.PositionID)
	assert.Equal(t, resources.PosEnded, p.State)
	assert.Equal(t, resources.EndAbandoned, p.EndReason)
}

// A position that left the offered state between two ticks is charged up to its
// first start, not up to the last tick. (M: flush without the elapsed time.)
func TestCost_TransitionBetweenTicksIsCounted(t *testing.T) {
	f := newPosFix(t)
	posA, _ := f.offeredWithAWaiter()
	f.advance(10 * time.Second)
	f.sweep() // tick: 10 s since the offer
	f.advance(10 * time.Second)
	run := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build")) // starts 20 s after the offer
	require.Equal(t, resources.StateHeld, run.State)
	f.advance(5 * time.Second) // the next tick is 5 s after the start
	f.sweep()
	assert.Equal(t, int64(20_000), f.position(posA).OfferWaitMS, "up to the start, not the last tick (10 s) and not this tick (25 s)")
}

// A passed offer stops charging at the pass: the next tick does not add the time
// the position no longer held the host. (M: leave the cost open at the pass.)
func TestCost_PassedOfferStopsChargingAtThePass(t *testing.T) {
	f := newPosFix(t)
	posA, _ := f.offeredWithAWaiter()
	f.advance(window)
	f.sweep() // the offer passes at 3:00
	require.Equal(t, resources.PosQueued, f.position(posA).State)
	f.advance(5 * time.Second)
	f.sweep()
	assert.Equal(t, window.Milliseconds(), f.position(posA).OfferWaitMS)
}

// A cost whose write failed is kept and tried again; once written it is not
// written twice, also when the failure was at a state change between ticks and
// the position went on into another charged state.
func TestCost_FailedWriteIsKeptAndNotRepeated(t *testing.T) {
	f := newPosFix(t)
	posA, _ := f.offeredWithAWaiter()
	f.advance(10 * time.Second)
	f.sweep()
	f.advance(10 * time.Second)
	f.sweep() // 20 s accrued in memory
	// The write fails at the state change (the position starts, then ends its command).
	_, err := f.m.store.db.Exec(`CREATE TRIGGER fail_cost BEFORE UPDATE OF offer_wait_ms ON resource_positions BEGIN SELECT RAISE(ABORT, 'busy'); END`)
	require.NoError(t, err)
	run := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	require.Equal(t, resources.StateHeld, run.State)
	f.advance(time.Second)
	f.sweep() // the change is noticed: the write fails, the cost is kept
	require.Len(t, f.m.pendingCosts, 1)
	f.advance(time.Second)
	f.sweep() // still failing, and nothing is added a second time
	require.Len(t, f.m.pendingCosts, 1)
	assert.Equal(t, int64(20_000), f.m.pendingCosts[0].ms)
	f.release(run.ID, true) // into the next charged state while the old write is still pending
	f.advance(time.Second)
	f.sweep()
	require.NotNil(t, f.m.costs[posA], "the idle window is tracked on its own")
	_, err = f.m.store.db.Exec(`DROP TRIGGER fail_cost`)
	require.NoError(t, err)
	f.advance(time.Second)
	f.sweep()
	assert.Empty(t, f.m.pendingCosts)
	assert.Equal(t, int64(20_000), f.position(posA).OfferWaitMS, "written once, exactly")
	f.sweep()
	assert.Equal(t, int64(20_000), f.position(posA).OfferWaitMS, "not written twice")
}

// With the writes failing for good, the pending list is bounded by the positions
// and each pending cost is tried once per sweep. (M: append instead of merging.)
func TestCost_PendingListIsBoundedWhenWritesKeepFailing(t *testing.T) {
	f := newPosFix(t)
	posA, _ := f.offeredWithAWaiter()
	_, err := f.m.store.db.Exec(`CREATE TRIGGER fail_cost BEFORE UPDATE OF offer_wait_ms, idle_wait_ms ON resource_positions BEGIN SELECT RAISE(ABORT, 'busy'); END`)
	require.NoError(t, err)
	for i := 0; i < 6; i++ { // a charged offered window, settled again and again
		f.advance(10 * time.Second)
		f.sweep()
		f.m.costs[posA].state = resources.PosIdle // forces a change of state at the next tick
		f.advance(time.Second)
		f.sweep()
	}
	assert.LessOrEqual(t, len(f.m.pendingCosts), 2, "one entry per position and kind of cost")
}
