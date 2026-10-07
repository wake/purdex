package core

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// HostEvent is the JSON structure broadcast to all WS subscribers.
type HostEvent struct {
	Type    string `json:"type"`
	Session string `json:"session"`
	Value   string `json:"value"`
	// Epoch/Seq version a "sessions" frame (see the session module's
	// versionedList). omitempty keeps every other frame byte-identical.
	Epoch string `json:"epoch,omitempty"`
	Seq   uint64 `json:"seq,omitempty"`
}

// FeatureNexV1 is the opt-in to the nex execution frames (#1866, spec
// 2026-10-08 §3.5): nex.executions.hello and nex.execution. A client asks
// for it with /ws/host-events?nex=v1. A subscriber that did not is never
// sent one — an old SPA, or purdex-ios, would not know what to do with an
// unknown type on this WS, which also carries the tmux agent status,
// notifications and sessions — and is never disconnected over one.
const FeatureNexV1 = "nex.v1"

const (
	// defaultSendBuffer is a subscriber's send buffer, in frames.
	defaultSendBuffer = 64

	// optedInSendBuffer is the send buffer of a subscriber that opted into a
	// feature. The only feature, nex.v1, is a strict stream
	// (BroadcastStrictTo): a frame that does not fit closes the connection.
	// The projector pushes one delta per changed execution, back to back,
	// so every execution touched in one coalescing window arrives as one
	// burst; with 64 slots, a burst of 65 would disconnect a client that
	// merely paused for that long, as if it were dead.
	//
	// Memory: the channel is 1024 slice headers (24 KiB) per opted-in
	// connection. The frames in it exist only while that client is behind
	// — at worst a full buffer of deltas, one execution row each, until it
	// catches up or the next frame closes it.
	optedInSendBuffer = 1024
)

// EventSubscriber wraps a WebSocket connection with a buffered send channel.
// A dedicated goroutine per subscriber handles all writes to avoid concurrent
// WriteMessage calls (gorilla/websocket requires one concurrent writer max).
//
// A subscriber handle may outlive its connection (a background retry holds
// one, #1293): once removed, Send is a no-op and Done is closed.
type EventSubscriber struct {
	conn     *websocket.Conn
	send     chan []byte
	features map[string]struct{} // what it opted into when it connected; never changes

	mu     sync.Mutex // guards closed; held across a send so close never races it
	closed bool
	done   chan struct{}
}

// newEventSubscriber builds a subscriber that opted into features: any
// opt-in gets optedInSendBuffer, none the default.
func newEventSubscriber(conn *websocket.Conn, features ...string) *EventSubscriber {
	sub := &EventSubscriber{
		conn: conn,
		send: make(chan []byte, defaultSendBuffer),
		done: make(chan struct{}),
	}
	if len(features) > 0 {
		sub.features = make(map[string]struct{}, len(features))
		for _, f := range features {
			sub.features[f] = struct{}{}
		}
		sub.send = make(chan []byte, optedInSendBuffer)
	}
	return sub
}

// Wants reports whether the subscriber opted into feature when it
// connected.
func (sub *EventSubscriber) Wants(feature string) bool {
	_, ok := sub.features[feature]
	return ok
}

// Send pushes data to the subscriber's write pump. Non-blocking — if the
// buffer is full the message is silently dropped; after Remove it is a no-op.
func (sub *EventSubscriber) Send(data []byte) {
	sub.TrySend(data)
}

// TrySend is Send that reports whether data was actually queued: false when
// the buffer is full (data dropped) or the subscriber has been removed.
// Non-blocking.
func (sub *EventSubscriber) TrySend(data []byte) bool {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed {
		return false
	}
	select {
	case sub.send <- data:
		return true
	default: // drop if full
		return false
	}
}

// Done is closed once the subscriber has been removed (its connection is
// closed or closing).
func (sub *EventSubscriber) Done() <-chan struct{} { return sub.done }

// shut marks the subscriber closed and closes its channels, once.
func (sub *EventSubscriber) shut() {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed {
		return
	}
	sub.closed = true
	close(sub.send)
	close(sub.done)
}

// SendCh returns the send channel for reading in tests.
func (sub *EventSubscriber) SendCh() <-chan []byte {
	return sub.send
}

// EventsBroadcaster manages WebSocket subscribers for host events.
type EventsBroadcaster struct {
	mu           sync.RWMutex
	subscribers  map[*EventSubscriber]struct{}
	onSubscribe  []func(*EventSubscriber)
	PingInterval time.Duration
	PongTimeout  time.Duration
}

