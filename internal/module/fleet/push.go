package fleetmod

import (
	"encoding/json"
	"time"

	"github.com/wake/purdex/internal/core"
	leaf "github.com/wake/purdex/internal/fleet"
)

// EventFleetChanged is the host event the primary pushes once a second to the subscribers that opted in (?fleet=v1).
const EventFleetChanged = "fleet.changed"

// Pusher is the events broadcaster as the push uses it: best-effort, newest-wins, to the opted-in subscribers only.
type Pusher interface {
	BroadcastNewestTo(feature string, ev core.HostEvent)
	HasSubscribersWanting(feature string) bool
}

// PushPayload is the Value of a fleet.changed event.
type PushPayload struct {
	Term    int64      `json:"term"`
	Primary string     `json:"primary"`
	Hosts   []PushHost `json:"hosts"`
}

// idOf is a host's id as the App knows it: a daemon host's own daemon host id (a peer's is looked up now, the pairing may
// have changed), a probe host's config row id.
func (m *Measure) idOf(h *hostRow) string {
	if h.key == "peer" && m.d.PeerHostID != nil {
		if id := m.d.PeerHostID(h.fleetAlias); id != "" {
			return id
		}
		return h.fleetAlias
	}
	if h.staticID != "" {
		return h.staticID
	}
	return h.fleetAlias
}

func viewOK(h *hostRow) (status, errCode string, lastOK *time.Time) {
	if !h.lastOK.IsZero() {
		t := h.lastOK
		lastOK = &t
	}
	return string(h.status), string(h.code), lastOK
}

// lookRepeat is how many pushes in a row carry a changed look. The push is newest-wins per subscriber, so a push a slow
// subscriber never got is gone, and a look changes rarely: sent once, a drop would lose it until the App's own full re-read.
// Ten pushes (about ten seconds) matches that re-read: a subscriber that went ten seconds without any push re-reads in full,
// and one that did not saw at least one of the ten. The App applies a repeated Full idempotently.
const lookRepeat = 10

// lookDueLocked says whether this push carries the host's Full row (m.mu held). The first push of a sequence only records a
// baseline (the App has just read every host in full); after that a host that is new or whose look changed starts a window.
func (m *Measure) lookDueLocked(key string, h *hostRow, baseline bool) bool {
	sig, _ := json.Marshal([]any{h.kind, h.fleetAlias, h.name, h.icon, h.colors, h.iconWeight})
	if prev, seen := m.lookSig[key]; !seen || prev != string(sig) {
		m.lookSig[key] = string(sig)
		if !baseline {
			m.lookLeft[key] = lookRepeat
		}
	}
	if m.lookLeft[key] > 0 {
		m.lookLeft[key]--
		return true
	}
	return false
}

// push sends the fleet to the opted-in subscribers: the fast part of every host, a slow part only in the push right after it
// changed. A standby or an off daemon sends nothing, and nothing is built while no subscriber asked.
func (m *Measure) push() {
	st := m.d.Role.State()
	if st.Role != leaf.RolePrimary || m.d.Events == nil || !m.d.Events.HasSubscribersWanting(core.FeatureFleetV1) {
		return
	}
	m.mu.Lock()
	baseline := len(m.lookSig) == 0
	for key := range m.lookSig {
		if m.hosts[key] == nil {
			delete(m.lookSig, key)
			delete(m.lookLeft, key)
		}
	}
	hosts := make([]PushHost, 0, len(m.order))
	for _, key := range m.order {
		h := m.hosts[key]
		status, code, lastOK := viewOK(h)
		ph := PushHost{ID: m.idOf(h), Status: status, LastOKAt: lastOK, Fast: h.fast}
		if code != "" {
			ph.Error = &code
		}
		if h.slow != nil && m.pushedSlow[key] != h.slowSeq {
			ph.Slow = h.slow
			m.pushedSlow[key] = h.slowSeq
		}
		if m.lookDueLocked(key, h, baseline) {
			full := m.hostJSONLocked(h)
			ph.Full = &full
		}
		hosts = append(hosts, ph)
	}
	val, err := json.Marshal(PushPayload{Term: st.Term, Primary: primaryOf(st), Hosts: hosts})
	m.mu.Unlock()
	if err != nil {
		return
	}
	m.d.Events.BroadcastNewestTo(core.FeatureFleetV1, core.HostEvent{Type: EventFleetChanged, Value: string(val)})
}

func primaryOf(st leaf.RoleState) string {
	if st.Primary != "" {
		return st.Primary
	}
	return st.Self
}
