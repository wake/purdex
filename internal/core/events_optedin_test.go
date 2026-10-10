package core

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
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

// pumpGate holds a subscriber's write pump still, deterministically (#2590). The pump blocks in conn.Write; a real socket only
// does that when the kernel's buffers are full, and a pump blocked on a client that reads nothing is not blocked for good — the
// kernel finishes the write it was in (buffers grow, a window probe lands) and the pump takes the next frame, which freed a slot
// a test had just found full. The gate is the server side's net.Conn: while armed, every Write waits for a permit, for open(), or
// for the connection to be closed.
type pumpGate struct {
	armed   atomic.Bool
	permits chan struct{}
	opened  chan struct{}
	waiting atomic.Int32 // Writes parked at the gate right now
	closeCh chan struct{}
	once    sync.Once
	openOnc sync.Once
}

func newPumpGate() *pumpGate {
	return &pumpGate{permits: make(chan struct{}, 1<<16), opened: make(chan struct{}), closeCh: make(chan struct{})}
}

// open lets every Write through for good.
func (g *pumpGate) open() { g.openOnc.Do(func() { close(g.opened) }) }

type gatedConn struct {
	net.Conn
	g *pumpGate
}

func (c *gatedConn) Write(p []byte) (int, error) {
	if c.g.armed.Load() {
		c.g.waiting.Add(1)
		defer c.g.waiting.Add(-1)
		select {
		case <-c.g.permits:
		case <-c.g.opened:
		case <-c.g.closeCh:
			return 0, net.ErrClosed
		}
	}
	return c.Conn.Write(p)
}

func (c *gatedConn) Close() error {
	c.g.once.Do(func() { close(c.g.closeCh) })
	return c.Conn.Close()
}

// gateWriter hands the upgrader a hijacked connection that goes through a pumpGate; the gate is announced on gates.
type gateWriter struct {
	http.ResponseWriter
	gates chan<- *pumpGate
}

func (w gateWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.ResponseWriter.(http.Hijacker).Hijack()
	if err != nil {
		return nil, nil, err
	}
	g := newPumpGate()
	w.gates <- g
	return &gatedConn{Conn: conn, g: g}, rw, nil
}

// gatedServer serves eb's /ws/host-events with every connection's pump behind a gate; the gates arrive in connection order.
func gatedServer(t *testing.T, eb *EventsBroadcaster) (*httptest.Server, <-chan *pumpGate) {
	t.Helper()
	gates := make(chan *pumpGate, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		eb.HandleHostEvents(gateWriter{w, gates}, r)
	}))
	t.Cleanup(server.Close)
	return server, gates
}

// jam makes sub's send buffer full behind a pump that is parked at gate, with certainty: it arms the gate, queues one frame
// and waits for the pump to take it and park in its Write, then queues cap(send) more — all of which fit. When it returns the
// pump holds a frame it cannot write and len(send) == cap(send); nothing changes that until the gate lets a Write through.
func jam(t *testing.T, sub *EventSubscriber, g *pumpGate) {
	t.Helper()
	filler := []byte(`{"type":"fill","session":"s","value":"{}"}`)
	g.armed.Store(true)
	require.True(t, sub.TrySend(filler))
	require.Eventually(t, func() bool { return len(sub.send) == 0 && g.waiting.Load() == 1 }, 5*time.Second, time.Millisecond,
		"the pump never parked at the gate with a frame in hand")
	for i := 0; i < cap(sub.send); i++ {
		require.True(t, sub.TrySend(filler), "a send with room in the buffer was not queued")
	}
	require.Equal(t, cap(sub.send), len(sub.send))
}

// The reads below wait for progress, not for a total time (#2590): a stalled client's backlog is hundreds of 256 KiB frames,
// and how long the machine takes to move them depends on its load. Every frame read renews the window; only readIdle
// without a frame (and without the close) fails, and readTotal is a backstop for a server that sends forever.
var (
	readIdle  = 10 * time.Second
	readTotal = 2 * time.Minute
)

var (
	errNoProgress = errors.New("no frame and no close within the idle window")
	errTooLong    = errors.New("frames kept coming past the total limit")
)

