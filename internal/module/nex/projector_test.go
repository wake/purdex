package nex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/bus"

	"github.com/wake/purdex/internal/core"
)

// #1866 PR1b (spec 2026-10-08 §3.2, §3.3, §3.5): the projector's flush
// worker turns marked executions into rows pushed on /ws/host-events.

// rowServer is a fake engine handler. GET /v1/executions/{id} answers id's
// row — 404 execution_not_found when it has none — after failing the first
// fails[id] reads with a 500; a script (states) sets the row's state anew
// for each read until it runs out. GET /v1/executions answers listPage,
// first waiting on listGate when one is set.
type rowServer struct {
	mu          sync.Mutex
	rows        map[string]string
	fails       map[string]int
	reads       map[string]int
	script      map[string][]string
	listEntered chan struct{}
	listGate    chan struct{}
}

func newRowServer() *rowServer {
	return &rowServer{rows: map[string]string{}, fails: map[string]int{}, reads: map[string]int{},
		script: map[string][]string{}}
}

// states makes id's next reads answer these states, one per read, the last
// one for good.
func (s *rowServer) states(id string, states ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.script[id] = states
}

func rowBody(id, state string) string {
	return fmt.Sprintf(`{"id":%q,"state":%q,"turn_count":1}`, id, state) + "\n"
}

// set gives id a row in state.
func (s *rowServer) set(id, state string) {
	s.setBody(id, rowBody(id, state))
}

func (s *rowServer) setBody(id, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[id] = body
}

func (s *rowServer) failNext(id string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fails[id] = n
}

func (s *rowServer) readsOf(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads[id]
}