// NewEventsBroadcaster creates a new EventsBroadcaster.
func NewEventsBroadcaster() *EventsBroadcaster {
	return &EventsBroadcaster{
		subscribers:  make(map[*EventSubscriber]struct{}),
		PingInterval: 30 * time.Second,
		PongTimeout:  10 * time.Second,
	}
}

// Add registers a WebSocket connection as subscriber and starts its write pump.
// Returns the subscriber handle (needed for Remove). features are what the
// connection opted into (FeatureNexV1); Add(conn) opts into nothing.
func (eb *EventsBroadcaster) Add(conn *websocket.Conn, features ...string) *EventSubscriber {
	sub := newEventSubscriber(conn, features...)
	eb.mu.Lock()
	eb.subscribers[sub] = struct{}{}
	eb.mu.Unlock()

	// Write pump — the ONLY goroutine that calls WriteMessage on this conn.
	// Handles both data messages and periodic pings.
	go func() {
		ticker := time.NewTicker(eb.PingInterval)
		defer ticker.Stop()
		for {
			select {
			case msg, ok := <-sub.send:
				if !ok {
					return // channel closed by Remove
				}
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					eb.Remove(sub)
					return
				}
			case <-ticker.C:
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					eb.Remove(sub)
					return
				}
				// Pong timeout is handled by read-side deadline in HandleHostEvents
			}
		}
	}()

	return sub
}

// Remove unregisters a subscriber, closes its send channel and Done, and
// closes its connection (the client sees the close). Idempotent; also
// accepts a test subscriber, which has no connection.
func (eb *EventsBroadcaster) Remove(sub *EventSubscriber) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	if _, ok := eb.subscribers[sub]; ok {
		delete(eb.subscribers, sub)
		sub.shut()
		if sub.conn != nil {
			sub.conn.Close()
		}
	}
}

// Broadcast sends a JSON event to all subscribers.
// Messages are sent non-blocking; slow subscribers that have a full buffer are dropped.
func (eb *EventsBroadcaster) Broadcast(session, eventType, value string) {
	eb.BroadcastEvent(HostEvent{
		Type:    eventType,
		Session: session,
		Value:   value,
	})
}

// BroadcastEvent sends a fully-formed HostEvent (including optional version
// fields) to all subscribers, with the same non-blocking semantics as Broadcast.
func (eb *EventsBroadcaster) BroadcastEvent(ev HostEvent) {
	msg, err := json.Marshal(ev)
	if err != nil {
		log.Printf("events: marshal error: %v", err)
		return
	}

	eb.mu.RLock()
	defer eb.mu.RUnlock()

	for sub := range eb.subscribers {
		sub.Send(msg) // a subscriber too slow to keep up drops this message
	}
}

// BroadcastStrictTo sends ev to every subscriber that opted into feature,
// and to no other. Unlike BroadcastEvent, a subscriber whose buffer is full
// loses its connection instead of this frame: it is Removed (Done closes,
// the WS closes), so the client reconnects and starts over from a fresh
// subscribe. A subscriber that did not opt in is neither sent the frame nor
// ever Removed over it.
//
// Why strict (#1866, spec 2026-10-08 §3.5, round 2 #4): a frame stream a
// client checks for gaps — the nex execution deltas, numbered by a
// contiguous bseq — cannot afford a silent drop. A dropped frame in the
// middle shows up as a gap at the next one, but a dropped LAST frame never
// does: nothing follows it, and the client keeps the stale row. Closing the
// connection turns every drop into a reconnect, which the client already
// handles (a fresh hello, then a reconcile). Nothing is repaired on the
// same connection.
//
// Why scoped: closing a connection is a cost only a client that asked for
// the stream should pay. The same WS carries every other host event, to
// clients that know nothing of this one; they must never see its frames,
// let alone lose the connection over them.
//
// The frame is marshalled once. Subscribers that could not take it are
// collected while the read lock is held and Removed only after it is
// released: Remove takes the write lock, so calling it inside the loop
// would deadlock against the read lock held right there.
//
// Broadcast and BroadcastEvent stay best-effort for every other frame type;
// making those strict too is a separate decision (§3.5), not taken here.
// Concurrent strict broadcasts are not ordered against each other — a
// caller that needs order (the nex projector) serializes its own.
func (eb *EventsBroadcaster) BroadcastStrictTo(feature string, ev HostEvent) {
	msg, err := json.Marshal(ev)
	if err != nil {
		log.Printf("events: marshal error: %v", err)
		return
	}

	var failed []*EventSubscriber
	eb.mu.RLock()
	for sub := range eb.subscribers {
		if sub.Wants(feature) && !sub.TrySend(msg) {
			failed = append(failed, sub)
		}
	}
	eb.mu.RUnlock()

	for _, sub := range failed {
		log.Printf("events: %s frame could not be queued (send buffer full); closing the connection so the client reconnects", ev.Type)
		eb.Remove(sub)
	}
}