// readFrames reads frames until the connection fails and returns the frame types it read and the error that ended the reads.
// A read that sees nothing for idle ends with errNoProgress (wrapping the timeout); total bounds the whole read with
// errTooLong. Any other error is the connection's own ending.
func readFrames(conn *websocket.Conn, idle, total time.Duration) ([]string, error) {
	start := time.Now()
	var types []string
	for {
		if err := conn.SetReadDeadline(time.Now().Add(idle)); err != nil {
			return types, err
		}
		_, msg, err := conn.ReadMessage()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return types, fmt.Errorf("%w (%s): %v", errNoProgress, idle, err)
			}
			return types, err
		}
		typ, err := frameType(msg)
		if err != nil {
			return types, err
		}
		types = append(types, typ)
		if time.Since(start) > total {
			return types, fmt.Errorf("%w (%s)", errTooLong, total)
		}
	}
}

// readUntilError reads frames until the connection fails, and returns the frame types it read and the error. No progress for
// readIdle fails the test: the server never closed the connection.
func readUntilError(t *testing.T, conn *websocket.Conn) ([]string, error) {
	t.Helper()
	types, err := readFrames(conn, readIdle, readTotal)
	if errors.Is(err, errNoProgress) {
		dumpGoroutines(t) // #2590: evidence of where the server side is stuck, if it is
	}
	require.False(t, errors.Is(err, errNoProgress), "the server never closed the connection: %v", err)
	require.False(t, errors.Is(err, errTooLong), "the server never closed the connection: %v", err)
	return types, err
}

// readUntilType reads frames until one of type want arrives, and returns
// the types read before it, or the error that ended the reads first. It
// runs off the test goroutine, so it reports rather than fails. Each frame
// renews the idle window, as in readFrames.
func readUntilType(conn *websocket.Conn, want string) ([]string, error) {
	var types []string
	for {
		if err := conn.SetReadDeadline(time.Now().Add(readIdle)); err != nil {
			return nil, err
		}
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
	server, gates := gatedServer(t, eb)
	optedConn, opted := dialQuery(t, server, subs, "?nex=v1")
	optedGate := <-gates
	plainConn, plain := dialQuery(t, server, subs, "")
	plainGate := <-gates
	require.True(t, opted.Wants(FeatureNexV1))

	jam(t, opted, optedGate)
	jam(t, plain, plainGate)

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
	plainGate.open() // the client catches up: its backlog drains
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
	server, gates := gatedServer(t, eb)
	conn, opted := dialQuery(t, server, subs, "?nex=v1")
	jam(t, opted, <-gates)

	require.False(t, opted.TrySend([]byte(`{"type":"hook","session":"s","value":"{}"}`)), "a send into a full buffer was queued")

	types, err := readUntilError(t, conn)
	require.Error(t, err)
	assert.NotContains(t, types, "hook")
	assert.True(t, isDone(opted))
	require.Eventually(t, func() bool { return !eb.HasSubscribers() }, 5*time.Second, 10*time.Millisecond,
		"the closed connection's subscriber is still registered")
}

// #2590: the other side of the same rule. A pump that finishes the write it was in takes the next frame and frees one slot; a
// direct send that finds that slot is queued and delivered, and the connection stays open — the client is slow, not gone. (This
// is what a real, un-gated socket does at random, and what made the test above fail one run in six under load.)
func TestHandleHostEvents_OptedInDirectSendThatFindsASlotIsDeliveredNotClosed(t *testing.T) {
	eb := NewEventsBroadcaster()
	subs := make(chan *EventSubscriber, 1)
	eb.OnSubscribe(func(sub *EventSubscriber) { subs <- sub })
	server, gates := gatedServer(t, eb)
	conn, opted := dialQuery(t, server, subs, "?nex=v1")
	gate := <-gates
	jam(t, opted, gate)

	gate.permits <- struct{}{} // one Write completes: the pump takes the next frame and parks again
	require.Eventually(t, func() bool { return len(opted.send) < cap(opted.send) && gate.waiting.Load() == 1 }, 5*time.Second, time.Millisecond,
		"the pump never freed a slot")
	require.True(t, opted.TrySend([]byte(`{"type":"hook","session":"s","value":"{}"}`)), "a send into the freed slot was not queued")
	assert.False(t, isDone(opted), "the connection was closed over a frame that fit")

	gate.open()
	got := make(chan error, 1)
	go func() {
		_, err := readUntilType(conn, "hook")
		got <- err
	}()
	select {
	case err := <-got:
		require.NoError(t, err, "the queued frame never reached the client")
	case <-time.After(15 * time.Second):
		t.Fatal("the queued frame never reached the client")
	}
	assert.False(t, isDone(opted))
}

// dumpGoroutines prints every goroutine's stack to the test log (#2590): a server that neither sends nor closes is a goroutine
// parked somewhere, and where is the whole question.
func dumpGoroutines(t *testing.T) {
	t.Helper()
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	t.Logf("goroutines at the stall:\n%s", buf)
}
