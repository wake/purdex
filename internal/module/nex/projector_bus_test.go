package nex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/bus"
	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/store"
)

// #1866 PR1b (spec 2026-10-08 §3.2, §3.7): the projector fed from the
// engine's bus, wired into the module's lifecycle, keeping lastPushed.

func publish(b *bus.Bus, id, kind string) {
	b.PublishDurable(store.Event{ExecutionID: id, Kind: kind, Payload: json.RawMessage(`{}`)})
}

// specTriggers is §3.2's trigger list, spelled out, plus peer_message
// (Nexen v0.20.0), which the list predates.
var specTriggers = []string{
	"execution.delegated", "execution.rejected", "execution.running", "execution.terminal",
	"execution.interrupted", "execution.error", "execution.message_accepted", "execution.interrupt_requested",
	"execution.turn_stalled", "execution.turn_orphaned", "execution.terminated", "execution.archived",
	"execution.unarchived", "execution.title_changed", "execution.observer_attached", "execution.observer_detached",
	"execution.credential_repaired", "permission.requested", "permission.resolved", "tool_use", "tool_result",
	"task_start", "task_end", "result", "lease.acquired", "lease.released", "lease.renewed",
	"peer_message",
}

// notRowChanges are the durable kinds Nexen declares (execution.EventKinds)
// that deliberately do not mark their execution, each with the reason it
// never changes a list row. Empty: every kind Nexen declares today can.
var notRowChanges = map[string]string{}

// undeclaredTriggers are the triggers Nexen does not declare in
// execution.EventKinds, each with the reason.
var undeclaredTriggers = map[string]string{
	"result":        "provider-native (claude's stream-json), an open set Nexen only passes through",
	"lease.renewed": "transient: bus only, never persisted, so not a durable kind",
}

// Every durable kind Nexen declares is classified — a trigger, or in
// notRowChanges with its reason — so a kind a later Nexen adds fails here
// until someone decides whether it changes a row (peer_message, Nexen
// v0.20.0, was missed that way). And every trigger is a kind Nexen
// declares, or in undeclaredTriggers: a kind Nexen renames would otherwise
// leave a trigger that never fires.
func TestProjector_EveryNexenEventKindIsClassified(t *testing.T) {
	declared := map[string]bool{}
	for _, kind := range execution.EventKinds {
		declared[kind] = true
		_, excluded := notRowChanges[kind]
		switch {
		case triggerKinds[kind] && excluded:
			t.Errorf("%q is both a trigger and in notRowChanges", kind)
		case !triggerKinds[kind] && !excluded:
			t.Errorf("Nexen event kind %q is neither in triggerKinds nor in notRowChanges: decide whether it changes a list row", kind)
		}
	}
	for kind := range notRowChanges {
		assert.True(t, declared[kind], "notRowChanges lists %q, which Nexen no longer declares", kind)
	}
	for kind := range triggerKinds {
		_, undeclared := undeclaredTriggers[kind]
		assert.True(t, declared[kind] != undeclared,
			"trigger %q: declared by Nexen = %v, in undeclaredTriggers = %v; exactly one must hold", kind, declared[kind], undeclared)
	}
}

func TestProjector_TriggerKindsMarkAndNoiseDoesNot(t *testing.T) {
	e := newProjEnv(t, projectorTiming{trailing: time.Hour, maxDelay: time.Hour, retryDelay: time.Hour})
	var want []string
	for i, kind := range specTriggers {
		id := fmt.Sprintf("exc_t%02d", i)
		want = append(want, id)
		if kind == "lease.renewed" { // transient on the real bus
			e.bus.PublishTransient(id, kind, json.RawMessage(`{}`))
			continue
		}
		publish(e.bus, id, kind)
	}
	noise := []string{"stream_event", "stream_snapshot", "assistant", "user", "system", "rate_limit_event",
		"control_request", "control_response", "raw", "execution.some_future_kind"}
	for i, kind := range noise {
		e.bus.PublishTransient(fmt.Sprintf("exc_n%02d", i), kind, json.RawMessage(`{}`))
	}
	publish(e.bus, "", "execution.running")       // no attribution: nothing to read
	publish(e.bus, "bad/id", "execution.running") // the row reader would refuse it anyway
	publish(e.bus, "exc_zz", "execution.running") // last: once it is marked, every frame above was seen
	want = append(want, "exc_zz")

	waitDirty(t, e.p, "exc_zz")
	assert.Equal(t, want, dirtyIDs(e.p))
	e.p.mu.Lock()
	defer e.p.mu.Unlock()
	assert.Equal(t, []string{"execution.archived"}, sortedKinds(e.p.dirty["exc_t11"].cause))
}

