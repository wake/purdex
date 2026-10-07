package nex

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/core"
)

// #1866 PR1c (spec 2026-10-08 §3.7, §8 R3-2): the daemon's safety
// reconcile, which counts and repairs pushes that went missing.

// reconcileTiming flushes fast and judges suspects 30 ms after their page.
func reconcileTiming() projectorTiming {
	tm := fastTiming
	tm.grace = 30 * time.Millisecond
	return tm
}

// linesWith is every log line containing s.
func linesWith(logs *logRecorder, s string) []string {
	var out []string
	for _, l := range logs.all() {
		if strings.Contains(l, s) {
			out = append(out, l)
		}
	}
	return out
}

func totals(p *projector) [2]int64 { return [2]int64{p.mismatchTotal.Load(), p.unseenTotal.Load()} }

// The reconcile reads nothing while no client consumes the deltas — a
// subscriber without nex does not count — and starts on its own once one
// opts in.
func TestProjector_ReconcileRunsOnlyWhileSomeoneOptedIn(t *testing.T) {
	tm := reconcileTiming()
	tm.reconcile = 20 * time.Millisecond
	e := startProjEnv(t, tm, rowsWith("exc_a"))
	e.events.RemoveTestSubscriber(e.sub)
	plain := e.events.AddTestSubscriber()
	t.Cleanup(func() { e.events.RemoveTestSubscriber(plain) })
	seeded := e.rows.listReadCount()

	e.p.reconcile()
	time.Sleep(150 * time.Millisecond) // several ticks
	assert.Equal(t, seeded, e.rows.listReadCount(), "the reconcile walked with nobody opted in")

	opted := e.events.AddTestSubscriberWith(core.FeatureNexV1)
	t.Cleanup(func() { e.events.RemoveTestSubscriber(opted) })
	require.Eventually(t, func() bool {
		return len(linesWith(e.logs, "nex-delta: reconcile: nex_delta_mismatch_total=0 nex_delta_reconcile_unseen_total=0 nex_delta_slot_max_hold_ms=")) > 0
	}, 2*time.Second, 5*time.Millisecond, "no tick ran once a subscriber opted in")
	assert.Greater(t, e.rows.listReadCount(), seeded)
}

// A row that changed with no bus frame at all is a missed push: counted,
// logged with the field that differs, and repaired by a delta whose cause
// is the reconcile's own.
func TestProjector_ReconcileRepairsSilentDrift(t *testing.T) {
	e := startProjEnv(t, reconcileTiming(), rowsWith("exc_a", "exc_b"))
	e.rows.set("exc_a", "running") // no frame

	e.p.reconcile()
	assert.Equal(t, [2]int64{1, 0}, totals(e.p))
	assert.Equal(t, []string{"nex-delta: missed push exec=exc_a field=state pushed=idle actual=running total=1"},
		linesWith(e.logs, "missed push"))
	assert.Len(t, linesWith(e.logs, "nex-delta: reconcile: nex_delta_mismatch_total=1 nex_delta_reconcile_unseen_total=0 "), 1)
	d := nextDelta(t, e.sub)
	assert.Equal(t, "exc_a", d.ID)
	assert.Equal(t, []string{"pdx.reconcile"}, d.Cause)
	assert.Equal(t, "running", stateOf(t, d))
	noFrame(t, e.sub, 50*time.Millisecond)

	// Repaired: the next tick finds nothing.
	e.p.reconcile()
	assert.Equal(t, [2]int64{1, 0}, totals(e.p))
	noFrame(t, e.sub, 50*time.Millisecond)
}

