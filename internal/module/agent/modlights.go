package agent

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
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

	modMu       sync.Mutex
	modStreams  map[string]*lights.StreamState // by stream id
	modBySID    map[string]string              // sid → the newest stream reporting it
	modDirty    map[string]string              // sids whose panes must be re-emitted → the latest event type that dirtied them
	modKick     chan struct{}                  // cap 1: wakes the re-emit worker
	modLiveSeen map[string]bool                // sid → Live() as of the worker's last round; under modMu

	// modOverlayOn is the overlay switch. It is on exactly while the re-emit
	// worker runs (startModLights turns it on once the worker is in its loop,
	// stopModLights turns it off first): without the worker a mod change
	// after a hook emit is never re-sent (the Stop hook arrives before the
	// mod's 150 ms-batched turn.complete, and the light would stay running).
	// The subscriber records either way; only applyModOverlay reads the
	// switch.
	modOverlayOn atomic.Bool

	// The re-emit worker (modworker.go). Touched by startModLights /
	// stopModLights only, which Start / Stop call one at a time.
	modTick         time.Duration      // worker period; test seam
	modWorkerCancel context.CancelFunc // nil: no worker
	modWorkerDone   chan struct{}      // closed when the worker goroutine returns
	// modWorkerStartHook, when set, runs in the worker goroutine before it
	// reports ready: a test seam to hold the worker back and observe that the
	// overlay is still off.
	modWorkerStartHook func()
}

// modTickDefault is how often the worker looks for stale flips when no mod
// event kicks it (LiveWindow is 30 s, so a flip is noticed within one tick).
const modTickDefault = 5 * time.Second

// What dirtied a sid when no mod event did.
const (
	modEventStale = "stale" // the stream stopped driving the light (or vanished)
	modEventLive  = "live"  // the stream started driving it with no event in between
	modEventEvict = "evict" // the sid index moved because a stream was dropped
)

func newModLights() modLights {
	return modLights{
		modNow:      time.Now,
		modStreams:  make(map[string]*lights.StreamState),
		modBySID:    make(map[string]string),
		modDirty:    make(map[string]string),
		modKick:     make(chan struct{}, 1),
		modLiveSeen: make(map[string]bool),
		modTick:     modTickDefault,
	}
}

// modClock reads modNow, or the wall clock on a Module built without New.
func (l *modLights) modClock() time.Time {
	if l.modNow == nil {
		return time.Now()
	}
	return l.modNow()
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
	now := m.modClock()
	m.modMu.Lock()
	st := m.modStreams[info.Stream]
	if st == nil {
		// Only a new stream pays for the sweep, so steady-state events
		// stay O(1). The registry evicts its own streams without telling
		// subscribers; the mirror bounds itself the same way.
		m.evictModStreamsLocked(now)
		for len(m.modStreams) >= modevents.MaxStreams && m.dropOldestModStreamLocked() {
		}
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
		m.markDirtyLocked(prevSID, ev.Type)
		if st.SID != "" {
			m.markDirtyLocked(st.SID, ev.Type)
		}
	}
	if st.SID != "" {
		switch {
		case !st.Ended:
			m.modBySID[st.SID] = info.Stream // the stream that just reported is the newest
		case m.modBySID[st.SID] == info.Stream:
			// An ended stream never takes or keeps the index: a live
			// sibling with this sid keeps the pane's overlay.
			m.repointSIDLocked(st.SID)
			m.markDirtyLocked(st.SID, ev.Type)
		}
		if changed {
			m.markDirtyLocked(st.SID, ev.Type)
		}
	}
	m.modMu.Unlock()
	select {
	case m.modKick <- struct{}{}:
	default:
	}
}

// markDirtyLocked records that sid's panes must be re-emitted, and what
// dirtied it last (the worker reports it as detail.mod_event). modMu must be
// held.
func (m *Module) markDirtyLocked(sid, why string) {
	m.modDirty[sid] = why
}

