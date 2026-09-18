package nex

// The individual steps of handleNexHandoff (handoff.go): identity, stopping
// Claude Code, and the rollback after a rejected delegate.

import (
	"context"
	"strings"
	"time"

	pdxagent "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/module/session"
)

// resolveHandoffOwner asks the agent module who owns the pane, and accepts
// only a found Claude Code owner with a session id. A lookup error is
// reported the same way as no owner: either way the identity could not be
// established, and nothing has been touched yet.
func (m *Module) resolveHandoffOwner(code string) (owner handoffOwner, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), m.handoffResolveTimeout)
	defer cancel()
	po, found, err := m.owners.ResolveSessionOwner(ctx, code)
	if err != nil {
		m.logf("nex: handoff %s: resolving owner: %v", code, err)
		return handoffOwner{}, false
	}
	if !found || po.AgentType != "cc" || po.SessionID == "" {
		return handoffOwner{}, false
	}
	return handoffOwner{SessionID: po.SessionID, Cwd: po.Cwd}, true
}

// handoffOwner is the slice of agent.PaneOwner the handoff carries forward.
type handoffOwner struct {
	SessionID string
	Cwd       string
}

// stopCC brings the pane's Claude Code to a stop: Interrupt first when it is
// not idle, then Exit — each under its own budget. The returned step names
// the one that failed ("interrupt" | "exit"); after a failed interrupt Exit
// is not attempted.
func (m *Module) stopCC(target string) (step string, err error) {
	if res, _ := m.prober.CheckReadiness("cc", target); res.Status != pdxagent.StatusIdle {
		ctx, cancel := context.WithTimeout(context.Background(), m.handoffInterruptTimeout)
		defer cancel()
		if err := m.ccOps.Interrupt(ctx, target); err != nil {
			return "interrupt", err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.handoffExitTimeout)
	defer cancel()
	if err := m.ccOps.Exit(ctx, target); err != nil {
		return "exit", err
	}
	return "", nil
}

// rollbackHandoff resumes Claude Code in the pane after a rejected
// delegate: the rendered rollback command is sent by session id, guarded by
// the same generation the caller expected (so a restart between the
// post-exit sample and the send delivers nothing), and CC is polled back to
// life within rollbackWait. Returns whether CC is running again; with no
// rollback command the shell is left idle and false says so.
func (m *Module) rollbackHandoff(sess *session.SessionInfo, expected, command, sessionID, target string) bool {
	if command == "" {
		return false
	}
	keys := strings.ReplaceAll(command, "{id}", sessionID) + "\n"
	sent, err := m.tmux.SendKeysIfInstance(sess.TmuxID, expected, keys)
	if err != nil {
		m.logf("nex: handoff %s rollback: send-keys: %v", sess.Code, err)
		return false
	}
	if !sent {
		m.logf("nex: handoff %s rollback: tmux generation moved, nothing sent", sess.Code)
		return false
	}
	deadline := time.Now().Add(m.rollbackWait)
	for {
		if m.prober.IsAliveFor("cc", target) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(m.rollbackPoll)
	}
}
