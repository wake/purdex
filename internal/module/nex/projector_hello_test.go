package nex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/bus"

	"github.com/wake/purdex/internal/core"
)

// #1866 PR1b (spec 2026-10-08 §3.5): the hello a new subscriber gets under
// the slot.

// hello is a nex.executions.hello frame's value, decoded.
type hello struct {
	Epoch string `json:"epoch"`
	Bseq  uint64 `json:"bseq"`
}

func helloOf(t *testing.T, ev core.HostEvent) hello {
	t.Helper()
	require.Equal(t, "nex.executions.hello", ev.Type)
	var h hello
	require.NoError(t, json.Unmarshal([]byte(ev.Value), &h))
	return h
}

func isRemoved(sub *core.EventSubscriber) bool {
	select {
	case <-sub.Done():
		return true
	default:
		return false
	}
}

// At epoch start the hello's bseq is 0, spelled out in the wire JSON
// (§3.5, round 2 #3), and the versions live in value only.
func TestProjector_HelloAtEpochStartSpellsOutBseqZero(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	e.p.sendHello(e.sub)
	ev, msg := nextFrame(t, e.sub)
	assert.Equal(t, "nex.executions.hello", ev.Type)
	assert.Equal(t, `{"epoch":"`+e.slot.epoch+`","bseq":0}`, ev.Value)
	assert.Equal(t, []string{"session", "type", "value"}, rawKeys(decodeTop(t, []byte(msg))))
}

// The window between Add and the OnSubscribe callback, made deterministic:
// a delta broadcast to the new subscriber before its hello has bseq ≤
// hello.bseq, and the next one is hello.bseq+1.
func TestProjector_HelloSplitsTheDeltasAroundIt(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	e.rows.set("exc_a", "running")
	e.p.markFrame("exc_a", "execution.running")
	nextDelta(t, e.sub) // bseq 1, before the new subscriber exists

	sub := e.events.AddTestSubscriberWith(core.FeatureNexV1) // HandleHostEvents' Add
	t.Cleanup(func() { e.events.RemoveTestSubscriber(sub) })
	e.p.markFrame("exc_a", "tool_use")
	early := nextDelta(t, sub) // broadcast before the callback ran
	e.p.sendHello(sub)         // the OnSubscribe callback
	ev, _ := nextFrame(t, sub)
	h := helloOf(t, ev)
	e.p.markFrame("exc_a", "tool_result")
	late := nextDelta(t, sub)

	assert.Equal(t, uint64(2), early.Bseq)
	assert.LessOrEqual(t, early.Bseq, h.Bseq)
	assert.Equal(t, h.Bseq+1, late.Bseq)
}

// The same with deltas flowing: a hello taken at any moment splits the
// subscriber's frames at its bseq, and the deltas after it are contiguous.
func TestProjector_HelloSplitsAStreamOfDeltas(t *testing.T) {
	e := newProjEnv(t, projectorTiming{trailing: time.Millisecond, maxDelay: 2 * time.Millisecond, retryDelay: time.Hour})
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
				e.p.markFrame(fmt.Sprintf("exc_%d", i%5), "tool_use")
			}
		}
	}()
	defer func() {
		close(stop)
		wg.Wait()
	}()
	time.Sleep(20 * time.Millisecond)

	sub := e.events.AddTestSubscriberWith(core.FeatureNexV1)
	t.Cleanup(func() { e.events.RemoveTestSubscriber(sub) })
	e.p.sendHello(sub)
	var h *hello
	var before, after []uint64
	for len(after) < 5 {
		ev, _ := nextFrame(t, sub)
		if ev.Type == "nex.executions.hello" {
			got := helloOf(t, ev)
			h = &got
			continue
		}
		var d delta
		require.NoError(t, json.Unmarshal([]byte(ev.Value), &d))
		if h == nil {
			before = append(before, d.Bseq)
		} else {
			after = append(after, d.Bseq)
		}
	}
	for _, b := range before {
		assert.LessOrEqual(t, b, h.Bseq, "a delta queued before the hello is newer than it")
	}
	for i, a := range after {
		assert.Equal(t, h.Bseq+1+uint64(i), a, "the deltas after the hello are not contiguous from it")
	}
}

