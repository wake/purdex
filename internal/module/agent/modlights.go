package agent

import (
	"log"
	"sync"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/lights"
	"github.com/wake/purdex/internal/modevents"
	modeventsmod "github.com/wake/purdex/internal/module/modevents"
)

// modLights is the agent module's view of the mod event streams (lights v2,
// spec §7): one lights.StreamState per stream, fed by a registry subscriber.
//
// modMu is a leaf lock: nothing else (the frame store, tmux, m.mu, the
// events bus, the registry) is taken or called while it is held. The
// subscriber runs inside modevents.Registry.Apply under the stream's order
// mutex (spec §6.4), so it only updates this state and kicks; whoever reads
// the state copies what it needs and releases modMu before doing anything
// else.
type modLights struct {
	modReg    *modevents.Registry // nil: the mod path is off
	modCancel func()              // the registry subscription; nil when not subscribed
	modNow    func() time.Time    // daemon receive time of mod events; test seam

	modMu      sync.Mutex
	modStreams map[string]*lights.StreamState // by stream id
	modBySID   map[string]string              // sid → the newest stream reporting it
	modDirty   map[string]struct{}            // sids whose panes must be re-emitted
	modKick    chan struct{}                  // cap 1: wakes the re-emit worker
}

func newModLights() modLights {
	return modLights{
		modNow:     time.Now,
		modStreams: make(map[string]*lights.StreamState),
		modBySID:   make(map[string]string),
		modDirty:   make(map[string]struct{}),
		modKick:    make(chan struct{}, 1),
	}
}

// initModLights looks up the mod event registry. Absent (a daemon built
// without the modevents module, or a test core) leaves the mod path off:
// no stream is ever known, so every overlay lookup misses.
func (m *Module) initModLights(c *core.Core) {
	if c == nil || c.Registry == nil {
		return
	}
	svc, ok := c.Registry.Get(modeventsmod.ServiceName)
	if !ok {
		log.Print("[agent] mod event registry not found; lights come from hooks only")
		return
	}
	if reg, ok := svc.(*modevents.Registry); ok {
		m.modReg = reg
	}
}

// startModLights subscribes to the registry; stopModLights cancels it.
func (m *Module) startModLights() {
	if m.modReg != nil && m.modCancel == nil {
		m.modCancel = m.modReg.Subscribe(m.onModEvent)
	}
}

func (m *Module) stopModLights() {
	if m.modCancel != nil {
		m.modCancel()
		m.modCancel = nil
	}
}

// onModEvent is the registry subscriber. It runs synchronously inside
// Registry.Apply (the stream's order mutex is held), so it must not block
// and must never lead back into Apply: it updates the stream state under
// modMu and kicks the worker, nothing else — no frame store, no tmux, no
// m.mu, no events bus, no registry call.
func (m *Module) onModEvent(info modevents.StreamInfo, ev modevents.Event) {
	now := m.modNow()
	m.modMu.Lock()
	st := m.modStreams[info.Stream]
	if st == nil {
		st = lights.NewStreamState(info.Stream)
		m.modStreams[info.Stream] = st
	}
	prevSID := st.SID
	changed := st.Apply(ev, now)
	if prevSID != "" && prevSID != st.SID {
		// A /clear or /resume moved the stream to a new conversation: the
		// old sid's panes lose the overlay and the new sid's gain it, so
		// both are re-emitted whether or not the light changed.
		if m.modBySID[prevSID] == info.Stream {
			m.repointSIDLocked(prevSID)
		}
		m.modDirty[prevSID] = struct{}{}
	}
	if st.SID != "" {
		m.modBySID[st.SID] = info.Stream // the stream that just reported is the newest
		if changed {
			m.modDirty[st.SID] = struct{}{}
		}
	}
	m.modMu.Unlock()
	select {
	case m.modKick <- struct{}{}:
	default:
	}
}

// repointSIDLocked points sid at the other stream reporting it that heard
// from its mod last, or drops the entry when there is none. modMu must be
// held.
func (m *Module) repointSIDLocked(sid string) {
	best := ""
	var bestAt time.Time
	for id, st := range m.modStreams {
		if st.SID != sid || id == m.modBySID[sid] {
			continue
		}
		if best == "" || st.LastEvent.After(bestAt) {
			best, bestAt = id, st.LastEvent
		}
	}
	if best == "" {
		delete(m.modBySID, sid)
		return
	}
	m.modBySID[sid] = best
}
