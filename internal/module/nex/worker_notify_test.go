package nex

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collector is a subscriber that records every event it is given.
type collector struct {
	mu  sync.Mutex
	evs []WorkerNotifyEvent
}

func (c *collector) add(ev WorkerNotifyEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evs = append(c.evs, ev)
}

func (c *collector) got() []WorkerNotifyEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]WorkerNotifyEvent(nil), c.evs...)
}

func (c *collector) waitFor(t *testing.T, n int) []WorkerNotifyEvent {
	t.Helper()
	require.Eventually(t, func() bool { return len(c.got()) >= n }, 2*time.Second, time.Millisecond)
	return c.got()
}

func permRow(id, state, req string) string {
	return fmt.Sprintf(`{"id":%q,"state":%q,"turn_count":2,"provider":"claude","brief":"fix it",`+
		`"session_title":{"text":"My worker","source":"ai"},"pending_permission":{"request_id":%q,"tool_name":"Bash","since":5}}`+"\n", id, state, req)
}

// flushOnce marks id and waits for the delta it produces, then lets the hub's consumer run.
func flushOnce(t *testing.T, e *projEnv, id string) {
	t.Helper()
	e.p.markFrame(id, "execution.running")
	nextDelta(t, e.sub)
	time.Sleep(30 * time.Millisecond)
}

func TestProjector_PublishesOnlyTransitions(t *testing.T) {
	rows := newRowServer()
	rows.set("exc_a", "idle") // seeded as the baseline
	rows.set("exc_b", "running")
	e := startProjEnv(t, fastTiming, rows)
	hub := &workerNotifyHub{}
	e.p.notify = hub
	c := &collector{}
	unsub := hub.subscribe(c.add)
	defer unsub()

	flushOnce(t, e, "exc_a") // the first flush after the seed, same digest: a baseline stays quiet
	assert.Empty(t, c.got())

	rows.set("exc_a", "running")
	flushOnce(t, e, "exc_a")
	assert.Empty(t, c.got(), "running is not a notification")

	rows.setBody("exc_a", permRow("exc_a", "running", "p1"))
	flushOnce(t, e, "exc_a")
	evs := c.waitFor(t, 1)
	assert.Equal(t, WorkerNotifyEvent{ExecID: "exc_a", Status: "waiting", DedupKey: "exc_a|waiting|p1", RequestID: "p1", ToolName: "Bash",
		Title: "My worker", Brief: "fix it", Provider: "claude", TurnCount: 2, Stamp: evs[0].Stamp}, evs[0])
	assert.NotZero(t, evs[0].Stamp)

	flushOnce(t, e, "exc_a") // the same digest again (a lease.renewed re-flush)
	assert.Len(t, c.got(), 1)

	rows.setBody("exc_a", permRow("exc_a", "running", "p2"))
	flushOnce(t, e, "exc_a")
	evs = c.waitFor(t, 2)
	assert.Equal(t, "exc_a|waiting|p2", evs[1].DedupKey)

	rows.set("exc_a", "idle")
	flushOnce(t, e, "exc_a")
	evs = c.waitFor(t, 3)
	assert.Equal(t, "idle", evs[2].Status)
	assert.Equal(t, "exc_a|idle|1", evs[2].DedupKey)

	flushOnce(t, e, "exc_a")
	assert.Len(t, c.got(), 3, "an unchanged idle row is not done twice")

	rows.set("exc_b", "failed") // seeded running, so this is a transition
	flushOnce(t, e, "exc_b")
	evs = c.waitFor(t, 4)
	assert.Equal(t, "error", evs[3].Status)
	assert.Equal(t, "failed", evs[3].Reason)
}

// A row that comes back after a removal is a baseline, not a transition.
func TestProjector_RemovedThenBackIsABaseline(t *testing.T) {
	rows := newRowServer()
	rows.set("exc_a", "running")
	e := startProjEnv(t, fastTiming, rows)
	hub := &workerNotifyHub{}
	e.p.notify = hub
	c := &collector{}
	defer hub.subscribe(c.add)()

	rows.remove("exc_a")
	flushOnce(t, e, "exc_a")
	rows.set("exc_a", "idle")
	flushOnce(t, e, "exc_a")
	assert.Empty(t, c.got())
}

// A digest that does not parse neither publishes nor wipes the baseline.
func TestProjector_UnparsedRowKeepsTheBaseline(t *testing.T) {
	rows := newRowServer()
	rows.set("exc_a", "running")
	e := startProjEnv(t, fastTiming, rows)
	hub := &workerNotifyHub{}
	e.p.notify = hub
	c := &collector{}
	defer hub.subscribe(c.add)()

	rows.setBody("exc_a", `{"id":"exc_a","state":"running","turn_count":"x"}`+"\n")
	flushOnce(t, e, "exc_a")
	assert.Empty(t, c.got(), "an unparsed row is not a transition")
	rows.set("exc_a", "running")
	flushOnce(t, e, "exc_a")
	assert.Empty(t, c.got())
	rows.setBody("exc_a", `{"id":"exc_a","state":"running","turn_count":"x"}`+"\n")
	flushOnce(t, e, "exc_a")
	rows.set("exc_a", "idle") // straight after an unparsed read: the baseline is still the running one
	flushOnce(t, e, "exc_a")
	assert.Len(t, c.waitFor(t, 1), 1, "the running baseline survived: one done")
	flushOnce(t, e, "exc_a")
	assert.Len(t, c.got(), 1)
}

