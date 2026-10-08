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
//
// Opting in also makes the subscriber strict for EVERY frame it is sent,
// not only the nex ones: a frame that cannot be queued for it ends it (its
// connection closes) instead of being dropped (EventSubscriber.offer). Its
// one queue carries the nex deltas and every other host event, so a nex
// burst can fill it; a tmux, sessions or agent.status frame dropped after
// that would leave the client stale with nothing to show it, while a
// reconnect gives it every snapshot again. A subscriber that did not opt in
// keeps the best effort it always had.
const FeatureNexV1 = "nex.v1"

// FeatureAgentV2 is the opt-in to the agent snapshot (interface U1-2b-3, spec
// 2026-10-08 §7): one agent.snapshot frame in place of the per-session hook
// replay, after which the hook frames carry a contiguous (epoch, seq) the
// client checks for gaps. A client asks for it with /ws/host-events?agent=v2,
// alone or together with ?nex=v1; the two are parsed independently
// (featuresOf).
//
// It is strict like FeatureNexV1, for the same reason: the client detects a
// lost hook frame by a gap in seq, and a dropped LAST frame leaves no gap. A
// frame that cannot be queued ends the connection and the reconnect brings a
// fresh snapshot. A subscriber that did not opt in is sent no agent.snapshot,
// keeps its per-session replay, and keeps its best effort.
const FeatureAgentV2 = "agent.v2"

