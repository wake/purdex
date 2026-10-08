package resourcesmod

import (
	"context"
	"encoding/json"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/resources"
)

// The WS host event resources.changed (plan Task 1.6): the snapshot, without
// the recent list, pushed while something is held or waiting, once right after
// a change (a grant or an end), and to a new subscriber on connect. At most
// one a eventEvery; a change inside the window waits for its end and is sent
// once, whatever the number of changes (they coalesce). Nothing while idle.
//
// The sending runs on a goroutine of its own, never on the path that changed
// the rows: that path only signals (requestEvent never blocks), and the
// snapshot's database reads happen outside every lock.

// eventEvery is the throttle window (a var so tests can shorten it).
var eventEvery = 5 * time.Second

// requestEvent asks for a resources.changed to go out, at the throttle's next
// opportunity. Safe from anywhere, never blocks; many requests are one send.
func (m *Module) requestEvent() {
	select {
	case m.evSig <- struct{}{}:
	default:
	}
}

// runEvents is the publisher goroutine: one send per signal, at most one per
// eventEvery.
func (m *Module) runEvents(ctx context.Context) {
	defer m.wg.Done()
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.evSig:
		}
		if wait := eventEvery - time.Since(last); !last.IsZero() && wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
		select { // whatever came in while it waited is this send
		case <-m.evSig:
		default:
		}
		last = time.Now()
		if ev, err := m.eventNow(); err != nil {
			m.logf("[resources] resources.changed not sent: %v", err)
		} else if m.core != nil && m.core.Events != nil {
			m.core.Events.BroadcastEvent(ev)
		}
	}
}

// eventNow is the event for the current state: the snapshot as GET
// /api/resources gives it, minus recent.
func (m *Module) eventNow() (core.HostEvent, error) {
	snap := m.current()
	m.addLeases(&snap)
	snap.Recent = nil
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

// hasLeaseActivity says whether a lease is held (held: the rows this tick's
// measuring read) or a request waits (one indexed count). False when the
// waiting count cannot be read.
func (m *Module) hasLeaseActivity(held []leaseRow) bool {
	if len(held) > 0 {
		return true
	}
	n, err := m.store.CountWaiting(context.Background())
	return err == nil && n > 0
}
