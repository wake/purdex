package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1866 PR1b-5: a subscriber that opted into nex.v1 is strict for EVERY
// frame, not only the nex ones. Its one queue carries the nex deltas and
// every other host event (tmux, sessions, agent.status, approvals…); if a
// nex burst fills it, a best-effort frame dropped after that would leave
// the client stale for good — nothing may ever follow it to show the gap.
// So any frame that cannot be queued for it ends it (its connection
// closes, the client reconnects and gets every snapshot again). A
// subscriber that did not opt in keeps today's best effort.

// registered reports whether sub is still in eb's subscriber set.
func registered(eb *EventsBroadcaster, sub *EventSubscriber) bool {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	_, ok := eb.subscribers[sub]
	return ok
}

func TestBroadcastEvent_EndsAnOptedInSubscriberWhoseBufferIsFull(t *testing.T) {
	for _, tc := range []struct {
		name      string
		broadcast func(eb *EventsBroadcaster)
	}{
		{"BroadcastEvent", func(eb *EventsBroadcaster) { eb.BroadcastEvent(HostEvent{Type: "tmux", Value: "unavailable"}) }},
		{"Broadcast", func(eb *EventsBroadcaster) { eb.Broadcast("s", "agent.status", `{}`) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eb := NewEventsBroadcaster()
			plain := eb.AddTestSubscriber()
			defer eb.RemoveTestSubscriber(plain)
			fillBuffer(t, plain)
			roomy := eb.AddTestSubscriberWith(FeatureNexV1)
			defer eb.RemoveTestSubscriber(roomy)
			opted := eb.AddTestSubscriberWith(FeatureNexV1)
			fillBuffer(t, opted)

			tc.broadcast(eb)

			assert.True(t, isDone(opted), "a frame an opted-in subscriber could not take was dropped silently")
			assert.False(t, registered(eb, opted), "the ended subscriber is still registered")
			left := drained(t, opted)
			assert.Len(t, left, cap(opted.send), "the frame that did not fit was queued anyway")
			for _, msg := range left {
				assert.Equal(t, `{"type":"fill"}`, msg)
			}

			assert.False(t, isDone(roomy), "an opted-in subscriber with room was ended")
			assert.Len(t, roomy.send, 1)
			assert.False(t, isDone(plain), "a subscriber without nex was ended by a best-effort frame")
			assert.True(t, registered(eb, plain))
			assert.Len(t, plain.send, cap(plain.send))
		})
	}
}

// An ended subscriber is out of every later broadcast: nothing more is
// queued for it, and it is not ended (or logged) twice.
func TestBroadcastEvent_EndedSubscriberIsNotSentToAgain(t *testing.T) {
	eb := NewEventsBroadcaster()
	opted := eb.AddTestSubscriberWith(FeatureNexV1)
	fillBuffer(t, opted)
	eb.BroadcastEvent(HostEvent{Type: "tmux", Value: "unavailable"})
	require.True(t, isDone(opted))

	assert.NotPanics(t, func() {
		eb.BroadcastEvent(HostEvent{Type: "tmux", Value: "ok"})
		eb.BroadcastStrictTo(FeatureNexV1, HostEvent{Type: "nex.execution"})
		assert.False(t, opted.TrySend([]byte(`{"type":"late"}`)))
	})
	assert.Len(t, drained(t, opted), cap(opted.send))
	assert.False(t, eb.HasSubscribers())
}

