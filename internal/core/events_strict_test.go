package core

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1866 PR1b (spec 2026-10-08 §3.5, §8 round 2 #4): strict sends drop the
// subscriber, never the frame — and only ever a subscriber that opted into
// the frame's feature.

// fillBuffer queues frames until sub's send buffer is full.
func fillBuffer(t *testing.T, sub *EventSubscriber) {
	t.Helper()
	for i := 0; i < cap(sub.send); i++ {
		require.True(t, sub.TrySend([]byte(`{"type":"fill"}`)))
	}
}

// isDone reports whether sub's Done is closed, without waiting.
func isDone(sub *EventSubscriber) bool {
	select {
	case <-sub.Done():
		return true
	default:
		return false
	}
}

// drained returns every frame left in a removed subscriber's buffer (its
// channel is closed, so the range ends).
func drained(t *testing.T, sub *EventSubscriber) []string {
	t.Helper()
	require.True(t, isDone(sub), "drained needs a removed subscriber")
	var out []string
	for msg := range sub.SendCh() {
		out = append(out, string(msg))
	}
	return out
}

// next returns the next queued frame, failing if there is none.
func next(t *testing.T, sub *EventSubscriber) string {
	t.Helper()
	select {
	case msg := <-sub.SendCh():
		return string(msg)
	default:
		t.Fatal("no frame queued")
		return ""
	}
}

func marshalled(t *testing.T, ev HostEvent) string {
	t.Helper()
	b, err := json.Marshal(ev)
	require.NoError(t, err)
	return string(b)
}

func TestBroadcastStrictTo_RemovesOnlyTheOptedInSubscriberWhoseBufferIsFull(t *testing.T) {
	eb := NewEventsBroadcaster()
	fast := eb.AddTestSubscriberWith(FeatureNexV1)
	defer eb.RemoveTestSubscriber(fast)
	slow := eb.AddTestSubscriberWith(FeatureNexV1)
	fillBuffer(t, slow)

	first := HostEvent{Type: "nex.execution", Value: `{"bseq":1}`}
	eb.BroadcastStrictTo(FeatureNexV1, first)

	assert.Equal(t, marshalled(t, first), next(t, fast), "the subscriber with room got the frame")
	assert.False(t, isDone(fast), "the subscriber with room was removed")
	assert.True(t, isDone(slow), "the subscriber whose buffer was full is still registered")
	assert.True(t, eb.HasSubscribers())

	// A removed subscriber is never sent to again: its buffer holds exactly
	// the frames queued before the drop, and later strict frames reach the
	// one left.
	second := HostEvent{Type: "nex.execution", Value: `{"bseq":2}`}
	eb.BroadcastStrictTo(FeatureNexV1, second)
	assert.Equal(t, marshalled(t, second), next(t, fast))
	left := drained(t, slow)
	assert.Len(t, left, cap(slow.send))
	for _, msg := range left {
		assert.Equal(t, `{"type":"fill"}`, msg)
	}
}

// A subscriber that did not opt into the feature is outside its stream
// entirely: it is never sent a frame, and a full buffer never costs it its
// connection — a frame it never asked for is not one it can miss.
func TestBroadcastStrictTo_LeavesSubscribersWithoutTheFeatureAlone(t *testing.T) {
	eb := NewEventsBroadcaster()
	plain := eb.AddTestSubscriber()
	defer eb.RemoveTestSubscriber(plain)
	full := eb.AddTestSubscriber()
	defer eb.RemoveTestSubscriber(full)
	fillBuffer(t, full)
	other := eb.AddTestSubscriberWith("other.v1")
	defer eb.RemoveTestSubscriber(other)
	fillBuffer(t, other)
	opted := eb.AddTestSubscriberWith(FeatureNexV1)
	defer eb.RemoveTestSubscriber(opted)

	ev := HostEvent{Type: "nex.execution", Value: `{"bseq":1}`}
	eb.BroadcastStrictTo(FeatureNexV1, ev)

	assert.Equal(t, marshalled(t, ev), next(t, opted))
	assert.Empty(t, plain.send, "a subscriber without the feature was sent its frame")
	for _, sub := range []*EventSubscriber{plain, full, other} {
		assert.False(t, isDone(sub), "a subscriber without the feature was removed")
	}
	assert.Len(t, full.send, cap(full.send))
	assert.Len(t, other.send, cap(other.send))
}

// The frame is the same marshalled HostEvent BroadcastEvent would send: the
// versions of a nex frame live in its value, and omitempty keeps Epoch/Seq
// out of the wire when they are empty.
func TestBroadcastStrictTo_SendsTheMarshalledEvent(t *testing.T) {
	eb := NewEventsBroadcaster()
	sub := eb.AddTestSubscriberWith(FeatureNexV1)
	defer eb.RemoveTestSubscriber(sub)

	eb.BroadcastStrictTo(FeatureNexV1, HostEvent{Type: "nex.executions.hello", Value: `{"epoch":"e","bseq":0}`})
	assert.Equal(t, `{"type":"nex.executions.hello","session":"","value":"{\"epoch\":\"e\",\"bseq\":0}"}`, next(t, sub))
}

// Broadcast and BroadcastEvent stay best-effort for a subscriber that did
// not opt into nex.v1: a full buffer loses that frame and keeps the
// subscriber, exactly as before. (An opted-in one is strict for every
// frame: events_optedin_test.go.)
func TestBroadcastEvent_StaysBestEffort(t *testing.T) {
	eb := NewEventsBroadcaster()
	sub := eb.AddTestSubscriber()
	defer eb.RemoveTestSubscriber(sub)
	fillBuffer(t, sub)
	other := eb.AddTestSubscriberWith("other.v1")
	defer eb.RemoveTestSubscriber(other)
	fillBuffer(t, other)

	eb.BroadcastEvent(HostEvent{Type: "status", Value: "x"})
	eb.Broadcast("s", "status", "y")

	for _, s := range []*EventSubscriber{sub, other} {
		assert.False(t, isDone(s), "a best-effort broadcast removed a slow subscriber")
		assert.True(t, registered(eb, s), "a best-effort broadcast deregistered a slow subscriber")
		assert.Len(t, s.send, cap(s.send))
	}
}

func TestBroadcastStrictTo_NoSubscribersIsANoOp(t *testing.T) {
	eb := NewEventsBroadcaster()
	assert.NotPanics(t, func() { eb.BroadcastStrictTo(FeatureNexV1, HostEvent{Type: "nex.execution"}) })
}

// Remove takes the write lock, so the strict broadcast must not call it
// while it still holds the read lock: run strict broadcasts against
// subscribers that come, go and fill up, and require them all to finish.
func TestBroadcastStrictTo_NoDeadlockWithConcurrentAddAndRemove(t *testing.T) {
	eb := NewEventsBroadcaster()
	const rounds = 100
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { // never drained: each opted-in one fills up and is dropped by a strict broadcast
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			if i%2 == 0 {
				eb.AddTestSubscriberWith(FeatureNexV1)
			} else {
				eb.AddTestSubscriber() // never sent to, never dropped
			}
		}
	}()
	go func() { // added and removed by hand, racing the drops
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			sub := eb.AddTestSubscriberWith(FeatureNexV1)
			eb.Remove(sub)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 3*optedInSendBuffer; i++ { // enough to fill and drop every never-drained opted-in one
			eb.BroadcastStrictTo(FeatureNexV1, HostEvent{Type: "nex.execution", Value: fmt.Sprint(i)})
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("strict broadcasts deadlocked against Add/Remove")
	}
}

func TestSendStrict_QueuesToThatSubscriberOnly(t *testing.T) {
	eb := NewEventsBroadcaster()
	target := eb.AddTestSubscriber()
	defer eb.RemoveTestSubscriber(target)
	other := eb.AddTestSubscriber()
	defer eb.RemoveTestSubscriber(other)

	ev := HostEvent{Type: "nex.executions.hello", Value: `{"epoch":"e","bseq":0}`}
	assert.True(t, eb.SendStrict(target, ev))
	assert.Equal(t, marshalled(t, ev), next(t, target))
	assert.Empty(t, other.send, "a per-subscriber send reached another subscriber")
}

func TestSendStrict_FullBufferRemovesTheSubscriber(t *testing.T) {
	eb := NewEventsBroadcaster()
	other := eb.AddTestSubscriber()
	defer eb.RemoveTestSubscriber(other)
	sub := eb.AddTestSubscriber()
	fillBuffer(t, sub)

	assert.False(t, eb.SendStrict(sub, HostEvent{Type: "nex.executions.hello"}))
	assert.True(t, isDone(sub), "a strict send that could not be queued kept the subscriber")
	assert.Len(t, drained(t, sub), cap(sub.send), "the frame that did not fit was queued anyway")
	assert.False(t, isDone(other))
	assert.True(t, eb.HasSubscribers())
}

func TestSendStrict_RemovedSubscriberIsANoOpReturningFalse(t *testing.T) {
	eb := NewEventsBroadcaster()
	sub := eb.AddTestSubscriber()
	eb.Remove(sub)

	var ok bool
	assert.NotPanics(t, func() { ok = eb.SendStrict(sub, HostEvent{Type: "nex.executions.hello"}) })
	assert.False(t, ok)
	assert.Empty(t, drained(t, sub))
}
