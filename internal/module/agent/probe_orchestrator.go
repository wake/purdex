package agent

import (
	"sync"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
)

// proberWatcher is the minimal screen-watcher contract the orchestrator
// depends on. *probe.Prober trivially satisfies it; tests inject a recording
// fake. Kept unexported so the seam is scoped to this file.
type proberWatcher interface {
	Watch(target string, opts probe.WatchOptions, cb probe.ScreenChangeCallback)
	StopWatch(target string)
}

// defaultProbeProfile is the fallback profile used when an agent provider
// does not implement agentpkg.ProbeProfileProvider.
//
// R9 fix: BottomLines (not TopLines) preserves legacy
// CapturePaneContent(target, 10) capture region for codex / opencode in
// PR-4a-1, satisfying the §6 G5 default-profile parity gate. Per-agent
// TopLines profiles arrive in PR-4a-2 (cc) and beyond.
var defaultProbeProfile = agentpkg.ProbeProfile{BottomLines: 10, IdleStableTicks: 3}

// probeOrchestrator owns the per-session probe-watcher lifecycle.
//
// In Commit 3 (this slice) it only resolves the agent's ProbeProfile and
// starts/stops watchers via the prober. The screen-change callback is a
// no-op stub — Commit 4 wires interpretScreenEvent (graceWindow + transition
// gate + Error Guard + broadcast).
//
// R3 fix: orchestrator deliberately does NOT touch m.activeWatchers. That
// map is owned by Module.manageActivityWatch / renameSessionLocked callers,
// which already serialize updates under m.mu. Keeping the orchestrator
// lock-free with respect to m.mu lets renameSessionLocked invoke
// stopWatch/startWatch while holding m.mu without deadlocking.
type probeOrchestrator struct {
	parent *Module

	// watcher overrides parent.prober when non-nil. Tests inject a recording
	// fake here; production leaves it nil so prober() falls through to
	// parent.prober (set during Module.Init).
	watcher proberWatcher

	// graceMu / lastHookAt are placeholders for Commit 4 (recordHookAt +
	// graceWindow). Declared here so the struct shape is stable across
	// commits; not consumed in Commit 3.
	graceMu    sync.Mutex
	lastHookAt map[string]time.Time
}

// newProbeOrchestrator returns a fresh orchestrator bound to m. Called from
// Module.New so the orchestrator is always available; the prober itself is
// resolved lazily inside startWatch/stopWatch (it isn't assigned until
// Module.Init).
func newProbeOrchestrator(m *Module) *probeOrchestrator {
	return &probeOrchestrator{
		parent:     m,
		lastHookAt: make(map[string]time.Time),
	}
}

// prober returns the active proberWatcher: the test override if set,
// otherwise the Module's *probe.Prober. Returns nil when no prober has been
// wired (R14 fix nil-prober guard for partially-initialized Module fixtures).
func (o *probeOrchestrator) prober() proberWatcher {
	if o.watcher != nil {
		return o.watcher
	}
	if o.parent == nil || o.parent.prober == nil {
		return nil
	}
	return o.parent.prober
}

// startWatch resolves the agent's ProbeProfile and starts a probe watcher.
//
// Caller passes session WITHOUT the ":" suffix; orchestrator appends it to
// match the existing tmux-target convention (R7 fix).
//
// Profile resolution (R8 fix): looks up the provider via parent.registry.Get
// and asserts agentpkg.ProbeProfileProvider. Missing agentType or providers
// that don't implement the interface fall back to defaultProbeProfile.
func (o *probeOrchestrator) startWatch(session, agentType string) {
	pw := o.prober()
	if pw == nil {
		return
	}
	target := session + ":"

	profile := defaultProbeProfile
	if o.parent != nil && o.parent.registry != nil {
		if provider, ok := o.parent.registry.Get(agentType); ok {
			if pp, ok := provider.(agentpkg.ProbeProfileProvider); ok {
				profile = pp.ProbeProfile()
			}
		}
	}

	opts := probe.WatchOptions{
		TopLines:        profile.TopLines,
		BottomLines:     profile.BottomLines,
		IdleStableTicks: profile.IdleStableTicks,
	}
	pw.Watch(target, opts, o.makeCallback(session, agentType))
}

// stopWatch cancels the watcher for session. Same ":" suffix convention as
// startWatch (R7 fix); symmetric nil-prober guard (R14 fix).
func (o *probeOrchestrator) stopWatch(session string) {
	pw := o.prober()
	if pw == nil {
		return
	}
	pw.StopWatch(session + ":")
}

// makeCallback returns the ScreenChangeCallback installed on Watch. In
// Commit 3 it is intentionally a no-op stub — Commit 4 replaces the body
// with interpretScreenEvent (stale-callback guard / graceWindow / Error
// Guard / transition gate / broadcast). The closure already captures
// session + agentType so Commit 4 can wire those through without changing
// the registration site.
func (o *probeOrchestrator) makeCallback(session, agentType string) probe.ScreenChangeCallback {
	_ = session
	_ = agentType
	return func(probe.ScreenChangeEvent) {
		// TODO Commit 4: invoke o.interpretScreenEvent(session, agentType, ev).
	}
}