// A difference whose push is in flight — a newer read lands during the
// grace — is benign, for a drifted row and an unseen one alike: nothing
// is counted and the reconcile pushes nothing of its own.
func TestProjector_ReconcileInFlightChangesAreBenign(t *testing.T) {
	tm := reconcileTiming()
	tm.grace = 300 * time.Millisecond
	e := startProjEnv(t, tm, rowsWith("exc_a"))
	e.rows.set("exc_a", "running")
	e.rows.set("exc_new", "running")
	before := e.rows.listReadCount()

	done := make(chan struct{})
	go func() {
		e.p.reconcile()
		close(done)
	}()
	// Once the page is being read, the flushes below can only follow it.
	require.Eventually(t, func() bool { return e.rows.listReadCount() > before }, 2*time.Second, time.Millisecond)
	e.p.markFrame("exc_a", "execution.running")
	e.p.markFrame("exc_new", "execution.delegated")
	ids := map[string]bool{}
	for i := 0; i < 2; i++ {
		d := nextDelta(t, e.sub)
		assert.NotContains(t, d.Cause, "pdx.reconcile")
		ids[d.ID] = true
	}
	assert.Equal(t, map[string]bool{"exc_a": true, "exc_new": true}, ids)
	<-done
	assert.Equal(t, [2]int64{0, 0}, totals(e.p))
	noFrame(t, e.sub, 50*time.Millisecond)
}

// An execution never pushed (and not seeded) is pushed and counted as
// unseen, not as a mismatch.
func TestProjector_ReconcilePushesAnUnseenExecution(t *testing.T) {
	e := startProjEnv(t, reconcileTiming(), rowsWith("exc_a"))
	e.rows.set("exc_new", "idle") // created with no frame

	e.p.reconcile()
	assert.Equal(t, [2]int64{0, 1}, totals(e.p))
	assert.Equal(t, []string{"nex-delta: unseen exec=exc_new total=1"}, linesWith(e.logs, "unseen exec="))
	d := nextDelta(t, e.sub)
	assert.Equal(t, "exc_new", d.ID)
	assert.Equal(t, []string{"pdx.reconcile"}, d.Cause)
}

// A pushed execution that its covering page does not list — deleted, or
// archived, with no frame — is a missed push of field presence; the re-read
// pushes what it is now (a removal, the archived row). One whose last push
// already said it is gone is not.
func TestProjector_ReconcileCountsAPushedRowMissingFromItsPage(t *testing.T) {
	tm := reconcileTiming()
	tm.walkLimit = 1
	e := startProjEnv(t, tm, rowsWith("exc_a", "exc_b", "exc_c"))
	e.p.markFrame("exc_gone", "execution.terminated") // 404: lastPushed says removed
	nextDelta(t, e.sub)
	e.rows.remove("exc_a")
	e.rows.setBody("exc_b", `{"id":"exc_b","state":"idle","archived":true,"turn_count":1}`)

	e.p.reconcile()
	assert.Equal(t, [2]int64{2, 0}, totals(e.p))
	assert.Equal(t, []string{
		"nex-delta: missed push exec=exc_a field=presence pushed=present actual=absent total=1",
		"nex-delta: missed push exec=exc_b field=presence pushed=present actual=absent total=2",
	}, linesWith(e.logs, "missed push"))
	gone := nextDelta(t, e.sub)
	assert.Equal(t, "exc_a", gone.ID)
	assert.Equal(t, "null", string(gone.Row))
	archived := nextDelta(t, e.sub)
	assert.Equal(t, "exc_b", archived.ID)
	assert.Contains(t, string(archived.Row), `"archived":true`)
	noFrame(t, e.sub, 50*time.Millisecond)
}

// At most 32 pushes per tick, oldest id first; the rest are found again
// next tick, and every detection is counted, pushed or not. The subscriber
// keeps its connection.
func TestProjector_ReconcilePushesAtMostThirtyTwoPerTick(t *testing.T) {
	tm := reconcileTiming()
	tm.walkLimit = 40
	e := startProjEnv(t, tm, newRowServer())
	for i := 0; i < 100; i++ {
		e.rows.set(fmt.Sprintf("exc_u%03d", i), "idle")
	}

	for tick, want := range [][2]int{{0, 32}, {32, 64}} {
		e.p.reconcile()
		for i := want[0]; i < want[1]; i++ {
			assert.Equal(t, fmt.Sprintf("exc_u%03d", i), nextDelta(t, e.sub).ID, "tick %d", tick)
		}
		noFrame(t, e.sub, 50*time.Millisecond)
	}
	assert.Equal(t, [2]int64{0, 100 + 68}, totals(e.p))
	assert.False(t, isRemoved(e.sub))
}