func TestProjector_EventTextIsClippedByRunes(t *testing.T) {
	rows := newRowServer()
	rows.set("exc_a", "running")
	e := startProjEnv(t, fastTiming, rows)
	hub := &workerNotifyHub{}
	e.p.notify = hub
	c := &collector{}
	defer hub.subscribe(c.add)()

	long := strings.Repeat("字", 300)
	rows.setBody("exc_a", fmt.Sprintf(`{"id":"exc_a","state":"running","turn_count":1,"brief":%q,"session_title":{"text":%q},`+
		`"pending_permission":{"request_id":"p1","tool_name":%q}}`+"\n", long, long, long))
	flushOnce(t, e, "exc_a")
	ev := c.waitFor(t, 1)[0]
	want := strings.Repeat("字", 256)
	assert.Equal(t, want, ev.Title)
	assert.Equal(t, want, ev.Brief)
	assert.Equal(t, want, ev.ToolName)
	assert.True(t, utf8.ValidString(ev.Title))
	assert.Equal(t, "ab", clipRunes("ab"))
	assert.Equal(t, strings.Repeat("a", 256), clipRunes(strings.Repeat("a", 300)))
	assert.Equal(t, strings.Repeat("字", 100), clipRunes(strings.Repeat("字", 100)))
}

func TestWorkerNotifyHub_CloseIsBoundedWhenASubscriberIsStuck(t *testing.T) {
	h := &workerNotifyHub{closeWait: 100 * time.Millisecond}
	block, entered := make(chan struct{}), make(chan struct{})
	defer close(block)
	h.subscribe(func(WorkerNotifyEvent) { close(entered); <-block })
	h.publish(WorkerNotifyEvent{})
	<-entered
	start := time.Now()
	h.close()
	assert.Less(t, time.Since(start), 2*time.Second)
	second := time.Now()
	h.close() // Stop then Close: the second must not wait out the budget again
	assert.Less(t, time.Since(second), 50*time.Millisecond)
	assert.Less(t, time.Since(start), 190*time.Millisecond, "two closes cost about one wait")
}

func TestProjector_NoSubscriberChangesNothing(t *testing.T) {
	rows := newRowServer()
	rows.set("exc_a", "running")
	e := startProjEnv(t, fastTiming, rows)
	e.p.notify = &workerNotifyHub{} // a hub nobody listens to
	rows.set("exc_a", "idle")
	e.p.markFrame("exc_a", "execution.terminal")
	d := nextDelta(t, e.sub)
	assert.Equal(t, "exc_a", d.ID)
	assert.Equal(t, int64(0), e.p.notify.Dropped())
	// A projector with no hub at all (notify nil) is what every other projector test runs.
}

func TestWorkerNotifyHub_FullQueueDropsAndCounts(t *testing.T) {
	h := &workerNotifyHub{}
	block, entered := make(chan struct{}), make(chan struct{}, 1)
	unsub := h.subscribe(func(WorkerNotifyEvent) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-block
	})
	h.publish(WorkerNotifyEvent{})
	<-entered // the consumer holds one; the queue is empty and then fills
	for i := 0; i < workerNotifySubBuffer+5; i++ {
		h.publish(WorkerNotifyEvent{})
	}
	assert.Equal(t, int64(5), h.Dropped())
	close(block)
	unsub()
	h.close()
}

func TestWorkerNotifyHub_PanicIsRecoveredAndCloseEndsGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()
	h := &workerNotifyHub{}
	c := &collector{}
	h.subscribe(func(ev WorkerNotifyEvent) {
		if ev.ExecID == "boom" {
			panic("x")
		}
		c.add(ev)
	})
	h.subscribe(func(WorkerNotifyEvent) {})
	h.publish(WorkerNotifyEvent{ExecID: "boom"})
	h.publish(WorkerNotifyEvent{ExecID: "ok"})
	c.waitFor(t, 1)
	h.close()
	h.publish(WorkerNotifyEvent{ExecID: "late"}) // after close: nothing, no panic
	h.subscribe(func(WorkerNotifyEvent) { t.Error("subscribed after close") })()
	assert.False(t, h.has())
	require.Eventually(t, func() bool { return runtime.NumGoroutine() <= before }, 2*time.Second, 5*time.Millisecond,
		"subscriber goroutines outlived close")
}

func TestInitRegistersWorkerNotifyFeed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	c := newTestCore(&cfg)
	m := New()
	m.logf = discardLogf
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, noopEngine(), nil)
	require.NoError(t, m.Init(c))
	assert.Equal(t, "nex.worker-notify-feed", WorkerNotifyKey)
	svc, ok := c.Registry.Get(WorkerNotifyKey)
	require.True(t, ok)
	feed, ok := svc.(WorkerNotifyFeed)
	require.True(t, ok)
	got := make(chan WorkerNotifyEvent, 1)
	unsub := feed.SubscribeWorkerNotify(func(ev WorkerNotifyEvent) { got <- ev })
	defer unsub()
	m.workerHub.publish(WorkerNotifyEvent{ExecID: "e1"})
	select {
	case ev := <-got:
		assert.Equal(t, "e1", ev.ExecID)
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
	}
	require.NoError(t, m.Stop(t.Context()))
}

func TestModule_StopClosesTheWorkerHub(t *testing.T) {
	m := &Module{}
	unsub := m.SubscribeWorkerNotify(func(WorkerNotifyEvent) {})
	assert.True(t, m.workerHub.has())
	require.NoError(t, m.Stop(t.Context()))
	assert.False(t, m.workerHub.has())
	unsub()
}
