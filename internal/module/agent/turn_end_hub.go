package agent

import (
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

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
	Failed    bool   // the main turn ended in a StopFailure, not a Stop
}

// turnEndStamp is the arrival stamp handleEvent takes before anything waits.
type turnEndStamp struct {
	at, seq int64
}

// turnEndSubBuffer is the capacity of each subscriber's queue.
const turnEndSubBuffer = 64

// turnEndHub fans an accepted Stop out to in-process subscribers.
//
// It is at-least-once: a hook the daemon answered 500 (or whose answer was lost) is retried by the
// sender and published again with a newer stamp; consumers must be idempotent (T-3a2 keeps the newest
// turn by (At, Seq)). Only the tmux hook path publishes: a member is a tmux session, and a headless
// (non-tmux) Claude Code session's turns are not part of this feed.
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

// stampTurnEnd takes the arrival stamp: time and the next sequence number. The sequence starts at the
// daemon's first hook in microseconds since the epoch, not at 0, so it keeps rising across a restart
// (a consumer comparing (At, Seq) never ranks a new daemon's turn below an old one's at the same ms).
func (m *Module) stampTurnEnd() turnEndStamp {
	now := time.Now()
	m.turnEndSeq.CompareAndSwap(0, now.UnixMicro())
	return turnEndStamp{at: now.UnixMilli(), seq: m.turnEndSeq.Add(1)}
}

// turnEndTextMaxBytes bounds the text a queue holds: a consumer needs the first sentence of the turn,
// not the whole message, and a stuck subscriber's 64 slots must not pin 64 huge messages.
const turnEndTextMaxBytes = 4096

func boundText(s string) string {
	if len(s) <= turnEndTextMaxBytes {
		return s
	}
	cut := turnEndTextMaxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// publishTurnEnd publishes the turn end of an accepted cc PdxStop whose frame
// event applied, and of a main-turn PdxStopFailure (Failed: true). Not for
// SubagentStop, a StopFailure that names a subagent, another provider, an event whose frame
// application was skipped, or one with no session id: the id is the
// provider's IdentifyEvent of the raw hook (it is not in the status detail).
// No lock is held by the caller; this takes none but the hub's.
func (m *Module) publishTurnEnd(req EventRequest, provider agentpkg.AgentProvider, lifecycle agentpkg.LifecycleEventKind, meta FrameTraceMeta, st turnEndStamp) {
	failed := req.PurdexName == "PdxStopFailure" && lifecycle == agentpkg.LifecycleStopFailure
	stopped := req.PurdexName == "PdxStop" && lifecycle == agentpkg.LifecycleStop
	if req.AgentType != "cc" || (!failed && !stopped) || meta.Decision == "skipped" {
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
		Last    string `json:"last_assistant_message"`
		AgentID string `json:"agent_id"`
	}
	_ = json.Unmarshal(req.RawEvent, &raw)
	if failed && raw.AgentID != "" {
		return // a StopFailure that names a subagent is that subagent's, not the main turn's
	}
	m.turnEnds.publish(TurnEndEvent{SessionID: sid, Text: boundText(raw.Last), At: st.at, Seq: st.seq, Failed: failed})
}
