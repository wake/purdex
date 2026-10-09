package agent

import (
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// TurnEndEvent is one accepted end of a Claude Code main turn (the hook Stop):
// the session it ended in and the text of its last assistant message. At and
// Seq are stamped when the hook ARRIVES (stampTurnEnd, at handleEvent's entry),
// not when the event is published: two Stops of one session that finish
// processing out of order still keep their arrival order, and a subscriber
// that keeps the newest turn compares (At, Seq).
type TurnEndEvent struct {
	SessionID string
	Text      string // last_assistant_message of the hook; "" when it had none
	At        int64  // unix ms at the hook's arrival
	Seq       int64  // per-daemon arrival order; breaks a tie of At
}

// turnEndStamp is the arrival stamp handleEvent takes before anything waits.
type turnEndStamp struct {
	at, seq int64
}

// turnEndSubBuffer is the capacity of each subscriber's queue.
const turnEndSubBuffer = 64

// turnEndHub fans an accepted Stop out to in-process subscribers.
//
// Delivery contract: every subscriber owns a fixed-capacity channel and one
// consumer goroutine that calls its callback (recovered). publish is a
// non-blocking send to each: a subscriber whose queue is full loses the event
// (counted, logged once per burst), so the hook handler never waits for a
// subscriber, a database or a lock a subscriber holds. The hub's own mutex
// guards only the subscriber map and the sends, which are memory operations.
// Unlike sessionStartHub nothing is coalesced: each turn is its own event,
// ordered per subscriber by publication, and a consumer that reads the newest
// turn compares (At, Seq).
//
// unsubscribe removes the subscriber and closes its channel under the same
// mutex publish sends under, so a publish after (or racing) unsubscribe never
// sends on a closed channel and is a no-op for that subscriber. Events already
// queued may still be delivered after unsubscribe returns.
type turnEndHub struct {
	mu      sync.Mutex
	next    int
	subs    map[int]chan TurnEndEvent
	dropped atomic.Int64
}

func (h *turnEndHub) subscribe(fn func(TurnEndEvent)) func() {
	ch := make(chan TurnEndEvent, turnEndSubBuffer)
	h.mu.Lock()
	if h.subs == nil {
		h.subs = map[int]chan TurnEndEvent{}
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
						log.Printf("[agent] turn_end subscriber panic: %v", r)
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
func (h *turnEndHub) publish(ev TurnEndEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs {
		select {
		case ch <- ev:
		default:
			if n := h.dropped.Add(1); n == 1 || n%100 == 0 {
				log.Printf("[agent] turn_end subscriber queue full (cap %d); %d event(s) dropped so far", turnEndSubBuffer, n)
			}
		}
	}
}

// Dropped is how many (subscriber, event) deliveries were lost to a full queue.
func (h *turnEndHub) Dropped() int64 { return h.dropped.Load() }

// SubscribeTurnEnd registers fn for every accepted main-turn Stop of a Claude
// Code session, under turnEndHub's delivery contract. fn runs on the
// subscriber's own goroutine: never under m.mu, emitMu or modMu.
func (m *Module) SubscribeTurnEnd(fn func(TurnEndEvent)) func() {
	return m.turnEnds.subscribe(fn)
}

// stampTurnEnd takes the arrival stamp: time and the next sequence number.
func (m *Module) stampTurnEnd() turnEndStamp {
	return turnEndStamp{at: time.Now().UnixMilli(), seq: m.turnEndSeq.Add(1)}
}

// publishTurnEnd publishes the turn end of an accepted cc PdxStop whose frame
// event applied. Not for SubagentStop, another provider, an event whose frame
// application was skipped, or one with no session id: the id is the
// provider's IdentifyEvent of the raw hook (it is not in the status detail).
// No lock is held by the caller; this takes none but the hub's.
func (m *Module) publishTurnEnd(req EventRequest, provider agentpkg.AgentProvider, lifecycle agentpkg.LifecycleEventKind, meta FrameTraceMeta, st turnEndStamp) {
	if req.AgentType != "cc" || req.PurdexName != "PdxStop" || lifecycle != agentpkg.LifecycleStop || meta.Decision == "skipped" {
		return
	}
	ident, ok := provider.(agentpkg.SessionIdentifier)
	if !ok {
		return
	}
	sid, _ := ident.IdentifyEvent(req.PurdexName, req.RawEvent)
	if sid == "" {
		return
	}
	var raw struct {
		Last string `json:"last_assistant_message"`
	}
	_ = json.Unmarshal(req.RawEvent, &raw)
	m.turnEnds.publish(TurnEndEvent{SessionID: sid, Text: raw.Last, At: st.at, Seq: st.seq})
}
