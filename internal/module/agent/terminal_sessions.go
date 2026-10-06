package agent

import (
	"context"
	"encoding/json"
	"log"
	"sync"
)

// TerminalSessionsKey names the service the nex module uses for the
// conversation-entity owner check (spec §4.2 "terminal": a live Purdex frame
// with session_id = S) and for the manual-resume trigger (Q1, D3).
const TerminalSessionsKey = "agent.terminal-sessions"

// TerminalSession is one root agent run recorded for a session id.
type TerminalSession struct {
	FrameID, PaneID, AgentType, SessionID, Cwd, TranscriptPath string
	// Verified is true when the pid is alive AND its start time matched the
	// recorded one. False means "alive, start time unreadable": it is NOT an
	// owner under spec D1, but the session is not provably free either. An
	// owner check therefore answers 503 owner_check_failed (retryable) rather
	// than "owned" or "free" — unless another frame of the session is
	// verified, which proves it owned (409 session_owned) — and the
	// manual-resume handler does not act on it.
	Verified bool
}

// SessionStartEvent is one SessionStart that applyFrameEvent granted a
// provenance envelope: a verified, top-level agent run that now records
// SessionID on its frame.
type SessionStartEvent struct {
	AgentType      string
	SessionID      string
	Source         string // the hook's "source": "startup" | "resume" | "clear" | …
	TmuxSession    string // the hook's tmux session name
	TmuxPaneID     string
	FrameID        string
	Cwd            string
	TranscriptPath string
	// Overflow, when true, carries no session: events were coalesced away because the subscriber fell behind; the subscriber must re-check every session it cares about.
	Overflow bool
}

type TerminalSessions interface {
	LiveBySessionID(ctx context.Context, agentType, sessionID string) ([]TerminalSession, error)
	SubscribeSessionStart(fn func(SessionStartEvent)) (unsubscribe func()) // Task 3
}

// sessionStartHub fans a granted SessionStart out to in-process subscribers.
//
// Delivery contract: every subscriber owns a coalescing queue keyed by session
// id and exactly one consumer goroutine, started at subscribe time. publish
// never blocks and never drops a session: if the session id is already
// pending for that subscriber the earlier event is replaced by the later one,
// otherwise the id is appended to the pending order. The consumer wakes,
// takes the whole pending set, and calls the callback once per session id.
//
// Coalescing is safe because subscribers must treat an event as "re-check
// this session" (they re-read state), not as a counter: the latest event for
// a session carries all the information the earlier ones did. Ordering is
// preserved per session id; across sessions delivery follows first-pending
// order. Memory is bounded: at most sessionStartPendingCap distinct session
// ids are held per subscriber (cap x one event), and a stuck subscriber costs
// one goroutine. No re-check signal is ever lost: a session id beyond the cap
// is not stored, but the subscriber is flagged and, after the stored events,
// receives one SessionStartEvent{Overflow: true}, meaning "re-check every
// session you care about".
//
// Each callback runs in its own recover, so a panic is logged and the
// consumer moves on. unsubscribe is idempotent: it removes the subscriber
// from the hub and stops the consumer. Events published before unsubscribe
// may still be delivered, and unsubscribe does not wait for the consumer: a
// callback already in flight finishes, and one the consumer had begun (past
// its stop check) may still start just after unsubscribe returns. Subscribers
// tolerate this; see SubscribeSessionStart.
type sessionStartHub struct {
	mu   sync.Mutex
	next int
	subs map[int]*sessionStartSub
}

// sessionStartPendingCap bounds the distinct session ids pending per subscriber.
const sessionStartPendingCap = 1024

type sessionStartSub struct {
	mu       sync.Mutex
	overflow bool
	pending  map[string]SessionStartEvent
	order    []string
	wake     chan struct{} // capacity 1
	done     chan struct{}
}

func (s *sessionStartSub) push(ev SessionStartEvent) {
	var logOverflow bool
	s.mu.Lock()
	if _, ok := s.pending[ev.SessionID]; ok {
		s.pending[ev.SessionID] = ev
	} else if len(s.order) < sessionStartPendingCap {
		s.order = append(s.order, ev.SessionID)
		s.pending[ev.SessionID] = ev
	} else {
		if !s.overflow {
			logOverflow = true
		}
		s.overflow = true
	}
	s.mu.Unlock()
	if logOverflow {
		log.Printf("[agent] session_start subscriber backlog full (cap %d); coalescing into a full re-check", sessionStartPendingCap)
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *sessionStartSub) take() []SessionStartEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SessionStartEvent, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.pending[id])
	}
	if s.overflow {
		out = append(out, SessionStartEvent{Overflow: true})
	}
	s.order = nil
	s.overflow = false
	s.pending = map[string]SessionStartEvent{}
	return out
}

