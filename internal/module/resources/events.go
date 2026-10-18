package resourcesmod

import (
	"encoding/json"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/resources"
)

// The WS host event resources.changed: the snapshot, sent to a new subscriber on connect. (It was also pushed while a lease was
// held or waiting; with the leases gone nothing changes the state between samples, WWD-0a.)

// eventNow is the event for the current state: the snapshot as GET /api/resources gives it.
func (m *Module) eventNow() (core.HostEvent, error) {
	snap := m.current()
	b, err := json.Marshal(snap)
	if err != nil {
		return core.HostEvent{}, err
	}
	return core.HostEvent{Type: resources.EventType, Value: string(b)}, nil
}

// sendEventSnapshot is the OnSubscribe callback: a new subscriber gets the
// state once, so it does not wait for the next change.
func (m *Module) sendEventSnapshot(sub *core.EventSubscriber) {
	ev, err := m.eventNow()
	if err != nil {
		m.logf("[resources] resources.changed snapshot not sent: %v", err)
		return
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	sub.TrySend(data)
}
