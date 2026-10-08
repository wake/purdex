package nex

import (
	"context"
	"errors"
	"strings"

	"lab.protype.tw/wake/nexen/store"

	"github.com/wake/purdex/internal/peers/execpeers"
)

// execPeers is the module's execpeers.ExecPeers (peer mailbox spec §4.1,
// plan C7), published in Init under execpeers.RegistryKey once the engine
// assembled. The peers module reads it on every inventory pass to list each
// execution as one addressable row.
type execPeers struct{ m *Module }

var _ execpeers.ExecPeers = (*execPeers)(nil)

// Rows lists every execution a peer can address: not terminal, not archived,
// with a session id. It walks every page (walkExecutions: the same complete
// walk and repeated-cursor guard as the conversation listing) and fails whole
// when any page does, so the peers module never takes a partial list for the
// whole one. The walk is not detached: the caller is an inventory pass with a
// budget of its own, and a page must end with it, never outlive it.
func (p *execPeers) Rows(ctx context.Context) ([]execpeers.Row, error) {
	execs, err := p.m.walkExecutions(ctx, false, false)
	if err != nil {
		return nil, err
	}
	out := make([]execpeers.Row, 0, len(execs))
	for _, e := range execs {
		if row, ok := execPeerRow(e); ok {
			out = append(out, row)
		}
	}
	return out, nil
}

// execPeerRow is e as a peer row, or false when e cannot be addressed: a
// terminal state (rejected, failed, terminated) never takes another turn, an
// archived execution is out of sight, and one with no session id names no
// conversation. The session id is Nexen's session_id, else the
// resume_session_id a first turn that has not reported yet will take.
//
// The pid is carried only for a running turn whose process has reported
// (LiveTurnStarted): executions.pid mirrors the most recent turn and is never
// cleared, so an idle row's pid names a process that has already exited.
func execPeerRow(e store.Execution) (execpeers.Row, bool) {
	if e.ArchivedAt != 0 {
		return execpeers.Row{}, false
	}
	switch e.State {
	case store.StateIdle, store.StateRunning, store.StateQueued:
	default:
		return execpeers.Row{}, false
	}
	sid := e.SessionID
	if sid == "" {
		sid = e.ResumeSessionID
	}
	if sid == "" {
		return execpeers.Row{}, false
	}
	pid := 0
	if e.State == store.StateRunning && e.LiveTurnStarted {
		pid = e.Pid
	}
	return execpeers.Row{
		ExecutionID: e.ID,
		SessionID:   strings.ToLower(sid),
		Cwd:         e.Cwd,
		State:       string(e.State),
		Title:       e.TitleText,
		PID:         pid,
	}, true
}

// MailboxEnabled is [nex.peer].enabled as the engine was assembled with it.
func (p *execPeers) MailboxEnabled() bool {
	cfg := p.m.opts.Config
	return cfg != nil && cfg.Peer.Enabled
}

// errPeerSendNotWired is Send's answer until the mailbox last hop lands
// (peer mailbox plan P4b): executions are listed and addressable, but nothing
// is handed to Nexen yet.
var errPeerSendNotWired = errors.New("nex: peer messages to executions are not wired yet")

// Send is not wired yet (errPeerSendNotWired).
func (p *execPeers) Send(context.Context, string, execpeers.PeerSend) (execpeers.PeerSendResult, error) {
	return execpeers.PeerSendResult{}, errPeerSendNotWired
}