const (
	// defaultSendBuffer is a subscriber's send buffer, in frames.
	defaultSendBuffer = 64

	// optedInSendBuffer is the send buffer of a subscriber that opted into a
	// feature. Every feature (nex.v1, agent.v2) makes the subscriber strict
	// (see FeatureNexV1, FeatureAgentV2): any frame that does not fit closes
	// the connection.
	// The projector pushes one delta per changed execution, back to back,
	// so every execution touched in one coalescing window arrives as one
	// burst; with 64 slots, a burst of 65 would disconnect a client that
	// merely paused for that long, as if it were dead. The buffer absorbs
	// a burst; strictness handles anything beyond it.
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
// one, #1293): once removed — or ended by a frame it could not take, if it
// is strict — Send is a no-op and Done is closed.
type EventSubscriber struct {
	conn     *websocket.Conn
	send     chan []byte
	features map[string]struct{} // what it opted into when it connected; never changes
	strict   bool                // opted into any feature: a frame that does not fit ends it; never changes

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
	sub.strict = sub.Wants(FeatureNexV1) || sub.Wants(FeatureAgentV2)
	return sub
}

// Wants reports whether the subscriber opted into feature when it
// connected.
func (sub *EventSubscriber) Wants(feature string) bool {
	_, ok := sub.features[feature]
	return ok
}

// Send pushes data to the subscriber's write pump. Non-blocking — if the
// buffer is full the message is silently dropped, unless the subscriber
// opted into a feature (nex.v1, agent.v2): then the subscriber is ended
// instead (its connection closes, the client reconnects; see offer). After
// Remove it is a no-op.
func (sub *EventSubscriber) Send(data []byte) {
	sub.TrySend(data)
}

// TrySend is Send that reports whether data was actually queued: false when
// the buffer is full (data dropped — and, for a subscriber that opted into
// a feature, the subscriber ended) or the subscriber has been removed.
// Non-blocking.
func (sub *EventSubscriber) TrySend(data []byte) bool {
	r := sub.offer(data)
	if r == offerEnded {
		log.Printf("events: a frame for an opted-in subscriber could not be queued (send buffer full); closing the connection so the client reconnects")
	}
	return r == offerQueued
}

// offerResult is what offer did with a frame.
type offerResult uint8

const (
	offerQueued  offerResult = iota // queued for the write pump
	offerDropped                    // buffer full, best-effort subscriber: the frame is lost, the subscriber kept
	offerEnded                      // buffer full, strict subscriber: this call ended the subscriber
	offerClosed                     // the subscriber was already removed or ended: nothing queued
)

// offer is the one place a frame is queued for a subscriber, whoever sends
// it: a broadcast, a strict send, or a direct Send from an OnSubscribe
// snapshot callback. Non-blocking.
//
// A strict subscriber (opted into nex.v1 or agent.v2) whose buffer is full is ended
// right here: marked closed, its send channel and Done closed, then its
// connection closed. Ending takes only sub.mu, never the broadcaster's
// lock, so it is safe from inside a broadcast's read-locked loop and from
// inside a caller's own lock (the session module's statusMu, the team
// module's eventMu); the connection is closed after sub.mu is released, and
// gorilla allows Close concurrently with the read loop and the write pump.
//
// Taking the ended subscriber out of the broadcaster's set needs the write
// lock, so it is left to whoever can take it: the broadcast methods once
// their read lock is released, SendStrict, and — for a direct Send, which
// knows no broadcaster — the connection's own goroutines, which see the
// closed connection (the read loop exits into HandleHostEvents' deferred
// Remove; the write pump fails its next write, or finds the send channel
// closed, and Removes). Until then the ended subscriber is still in the
// set, but closed: every later offer to it is offerClosed. A test
// subscriber (no connection) stays registered until RemoveTestSubscriber;
// its Done shows it ended.
func (sub *EventSubscriber) offer(data []byte) offerResult {
	sub.mu.Lock()
	if sub.closed {
		sub.mu.Unlock()
		return offerClosed
	}
	select {
	case sub.send <- data:
		sub.mu.Unlock()
		return offerQueued
	default:
	}
	if !sub.strict {
		sub.mu.Unlock()
		return offerDropped
	}
	sub.shutLocked()
	sub.mu.Unlock()
	if sub.conn != nil {
		sub.conn.Close()
	}
	return offerEnded
}

// Done is closed once the subscriber has been removed (its connection is
// closed or closing).
func (sub *EventSubscriber) Done() <-chan struct{} { return sub.done }

// shut marks the subscriber closed and closes its channels, once.
func (sub *EventSubscriber) shut() {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	sub.shutLocked()
}

// shutLocked is shut with sub.mu already held.
func (sub *EventSubscriber) shutLocked() {
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
// connection opted into (FeatureNexV1, FeatureAgentV2); Add(conn) opts into
// nothing.
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
					// Closed by Remove, or by the subscriber ending itself
					// (offer), which leaves it registered: Remove is
					// idempotent and takes it out of the set.
					eb.Remove(sub)
					return
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

// Broadcast sends a JSON event to all subscribers, with BroadcastEvent's
// rules: non-blocking; a subscriber with a full buffer loses the message,
// or — if it opted into a feature (nex.v1, agent.v2) — its connection.
func (eb *EventsBroadcaster) Broadcast(session, eventType, value string) {
	eb.BroadcastEvent(HostEvent{
		Type:    eventType,
		Session: session,
		Value:   value,
	})
}

// BroadcastEvent sends a fully-formed HostEvent (including optional version
// fields) to all subscribers. Non-blocking. A subscriber whose buffer is
// full loses this frame and keeps running — unless it opted into a feature,
// which makes it strict for every frame (FeatureNexV1, FeatureAgentV2): then it is ended
// (offer) and Removed once the read lock is released, so its client
// reconnects instead of running without the frame.
func (eb *EventsBroadcaster) BroadcastEvent(ev HostEvent) {
	msg, err := json.Marshal(ev)
	if err != nil {
		log.Printf("events: marshal error: %v", err)
		return
	}

	var ended []*EventSubscriber
	eb.mu.RLock()
	for sub := range eb.subscribers {
		if sub.offer(msg) == offerEnded {
			ended = append(ended, sub)
		}
	}
	eb.mu.RUnlock()

	for _, sub := range ended {
		log.Printf("events: %s frame could not be queued for an opted-in subscriber (send buffer full); closing the connection so the client reconnects", ev.Type)
		eb.Remove(sub)
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
// A subscriber that opted into any feature is strict for every frame, Broadcast
// and BroadcastEvent included (FeatureNexV1); for every other subscriber
// those stay best-effort. Concurrent strict broadcasts are not ordered
// against each other — a caller that needs order (the nex projector)
// serializes its own.
func (eb *EventsBroadcaster) BroadcastStrictTo(feature string, ev HostEvent) {
	msg, err := json.Marshal(ev)
	if err != nil {
		log.Printf("events: marshal error: %v", err)
		return
	}

	var failed []*EventSubscriber
	eb.mu.RLock()
	for sub := range eb.subscribers {
		if !sub.Wants(feature) {
			continue
		}
		// offerEnded: an opted-in subscriber ended itself. offerDropped: one
		// that opted into some other feature, which this send is strict for
		// all the same. offerClosed: already gone, nothing to close.
		if r := sub.offer(msg); r == offerEnded || r == offerDropped {
			failed = append(failed, sub)
		}
	}
	eb.mu.RUnlock()

	for _, sub := range failed {
		log.Printf("events: %s frame could not be queued (send buffer full); closing the connection so the client reconnects", ev.Type)
		eb.Remove(sub)
	}
}

// BroadcastStrict sends ev to every subscriber, strict for all of them,
// whatever they opted into: a subscriber whose buffer is full loses its
// connection instead of this frame (Removed: Done closes, the WS closes),
// so its client reconnects and gets the snapshots again. It is
// BroadcastStrictTo without the feature filter.
//
// Only for a frame that every client takes, that is rare, and whose loss
// would leave a client wrong for good with nothing to show it — the team
// module's team.unattended changed (D-U23-6: a window that missed it would
// show the switch off until it reconnected). A frequent frame does not
// belong here: under load it would turn a slow client's dropped frames
// into a stream of reconnects. Everything else stays on BroadcastEvent.
//
// As in BroadcastStrictTo, the subscribers that could not take the frame
// are collected under the read lock and Removed after it is released.
func (eb *EventsBroadcaster) BroadcastStrict(ev HostEvent) {
	msg, err := json.Marshal(ev)
	if err != nil {
		log.Printf("events: marshal error: %v", err)
		return
	}

	var failed []*EventSubscriber
	eb.mu.RLock()
	for sub := range eb.subscribers {
		// offerEnded: an opted-in subscriber ended itself. offerDropped: any
		// other, which this send is strict for. offerClosed: already gone.
		if r := sub.offer(msg); r == offerEnded || r == offerDropped {
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
	switch sub.offer(msg) {
	case offerQueued:
		return true
	case offerClosed: // already removed: nothing to close
		return false
	}
	// offerEnded (an opted-in subscriber ended itself and needs deregistering)
	// or offerDropped (any other subscriber, which this send is strict for).
	log.Printf("events: %s frame could not be queued (send buffer full); closing the connection so the client reconnects", ev.Type)
	eb.Remove(sub)
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

// HasSubscribersWanting reports whether any registered subscriber opted
// into feature (each feature is asked for on its own: a subscriber that
// wants agent.v2 does not count for nex.v1). The nex projector's safety
// reconcile runs only while one wants nex.v1 (#1866 spec §3.7): only such a
// subscriber consumes the deltas it would repair. A subscriber a strict send has just ended may still be
// counted until its removal lands, a moment later.
func (eb *EventsBroadcaster) HasSubscribersWanting(feature string) bool {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	for sub := range eb.subscribers {
		if sub.Wants(feature) {
			return true
		}
	}
	return false
}

// OnSubscribe registers a callback invoked when a new WS subscriber connects.
// Callbacks receive the subscriber and can use sub.Send() to push snapshot data
// (for a subscriber that opted into a feature, a snapshot frame that does not
// fit ends it, as any frame would).
func (eb *EventsBroadcaster) OnSubscribe(fn func(sub *EventSubscriber)) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.onSubscribe = append(eb.onSubscribe, fn)
}

// featuresOf reads what a /ws/host-events upgrade request opts into. The
// nex and agent query parameters are read independently and may both be
// present (?nex=v1&agent=v2): a nex with exactly one value, exactly "v1", is
// FeatureNexV1; an agent with exactly one value, exactly "v2", is
// FeatureAgentV2. Anything else opts that feature out: the parameter missing,
// any other value, or more than one value — even the right one twice.
// Query().Get would read only the first value, so ?nex=v1&nex=v2 would opt in
// while ?nex=v2&nex=v1 would not; an ambiguous request gets the conservative
// answer instead, since opting in changes what the connection is sent and
// when it is closed. A bad value for one feature leaves the other alone.
// The result is in a fixed order: nex first, agent second.
func featuresOf(r *http.Request) []string {
	q := r.URL.Query()
	var features []string
	if nex := q["nex"]; len(nex) == 1 && nex[0] == "v1" {
		features = append(features, FeatureNexV1)
	}
	if agent := q["agent"]; len(agent) == 1 && agent[0] == "v2" {
		features = append(features, FeatureAgentV2)
	}
	return features
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
