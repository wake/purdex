package agent

import (
	"log"
	"os"
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

// probeGraceWindow is the post-recordHookAt suppression window. While
// active, every screen-change event for the session is dropped (counter
// +1 each) so a hook event remains the authoritative status source for the
// duration of the window. v2.0 design: probe is dumb and keeps emitting
// raw events; once the window expires the next ScreenChanged / ScreenStable
// event naturally drives a transition via the gate (no Rearm API needed).
const probeGraceWindow = 2 * time.Second

// orchNowFn is the test-only seam used by graceWindow checks. Production
// uses time.Now; OR4 overrides this to fast-forward past probeGraceWindow
// without sleeping. Package-private so the production path stays uncluttered.
var orchNowFn = time.Now

// isDevMode reports whether the daemon is running with PDX_DEV_MODE=1. Probe
// log gating uses this so production logs stay quiet. We read the env on
// every call so flipping the var at runtime (e.g. in tests via t.Setenv)
// flips the gate.
//
// Defined locally to avoid importing the dev module (would create a circular
// dependency) — the env-var read is the cheapest possible check anyway.
func isDevMode() bool { return os.Getenv("PDX_DEV_MODE") == "1" }

// probeOrchestrator owns the per-session probe-watcher lifecycle and
// translates raw probe.ScreenChangeEvent ticks into agent status broadcasts.
//
// Lifecycle (v2.0 — dumb probe, orchestrator-only dedup):
//   - startWatch resolves the agent's ProbeProfile, registers a callback
//     bound to (session, agentType), and increments MetricProbeWatchStarted
//   - The watcher fires ScreenChanged on every diff tick + ScreenStable
//     after IdleStableTicks consecutive matches (no emit-once flags)
//   - interpretScreenEvent applies guards (stale-callback, graceWindow,
//     ErrorGuard) and a v2.0 transition gate before broadcasting; the gate
//     suppresses repeats so probe storms cannot saturate WS clients
//   - stopWatch tears the watcher down + increments MetricProbeWatchStopped
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

	// graceMu protects lastHookAt. Independent of parent.mu so recordHookAt
	// (called from the hook hot-path) does not contend with currentStatus
	// readers. Always acquired alone — never nest with parent.mu.
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
	agentpkg.MetricProbeWatchStarted.Add(1)
}

// stopWatch cancels the watcher for session. Same ":" suffix convention as
// startWatch (R7 fix); symmetric nil-prober guard (R14 fix).
func (o *probeOrchestrator) stopWatch(session string) {
	pw := o.prober()
	if pw == nil {
		return
	}
	pw.StopWatch(session + ":")
	agentpkg.MetricProbeWatchStopped.Add(1)
}

// recordHookAt registers a hook acceptance timestamp for session. Subsequent
// screen-change events arriving within probeGraceWindow of this timestamp are
// suppressed by interpretScreenEvent so the hook (the authoritative source)
// stays in charge while the agent is mid-transition.
func (o *probeOrchestrator) recordHookAt(session string) {
	o.graceMu.Lock()
	o.lastHookAt[session] = orchNowFn()
	o.graceMu.Unlock()
	if isDevMode() {
		log.Printf("[probe] recordHookAt session=%s", session)
	}
}

// makeCallback returns the ScreenChangeCallback installed on Watch. The
// closure captures (session, agentType) so the stale-callback guard inside
// interpretScreenEvent can detect rename / agent-swap mismatches against
// the parent's activeWatchers map.
func (o *probeOrchestrator) makeCallback(session, agentType string) probe.ScreenChangeCallback {
	return func(ev probe.ScreenChangeEvent) {
		o.interpretScreenEvent(session, agentType, ev)
	}
}