// repointSIDLocked points sid at the other stream reporting it that heard
// from its mod last and has not ended, or drops the entry when there is
// none. modMu must be held.
func (m *Module) repointSIDLocked(sid string) {
	best := ""
	var bestAt time.Time
	for id, st := range m.modStreams {
		if st.SID != sid || st.Ended || id == m.modBySID[sid] {
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

// evictModStreamsLocked drops the streams the registry would have dropped
// by now: an ended one EndedTTL after its session.end, any one IdleTTL after
// its last event. StreamState has no EndedAt; the session.end is the last
// event an ended stream applied (any later event reopens it), so LastEvent
// stands in for it. a-3b's worker tick may call this too. modMu must be
// held.
func (m *Module) evictModStreamsLocked(now time.Time) {
	for id, st := range m.modStreams {
		age := now.Sub(st.LastEvent)
		if age >= modevents.IdleTTL || (st.Ended && age >= modevents.EndedTTL) {
			m.dropModStreamLocked(id)
		}
	}
}

// dropOldestModStreamLocked drops the stream heard from longest ago and
// reports whether there was one. modMu must be held.
func (m *Module) dropOldestModStreamLocked() bool {
	oldest := ""
	var oldestAt time.Time
	for id, st := range m.modStreams {
		if oldest == "" || st.LastEvent.Before(oldestAt) {
			oldest, oldestAt = id, st.LastEvent
		}
	}
	if oldest == "" {
		return false
	}
	m.dropModStreamLocked(oldest)
	return true
}

// dropModStreamLocked forgets a stream. When the sid index points at it,
// the index moves to the newest live sibling (or goes) and the sid is
// dirty. modMu must be held.
func (m *Module) dropModStreamLocked(id string) {
	st := m.modStreams[id]
	delete(m.modStreams, id)
	if st != nil && st.SID != "" && m.modBySID[st.SID] == id {
		m.repointSIDLocked(st.SID)
		m.markDirtyLocked(st.SID, modEventEvict)
	}
}

// modLight is what the overlay copies out of a live stream under modMu.
type modLight struct {
	status     agentpkg.Status
	background lights.Background
	dots       []lights.Dot
}

// applyModOverlay replaces, in place, the light of every projection whose
// top frame's session id has a live mod stream (spec §7): status, source
// "mod", background and the dots. Projections without one keep the hook
// light. It takes modMu only to copy the stream states (callers may hold
// m.mu: the order is m.mu → modMu, and modMu is a leaf).
func (m *Module) applyModOverlay(projections []SessionProjection) {
	if len(projections) == 0 || !m.modOverlayOn.Load() {
		return
	}
	lit := make(map[int]modLight)
	bySID := make(map[string]*modLight) // each sid's state is copied once; nil: no live stream
	now := m.modClock()
	m.modMu.Lock()
	for i := range projections {
		top := projections[i].TopFrame
		if top == nil || top.SessionID == "" {
			continue
		}
		l, seen := bySID[top.SessionID]
		if !seen {
			st := m.modStreams[m.modBySID[top.SessionID]]
			// An ended stream is never live: the pane falls back to its
			// frame, which the hook SessionEnd or the sweep removes; the
			// overlay never invents a clear.
			if st != nil && st.SID == top.SessionID && st.Live(now) {
				l = &modLight{status: st.Status(), background: st.Background, dots: st.DotList()}
			}
			bySID[top.SessionID] = l
		}
		if l != nil {
			lit[i] = *l
		}
	}
	m.modMu.Unlock()

	for i, l := range lit {
		p := &projections[i]
		p.Status = l.status
		p.Source = SourceMod
		p.Background = string(l.background)
		p.Subagents = overlayDots(p.Subagents, l.dots, p.TopFrame.AgentType)
	}
}

// overlayDots is the projection's proxy refs followed by one native ref per
// mod dot, in dot order. A dot the hooks also track keeps the hook ref
// (Delegating, DelegatingToolUseIDs, StartedAt); a new one starts at the
// spawn (ms → ns, the unit of a hook ref's StartedAt). Hook native refs the
// mod does not dot (workflow agents, N6) are left out — of the projection
// only, the frame row keeps them.
func overlayDots(refs []agentpkg.SubagentRef, dots []lights.Dot, agentType string) []agentpkg.SubagentRef {
	out := make([]agentpkg.SubagentRef, 0, len(refs)+len(dots))
	native := make(map[string]agentpkg.SubagentRef)
	for _, r := range refs {
		if r.IsProxy {
			out = append(out, r)
		} else if _, seen := native[r.ID]; !seen {
			native[r.ID] = r
		}
	}
	for _, d := range dots {
		if r, ok := native[d.ID]; ok {
			out = append(out, r)
			continue
		}
		out = append(out, agentpkg.SubagentRef{ID: d.ID, Type: agentType, StartedAt: d.StartedAt * int64(time.Millisecond)})
	}
	return out
}
