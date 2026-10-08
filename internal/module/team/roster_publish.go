package teammod

// Announcing the roster (plan PL-1f′): the team.roster host event, a
// snapshot to each new subscriber and a changed after each change. roster.go
// builds the roster; this file decides when it is sent.
//
// Who builds: a write that can change the roster only SIGNALS
// (rosterChanged); one publisher goroutine, started by Start and joined by
// Stop, turns signals into builds. The build resolves every session against
// the registry and the naming store, so it must never run on the caller's
// thread — most callers hold createMu (afterApproved), and a slow registry
// there would stall every create.

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/team"
)

// rosterEvent is the HostEvent of {op, teams}.
func rosterEvent(op string, teams []team.TeamRoster) (core.HostEvent, error) {
	v, err := json.Marshal(team.RosterEventValue{Op: op, Teams: teams})
	if err != nil {
		return core.HostEvent{}, fmt.Errorf("encode %s %s event: %w", team.RosterEventType, op, err)
	}
	return core.HostEvent{Type: team.RosterEventType, Value: string(v)}, nil
}

// rosterChanged is called after every write that can change the roster, once
// the member or team it concerns is in its final observable state, and on
// the sweeper's liveness tick (which also catches title and name changes the
// module never sees written). It only signals the publisher: a non-blocking
// send on a one-slot channel, so it never blocks and never does I/O, and a
// signal already pending absorbs the ones that follow (a burst becomes one
// more build). The publisher builds the roster and, when its JSON differs
// from the last one sent, sends {op:"changed"} (rosterSync).
func (m *Module) rosterChanged() {
	select {
	case m.rosterSig <- struct{}{}:
	default:
	}
}

// runRoster is the publisher: one rosterSync(true) per signal, until Stop.
// rosterBarrier is a test seam: the barrier handles a signal already pending
// and then releases its caller, so a test knows every signal sent before it
// has been published. Nothing in production sends on it.
func (m *Module) runRoster() {
	defer m.sweepWG.Done()
	for {
		select {
		case <-m.stopCtx.Done():
			return
		case <-m.rosterSig:
			m.rosterSync(true)
		case done := <-m.rosterBarrier:
			select {
			case <-m.rosterSig:
				m.rosterSync(true)
			default:
			}
			close(done)
		}
	}
}

// rosterBaseline records the roster as it is now as the last one sent,
// without sending: Start calls it before anything it does at boot, so the
// first tick does not announce a roster every client was already given.
func (m *Module) rosterBaseline() {
	m.rosterSync(false)
}

// rosterSync builds the roster and, when its JSON differs from the last one
// sent (or none was sent yet, or a snapshot could not be built since),
// sends {op:"changed"} to every subscriber with BroadcastStrict — the
// roster is state, a dropped changed would leave a window grouping its tabs
// wrong until the next change, so a subscriber that cannot take it is closed
// and reconnects for the snapshot. send=false only records. The read and
// the send share rosterMu with sendRosterSnapshot, so a snapshot and a
// changed never interleave. A roster that cannot be read is logged and the
// last one stays the last sent (the next call tries again). The publisher
// runs it; tests call it directly.
func (m *Module) rosterSync(send bool) {
	if m.core == nil || m.core.Events == nil || m.store == nil {
		return
	}
	m.rosterMu.Lock()
	defer m.rosterMu.Unlock()
	roster, err := m.buildRoster()
	if err != nil {
		m.logf("[team] roster not sent: %v", err)
		return
	}
	raw, err := json.Marshal(roster)
	if err != nil {
		m.logf("[team] encode roster: %v", err)
		return
	}
	sum := sha256.Sum256(raw)
	if m.rosterSent && sum == m.lastRosterHash {
		return
	}
	m.lastRosterHash, m.rosterSent = sum, true
	if !send {
		return
	}
	ev, err := rosterEvent("changed", roster.Teams)
	if err != nil {
		m.logf("[team] %v", err)
		return
	}
	m.core.Events.BroadcastStrict(ev)
}

// sendRosterSnapshot queues {op:"snapshot", teams} to a new subscriber,
// read and sent under rosterMu as sendSnapshot does for the approvals. A
// roster that cannot be read sends nothing and keeps the subscriber:
// reconnecting would not fix a stored value and the other streams on the
// connection must not suffer for it (the App's GET answers the error). It
// does mark the publisher "unsent", though: that subscriber now holds no
// roster, and the hash gate would otherwise keep an unchanged roster from
// ever reaching it, so the next successful sync (the next signal, at the
// latest the 10 s liveness tick) broadcasts the full roster as changed to
// everyone. A full buffer closes the subscriber so it reconnects.
func (m *Module) sendRosterSnapshot(sub *core.EventSubscriber) {
	m.rosterMu.Lock()
	roster, err := m.buildRoster()
	var ev core.HostEvent
	if err == nil {
		ev, err = rosterEvent("snapshot", roster.Teams)
	}
	var data []byte
	if err == nil {
		data, err = json.Marshal(ev)
	}
	if err != nil {
		m.rosterSent = false
	}
	sent := err == nil && sub.TrySend(data)
	m.rosterMu.Unlock()
	switch {
	case err != nil:
		m.logf("[team] roster snapshot not sent: %v", err)
	case !sent:
		select {
		case <-sub.Done():
		default:
			m.logf("[team] roster snapshot could not be queued (send buffer full); closing the connection so the client reconnects")
			m.core.Events.Remove(sub)
		}
	}
}
