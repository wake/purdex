package nex

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/bus"

	"github.com/wake/purdex/internal/core"
)

// #1866 PR1c (spec 2026-10-08 §3.5, §3.6): a new epoch after the bus kicked
// the consumer, or when bseq runs out, and the backoff that keeps a closed
// bus from spinning.

// backoffTiming flushes fast and resubscribes within tens of ms.
var backoffTiming = projectorTiming{trailing: 5 * time.Millisecond, maxDelay: 20 * time.Millisecond,
	retryDelay: time.Hour, backoffMin: 10 * time.Millisecond, backoffMax: 40 * time.Millisecond}

func TestProjector_KickedConsumerResubscribesIntoANewEpoch(t *testing.T) {
	e := startProjEnv(t, backoffTiming, rowsWith("exc_a"))
	plain := e.events.AddTestSubscriber()
	t.Cleanup(func() { e.events.RemoveTestSubscriber(plain) })
	old := e.slot.epoch // nothing rotated it yet: the start seed only reads

	kicked := time.Now()
	e.bus.Unsubscribe(e.p.subscription()) // what the bus does to a consumer that fell behind
	ev, _ := nextFrame(t, e.sub)
	h := helloOf(t, ev)
	assert.GreaterOrEqual(t, time.Since(kicked), backoffTiming.backoffMin, "resubscribed without backing off")
	assert.NotEqual(t, old, h.Epoch)
	assert.Equal(t, `{"epoch":"`+h.Epoch+`","bseq":0}`, ev.Value, "bseq 0 must be spelled out")
	assert.Empty(t, plain.SendCh(), "a subscriber without nex got the hello")

	// The seed after the resubscribe re-read the row from a page read after
	// the hello (ver 1 was the start seed's page).
	waitSeeds(t, e.p, 2)
	e.p.mu.Lock()
	assert.Equal(t, uint64(2), e.p.pushed["exc_a"].ver)
	e.p.mu.Unlock()
	assert.Contains(t, e.logs.all(), "nex-delta: bus subscription closed; resubscribing")
	assert.Contains(t, e.logs.all(), "nex-delta: bus resubscribed; new epoch "+h.Epoch+", hello sent to every nex.v1 subscriber")

	// The new subscription is live, and its deltas count from 1 in the new
	// epoch.
	e.rows.set("exc_b", "running")
	publish(e.bus, "exc_b", "execution.running")
	d := nextDelta(t, e.sub)
	assert.Equal(t, "exc_b", d.ID)
	assert.Equal(t, h.Epoch, d.Epoch)
	assert.Equal(t, uint64(1), d.Bseq)
}

// scriptedBus hands out subscriptions in script order: "open"; "closed"
// (over before it starts, as a closed bus answers); or "frame:<id>" (open,
// with one execution.running frame for id already queued). Once the script
// runs out, every subscription is closed.
type scriptedBus struct {
	mu     sync.Mutex
	script []string
	chans  map[*bus.Subscription]chan bus.Frame
	shut   map[*bus.Subscription]bool
	handed []*bus.Subscription
}

func newScriptedBus(script ...string) *scriptedBus {
	return &scriptedBus{script: script, chans: map[*bus.Subscription]chan bus.Frame{}, shut: map[*bus.Subscription]bool{}}
}

func (b *scriptedBus) Subscribe(string, int) *bus.Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()
	step := "closed"
	if len(b.script) > 0 {
		step, b.script = b.script[0], b.script[1:]
	}
	ch := make(chan bus.Frame, 4)
	s := &bus.Subscription{Ch: ch}
	b.chans[s] = ch
	b.handed = append(b.handed, s)
	switch {
	case step == "closed":
		b.closeLocked(s)
	case strings.HasPrefix(step, "frame:"):
		ch <- bus.Frame{ExecutionID: strings.TrimPrefix(step, "frame:"), Kind: "execution.running"}
	}
	return s
}

func (b *scriptedBus) Unsubscribe(s *bus.Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeLocked(s)
}

func (b *scriptedBus) closeLocked(s *bus.Subscription) {
	if !b.shut[s] {
		b.shut[s] = true
		close(b.chans[s])
	}
}

// kick closes the i-th subscription handed out.
func (b *scriptedBus) kick(i int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeLocked(b.handed[i])
}

func (b *scriptedBus) subscribes() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.handed)
}

