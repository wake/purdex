package agent

import (
	"log"
	"sync"
	"sync/atomic"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// NotifyEvent is one live `hook` frame of a tmux session, as the push module sees it: the frame exactly as it went
// on the events bus, with the identity of the session it belongs to. SessionID is the agent's own session id (""
// when the frame has no frame row to read it from), SessionName the tmux session name.
type NotifyEvent struct {
	SessionCode string
	SessionName string
	SessionID   string
	Event       agentpkg.NormalizedEvent
}

// notifySubBuffer is the capacity of each subscriber's queue.
const notifySubBuffer = 64

// notifyHub fans every live tmux `hook` frame out to in-process subscribers, from the single place the frames
// are broadcast (emitSessionWith, inside the emit slot): hook, mod-stream, sweep and probe frames alike, never a
// subscribe-time replay (those do not go through the slot) and never a session outside tmux.
//
// Delivery contract, the same as turnEndHub's: every subscriber owns a fixed-capacity channel and one consumer
// goroutine that calls its callback (recovered). publish is a non-blocking send to each, so the emitter, which
// holds the emit slot, never waits for a subscriber; a subscriber whose queue is full loses the event (counted,
// logged once per burst). Nothing is coalesced. unsubscribe removes the subscriber and closes its channel under
// the mutex publish sends under, so a racing publish never sends on a closed channel; events already queued may
// still be delivered after unsubscribe returns.
type notifyHub struct {
	mu      sync.Mutex
	next    int
	subs    map[int]chan NotifyEvent
	dropped atomic.Int64
}

func (h *notifyHub) subscribe(fn func(NotifyEvent)) func() {
	ch := make(chan NotifyEvent, notifySubBuffer)
	h.mu.Lock()
	if h.subs == nil {
		h.subs = map[int]chan NotifyEvent{}
	}
	id := h.next
	h.next++
	h.subs[id] = ch
	h.mu.Unlock()
	go func() {
		for ev := range ch {
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[agent] notify subscriber panic: %v", r)
					}
				}()
				fn(ev)
			}()
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, id)
			close(ch)
			h.mu.Unlock()
		})
	}
}

// publish never waits: a full queue drops the event for that subscriber.
func (h *notifyHub) publish(ev NotifyEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs {
		select {
		case ch <- ev:
		default:
			if n := h.dropped.Add(1); n == 1 || n%100 == 0 {
				log.Printf("[agent] notify subscriber queue full (cap %d); %d event(s) dropped so far", notifySubBuffer, n)
			}
		}
	}
}

// Dropped is how many (subscriber, event) deliveries were lost to a full queue.
func (h *notifyHub) Dropped() int64 { return h.dropped.Load() }

// SubscribeNotify registers fn for every live tmux `hook` frame, under notifyHub's delivery contract. fn runs on
// the subscriber's own goroutine: never under m.mu, emit.mu or modMu.
func (m *Module) SubscribeNotify(fn func(NotifyEvent)) func() {
	return m.notifies.subscribe(fn)
}

// NotifyDropped is the number of deliveries lost to full subscriber queues.
func (m *Module) NotifyDropped() int64 { return m.notifies.Dropped() }

// publishNotify publishes a frame that was just put on the bus. Under emit.mu, so it only does memory work.
// A non-tmux frame (kindNonTmux) is not published, and neither is one with no session name to say whose it is.
func (m *Module) publishNotify(kind slotKind, code, notifyName string, p *SessionProjection, n agentpkg.NormalizedEvent) {
	if kind == kindNonTmux || notifyName == "" {
		return
	}
	sid := ""
	if p != nil {
		switch {
		case p.TopFrame != nil && p.TopFrame.SessionID != "":
			sid = p.TopFrame.SessionID
		case p.PrimaryFrame != nil:
			sid = p.PrimaryFrame.SessionID
		}
	}
	m.notifies.publish(NotifyEvent{SessionCode: code, SessionName: notifyName, SessionID: sid, Event: n})
}
