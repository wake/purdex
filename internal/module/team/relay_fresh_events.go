package teammod

// The fresh halves of the host as a list (RT-3a2 T32, S-21): GET /api/relay/fresh and the relay.fresh host event, a snapshot to each new
// subscriber and a changed after each change. The roster publisher drives it (rosterSync calls freshSync), so every write that signals the
// roster also refreshes this list, and the 10 s liveness tick catches the rest; a list equal to the last one sent is not sent again.

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/wake/purdex/internal/core"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// freshStream orders the relay.fresh stream: held across freshSync's read + send and across sendFreshSnapshot's, so a snapshot and a
// changed never interleave. lastHash is the hash of the list last sent as changed, valid when sent.
type freshStream struct {
	mu       sync.Mutex
	lastHash [sha256.Size]byte
	sent     bool
}

func freshEvent(op string, sessions []team.RelayFreshSession) (core.HostEvent, error) {
	v, err := json.Marshal(team.RelayFreshEventValue{Op: op, Sessions: sessions})
	if err != nil {
		return core.HostEvent{}, fmt.Errorf("encode %s %s event: %w", team.RelayFreshEventType, op, err)
	}
	return core.HostEvent{Type: team.RelayFreshEventType, Value: string(v)}, nil
}

// buildFresh is the sessions that hold a fresh half now, oldest first.
func (m *Module) buildFresh() ([]team.RelayFreshSession, error) {
	halves, order, err := m.store.FreshHalves()
	if err != nil {
		return nil, err
	}
	var origins map[string]team.Origin
	if len(order) > 0 && m.origins != nil {
		if origins, err = m.origins.ResolveOriginsBySession(order); err != nil {
			m.logf("[team] fresh list codes: %v", err) // a list without codes is still a list
			origins = nil
		}
	}
	out := make([]team.RelayFreshSession, 0, len(order))
	for _, sid := range order {
		h := halves[sid]
		out = append(out, team.RelayFreshSession{SessionID: sid, Ref: ipeers.RefID(sid), OpID: h.OpID, Path: h.Path, WrittenAt: h.WrittenAt,
			Code: m.sessionCodeOf(origins[sid].Tmux)})
	}
	return out, nil
}

// handleRelayFreshGet is GET /api/relay/fresh.
func (m *Module) handleRelayFreshGet(w http.ResponseWriter, r *http.Request) {
	sessions, err := m.buildFresh()
	if err != nil {
		m.logf("[team] relay fresh list: %v", err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, team.RelayFreshView{Sessions: sessions})
}

// freshSync sends {op:"changed"} to every subscriber when the list differs from the last one sent (BroadcastStrict: it is state, a
// subscriber that cannot take it reconnects for the snapshot). send=false only records. A list that cannot be read is logged and the last
// one stays the last sent.
func (m *Module) freshSync(send bool) {
	if m.core == nil || m.core.Events == nil || m.store == nil {
		return
	}
	m.fresh.mu.Lock()
	defer m.fresh.mu.Unlock()
	sessions, err := m.buildFresh()
	if err != nil {
		m.logf("[team] fresh list not sent: %v", err)
		return
	}
	raw, err := json.Marshal(sessions)
	if err != nil {
		m.logf("[team] encode fresh list: %v", err)
		return
	}
	sum := sha256.Sum256(raw)
	if m.fresh.sent && sum == m.fresh.lastHash {
		return
	}
	m.fresh.lastHash, m.fresh.sent = sum, true
	if !send {
		return
	}
	ev, err := freshEvent("changed", sessions)
	if err != nil {
		m.logf("[team] %v", err)
		return
	}
	m.core.Events.BroadcastStrict(ev)
}

// sendFreshSnapshot queues {op:"snapshot"} to a new subscriber, as sendRosterSnapshot does: a list that cannot be read sends nothing,
// keeps the subscriber and marks the publisher "unsent" so the next sync reaches everyone; a full buffer closes the subscriber.
func (m *Module) sendFreshSnapshot(sub *core.EventSubscriber) {
	m.fresh.mu.Lock()
	sessions, err := m.buildFresh()
	var ev core.HostEvent
	if err == nil {
		ev, err = freshEvent("snapshot", sessions)
	}
	var data []byte
	if err == nil {
		data, err = json.Marshal(ev)
	}
	if err != nil {
		m.fresh.sent = false
	}
	sent := err == nil && sub.TrySend(data)
	m.fresh.mu.Unlock()
	switch {
	case err != nil:
		m.logf("[team] fresh snapshot not sent: %v", err)
	case !sent:
		select {
		case <-sub.Done():
		default:
			m.logf("[team] fresh snapshot could not be queued (send buffer full); closing the connection so the client reconnects")
			m.core.Events.Remove(sub)
		}
	}
}