// A subscription that is over when it comes back starts no epoch — the
// projector keeps backing off — and one that already carries a frame is
// registered, gets its epoch, and keeps that frame.
func TestProjector_ResubscribeSkipsClosedSubscriptionsAndKeepsTheFirstFrame(t *testing.T) {
	sb := newScriptedBus("open", "closed", "closed", "frame:exc_f")
	events := core.NewEventsBroadcaster()
	sub := events.AddTestSubscriberWith(core.FeatureNexV1)
	rows := rowsWith("exc_f")
	p := newProjector(newReadSlot(discardLogf), rowReader{handler: rows, logf: discardLogf}, events, sb, discardLogf,
		backoffTiming)
	p.start()
	t.Cleanup(func() { p.stop(context.Background()) })
	waitSeeds(t, p, 1)

	sb.kick(0)
	ev, _ := nextFrame(t, sub)
	h := helloOf(t, ev)
	d := nextDelta(t, sub)
	assert.Equal(t, "exc_f", d.ID)
	assert.Equal(t, []string{"execution.running"}, d.Cause)
	assert.Equal(t, h.Epoch, d.Epoch)
	assert.Equal(t, uint64(1), d.Bseq)
	assert.Equal(t, 4, sb.subscribes())
	noFrame(t, sub, 50*time.Millisecond) // one hello, for the one live subscription
}

// countingBus is a real bus that counts Subscribe calls.
type countingBus struct {
	*bus.Bus
	subscribes atomic.Int32
}

func (c *countingBus) Subscribe(execID string, buf int) *bus.Subscription {
	c.subscribes.Add(1)
	return c.Bus.Subscribe(execID, buf)
}

// A closed bus answers every Subscribe with a subscription that is already
// over: the projector keeps backing off — no busy loop, and no hello, as
// there is no subscription to start an epoch for — the worker keeps
// flushing what is marked, and Stop ends the backoff.
func TestProjector_ClosedBusBacksOffWithoutAHello(t *testing.T) {
	cb := &countingBus{Bus: bus.New()}
	events := core.NewEventsBroadcaster()
	sub := events.AddTestSubscriberWith(core.FeatureNexV1)
	logs := &logRecorder{}
	p := newProjector(newReadSlot(discardLogf), rowReader{handler: rowsWith("exc_a"), logf: discardLogf}, events, cb,
		logs.logf, backoffTiming)
	p.start()
	t.Cleanup(func() { p.stop(context.Background()) })
	waitSeeds(t, p, 1)

	cb.Close()
	time.Sleep(300 * time.Millisecond)
	// Retries at about 10, 30, 70, 110, … ms: some ten in 300 ms. A spin
	// would make thousands.
	n := cb.subscribes.Load()
	assert.GreaterOrEqual(t, n, int32(2), "never tried again")
	assert.LessOrEqual(t, n, int32(15), "resubscribed without backing off")
	assert.Empty(t, sub.SendCh(), "a closed bus produced a frame")

	p.markFrame("exc_a", "execution.running")
	assert.Equal(t, "exc_a", nextDelta(t, sub).ID)

	stopped := make(chan struct{})
	go func() {
		p.stop(context.Background())
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not end the backoff")
	}
	select {
	case <-p.done:
	default:
		t.Fatal("stop returned before the consumer ended")
	}
	assert.Equal(t, []string{"nex-delta: bus subscription closed; resubscribing"}, logs.all())
}

// bseq reaching its limit (2^53−1; 2 here) starts a new epoch inside the
// hold that numbers the delta: the hello first, then the delta as bseq 1
// of the new epoch. ver goes on.
func TestProjector_BseqAtItsLimitRotatesTheEpoch(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	require.NoError(t, e.slot.acquire(context.Background(), 0))
	e.slot.bseqLimit = 2 // as a holder: only holders touch the counters
	old := e.slot.epoch
	e.slot.release()
	e.rows.set("exc_a", "running")

	var last delta
	for i := 1; i <= 2; i++ {
		e.p.markFrame("exc_a", "tool_use")
		last = nextDelta(t, e.sub)
		assert.Equal(t, old, last.Epoch)
		assert.Equal(t, uint64(i), last.Bseq)
	}
	e.p.markFrame("exc_a", "tool_result")
	ev, _ := nextFrame(t, e.sub)
	h := helloOf(t, ev)
	assert.NotEqual(t, old, h.Epoch)
	assert.Equal(t, `{"epoch":"`+h.Epoch+`","bseq":0}`, ev.Value, "bseq 0 must be spelled out")
	d := nextDelta(t, e.sub)
	assert.Equal(t, h.Epoch, d.Epoch)
	assert.Equal(t, uint64(1), d.Bseq)
	assert.Equal(t, []string{"tool_result"}, d.Cause)
	assert.Greater(t, d.Ver, last.Ver, "ver went back with the epoch")
}
