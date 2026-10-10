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

	// hookBackground is the hook-sourced background symbol by frame id
	// (hookbackground.go): what a Stop hook reported, shown while no live
	// stream drives the pane. Under modMu.
	hookBackground map[string]lights.Background
	// hookBgClearedAt is the broadcast stamp of each frame's last
	// SessionStart: a Stop stamped at or before it cannot set the symbol.
	hookBgClearedAt map[string]int64
	// hookEdge is each root cc frame's last turn-boundary hook by frame id
	// (hookedge.go). Under modMu.
	hookEdge map[string]hookEdge
	// hookEdgeClearedAt is the arrival time of each frame's last
	// SessionStart: a hook that arrived at or before it cannot set an edge.
	hookEdgeClearedAt map[string]time.Time

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

	modEventEdgeExpired = "edge-expired" // a hook turn edge ran out (hookedge.go)
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

		hookBackground:    make(map[string]lights.Background),
		hookBgClearedAt:   make(map[string]int64),
		hookEdge:          make(map[string]hookEdge),
		hookEdgeClearedAt: make(map[string]time.Time),
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

// startModLights subscribes to the registry, starts the re-emit worker and,
// only once the worker is in its loop, turns the overlay on. Without a
// registry nothing starts and the overlay stays off.
func (m *Module) startModLights() {
	if m.modReg == nil || m.modCancel != nil {
		return
	}
	m.modCancel = m.modReg.Subscribe(m.onModEvent)
	m.startModWorker()
	m.modOverlayOn.Store(true)
	m.remarkModSIDsDirty()
}

// remarkModSIDsDirty marks every sid with a stream dirty again and kicks the
// worker. Events that arrived between the subscription and the overlay going
// on may have been consumed by a round that still saw the overlay off; their
// panes are showing the hook light and nothing else would re-send them.
func (m *Module) remarkModSIDsDirty() {
	m.modMu.Lock()
	for sid := range m.modBySID {
		if _, ok := m.modDirty[sid]; !ok {
			m.markDirtyLocked(sid, modEventLive)
		}
	}
	clear(m.modLiveSeen)
	m.modMu.Unlock()
	select {
	case m.modKick <- struct{}{}:
	default:
	}
}

// stopModLights undoes startModLights in the opposite order: the overlay
// goes off first (nothing is left to re-emit what it would show), then the
// subscription, then the worker, which is waited for. Safe to call twice.
func (m *Module) stopModLights() {
	m.modOverlayOn.Store(false)
	if m.modCancel != nil {
		m.modCancel()
		m.modCancel = nil
	}
	m.stopModWorker()
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
	prevEventAt := st.StatusEventAt
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
		if changed || m.edgeSupersededLocked(st.SID, prevEventAt, st.StatusEventAt, now) {
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
	status        agentpkg.Status
	background    lights.Background
	dots          []lights.Dot
	statusEventAt time.Time // when the event that last moved the stream's light happened, for the hook edge
}

// applyModOverlay replaces, in place, the light of every projection whose
// top frame's session id has a live mod stream (spec §7): status, source
// "mod", background and the dots. The one exception is the status (and its
// source "hook") while a turn-boundary hook edge newer than the stream's last
// light event is in force (hookedge.go). Projections without one keep the hook
// light, with the background symbol a Stop hook reported (hookbackground.go;
// that part needs no mod, so it runs whether or not the overlay is on). It
// takes modMu only to copy the stream states and the hook symbols (callers
// may hold m.mu: the order is m.mu → modMu, and modMu is a leaf).
func (m *Module) applyModOverlay(projections []SessionProjection) {
	if len(projections) == 0 {
		return
	}
	overlayOn := m.modOverlayOn.Load()
	lit := make(map[int]modLight)
	hookBg := make(map[int]lights.Background)
	edged := make(map[int]agentpkg.Status) // projections a hook turn edge decides
	bySID := make(map[string]*modLight)    // each sid's state is copied once; nil: no live stream
	now := m.modClock()
	m.modMu.Lock()
	for i := range projections {
		top := projections[i].TopFrame
		if top == nil {
			continue
		}
		if b := m.hookBackground[top.FrameID]; b != "" {
			hookBg[i] = b
		}
		if !overlayOn || top.SessionID == "" {
			continue
		}
		l, seen := bySID[top.SessionID]
		if !seen {
			st := m.modStreams[m.modBySID[top.SessionID]]
			// An ended stream is never live: the pane falls back to its
			// frame, which the hook SessionEnd or the sweep removes; the
			// overlay never invents a clear.
			if st != nil && st.SID == top.SessionID && st.Live(now) {
				l = &modLight{status: st.Status(), background: st.Background, dots: st.DotList(), statusEventAt: st.StatusEventAt}
			}
			bySID[top.SessionID] = l
		}
		if l != nil {
			lit[i] = *l
			if es, ok := m.edgeStatusLocked(top.FrameID, top.SessionID, l.statusEventAt, now); ok {
				edged[i] = es
			}
		}
	}
	m.modMu.Unlock()

	for i, b := range hookBg {
		if _, driven := lit[i]; !driven {
			projections[i].Background = string(b)
		}
	}
	for i, l := range lit {
		p := &projections[i]
		p.Status = l.status
		p.Source = SourceMod
		if es, ok := edged[i]; ok {
			p.Status = es
			p.Source = SourceHook
		}
		p.Background = string(l.background)
		p.Subagents = overlayDots(p.Subagents, l.dots, p.TopFrame.AgentType)
	}
}

// overlayStatus is the light of one frame as the pane shows it: the status the frame's hooks derived (hookStatus), laid
// under the mod's light for the conversation when a live stream reports it, the same rule applyModOverlay applies to the
// pane's broadcast (a turn-boundary hook newer than the mod's last light event still decides). The conversation's header
// reads the frame store directly (LightStatus, ConfirmedOwners), not the broadcast, so without this a turn the hooks
// never close — Esc runs no Stop hook — stays 'running' in the header while the pane's own light is idle. With no live
// stream, or with the overlay off, it is hookStatus unchanged.
func (m *Module) overlayStatus(sessionID, frameID, hookStatus string) string {
	if m == nil || sessionID == "" || !m.modOverlayOn.Load() {
		return hookStatus
	}
	now := m.modClock()
	m.modMu.Lock()
	defer m.modMu.Unlock()
	st := m.modStreams[m.modBySID[sessionID]]
	if st == nil || st.SID != sessionID || !st.Live(now) {
		return hookStatus
	}
	if es, ok := m.edgeStatusLocked(frameID, sessionID, st.StatusEventAt, now); ok {
		return string(es)
	}
	return string(st.Status())
}

// edgeStatusLocked is the status a turn-boundary hook decides for the frame for now, when that hook is newer than the
// mod's last light event of the stream (hookedge.go); ok is false when no edge wins. The one rule both the pane's
// broadcast (applyModOverlay) and the conversation's header (overlayStatus) apply. modMu must be held.
func (m *Module) edgeStatusLocked(frameID, sessionID string, statusEventAt, now time.Time) (agentpkg.Status, bool) {
	if e, ok := m.hookEdge[frameID]; ok && e.wins(sessionID, statusEventAt, now) {
		return e.status, true
	}
	return "", false
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
