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
//   - startWatch registers a callback bound to (session, agentType) using
//     caller-provided probe.WatchOptions, and increments MetricProbeWatchStarted
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

// startWatch starts a probe watcher with caller-provided WatchOptions.
//
// Caller passes session WITHOUT the ":" suffix; orchestrator appends it to
// match the existing tmux-target convention (R7 fix).
//
// W3 (this PR): orchestrator no longer owns a default profile or looks up
// per-agent profile data via the registry (the former `ProbeProfile` /
// `ProbeProfileProvider` types have been removed). Callers (e.g. W6
// ProbeIntent or future explicit start sites) supply opts directly. While
// Phase 1 keeps
// manageActivityWatch / renameSessionLocked as default no-ops, no production
// caller invokes startWatch yet — tests exercise it directly to pin the
// surface (FX4 invalid-opts, CC4 explicit start) until W6 lands.
//
// Returns true iff a watcher was successfully registered. False is returned
// when the orchestrator has no prober wired (R14 nil-prober guard) or when
// opts are invalid (TopLines + BottomLines mutually exclusive — probe.Watch
// would log + early-return without registering, leaving a silent dead watcher
// otherwise; codex finding #7 regression). Returning false also skips the
// started-metric increment so /debug/vars stays an honest counter of
// registered watchers.
func (o *probeOrchestrator) startWatch(session, agentType string, opts probe.WatchOptions) bool {
	pw := o.prober()
	if pw == nil {
		return false
	}
	target := session + ":"

	// Opts validation — must mirror probe.Watch's contract (TopLines +
	// BottomLines mutually exclusive). Validating here lets the caller roll
	// back any local state it set up; without this, probe.Watch silently
	// drops the call and the orchestrator is left with a "started" metric
	// + an active map entry but no goroutine.
	if opts.TopLines > 0 && opts.BottomLines > 0 {
		if isDevMode() {
			log.Printf("[probe] startWatch invalid opts session=%s agent=%s TopLines=%d BottomLines=%d — mutually exclusive; not registering",
				session, agentType, opts.TopLines, opts.BottomLines)
		}
		return false
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

// probeGuardArgs is the input contract for applyProbeGuards. Caller fills:
//   - Session / AgentType: identity used by the dev log + downstream broadcast
//   - Reason: rawEventName placed on the broadcast NormalizedEvent
//     (ScreenChange path: "probe:activity"; ProbeIntent path: e.g.
//     "probe-intent:process_dead")
//   - Signal: raw detector signal, passed verbatim to Mapping. ScreenChange
//     callers may leave this zero; ProbeIntent callers fill from the channel
//   - Mapping: per-route status mapping callback. Invoked AFTER the
//     stale-callback fast-path + graceWindow guard pass (per by2z79ouc DEF-1
//     contract). Returning "" drops the signal.
//   - StaleCheck: per-route active-set checker. Invoked WHILE m.mu is held by
//     applyProbeGuards (caller therefore reads from m.* maps lock-free).
//     Run twice — once on the early fast-path (cheap stale-drop for ghost
//     callbacks) and again inside the final critical section (closes the
//     race window where stop / rename / generation-bump won between the
//     early unlock and the final lock; codex finding #4 regression).
type probeGuardArgs struct {
	Session    string
	AgentType  string
	Reason     string
	Signal     agentpkg.Signal
	Mapping    func(agentpkg.Signal) agentpkg.Status
	StaleCheck func(*Module) bool
}

// applyProbeGuards runs the shared probe-status-transition pipeline:
//
//  1. Stale-callback fast-path: m.mu Lock → StaleCheck(m); reject if false.
//  2. graceWindow suppression: hook within probeGraceWindow → drop + counter.
//  3. Mapping callback: caller-provided Signal → Status; "" means drop.
//  4. Final critical section: m.mu Lock → StaleCheck re-check + ErrorGuard +
//     transition gate; mutate currentStatus on pass.
//  5. Broadcast: setProjectionTopStatus + buildProjectionNormalized +
//     broadcastToSession (with NormalizedEvent fallback when the projection
//     is unavailable).
//
// Returns applied=true iff step 4 mutated currentStatus and step 5 broadcast
// the transition. Every other branch returns applied=false.
//
// W6-3 P1-T2: extracted from interpretScreenEvent so the W6-3 ProbeIntent
// dispatcher (P1-T4) can reuse the same guard pipeline through different
// StaleCheck / Mapping strategies. ScreenChange path is a strict mechanical
// extraction — pre-extraction tests must remain green.
func applyProbeGuards(m *Module, args probeGuardArgs) (applied bool) {
	if m == nil {
		return false
	}

	// 1. Stale-callback guard (early fast-path; lock-free read inside m.mu).
	m.mu.Lock()
	staleOK := args.StaleCheck != nil && args.StaleCheck(m)
	m.mu.Unlock()
	if !staleOK {
		return false
	}

	// 2. graceWindow suppression (independent graceMu — no nesting w/ m.mu).
	if m.probeOrch != nil {
		o := m.probeOrch
		o.graceMu.Lock()
		last, hasHook := o.lastHookAt[args.Session]
		o.graceMu.Unlock()
		if hasHook && orchNowFn().Sub(last) < probeGraceWindow {
			agentpkg.MetricProbeGraceWindowSuppressed.Add(1)
			if isDevMode() {
				log.Printf("[probe] graceWindow suppress session=%s agent=%s reason=%s",
					args.Session, args.AgentType, args.Reason)
			}
			return false
		}
	}

	// 3. Mapping callback (per by2z79ouc DEF-1 — invoked only after stale +
	//    graceWindow guards pass; OnSignal handlers may safely log/count).
	if args.Mapping == nil {
		return false
	}
	newStatus := args.Mapping(args.Signal)
	if newStatus == "" {
		return false
	}

	// Test-only seam: simulate a concurrent stop/rename that mutates the
	// active-set between the early fast-path and the final critical section.
	// Production leaves interruptBeforeFinalLockFn nil (no-op).
	if hook := interruptBeforeFinalLockFn; hook != nil {
		hook(args.Session)
	}

	// 4. Final critical section: atomic re-check + ErrorGuard + transition gate.
	m.mu.Lock()
	if args.StaleCheck == nil || !args.StaleCheck(m) {
		m.mu.Unlock()
		return false
	}
	if m.currentStatus[args.Session] == agentpkg.StatusError {
		m.mu.Unlock()
		return false
	}
	if prev, ok := m.currentStatus[args.Session]; ok && prev == newStatus {
		m.mu.Unlock()
		return false
	}
	m.currentStatus[args.Session] = newStatus
	m.mu.Unlock()

	// 5. Broadcast.
	agentpkg.MetricProbeScreenEvent.Add(1)
	if isDevMode() {
		log.Printf("[probe] status session=%s agent=%s status=%s reason=%s",
			args.Session, args.AgentType, newStatus, args.Reason)
	}
	if projection, err := m.setProjectionTopStatus(args.Session, newStatus); err == nil && projection != nil {
		normalized := buildProjectionNormalized(projection, args.AgentType, args.Reason, time.Now().UnixNano(), agentpkg.DeriveResult{})
		m.broadcastToSession(args.Session, normalized)
		return true
	}
	// Fallback when the projection is unavailable (e.g. frames row removed
	// concurrently with the event). Broadcast a minimal normalized event so
	// SPA clients still see the status change.
	normalized := agentpkg.NormalizedEvent{
		AgentType:    args.AgentType,
		Status:       string(newStatus),
		RawEventName: args.Reason,
		BroadcastTs:  time.Now().UnixNano(),
	}
	m.broadcastToSession(args.Session, normalized)
	return true
}

// interpretScreenEvent maps a raw probe.ScreenChangeEvent to a status
// transition via applyProbeGuards. The Kind→Status mapping (including the
// ScreenStable cheap pre-gate, the independent bottom capture, and the
// dead-PID + shell-prompt sweep branch) stays in this caller layer because
// the sweep branch may early-return without invoking the dispatcher.
//
// Guard layering — each layer encodes a specific invariant:
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

	// Read prevStatus for the cheap ScreenStable pre-gate (codex finding #8).
	// This is an upfront read independent of the guard pipeline; it does not
	// substitute for the StaleCheck closure (which still runs inside
	// applyProbeGuards under m.mu in steps 1 and 4).
	m.mu.Lock()
	prevStatus, hasPrev := m.currentStatus[session]
	m.mu.Unlock()

	// Step 3 mapping (Kind → status), kept in the caller layer because the
	// ScreenStable + dead-PID + shell-prompt branch must early-return on
	// sweep without entering the dispatcher.
	var status agentpkg.Status
	switch ev.Kind {
	case probe.ScreenChanged:
		status = agentpkg.StatusRunning
	case probe.ScreenStable:
		// Cheap pre-gate: stable-Idle + alive top-frame PID is a no-op.
		if hasPrev && prevStatus == agentpkg.StatusIdle {
			projection, _ := m.projectionForSession(session)
			if projection == nil || projection.TopFrame == nil || isPidAliveFn(projection.TopFrame.PID) {
				return
			}
			// Stable Idle + dead PID: fall through to bottom capture for
			// shell-prompt classification + sweep cleanup.
		}
		// Independent bottom capture for shell-prompt classification.
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

	// Steps 1 + 2 + 4 + 5 + 6 — delegate to the shared guard pipeline. The
	// StaleCheck closure encodes the ScreenChange-path active-set check
	// (activeWatchers[session] == agentType); the Mapping closure returns
	// the already-resolved status (mapping happened above so the sweep
	// branch could short-circuit).
	applyProbeGuards(m, probeGuardArgs{
		Session:   session,
		AgentType: agentType,
		Reason:    "probe:activity",
		Mapping:   func(agentpkg.Signal) agentpkg.Status { return status },
		StaleCheck: func(m *Module) bool {
			currentAgent, active := m.activeWatchers[session]
			return active && currentAgent == agentType
		},
	})
}