// interpretScreenEvent maps a raw probe.ScreenChangeEvent to a status
// transition (or drops it). Guards run in this order — each layer encodes
// a specific invariant:
//
//  1. Stale-callback guard (R4): activeWatchers must contain (session →
//     agentType). After stopWatch / rename the closure may still be invoked
//     by the watch loop; we drop these events to avoid ghost broadcasts.
//  2. graceWindow suppression (v2.0): events within probeGraceWindow of a
//     recordHookAt are dropped + counted. Hook authority over probe.
//  3. Kind → status mapping: ScreenChanged → Running; ScreenStable →
//     independent bottom capture (R14 fix #1) for shell-prompt
//     classification → Idle (or sweep on dead-PID + shell prompt).
//  4. Error Guard: never overwrite StatusError; probe is recovery-only.
//  5. Transition gate (v2.0): same currentStatus → drop. Probe storms cannot
//     saturate WS clients; first transition wins, repeats stay silent.
//  6. Apply: setProjectionTopStatus + buildProjectionNormalized + broadcast.
//     MetricProbeScreenEvent +1 only here.
func (o *probeOrchestrator) interpretScreenEvent(session, agentType string, ev probe.ScreenChangeEvent) {
	if o.parent == nil {
		return
	}
	m := o.parent

	// 1. Stale-callback guard.
	m.mu.Lock()
	currentAgent, active := m.activeWatchers[session]
	m.mu.Unlock()
	if !active || currentAgent != agentType {
		return
	}

	// 2. graceWindow suppression.
	o.graceMu.Lock()
	last, hasHook := o.lastHookAt[session]
	o.graceMu.Unlock()
	if hasHook && orchNowFn().Sub(last) < probeGraceWindow {
		agentpkg.MetricProbeGraceWindowSuppressed.Add(1)
		if isDevMode() {
			log.Printf("[probe] graceWindow suppress session=%s agent=%s kind=%s", session, agentType, ev.Kind)
		}
		return
	}

	// 3. Kind → status mapping.
	var status agentpkg.Status
	switch ev.Kind {
	case probe.ScreenChanged:
		status = agentpkg.StatusRunning
	case probe.ScreenStable:
		// R14 fix #1: ev.Content reflects the watcher's TopLines / BottomLines
		// configuration. For agents on a TopLines profile the captured content
		// won't include the bottom shell prompt, so LooksLikeShellPrompt would
		// false-negative. Take an independent bottom-N capture for the
		// shell-prompt classifier; treat any tmux error as "not a shell
		// prompt" (conservative — fall through to Idle, never block).
		var bottomContent string
		if m.tmux != nil {
			if c, err := m.tmux.CapturePaneContent(session+":", 10); err == nil {
				bottomContent = c
			}
		}
		if probe.LooksLikeShellPrompt(bottomContent) {
			projection, _ := m.projectionForSession(session)
			if projection != nil && projection.TopFrame != nil && !isPidAliveFn(projection.TopFrame.PID) {
				_ = m.sweepOnce()
				return
			}
		}
		status = agentpkg.StatusIdle
	default:
		return
	}

	// 4. Error Guard + 5. Transition gate (single critical section).
	m.mu.Lock()
	if m.currentStatus[session] == agentpkg.StatusError {
		m.mu.Unlock()
		return
	}
	if prev, ok := m.currentStatus[session]; ok && prev == status {
		m.mu.Unlock()
		return
	}
	m.currentStatus[session] = status
	m.mu.Unlock()

	// 6. Apply transition.
	agentpkg.MetricProbeScreenEvent.Add(1)
	if isDevMode() {
		log.Printf("[probe] status session=%s agent=%s status=%s reason=screen-%s", session, agentType, status, ev.Kind)
	}
	if projection, err := m.setProjectionTopStatus(session, status); err == nil && projection != nil {
		normalized := buildProjectionNormalized(projection, agentType, "probe:activity", time.Now().UnixNano(), agentpkg.DeriveResult{})
		m.broadcastToSession(session, normalized)
		return
	}
	// Fallback when the projection is unavailable (e.g. frames row removed
	// concurrently with the screen event). Broadcast a minimal normalized
	// event so SPA clients still see the status change.
	normalized := agentpkg.NormalizedEvent{
		AgentType:    agentType,
		Status:       string(status),
		RawEventName: "probe:activity",
		BroadcastTs:  time.Now().UnixNano(),
	}
	m.broadcastToSession(session, normalized)
}