func (s *rowServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/executions" {
		if s.listGate != nil {
			s.listEntered <- struct{}{}
			<-s.listGate
		}
		answer(http.StatusOK, listPage)(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/executions/")
	s.mu.Lock()
	s.reads[id]++
	if q := s.script[id]; len(q) > 0 {
		s.rows[id] = rowBody(id, q[0])
		s.script[id] = q[1:]
	}
	body, ok := s.rows[id]
	fail := s.fails[id] > 0
	if fail {
		s.fails[id]--
	}
	s.mu.Unlock()
	switch {
	case fail:
		answer(http.StatusInternalServerError, `{"error":"boom","code":"internal"}`)(w, r)
	case !ok:
		answer(http.StatusNotFound, `{"error":"no such execution","code":"execution_not_found"}`)(w, r)
	default:
		answer(http.StatusOK, body)(w, r)
	}
}

// fastTiming coalesces quickly enough for tests to wait in real time.
var fastTiming = projectorTiming{trailing: 20 * time.Millisecond, maxDelay: 80 * time.Millisecond, retryDelay: 40 * time.Millisecond}

// projEnv is a running projector over a real Nexen bus, a rowServer and a
// broadcaster with one test subscriber, opted into nex.v1.
type projEnv struct {
	p      *projector
	slot   *readSlot
	bus    *bus.Bus
	events *core.EventsBroadcaster
	sub    *core.EventSubscriber
	rows   *rowServer
	logs   *logRecorder
}

func newProjEnv(t *testing.T, timing projectorTiming) *projEnv {
	t.Helper()
	e := &projEnv{slot: newReadSlot(discardLogf), bus: bus.New(), events: core.NewEventsBroadcaster(),
		rows: newRowServer(), logs: &logRecorder{}}
	e.sub = e.events.AddTestSubscriberWith(core.FeatureNexV1)
	e.p = newProjector(e.slot, rowReader{handler: e.rows, logf: e.logs.logf}, e.events, e.bus, e.logs.logf, timing)
	e.p.start()
	t.Cleanup(func() {
		e.p.stop(context.Background())
		e.events.RemoveTestSubscriber(e.sub)
	})
	return e
}

// delta is a nex.execution frame's value, decoded.
type delta struct {
	Epoch string          `json:"epoch"`
	Bseq  uint64          `json:"bseq"`
	ID    string          `json:"id"`
	Ver   uint64          `json:"ver"`
	Cause []string        `json:"cause"`
	Row   json.RawMessage `json:"row"`
}

// nextFrame returns the next frame queued for sub, waiting up to 2 s.
func nextFrame(t *testing.T, sub *core.EventSubscriber) (core.HostEvent, string) {
	t.Helper()
	select {
	case msg, ok := <-sub.SendCh():
		require.True(t, ok, "the subscriber was removed")
		var ev core.HostEvent
		require.NoError(t, json.Unmarshal(msg, &ev))
		return ev, string(msg)
	case <-time.After(2 * time.Second):
		t.Fatal("no frame within 2s")
		return core.HostEvent{}, ""
	}
}

func nextDelta(t *testing.T, sub *core.EventSubscriber) delta {
	t.Helper()
	ev, _ := nextFrame(t, sub)
	require.Equal(t, "nex.execution", ev.Type)
	var d delta
	require.NoError(t, json.Unmarshal([]byte(ev.Value), &d))
	return d
}

func noFrame(t *testing.T, sub *core.EventSubscriber, within time.Duration) {
	t.Helper()
	select {
	case msg := <-sub.SendCh():
		t.Fatalf("unexpected frame %s", msg)
	case <-time.After(within):
	}
}

// dirtyIDs is the executions marked and not yet popped for a flush.
func dirtyIDs(p *projector) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make([]string, 0, len(p.dirty))
	for id := range p.dirty {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func waitDirty(t *testing.T, p *projector, id string) {
	t.Helper()
	require.Eventually(t, func() bool { return slices.Contains(dirtyIDs(p), id) }, 2*time.Second, 2*time.Millisecond,
		"%s was never marked", id)
}

// Coalescing, deterministically: a mark is due a trailing window after the
// latest event, but never later than the cap after the batch's first mark;
// a scheduled re-mark is due exactly when scheduled.
func TestDirtyExec_DueIsTrailingCappedAtTheFirstMark(t *testing.T) {
	tm := projectorTiming{trailing: 75 * time.Millisecond, maxDelay: 250 * time.Millisecond}
	p := &projector{timing: tm, dirty: map[string]*dirtyExec{}}
	t0 := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	ms := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Millisecond) }

	p.markLocked("x", mark{kinds: []string{"tool_use"}, at: ms(0)})
	assert.Equal(t, ms(75), p.dirty["x"].due(tm))
	p.markLocked("x", mark{kinds: []string{"tool_result"}, at: ms(50)})
	assert.Equal(t, ms(125), p.dirty["x"].due(tm), "a later event did not push the trailing edge")
	for n := 100; n <= 400; n += 40 {
		p.markLocked("x", mark{kinds: []string{"tool_use"}, at: ms(n)})
	}
	assert.Equal(t, ms(250), p.dirty["x"].due(tm), "a steady stream postponed the flush past the cap")
	assert.Equal(t, []string{"tool_result", "tool_use"}, sortedKinds(p.dirty["x"].cause))

	p.markLocked("y", mark{at: ms(1000), reMark: true})
	assert.Equal(t, ms(1000), p.dirty["y"].due(tm))
}

func TestProjector_CoalescesABurstIntoOneFlushWithEveryCause(t *testing.T) {
	e := newProjEnv(t, projectorTiming{trailing: 50 * time.Millisecond, maxDelay: time.Second, retryDelay: time.Hour})
	e.rows.set("exc_a", "running")
	for _, kind := range []string{"execution.running", "permission.requested", "tool_use", "permission.requested"} {
		e.p.markFrame("exc_a", kind)
	}
	d := nextDelta(t, e.sub)
	assert.Equal(t, []string{"execution.running", "permission.requested", "tool_use"}, d.Cause)
	noFrame(t, e.sub, 150*time.Millisecond)
	assert.Equal(t, 1, e.rows.readsOf("exc_a"))
}

