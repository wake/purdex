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
	// than "owned" or "free", and the manual-resume handler does not act on it.
	// (That behaviour lands in Task 7; this type only records the distinction.)
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
}

type TerminalSessions interface {
	LiveBySessionID(ctx context.Context, agentType, sessionID string) ([]TerminalSession, error)
	SubscribeSessionStart(fn func(SessionStartEvent)) (unsubscribe func()) // Task 3
}

// sessionStartQueueSize bounds each subscriber's pending events.
const sessionStartQueueSize = 64

// sessionStartHub fans a granted SessionStart out to in-process subscribers.
//
// Delivery contract: every subscriber owns a bounded queue
// (sessionStartQueueSize) and exactly one consumer goroutine, started at
// subscribe time, which calls the callback for each event in publish order.
// Each call has its own recover, so a panic is logged and the consumer moves
// on to the next event. publish never blocks: when a subscriber's queue is
// full the event is dropped for that subscriber and logged, so a stuck
// subscriber costs one goroutine and 64 queued events, never the hook
// response and never an unbounded number of goroutines.
//
// unsubscribe is idempotent. It closes the queue under the hub mutex (the
// same lock publish sends under, so a send can never hit a closed channel);
// the consumer drains what is already queued and exits, so events queued
// before unsubscribe may still be delivered.
type sessionStartHub struct {
	mu   sync.Mutex
	next int
	subs map[int]chan SessionStartEvent
}

func (h *sessionStartHub) subscribe(fn func(SessionStartEvent)) func() {
	ch := make(chan SessionStartEvent, sessionStartQueueSize)
	h.mu.Lock()
	if h.subs == nil {
		h.subs = map[int]chan SessionStartEvent{}
	}
	id := h.next
	h.next++
	h.subs[id] = ch
	h.mu.Unlock()

	go func() {
		for ev := range ch {
			deliverSessionStart(fn, ev)
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			delete(h.subs, id)
			close(ch)
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
	defer h.mu.Unlock()
	for _, ch := range h.subs {
		select {
		case ch <- ev:
		default:
			log.Printf("[agent] session_start subscriber queue full; event dropped (session=%s source=%s)", ev.SessionID, ev.Source)
		}
	}
}

// SubscribeSessionStart registers fn for every granted SessionStart.
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