// A frame an OnSubscribe snapshot callback sends straight to the subscriber
// (sub.Send / sub.TrySend, as the session, team and agent modules do) is
// owed just the same: if it does not fit, the subscriber ends.
func TestSend_EndsAnOptedInSubscriberWhoseBufferIsFull(t *testing.T) {
	for _, tc := range []struct {
		name string
		send func(sub *EventSubscriber) bool
	}{
		{"Send", func(sub *EventSubscriber) bool { sub.Send([]byte(`{"type":"hook"}`)); return false }},
		{"TrySend", func(sub *EventSubscriber) bool { return sub.TrySend([]byte(`{"type":"hook"}`)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eb := NewEventsBroadcaster()
			opted := eb.AddTestSubscriberWith(FeatureNexV1)
			defer eb.RemoveTestSubscriber(opted)
			fillBuffer(t, opted)

			assert.False(t, tc.send(opted), "a frame that did not fit was reported queued")
			assert.True(t, isDone(opted), "a direct send an opted-in subscriber could not take was dropped silently")
			assert.Len(t, drained(t, opted), cap(opted.send), "the frame that did not fit was queued anyway")
		})
	}
}

// The other side of the same rule: a subscriber that did not opt in loses a
// direct send that does not fit, and nothing else happens to it.
func TestSend_LeavesASubscriberWithoutNexRunningWhenItsBufferIsFull(t *testing.T) {
	eb := NewEventsBroadcaster()
	plain := eb.AddTestSubscriber()
	defer eb.RemoveTestSubscriber(plain)
	fillBuffer(t, plain)

	plain.Send([]byte(`{"type":"hook"}`))
	assert.False(t, plain.TrySend([]byte(`{"type":"hook"}`)))
	assert.False(t, isDone(plain))
	assert.True(t, registered(eb, plain))
	assert.Len(t, plain.send, cap(plain.send))
}

// SendStrict to an opted-in subscriber ends and deregisters it when the
// frame does not fit (the hello path), the same as for any other.
func TestSendStrict_EndsAndDeregistersAnOptedInSubscriberWhoseBufferIsFull(t *testing.T) {
	eb := NewEventsBroadcaster()
	opted := eb.AddTestSubscriberWith(FeatureNexV1)
	fillBuffer(t, opted)

	assert.False(t, eb.SendStrict(opted, HostEvent{Type: "nex.executions.hello"}))
	assert.True(t, isDone(opted))
	assert.False(t, registered(eb, opted))
	assert.Len(t, drained(t, opted), cap(opted.send))
}

// Every way a frame reaches a subscriber, all at once, against subscribers
// that come, go and fill up: best-effort broadcasts, strict broadcasts,
// direct sends (as snapshot callbacks make them), Add and Remove. Nothing
// deadlocks (an ended subscriber is never Removed under the read lock), the
// race detector stays quiet, and no subscriber without nex is ever ended.
func TestOptedInStrictness_NoDeadlockUnderConcurrentSendsAndChurn(t *testing.T) {
	eb := NewEventsBroadcaster()
	const rounds = 100
	direct := make(chan *EventSubscriber, rounds)
	var mu sync.Mutex
	var plains []*EventSubscriber

	var wg sync.WaitGroup
	wg.Add(5)
	go func() { // never drained: each opted-in one fills up and is ended by something
		defer wg.Done()
		defer close(direct)
		for i := 0; i < rounds; i++ {
			if i%2 == 0 {
				direct <- eb.AddTestSubscriberWith(FeatureNexV1)
			} else {
				sub := eb.AddTestSubscriber()
				mu.Lock()
				plains = append(plains, sub)
				mu.Unlock()
			}
		}
	}()
	go func() { // a snapshot callback's direct sends, one past a full buffer
		defer wg.Done()
		for sub := range direct {
			for i := 0; i <= optedInSendBuffer; i++ {
				sub.Send([]byte(`{"type":"hook"}`))
			}
		}
	}()
	go func() { // added and removed by hand, racing the ends
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			sub := eb.AddTestSubscriberWith(FeatureNexV1)
			eb.BroadcastEvent(HostEvent{Type: "tmux", Value: fmt.Sprint(i)})
			eb.Remove(sub)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < optedInSendBuffer+defaultSendBuffer; i++ {
			eb.BroadcastEvent(HostEvent{Type: "tmux", Value: fmt.Sprint(i)})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < optedInSendBuffer+defaultSendBuffer; i++ {
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
	case <-time.After(20 * time.Second):
		t.Fatal("sends to opted-in subscribers deadlocked against Add/Remove")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, sub := range plains {
		assert.False(t, isDone(sub), "a subscriber without nex was ended")
	}
}

// --- over real connections ---------------------------------------------

// bigFrame is a valid host-events frame of size bytes or so. Large frames
// fill the socket buffers between server and client after a few writes, so
// a client that reads nothing stalls its write pump quickly.
func bigFrame(t *testing.T, size int) []byte {
	t.Helper()
	b, err := json.Marshal(HostEvent{Type: "fill", Value: strings.Repeat("x", size)})
	require.NoError(t, err)
	return b
}

// stall fills sub's send buffer to the brim behind a write pump that is
// blocked on a client reading nothing, without ever overflowing it: it
// queues only while there is room (only this test queues), and stops once
// the buffer has stayed full for a while — the pump is stuck. Every slot
// holds the same slice, so the memory is one frame.
func stall(t *testing.T, sub *EventSubscriber, frame []byte) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for full := 0; full < 5; {
		require.True(t, time.Now().Before(deadline), "the write pump never stalled")
		if len(sub.send) < cap(sub.send) {
			require.True(t, sub.TrySend(frame), "a send with room in the buffer was not queued")
			full = 0
			continue
		}
		full++
		time.Sleep(20 * time.Millisecond)
	}
}

// readUntilError reads frames until the connection fails, and returns the
// frame types it read and the error. A read that times out fails the test:
// the server never closed the connection.
func readUntilError(t *testing.T, conn *websocket.Conn) ([]string, error) {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
	var types []string
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			var ne net.Error
			require.False(t, errors.As(err, &ne) && ne.Timeout(), "the server never closed the connection: %v", err)
			return types, err
		}
		typ, err := frameType(msg)
		require.NoError(t, err)
		types = append(types, typ)
	}
}

// readUntilType reads frames until one of type want arrives, and returns
// the types read before it, or the error that ended the reads first. It
// runs off the test goroutine, so it reports rather than fails.
func readUntilType(conn *websocket.Conn, want string) ([]string, error) {
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, err
	}
	var types []string
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return types, fmt.Errorf("the connection failed before a %s frame arrived: %w", want, err)
		}
		typ, err := frameType(msg)
		if err != nil {
			return types, err
		}
		if typ == want {
			return types, nil
		}
		types = append(types, typ)
	}
}