func (h *sessionStartHub) subscribe(fn func(SessionStartEvent)) func() {
	sub := &sessionStartSub{
		pending: map[string]SessionStartEvent{},
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	h.mu.Lock()
	if h.subs == nil {
		h.subs = map[int]*sessionStartSub{}
	}
	id := h.next
	h.next++
	h.subs[id] = sub
	h.mu.Unlock()

	go func() {
		for {
			select {
			case <-sub.wake:
			case <-sub.done:
				return
			}
			for _, ev := range sub.take() {
				select {
				case <-sub.done:
					return
				default:
				}
				deliverSessionStart(fn, ev)
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, id)
			h.mu.Unlock()
			close(sub.done)
		})
	}
}

func deliverSessionStart(fn func(SessionStartEvent), ev SessionStartEvent) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[agent] session_start subscriber panic: %v", r)
		}
	}()
	fn(ev)
}

func (h *sessionStartHub) publish(ev SessionStartEvent) {
	h.mu.Lock()
	subs := make([]*sessionStartSub, 0, len(h.subs))
	for _, sub := range h.subs {
		subs = append(subs, sub)
	}
	h.mu.Unlock()
	for _, sub := range subs {
		sub.push(ev)
	}
}

// SubscribeSessionStart registers fn for every granted SessionStart, under
// sessionStartHub's delivery contract. Two things are NOT guaranteed:
//   - ordering across subscribers: each one has its own consumer goroutine,
//     so two subscribers may see the same event in either order;
//   - a clean cut at unsubscribe: it may race a delivery in flight, so fn
//     can still be called just after unsubscribe returns.
//
// Both are tolerated because an event only means "re-check this session":
// the nex Q1 handler re-reads LiveBySessionID before it acts on one, and does
// nothing once its Stop began (manual_resume.go).
func (m *Module) SubscribeSessionStart(fn func(SessionStartEvent)) func() {
	return m.sessionStarts.subscribe(fn)
}

// sessionStartEventFrom builds the event from the granted envelope plus the
// two fields the envelope does not carry (source, tmux session name).
func sessionStartEventFrom(req EventRequest, prov Provenance) SessionStartEvent {
	var raw struct {
		Source string `json:"source"`
	}
	_ = json.Unmarshal(req.RawEvent, &raw)
	return SessionStartEvent{
		AgentType: prov.AgentType, SessionID: prov.SessionID, Source: raw.Source,
		TmuxSession: req.TmuxSession, TmuxPaneID: prov.TmuxPaneID, FrameID: prov.FrameID,
		Cwd: prov.Cwd, TranscriptPath: prov.TranscriptPath,
	}
}

// LiveBySessionID returns the root frames recorded for sessionID whose
// process is still the recorded one. A dead pid or a start-time mismatch is
// a stale row the sweep will clear; it is not an owner. An unreadable start
// time is kept with Verified=false (see TerminalSession).
//
// agentType filters by agent type ("cc", "codex", …); an empty agentType
// disables the filter and returns the frames of every type. An empty
// sessionID returns nothing, and a ctx already done returns its error
// without reading the store.
func (m *Module) LiveBySessionID(ctx context.Context, agentType, sessionID string) ([]TerminalSession, error) {
	if m == nil || m.frames == nil || sessionID == "" {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	frames, err := m.frames.ListRootsBySessionID(sessionID)
	if err != nil {
		return nil, err
	}
	var out []TerminalSession
	for _, f := range frames {
		if agentType != "" && f.AgentType != agentType {
			continue
		}
		if !isPidAliveFn(f.PID) {
			continue
		}
		verified := false
		if st, err := processStartTimeFn(f.PID); err == nil {
			if st != f.ProcessStartTime {
				continue
			}
			verified = true
		}
		out = append(out, TerminalSession{
			FrameID: f.FrameID, PaneID: f.PaneID, AgentType: f.AgentType, SessionID: f.SessionID,
			Cwd: f.Cwd, TranscriptPath: f.TranscriptPath, Verified: verified,
		})
	}
	return out, nil
}
