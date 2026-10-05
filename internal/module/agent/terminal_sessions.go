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
	// recorded one. False means "alive, start time unreadable": an owner check
	// counts it (conservative), the Q1 handler does not act on it alone.
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

// sessionStartHub fans a granted SessionStart out to in-process subscribers.
// Delivery contract: each subscriber runs on its own goroutine with a
// recover, so the hook response never waits for (or dies with) a subscriber.
type sessionStartHub struct {
	mu   sync.Mutex
	next int
	subs map[int]func(SessionStartEvent)
}

func (h *sessionStartHub) subscribe(fn func(SessionStartEvent)) func() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs == nil {
		h.subs = map[int]func(SessionStartEvent){}
	}
	id := h.next
	h.next++
	h.subs[id] = fn
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subs, id)
	}
}

func (h *sessionStartHub) publish(ev SessionStartEvent) {
	h.mu.Lock()
	fns := make([]func(SessionStartEvent), 0, len(h.subs))
	for _, fn := range h.subs {
		fns = append(fns, fn)
	}
	h.mu.Unlock()
	for _, fn := range fns {
		go func(fn func(SessionStartEvent)) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[agent] session_start subscriber panic: %v", r)
				}
			}()
			fn(ev)
		}(fn)
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
