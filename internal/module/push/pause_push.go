package push

import (
	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/team"
)

// The pause push (unattended quota pause spec 2026-10-12 §3.3, UP-2a): the team module's pause outbox asks, in process, for one phone
// push when an unattended lead or solo session is paused for lack of relay quota. The module publishes itself as the PauseNotifier
// when it is ready and started.

var _ team.PauseNotifier = (*Module)(nil)

// pauseNotifier is the module as the interface (a seam for tests).
func (m *Module) pauseNotifier() team.PauseNotifier { return m }

// NotifyPause queues the push to every device, collapsing on CollapseID (a repeat of the same pause replaces the earlier one on
// the phone). It goes through the same sender as an approval push: the sender reads the open approval set when it sends and adds
// open_approvals / open_approval_keys from that read, or leaves both out when it cannot read — this code never writes them — and
// resolves the session's session_code from its tmux session. The payload also names the session (session_id) and host_id. done is true when it was queued or deliberately not sent — a person at the computer needs no phone push, no device
// means nobody to tell — and false when the push side is not up (not started, stopped) or its queue is full: the outbox row stays
// and is tried again.
func (m *Module) NotifyPause(p team.PausePush) bool {
	snd := m.sender.Load() // one read: Stop may clear it at any moment
	if snd == nil {
		return false
	}
	if m.presence != nil && m.presence.AtComputer() {
		return true
	}
	devs := m.snapshot()
	if len(devs) == 0 {
		return true
	}
	ids := make([]string, len(devs))
	for i, d := range devs {
		ids[i] = d.DeviceID
	}
	// Approval is the sender's handle for "resolve this Job's session_code from its tmux session at send time"; a pause carries no approval.
	ja := &JobApproval{ID: p.CollapseID, TmuxSession: p.TmuxSession, CreatedAt: p.SinceMs}
	return snd.Enqueue(Job{DeviceIDs: ids, Approval: ja, Make: func(push.Device) (push.Content, bool) {
		return push.Content{Title: p.Text, Kind: "pause", CollapseID: p.CollapseID, SessionID: p.SessionID}, true
	}})
}
