package agent

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// The mod re-emit worker (lights v2, spec §7). The mod and the hooks report
// the same turn through two channels with different delays, so a hook emit
// can carry a light the mod has since moved on from. Every mod change (and
// every stream going quiet) marks its sid dirty; a round re-reads the
// projection of each dirty sid's panes' tmux sessions and sends the light
// again — but only when it differs from the last one sent, because the SPA
// marks every idle / waiting / error frame unread.
//
// Locks: modMu is a leaf (nothing is called under it); emitting holds
// neither modMu nor m.mu.

// lightsDigest is every wire field of a light frame that the session's
// representative pane decides: a change of representative with an equal
// status is still a change the SPA must see. Model is not part of it: a
// projection does not know the model (it rides on hook frames only, and the
// SPA keeps the last one), so a worker frame never carries one.
type lightsDigest struct {
	status     string
	background string
	source     string
	dots       string // the subagent refs' identities, in order
	frameID    string // the representative pane's top frame
	agentType  string
}

func lightsDigestOf(p *SessionProjection, n agentpkg.NormalizedEvent) lightsDigest {
	d := lightsDigest{
		status:     n.Status,
		background: n.Background,
		source:     n.Source,
		agentType:  n.AgentType,
	}
	if p != nil && p.TopFrame != nil {
		d.frameID = p.TopFrame.FrameID
	}
	// Every wire field of every ref, in the order the overlay decided: the
	// SPA draws delegating, the type and the start time too.
	dots, _ := json.Marshal(n.Subagents)
	d.dots = string(dots)
	return d
}

// recordEmittedLights remembers what was just put on the wire for session,
// so the worker compares against it. Hook, probe and sweep emits call it
// after they emit (they still emit every time; this only keeps the baseline
// right). A clear frame forgets the session. Takes m.mu.
func (m *Module) recordEmittedLights(session string, p *SessionProjection, n agentpkg.NormalizedEvent) {
	if session == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if n.Status == string(agentpkg.StatusClear) || p == nil || p.TopFrame == nil {
		delete(m.lastEmittedLights, session)
		return
	}
	m.lastEmittedLights[session] = lightsDigestOf(p, n)
}

// runModRound is one worker round: consume the dirty sids (plus the sids
// whose stream stopped or started driving the light since the last round)
// and re-emit each affected tmux session. It holds no lock across the
// emits. now is the round's clock.
func (m *Module) runModRound(now time.Time) {
	dirty := m.takeModDirty(now)
	if len(dirty) == 0 || m.frames == nil {
		return
	}
	sids := make([]string, 0, len(dirty))
	for sid := range dirty {
		sids = append(sids, sid)
	}
	slices.Sort(sids)

	type target struct{ session, why string }
	var targets []target
	seen := make(map[string]bool)
	for _, sid := range sids {
		roots, err := m.frames.ListRootsBySessionID(sid)
		if err != nil {
			continue
		}
		for _, f := range roots {
			name := m.paneSessionName(f.PaneID)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			targets = append(targets, target{name, dirty[sid]})
		}
	}
	for _, t := range targets {
		m.emitSessionState(t.session, "mod", map[string]any{"mod_event": t.why})
	}
}

// takeModDirty swaps out the dirty set and adds the sids whose stream went
// live or stale since the previous round (a quiet stream emits no event, yet
// its pane falls back to the hook status). It also runs the stream eviction
// the subscriber only does on a new stream's first event.
func (m *Module) takeModDirty(now time.Time) map[string]string {
	m.modMu.Lock()
	defer m.modMu.Unlock()
	m.evictModStreamsLocked(now)
	dirty := m.modDirty
	m.modDirty = make(map[string]string)
	if m.modLiveSeen == nil {
		m.modLiveSeen = make(map[string]bool)
	}
	for sid, id := range m.modBySID {
		st := m.modStreams[id]
		live := st != nil && st.SID == sid && st.Live(now)
		if m.modLiveSeen[sid] != live {
			why := modEventStale
			if live {
				why = modEventLive
			}
			if _, ok := dirty[sid]; !ok {
				dirty[sid] = why
			}
		}
		m.modLiveSeen[sid] = live
	}
	for sid, wasLive := range m.modLiveSeen {
		if _, ok := m.modBySID[sid]; ok {
			continue
		}
		if wasLive {
			if _, ok := dirty[sid]; !ok {
				dirty[sid] = modEventStale
			}
		}
		delete(m.modLiveSeen, sid)
	}
	return dirty
}

// emitSessionState re-sends sessionName's light when it is not the one
// already on the wire. It never invents a clear: a session with no frame
// (the hook SessionEnd or the sweep owns that) is left alone.
//
// The whole read-compare-send runs under emitMu, so a hook emit cannot go
// out between the projection read and the send: the worker never sends a
// projection older than the baseline it compares with, and the baseline is
// recorded only for a frame that went out. emitMu is taken with no other
// lock held; the projection read, the session code lookup and the broadcast
// hold nothing else, and m.mu is taken briefly for the baseline.
func (m *Module) emitSessionState(sessionName, rawEvent string, detail map[string]any) {
	if m.core == nil || m.core.Events == nil {
		return
	}
	m.emitMu.Lock()
	defer m.emitMu.Unlock()

	p, err := m.projectionForSession(sessionName)
	if err != nil || p == nil || p.TopFrame == nil || p.EffectiveStatus() == agentpkg.StatusClear {
		return
	}
	code := m.resolveSessionCode(sessionName)
	if code == "" {
		return
	}
	n := buildProjectionNormalized(p, p.TopFrame.AgentType, rawEvent, time.Now().UnixNano(), agentpkg.DeriveResult{Detail: detail})
	d := lightsDigestOf(p, n)

	m.mu.Lock()
	prev, ok := m.lastEmittedLights[sessionName]
	m.mu.Unlock()
	if ok && prev == d {
		return
	}
	if !m.emitNormalizedToCode(code, n) {
		return
	}
	m.mu.Lock()
	m.lastEmittedLights[sessionName] = d
	syncProjectionState(m.currentStatus, m.subagents, sessionName, p)
	m.mu.Unlock()
}

// startModWorker launches the worker and returns once it is in its loop, so
// the caller can turn the overlay on knowing every change from then on will
// be picked up.
func (m *Module) startModWorker() {
	if m.modWorkerCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	ready := make(chan struct{})
	m.modWorkerCancel, m.modWorkerDone = cancel, done
	tick := m.modTick
	if tick <= 0 {
		tick = modTickDefault
	}
	go func() {
		defer close(done)
		if hook := m.modWorkerStartHook; hook != nil {
			hook()
		}
		t := time.NewTicker(tick)
		defer t.Stop()
		close(ready)
		for {
			select {
			case <-ctx.Done():
				return
			case <-m.modKick:
			case <-t.C:
			}
			m.runModRound(m.modClock())
		}
	}()
	<-ready
}

// stopModWorker cancels the worker and waits for it. Safe to call when no
// worker runs.
func (m *Module) stopModWorker() {
	if m.modWorkerCancel == nil {
		return
	}
	m.modWorkerCancel()
	<-m.modWorkerDone
	m.modWorkerCancel = nil
}
