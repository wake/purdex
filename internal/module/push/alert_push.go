package push

import (
	"github.com/wake/purdex/internal/push"
)

// The alert push (WWD-8a-1b, spec §13): the module that delivers the wwd host alerts asks, in process, for one phone push per
// alert. The module publishes itself as the AlertNotifier when it is ready and started.

var _ push.AlertNotifier = (*Module)(nil)

// alertNotifier is the module as the interface (a seam for tests).
func (m *Module) alertNotifier() push.AlertNotifier { return m }

// NotifyAlert queues the push to every device, collapsing on CollapseID (the same alert again replaces the earlier one on the phone).
// done is true when it was queued or deliberately not sent — a person at the computer needs no phone push, no device means nobody to
// tell — and false when the push side is not up (not started, stopped) or its queue is full: the caller keeps the alert undelivered.
func (m *Module) NotifyAlert(p push.AlertPush) bool {
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
	// An alert is no one's approval and belongs to no session: the Job carries no Approval, so the sender resolves no session_code.
	return snd.Enqueue(Job{DeviceIDs: ids, Make: func(push.Device) (push.Content, bool) {
		return push.Content{Title: p.Title, Kind: "alert", CollapseID: p.CollapseID}, true
	}})
}
