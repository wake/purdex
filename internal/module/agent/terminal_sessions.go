package agent

import "context"

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

// SessionStartEvent is a placeholder; Task 3 defines it.
type SessionStartEvent struct{}

type TerminalSessions interface {
	LiveBySessionID(ctx context.Context, agentType, sessionID string) ([]TerminalSession, error)
	SubscribeSessionStart(fn func(SessionStartEvent)) (unsubscribe func()) // Task 3
}

// SubscribeSessionStart is a temporary stub; Task 3 replaces it.
func (m *Module) SubscribeSessionStart(func(SessionStartEvent)) func() { return func() {} }

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