// SendStrict queues ev for one subscriber — an OnSubscribe callback's
// snapshot, the nex hello — with BroadcastStrictTo's rule: if it cannot be
// queued, the subscriber is Removed (its connection closes) rather than
// left running without the frame. It reports whether ev was queued. It
// does not check what sub opted into: a caller sending a feature's frame
// checks sub.Wants first.
//
// A subscriber already removed (its connection gone, or dropped by an
// earlier strict send) is left alone: nothing is queued and false is
// returned, the same answer as TrySend's. Remove is idempotent, so a
// subscriber removed concurrently is never closed twice.
//
// It is a broadcaster method rather than one on EventSubscriber because
// removal is the broadcaster's: a subscriber does not know which
// broadcaster holds it.
func (eb *EventsBroadcaster) SendStrict(sub *EventSubscriber, ev HostEvent) bool {
	msg, err := json.Marshal(ev)
	if err != nil {
		log.Printf("events: marshal error: %v", err)
		return false
	}
	if sub.TrySend(msg) {
		return true
	}
	select {
	case <-sub.Done(): // already removed: nothing to close
	default:
		log.Printf("events: %s frame could not be queued (send buffer full); closing the connection so the client reconnects", ev.Type)
		eb.Remove(sub)
	}
	return false
}

// AddTestSubscriber creates a subscriber without a WebSocket connection.
// The subscriber is registered in the subscriber set (HasSubscribers returns true)
// but has no write pump — messages accumulate in SendCh() for test assertions.
// Caller must call RemoveTestSubscriber when done.
func (eb *EventsBroadcaster) AddTestSubscriber() *EventSubscriber {
	return eb.AddTestSubscriberWith()
}

// AddTestSubscriberWith is AddTestSubscriber for a subscriber that opted
// into features, as a connection's query would (HandleHostEvents).
func (eb *EventsBroadcaster) AddTestSubscriberWith(features ...string) *EventSubscriber {
	sub := newEventSubscriber(nil, features...)
	eb.mu.Lock()
	eb.subscribers[sub] = struct{}{}
	eb.mu.Unlock()
	return sub
}

// RemoveTestSubscriber unregisters a test subscriber (no conn to close).
func (eb *EventsBroadcaster) RemoveTestSubscriber(sub *EventSubscriber) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	if _, ok := eb.subscribers[sub]; ok {
		delete(eb.subscribers, sub)
		sub.shut()
	}
}

// HasSubscribers returns true if any clients are connected.
func (eb *EventsBroadcaster) HasSubscribers() bool {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	return len(eb.subscribers) > 0
}

// OnSubscribe registers a callback invoked when a new WS subscriber connects.
// Callbacks receive the subscriber and can use sub.Send() to push snapshot data.
func (eb *EventsBroadcaster) OnSubscribe(fn func(sub *EventSubscriber)) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.onSubscribe = append(eb.onSubscribe, fn)
}

// featuresOf reads what a /ws/host-events upgrade request opts into: nex=v1
// is FeatureNexV1; any other value of nex, or none, opts into nothing.
func featuresOf(r *http.Request) []string {
	if r.URL.Query().Get("nex") == "v1" {
		return []string{FeatureNexV1}
	}
	return nil
}

// HandleHostEvents handles /ws/host-events — SPA subscribes for
// status, relay, handoff, and init events. The request's query says which
// optional frame families the subscriber opts into (featuresOf).
func (eb *EventsBroadcaster) HandleHostEvents(w http.ResponseWriter, r *http.Request) {
	features := featuresOf(r)
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	// Pong handling — reset read deadline on each pong received
	conn.SetReadDeadline(time.Now().Add(eb.PingInterval + eb.PongTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(eb.PingInterval + eb.PongTimeout))
		return nil
	})

	sub := eb.Add(conn, features...)
	defer eb.Remove(sub)

	// Call all registered OnSubscribe callbacks.
	eb.mu.RLock()
	callbacks := make([]func(*EventSubscriber), len(eb.onSubscribe))
	copy(callbacks, eb.onSubscribe)
	eb.mu.RUnlock()
	for _, fn := range callbacks {
		fn(sub)
	}

	// Read loop — exits on disconnect or pong timeout (ReadDeadline exceeded)
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
