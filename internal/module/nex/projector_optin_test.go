package nex

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/core"
)

// #1866 PR1b: nex frames go only to the /ws/host-events subscribers that
// opted in (nex=v1). Every other client of that WS — an old SPA, purdex-ios
// — never sees a nex.* frame, and is never disconnected because of one.

// fill queues frames until sub's send buffer is full.
func fill(t *testing.T, sub *core.EventSubscriber) {
	t.Helper()
	for i := 0; i < cap(sub.SendCh()); i++ {
		require.True(t, sub.TrySend([]byte(`{"type":"fill"}`)))
	}
}

func TestProjector_DeltasAndHelloReachOnlyOptedInSubscribers(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	plain := e.events.AddTestSubscriber()
	t.Cleanup(func() { e.events.RemoveTestSubscriber(plain) })

	e.rows.set("exc_a", "running")
	e.p.markFrame("exc_a", "execution.running")
	assert.Equal(t, "exc_a", nextDelta(t, e.sub).ID)

	e.p.sendHello(plain)
	e.p.sendHello(e.sub)
	ev, _ := nextFrame(t, e.sub)
	assert.Equal(t, hello{Epoch: e.slot.epoch, Bseq: 1}, helloOf(t, ev))

	noFrame(t, plain, 50*time.Millisecond)
	assert.False(t, isRemoved(plain), "a subscriber without nex was removed")
}

// A subscriber without nex never waits for the slot: its hello is skipped
// before the slot is asked for, so a busy slot cannot cost it its
// connection.
func TestProjector_HelloSkipsASubscriberWithoutNexEvenWhenTheSlotIsBusy(t *testing.T) {
	e := newProjEnv(t, projectorTiming{helloWait: time.Hour})
	plain := e.events.AddTestSubscriber()
	t.Cleanup(func() { e.events.RemoveTestSubscriber(plain) })
	require.NoError(t, e.slot.acquire(context.Background(), 0))
	defer e.slot.release()

	done := make(chan struct{})
	go func() {
		e.p.sendHello(plain)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the hello waited for the slot for a subscriber without nex")
	}
	assert.False(t, isRemoved(plain))
	assert.Empty(t, plain.SendCh())
}

// markBurst marks n executions in one coalescing window and returns their
// ids in mark order.
func markBurst(e *projEnv, n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("exc_b%03d", i)
		e.rows.set(ids[i], "running")
	}
	for _, id := range ids {
		e.p.markFrame(id, "execution.running")
	}
	return ids
}

// A subscriber without nex whose buffer is full survives a burst of deltas
// it was never sent: the strict broadcast is scoped to nex.v1.
func TestProjector_FullSubscriberWithoutNexSurvivesADeltaBurst(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	plain := e.events.AddTestSubscriber()
	t.Cleanup(func() { e.events.RemoveTestSubscriber(plain) })
	fill(t, plain)

	ids := markBurst(e, 100)
	for range ids {
		nextDelta(t, e.sub)
	}
	assert.False(t, isRemoved(plain), "a subscriber without nex was removed by a delta burst")
	assert.Len(t, plain.SendCh(), cap(plain.SendCh()), "a subscriber without nex was sent a delta")
}

// An opted-in client that reads nothing while a burst flushes — 200
// executions in one coalescing window, one delta each, far more than the 64
// every other subscriber gets — keeps its connection, and then reads every
// delta, contiguous.
func TestProjector_OptedInSubscriberSurvivesABurstOf200WithoutReading(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	const burst = 200
	require.Greater(t, burst, 64)
	require.Less(t, burst, cap(e.sub.SendCh()))

	markBurst(e, burst)
	require.Eventually(t, func() bool { return len(e.sub.SendCh()) == burst || isRemoved(e.sub) },
		5*time.Second, time.Millisecond, "the burst never finished flushing")
	require.False(t, isRemoved(e.sub), "an opted-in subscriber was dropped by one burst")

	seen := map[string]bool{}
	for i := 0; i < burst; i++ {
		d := nextDelta(t, e.sub)
		assert.Equal(t, uint64(i+1), d.Bseq)
		seen[d.ID] = true
	}
	assert.Len(t, seen, burst)
	assert.False(t, isRemoved(e.sub))
}