// The bus consumer never waits on the slot: with the slot held, three bus
// buffers' worth of frames are still drained, chunk after chunk (a blocked
// consumer would never empty its channel, and would be kicked off the bus),
// and the marks flush once the slot frees.
func TestProjector_ConsumerKeepsDrainingWhileTheSlotIsHeld(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	require.NoError(t, e.slot.acquire(context.Background(), 0))
	for chunk := 0; chunk < 6; chunk++ {
		for i := 0; i < projectorBusBuffer/2; i++ {
			publish(e.bus, fmt.Sprintf("exc_%d", i%3), "tool_use")
		}
		require.Eventually(t, func() bool { return len(e.p.sub.Ch) == 0 }, 2*time.Second, time.Millisecond,
			"the consumer stopped draining while the slot was held")
	}
	publish(e.bus, "exc_zz", "execution.running")
	waitDirty(t, e.p, "exc_zz")
	e.slot.release()

	ids := map[string]bool{}
	for len(ids) < 4 {
		ids[nextDelta(t, e.sub).ID] = true
	}
	assert.Empty(t, e.logs.all(), "the consumer lost its subscription")
}

func TestProjector_StopUnsubscribesFromTheBus(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	e.p.stop(context.Background())
	closed := make(chan struct{})
	go func() {
		for range e.p.sub.Ch { // drain what was buffered; Unsubscribe closed it
		}
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("stop left the bus subscription open")
	}
	publish(e.bus, "exc_a", "execution.running")
	assert.Empty(t, dirtyIDs(e.p))
	assert.Empty(t, e.logs.all(), "an unsubscribe of our own was reported as a lost subscription")
}

// A bus that closes under a running projector ends the consumer with a log
// line (the resubscribe is PR1c's); the worker keeps flushing what is marked.
func TestProjector_ClosedBusEndsTheConsumerWithALogLine(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	e.bus.Close()
	require.Eventually(t, func() bool {
		return slices.Contains(e.logs.all(), "nex-delta: bus subscription closed; execution deltas stopped")
	}, 2*time.Second, 2*time.Millisecond)

	e.rows.set("exc_a", "idle")
	e.p.markFrame("exc_a", "execution.running")
	assert.Equal(t, "exc_a", nextDelta(t, e.sub).ID)
}

// lastPushed (§3.7) holds each execution's last pushed ver and status
// digest, for PR1c's safety reconcile.
func TestProjector_RecordsWhatItPushed(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	e.rows.setBody("exc_1", singleRow)
	e.rows.setBody("exc_2", `{"id":"exc_2","state":"idle","archived":true,"turn_count":4,`+
		`"last_turn_reason":"completed","terminal_reason":"terminated","pending_permission":null}`)
	for _, id := range []string{"exc_1", "exc_2", "exc_gone"} {
		publish(e.bus, id, "execution.running")
		nextDelta(t, e.sub)
	}

	e.p.mu.Lock()
	defer e.p.mu.Unlock()
	assert.Equal(t, map[string]pushedRow{
		"exc_1": {ver: 1, digest: rowDigest{State: "idle", PermissionRequest: "perm_1", TurnCount: 3}},
		"exc_2": {ver: 2, digest: rowDigest{State: "idle", Archived: true, TurnCount: 4,
			LastTurnReason: "completed", TerminalReason: "terminated"}},
		"exc_gone": {ver: 3, removed: true},
	}, e.p.pushed)
}

func TestModule_ProjectorRunsOnlyWithABusAndStopsBeforeTheEngine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	ctx := context.Background()

	m := New()
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, noopEngine(), nil)
	m.logf = discardLogf
	require.NoError(t, m.Init(newTestCore(&cfg)))
	require.NoError(t, m.Start(ctx))
	assert.Nil(t, m.proj, "a projector started without a bus")
	require.NoError(t, m.Stop(ctx))

	var p *projector
	stoppedFirst := false
	eng := noopEngine()
	eng.bus = bus.New()
	eng.shutdown = func(context.Context) error {
		select {
		case <-p.done:
			stoppedFirst = true
		default:
		}
		return nil
	}
	m = New()
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, eng, nil)
	m.logf = discardLogf
	require.NoError(t, m.Init(newTestCore(&cfg)))
	require.NoError(t, m.Start(ctx))
	p = m.proj
	require.NotNil(t, p)
	assert.Same(t, m.reads(), p.slot, "the projector must share the list wrapper's slot")
	require.NoError(t, m.Stop(ctx))
	assert.True(t, stoppedFirst, "the engine drained while the projector still ran")
	assert.Nil(t, m.proj)

	// Close without Stop stops it too, before the store closes.
	m = New()
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, engine{handler: http.NotFoundHandler(), bus: bus.New(),
		close: func() error {
			select {
			case <-p.done:
			default:
				t.Error("the store closed while the projector still ran")
			}
			return nil
		}}, nil)
	m.logf = discardLogf
	require.NoError(t, m.Init(newTestCore(&cfg)))
	require.NoError(t, m.Start(ctx))
	p = m.proj
	require.NoError(t, m.Close())
}