// frameType reads a host-events frame's type, which Marshal writes first,
// without scanning the (possibly large) rest of the frame.
func frameType(msg []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(msg))
	var toks [3]json.Token
	for i := range toks {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		toks[i] = tok
	}
	typ, ok := toks[2].(string)
	if toks[0] != json.Delim('{') || toks[1] != "type" || !ok {
		return "", fmt.Errorf("not a host-events frame: %.60s", msg)
	}
	return typ, nil
}

// An opted-in client stops reading, a burst fills its queue, and then a
// plain tmux frame is broadcast: the server closes that connection — the
// client reads what was already on the wire, then sees the close, never the
// tmux frame. A client without nex in the same state loses the tmux frame
// and keeps its connection, exactly as before.
func TestHandleHostEvents_OptedInConnectionClosesWhenABestEffortFrameDoesNotFit(t *testing.T) {
	eb := NewEventsBroadcaster()
	subs := make(chan *EventSubscriber, 2)
	eb.OnSubscribe(func(sub *EventSubscriber) { subs <- sub })
	server := httptest.NewServer(http.HandlerFunc(eb.HandleHostEvents))
	defer server.Close()
	optedConn, opted := dialQuery(t, server, subs, "?nex=v1")
	plainConn, plain := dialQuery(t, server, subs, "")
	require.True(t, opted.Wants(FeatureNexV1))

	frame := bigFrame(t, 256<<10)
	stall(t, opted, frame)
	stall(t, plain, frame)

	eb.Broadcast("", "tmux", "unavailable")

	types, err := readUntilError(t, optedConn)
	require.Error(t, err)
	assert.NotContains(t, types, "tmux", "the frame that did not fit reached the client")
	assert.True(t, isDone(opted))
	require.Eventually(t, func() bool { return !registered(eb, opted) }, 5*time.Second, 10*time.Millisecond,
		"the closed connection's subscriber is still registered")

	assert.False(t, isDone(plain), "a connection without nex was closed over a best-effort frame")
	assert.True(t, registered(eb, plain))
	type result struct {
		before []string
		err    error
	}
	got := make(chan result, 1)
	go func() {
		before, err := readUntilType(plainConn, "after")
		got <- result{before, err}
	}()
	require.Eventually(t, func() bool { return len(plain.send) == 0 }, 10*time.Second, 5*time.Millisecond,
		"the client without nex never drained its backlog")
	eb.Broadcast("", "after", "x")
	select {
	case r := <-got:
		require.NoError(t, r.err, "a connection without nex was closed")
		assert.NotContains(t, r.before, "tmux", "a best-effort frame that did not fit was queued anyway")
	case <-time.After(15 * time.Second):
		t.Fatal("the client without nex never got the frame after the dropped one")
	}
	assert.False(t, isDone(plain))
}

// The same for a frame sent straight to the subscriber, as an OnSubscribe
// snapshot callback does: the connection closes, and the subscriber is
// deregistered by the connection's own goroutines (a direct send has no
// broadcaster lock to release first).
func TestHandleHostEvents_OptedInConnectionClosesWhenADirectSendDoesNotFit(t *testing.T) {
	eb := NewEventsBroadcaster()
	subs := make(chan *EventSubscriber, 1)
	eb.OnSubscribe(func(sub *EventSubscriber) { subs <- sub })
	server := httptest.NewServer(http.HandlerFunc(eb.HandleHostEvents))
	defer server.Close()
	conn, opted := dialQuery(t, server, subs, "?nex=v1")

	stall(t, opted, bigFrame(t, 256<<10))
	opted.Send([]byte(`{"type":"hook","session":"s","value":"{}"}`))

	types, err := readUntilError(t, conn)
	require.Error(t, err)
	assert.NotContains(t, types, "hook")
	assert.True(t, isDone(opted))
	require.Eventually(t, func() bool { return !eb.HasSubscribers() }, 5*time.Second, 10*time.Millisecond,
		"the closed connection's subscriber is still registered")
}