func TestProjector_HelloThatCannotBeQueuedRemovesTheSubscriber(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	fill(t, e.sub)
	e.p.sendHello(e.sub)
	assert.True(t, isRemoved(e.sub), "a hello that could not be queued kept the subscriber")
}

// A hello waits for the slot at most helloWait; one that could not get it
// closes the connection, exactly as one that could not be queued.
func TestProjector_HelloThatCannotGetTheSlotRemovesTheSubscriber(t *testing.T) {
	e := newProjEnv(t, projectorTiming{helloWait: 30 * time.Millisecond})
	require.NoError(t, e.slot.acquire(context.Background(), 0))
	defer e.slot.release()
	e.p.sendHello(e.sub)
	assert.True(t, isRemoved(e.sub))
	assert.Empty(t, e.sub.SendCh(), "a hello was queued without the slot")
}

func TestProjector_StoppedProjectorSendsNoHello(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	e.p.stop(context.Background())
	e.p.sendHello(e.sub)
	noFrame(t, e.sub, 50*time.Millisecond)
	assert.False(t, isRemoved(e.sub), "a stopped projector closed a connection")
}

// startModuleWithEvents starts a module over a fake engine with a bus — so
// its projector runs — and serves its core's /ws/host-events; dial connects
// with a raw query string.
func startModuleWithEvents(t *testing.T) (*Module, func(query string) *websocket.Conn) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	eng := noopEngine()
	eng.bus = bus.New()
	m := New()
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, eng, nil)
	m.logf = discardLogf
	require.NoError(t, m.Init(newTestCore(&cfg)))
	require.NoError(t, m.Start(context.Background()))
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	srv := httptest.NewServer(http.HandlerFunc(m.core.Events.HandleHostEvents))
	t.Cleanup(srv.Close)
	return m, func(query string) *websocket.Conn {
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+query, nil)
		require.NoError(t, err)
		return conn
	}
}

// Start registers the hello as an OnSubscribe callback: it is the first nex
// frame on a new /ws/host-events?nex=v1 connection, and after Stop none is
// sent.
func TestModule_NewConnectionGetsTheHello(t *testing.T) {
	m, dial := startModuleWithEvents(t)

	conn := dial("?nex=v1")
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)
	var ev core.HostEvent
	require.NoError(t, json.Unmarshal(msg, &ev))
	assert.Equal(t, hello{Epoch: m.reads().epoch, Bseq: 0}, helloOf(t, ev))
	conn.Close()

	require.NoError(t, m.Stop(context.Background()))
	conn = dial("?nex=v1")
	defer conn.Close()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(100*time.Millisecond)))
	_, msg, err = conn.ReadMessage()
	assert.Error(t, err, "a stopped projector sent %s", msg)
}

// A connection that did not opt in — an old SPA, purdex-ios — gets no hello
// and stays connected: the first frame it reads is the marker queued by an
// OnSubscribe callback registered after the hello's, and a later broadcast
// still reaches it.
func TestModule_ConnectionWithoutNexGetsNoHello(t *testing.T) {
	m, dial := startModuleWithEvents(t)
	m.core.Events.OnSubscribe(func(sub *core.EventSubscriber) {
		sub.Send([]byte(`{"type":"marker","session":"","value":""}`))
	})

	for _, query := range []string{"", "?nex=v2"} {
		conn := dial(query)
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
		_, msg, err := conn.ReadMessage()
		require.NoError(t, err)
		var ev core.HostEvent
		require.NoError(t, json.Unmarshal(msg, &ev))
		assert.Equal(t, "marker", ev.Type, "query %q: a frame came before the marker", query)

		m.core.Events.Broadcast("s", "status", "running")
		_, msg, err = conn.ReadMessage()
		require.NoError(t, err, "query %q: the connection was closed", query)
		require.NoError(t, json.Unmarshal(msg, &ev))
		assert.Equal(t, "status", ev.Type)
		conn.Close()
	}
}