// An event every 10 ms never leaves a 60 ms quiet window, so only the cap
// can flush while the stream goes on.
func TestProjector_SteadyStreamStillFlushesByTheCap(t *testing.T) {
	e := newProjEnv(t, projectorTiming{trailing: 60 * time.Millisecond, maxDelay: 150 * time.Millisecond, retryDelay: time.Hour})
	e.rows.set("exc_a", "running")
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				e.p.markFrame("exc_a", "tool_use")
			}
		}
	}()
	d := nextDelta(t, e.sub) // the stream is still running
	close(stop)
	<-stopped
	assert.Equal(t, "exc_a", d.ID)
}

// The exact value of a delta, and HostEvent.Epoch/Seq stay empty (§3.5,
// round 2 #3).
func TestProjector_DeltaWireFormat(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	e.rows.setBody("exc_1", singleRow) // carries lease and live_turn_id, which the row reader drops
	e.p.markFrame("exc_1", "permission.requested")

	ev, msg := nextFrame(t, e.sub)
	assert.Equal(t, "nex.execution", ev.Type)
	assert.Equal(t, []string{"session", "type", "value"}, rawKeys(decodeTop(t, []byte(msg))),
		"versions must live in value, never in HostEvent.Epoch/Seq")
	row := `{"activity":{"phase":"idle"},"archived":false,"brief":"<b>fix & ship</b>","cost_usd":null,` +
		`"duration_ms":null,"event_count":12,"id":"exc_1","labels":{"team":"a"},"observers":0,` +
		`"pending_permission":{"request_id":"perm_1","tool_name":"Bash","since":5},"state":"idle","turn_count":3}`
	assert.Equal(t, `{"epoch":"`+e.slot.epoch+`","bseq":1,"id":"exc_1","ver":1,"cause":["permission.requested"],"row":`+row+`}`, ev.Value)

	e.p.markFrame("exc_gone", "execution.archived")
	ev, _ = nextFrame(t, e.sub)
	assert.Equal(t, `{"epoch":"`+e.slot.epoch+`","bseq":2,"id":"exc_gone","ver":2,"cause":["execution.archived"],"row":null}`, ev.Value)
}

func TestProjector_BseqIsContiguousInBroadcastOrderAndVerIncreases(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	for _, id := range []string{"exc_a", "exc_b", "exc_c"} {
		e.rows.set(id, "running")
		e.p.markFrame(id, "execution.running")
	}
	var got []delta
	for i := 0; i < 3; i++ {
		got = append(got, nextDelta(t, e.sub))
	}
	e.p.markFrame("exc_b", "execution.terminal")
	got = append(got, nextDelta(t, e.sub))

	var ids []string
	for i, d := range got {
		ids = append(ids, d.ID)
		assert.Equal(t, uint64(i+1), d.Bseq, "bseq out of broadcast order")
		if i > 0 {
			assert.Greater(t, d.Ver, got[i-1].Ver)
		}
	}
	assert.Equal(t, []string{"exc_a", "exc_b", "exc_c", "exc_b"}, ids, "ready executions were not flushed FIFO")
}

func TestProjector_FailedReadConsumesNothingAndIsRetriedOnce(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	e.rows.set("exc_f", "idle")
	e.rows.failNext("exc_f", 1)
	start := time.Now()
	e.p.markFrame("exc_f", "permission.resolved")

	d := nextDelta(t, e.sub)
	assert.GreaterOrEqual(t, time.Since(start), fastTiming.retryDelay, "retried before the retry delay")
	assert.Equal(t, uint64(1), d.Bseq, "the failed read consumed a bseq")
	assert.Equal(t, uint64(1), d.Ver, "the failed read consumed a ver")
	assert.Equal(t, []string{"permission.resolved"}, d.Cause, "the retry lost the failed batch's cause")
	assert.Equal(t, 2, e.rows.readsOf("exc_f"))
}