// A start with more executions than a subscriber's best-effort buffer
// seeds them all, and the first tick pushes nothing (§8 R3-2).
func TestProjector_SeededStartPushesNothingOnTheFirstTick(t *testing.T) {
	rows := newRowServer()
	for i := 0; i < 70; i++ {
		rows.set(fmt.Sprintf("exc_s%03d", i), "idle")
	}
	e := startProjEnv(t, reconcileTiming(), rows)
	e.p.mu.Lock()
	assert.Len(t, e.p.pushed, 70)
	e.p.mu.Unlock()

	e.p.reconcile()
	assert.Equal(t, [2]int64{0, 0}, totals(e.p))
	noFrame(t, e.sub, 50*time.Millisecond)
	assert.False(t, isRemoved(e.sub))
}

// The reconcile takes the slot per page: a read already waiting when page
// 1 ends gets the slot before page 2 (the slot's waiters are served in
// order), so it is stamped between the two pages.
func TestProjector_ReconcileReleasesTheSlotBetweenPages(t *testing.T) {
	tm := reconcileTiming()
	tm.walkLimit = 1
	e := startProjEnv(t, tm, rowsWith("exc_01", "exc_02"))
	var v0 uint64
	require.NoError(t, e.slot.hold(context.Background(), "test", 0, func(context.Context) error {
		v0 = e.slot.current().Ver
		return nil
	}))

	got := make(chan uint64, 1)
	e.rows.listHook = func(cursor string) {
		if cursor != "" {
			return
		}
		started := make(chan struct{})
		go func() {
			close(started)
			st, err := e.slot.read(context.Background(), "row", 0, okRead)
			assert.NoError(t, err)
			got <- st.Ver
		}()
		<-started
		time.Sleep(50 * time.Millisecond) // let it queue for the slot page 1 holds
	}
	e.p.reconcile()
	e.rows.listHook = nil

	assert.Equal(t, v0+2, <-got, "page 2 was read before the waiting read: the slot was held across pages")
	require.NoError(t, e.slot.hold(context.Background(), "test", 0, func(context.Context) error {
		assert.Equal(t, v0+3, e.slot.current().Ver)
		return nil
	}))
}

// Stop ends a reconcile waiting out its grace at once: nothing is judged,
// counted or pushed, and stop does not have to wait for it.
func TestProjector_StopCancelsAPendingGrace(t *testing.T) {
	tm := reconcileTiming()
	tm.reconcile, tm.grace = 20*time.Millisecond, time.Hour
	e := startProjEnv(t, tm, rowsWith("exc_a"))
	e.rows.set("exc_a", "running") // a suspect, so the tick waits out the grace
	before := e.rows.listReadCount()
	// Ticks every 20 ms read the list while they find nothing; reads that
	// stop for a while mean a tick is parked in its grace.
	require.Eventually(t, func() bool {
		n := e.rows.listReadCount()
		time.Sleep(80 * time.Millisecond)
		return n > before && e.rows.listReadCount() == n
	}, 3*time.Second, time.Millisecond, "no tick ever waited out a grace")

	start := time.Now()
	e.p.stop(context.Background())
	assert.Less(t, time.Since(start), time.Second, "stop waited for the grace")
	select {
	case <-e.p.done:
	default:
		t.Fatal("stop returned before the reconcile ended")
	}
	assert.Equal(t, [2]int64{0, 0}, totals(e.p))
	assert.Empty(t, linesWith(e.logs, "still running"))
	assert.Empty(t, e.sub.SendCh())
}
