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

// recordHookAtHook is a test-only order-witness seam. Production leaves it
// nil. Tests set it to capture in-memory state at the moment recordHookAt
// fires, used by FX2 to verify recordHookAt runs BEFORE the handler's
// currentStatus mutation (codex finding #3 regression).
var recordHookAtHook func(session string)

// interruptBeforeFinalLockFn is a test-only seam invoked just before the
// final m.mu critical section in interpretScreenEvent. Production leaves it
// nil. Tests set it to mutate activeWatchers concurrently — exercising the
// atomic stale-callback re-check (codex finding #4 regression).
var interruptBeforeFinalLockFn func(session string)

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
//
// Returns true iff a watcher was successfully registered. The caller
// (manageActivityWatch / renameSessionLocked) uses this to roll back its
// activeWatchers mutation when the profile is invalid (TopLines + BottomLines
// mutually exclusive — probe.Watch would log + early-return without
// registering, leaving a silent dead watcher otherwise; codex finding #7
// regression). Returning false also skips the started-metric increment so
// /debug/vars stays an honest counter of registered watchers.
func (o *probeOrchestrator) startWatch(session, agentType string) bool {
	pw := o.prober()
	if pw == nil {
		return false
	}
	target := session + ":"

	profile := defaultProbeProfile
	profileSource := "default"
	if o.parent != nil && o.parent.registry != nil {
		if provider, ok := o.parent.registry.Get(agentType); ok {
			if pp, ok := provider.(agentpkg.ProbeProfileProvider); ok {
				profile = pp.ProbeProfile()
				profileSource = "provider"
			}
		}
	}
	if isDevMode() {
		log.Printf("[probe] startWatch session=%s agent=%s profile=%s TopLines=%d BottomLines=%d IdleStableTicks=%d",
			session, agentType, profileSource, profile.TopLines, profile.BottomLines, profile.IdleStableTicks)
	}

	// Profile validation — must mirror probe.Watch's contract (TopLines +
	// BottomLines mutually exclusive). Validating here lets the caller roll
	// back its activeWatchers entry; without this, probe.Watch silently
	// drops the call and the orchestrator is left with a "started" metric
	// + an active map entry but no goroutine.
	if profile.TopLines > 0 && profile.BottomLines > 0 {
		if isDevMode() {
			log.Printf("[probe] startWatch invalid profile session=%s agent=%s TopLines=%d BottomLines=%d — mutually exclusive; not registering",
				session, agentType, profile.TopLines, profile.BottomLines)
		}
		return false
	}

	opts := probe.WatchOptions{
		TopLines:        profile.TopLines,
		BottomLines:     profile.BottomLines,
		IdleStableTicks: profile.IdleStableTicks,
	}
	pw.Watch(target, opts, o.makeCallback(session, agentType))
	agentpkg.MetricProbeWatchStarted.Add(1)
	return true
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
	if hook := recordHookAtHook; hook != nil {
		hook(session)
	}
	if isDevMode() {
		log.Printf("[probe] recordHookAt session=%s", session)
	}
}

// migrateLastHookAt transfers the lastHookAt entry from oldName to newName
// under graceMu, preserving the active graceWindow across a session rename.
// Codex finding #2 regression: without this, the orchestrator's screen-event
// callback for newName would observe "no graceWindow active" within 2s of
// rename and overwrite the hook-set status. No-op when oldName has no entry.
func (o *probeOrchestrator) migrateLastHookAt(oldName, newName string) {
	o.graceMu.Lock()
	defer o.graceMu.Unlock()
	if stamp, ok := o.lastHookAt[oldName]; ok {
		delete(o.lastHookAt, oldName)
		o.lastHookAt[newName] = stamp
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
//     Run as an EARLY fast-path AND re-checked atomically inside the final
//     critical section (codex finding #4 regression — closes the race
//     window between the early unlock and the final lock).
//  2. graceWindow suppression (v2.0): events within probeGraceWindow of a
//     recordHookAt are dropped + counted. Hook authority over probe.
//  3. Kind → status mapping: ScreenChanged → Running; ScreenStable →
//     independent bottom capture (R14 fix #1) for shell-prompt
//     classification → Idle (or sweep on dead-PID + shell prompt). Cheap
//     pre-gate (codex finding #8): a stable-Idle session with an alive
//     top-frame PID skips the bottom CapturePaneContent entirely — there
//     is no possible transition (gate would skip Idle→Idle anyway) and no
//     cleanup work (sweep needs dead PID + shell prompt). Continuous-stable
//     panes therefore stop issuing tmux subprocess calls every N ticks.
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

	// 1. Stale-callback guard (early fast-path).
	m.mu.Lock()
	currentAgent, active := m.activeWatchers[session]
	prevStatus, hasPrev := m.currentStatus[session]
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
		// Cheap pre-gate (codex finding #8): if already Idle and top-frame
		// PID is alive, the bottom capture would only feed a transition
		// gate that's about to drop Idle→Idle. Continuous-stable Idle
		// panes are common (a session sitting at a shell prompt) — skipping
		// the tmux subprocess here is a measurable cost win. Dead-PID
		// branch falls through so the sweep cleanup path keeps working.
		if hasPrev && prevStatus == agentpkg.StatusIdle {
			projection, _ := m.projectionForSession(session)
			if projection == nil || projection.TopFrame == nil || isPidAliveFn(projection.TopFrame.PID) {
				return
			}
			// Stable Idle + dead PID: fall through to bottom capture for
			// shell-prompt classification + sweep cleanup.
		}
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

	// Test-only seam: simulate a concurrent stop/rename that mutates
	// activeWatchers between the early fast-path and the final critical
	// section. Production leaves interruptBeforeFinalLockFn nil (no-op).
	if hook := interruptBeforeFinalLockFn; hook != nil {
		hook(session)
	}

	// 1 (re-check) + 4. Error Guard + 5. Transition gate.
	// Single critical section; the re-check of activeWatchers closes the
	// race window where stopWatch/rename mutated the map between the early
	// unlock at step 1 and this lock (codex finding #4 regression).
	m.mu.Lock()
	currentAgent, active = m.activeWatchers[session]
	if !active || currentAgent != agentType {
		m.mu.Unlock()
		if isDevMode() {
			log.Printf("[probe] stale callback re-check race session=%s agent=%s kind=%s", session, agentType, ev.Kind)
		}
		return
	}
	if m.currentStatus[session] == agentpkg.StatusError {
		m.mu.Unlock()
		if isDevMode() {
			log.Printf("[probe] error guard suppress session=%s agent=%s kind=%s", session, agentType, ev.Kind)
		}
		return
	}
	if prev, ok := m.currentStatus[session]; ok && prev == status {
		m.mu.Unlock()
		if isDevMode() {
			log.Printf("[probe] transition dedup session=%s agent=%s status=%s kind=%s", session, agentType, status, ev.Kind)
		}
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