func TestProjector_ReadFailingTwiceGivesUp(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	e.rows.set("exc_f", "idle")
	e.rows.failNext("exc_f", 100)
	e.p.markFrame("exc_f", "execution.running")
	require.Eventually(t, func() bool { return e.rows.readsOf("exc_f") == 2 }, 2*time.Second, 2*time.Millisecond)
	noFrame(t, e.sub, 4*fastTiming.retryDelay)
	assert.Equal(t, 2, e.rows.readsOf("exc_f"), "retried more than once")

	e.rows.set("exc_ok", "idle")
	e.p.markFrame("exc_ok", "execution.running")
	d := nextDelta(t, e.sub)
	assert.Equal(t, [2]uint64{1, 1}, [2]uint64{d.Bseq, d.Ver}, "the failed reads left a gap")
}

// A flush waits while a list page holds the slot, and a page stamped between
// two deltas gets a ver between theirs (rule V) and the first one's bseq as
// its high-water mark (§8 R3-1).
func TestProjector_FlushWaitsForAListPageAndIsOrderedAgainstIt(t *testing.T) {
	rows := newRowServer()
	rows.set("exc_a", "running")
	m, mux := newListEnv(t, rows)
	events := core.NewEventsBroadcaster()
	sub := events.AddTestSubscriberWith(core.FeatureNexV1)
	p := newProjector(m.reads(), rowReader{handler: rows, logf: discardLogf}, events, bus.New(), discardLogf, fastTiming)
	p.start()
	t.Cleanup(func() { p.stop(context.Background()) })

	p.markFrame("exc_a", "execution.running")
	first := nextDelta(t, sub)

	rows.listEntered, rows.listGate = make(chan struct{}, 1), make(chan struct{})
	pageDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { pageDone <- serve(mux, context.Background(), http.MethodGet, "/api/nex/v1/executions") }()
	<-rows.listEntered
	p.markFrame("exc_a", "execution.terminal")
	noFrame(t, sub, 4*fastTiming.maxDelay) // due long ago, but the page holds the slot
	close(rows.listGate)
	page := stampOf(t, <-pageDone)
	second := nextDelta(t, sub)

	assert.Less(t, first.Ver, page.Ver)
	assert.Less(t, page.Ver, second.Ver)
	assert.Equal(t, first.Bseq, page.Bseq)
	assert.Equal(t, first.Bseq+1, second.Bseq)
}

// Marking never waits on the slot: with the slot held, marks still land, and
// they flush once it frees.
func TestProjector_MarkingNeverWaitsForTheSlot(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	require.NoError(t, e.slot.acquire(context.Background(), 0))
	marked := make(chan struct{})
	go func() {
		for i := 0; i < 3000; i++ {
			e.p.markFrame(fmt.Sprintf("exc_%d", i%3), "tool_use")
		}
		e.p.markFrame("exc_zz", "execution.running")
		close(marked)
	}()
	select {
	case <-marked:
	case <-time.After(2 * time.Second):
		t.Fatal("marking blocked while the slot was held")
	}
	waitDirty(t, e.p, "exc_zz")
	e.slot.release()

	// The batch popped before the slot was taken flushes first; the marks
	// that arrived meanwhile (its own included, as a new batch) follow.
	ids := map[string]bool{}
	for len(ids) < 4 {
		ids[nextDelta(t, e.sub).ID] = true
	}
}

// Stop ends the worker — waiting for the slot, with a retry pending — and
// nothing is read after it returns.
func TestProjector_StopEndsTheWorker(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	e.rows.set("exc_r", "idle")
	e.rows.failNext("exc_r", 1)
	e.p.markFrame("exc_r", "execution.running")
	require.Eventually(t, func() bool { return e.rows.readsOf("exc_r") == 1 }, 2*time.Second, 2*time.Millisecond)
	require.NoError(t, e.slot.acquire(context.Background(), 0))
	e.p.markFrame("exc_w", "execution.running")

	stopped := make(chan struct{})
	go func() {
		e.p.stop(context.Background())
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not return")
	}
	select {
	case <-e.p.done:
	default:
		t.Fatal("stop returned before the worker ended")
	}
	e.slot.release()
	time.Sleep(3 * fastTiming.retryDelay)
	assert.Equal(t, 1, e.rows.readsOf("exc_r"), "the retry ran after stop")
	assert.Equal(t, 0, e.rows.readsOf("exc_w"))
}
