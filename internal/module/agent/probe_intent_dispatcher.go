package agent

import (
	"context"
	"log"
	"slices"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// probeIntentOnDrop is the OnDrop callback installed on probeGuardArgs by
// the dispatcher's consumeSignals path. It maps the canonical drop-reason
// string (per applyProbeGuards contract) to the matching expvar counter
// + dev log line. ScreenChange callers leave probeGuardArgs.OnDrop nil so
// the legacy MetricProbeGraceWindowSuppressed semantics stay isolated.
//
// reason values are stable strings emitted by applyProbeGuards:
// "stale-callback" / "grace" / "error-guard" / "transition-gate". Anything
// else is logged but not counted (defensive landing for future drop
// branches that may add reasons before this map is updated).
func probeIntentOnDrop(reason string) {
	switch reason {
	case "stale-callback":
		agentpkg.MetricProbeIntentDroppedStale.Add(1)
	case "grace":
		agentpkg.MetricProbeIntentDroppedGrace.Add(1)
	case "error-guard":
		agentpkg.MetricProbeIntentDroppedErrorGuard.Add(1)
	case "transition-gate":
		agentpkg.MetricProbeIntentDroppedTransitionGate.Add(1)
	}
	if isDevMode() {
		log.Printf("[probe-intent] drop reason=%s", reason)
	}
}

// probeIntentOnDropForSession returns an OnDrop callback that prefixes the
// drop log with session + kind context so dev-mode tail can correlate the
// drop with the originating dispatcher path. Counter increments mirror
// probeIntentOnDrop. The closure variant exists because applyProbeGuards
// invokes OnDrop without re-supplying session/kind context.
func probeIntentOnDropForSession(session string, kind agentpkg.ProbeIntentKind) func(string) {
	return func(reason string) {
		switch reason {
		case "stale-callback":
			agentpkg.MetricProbeIntentDroppedStale.Add(1)
		case "grace":
			agentpkg.MetricProbeIntentDroppedGrace.Add(1)
		case "error-guard":
			agentpkg.MetricProbeIntentDroppedErrorGuard.Add(1)
		case "transition-gate":
			agentpkg.MetricProbeIntentDroppedTransitionGate.Add(1)
		}
		if isDevMode() {
			log.Printf("[probe-intent] drop session=%s kind=%s reason=%s",
				session, kind, reason)
		}
	}
}

// probeIntentDispatcher routes ProbeIntent lifecycle events from
// manageActivityWatch / replayStatus into per-(session, kind) detector
// goroutines and then funnels the detector signals back through
// applyProbeGuards.
//
// Lock ownership (per spec §5.1 / by2z79ouc ATK-1): all active-set state
// (m.activeProbeIntents + m.probeIntentGen) lives under m.mu. The dispatcher
// holds no mutex of its own — every state mutation routes through m.mu.
// Detector goroutines + consumeSignals goroutines run lock-free; they
// re-acquire m.mu through applyProbeGuards' StaleCheck closure when they
// need to broadcast.
//
// Per spec §5.4 / round 5 P1: applyIntentLifecycle is the single helper
// that decides start / stop / re-arm; callers (applyStatus,
// reconcileSessionActive, consumeSignals teardown) all funnel through it
// so the lifecycle decision is atomic with the active-set read under m.mu.
type probeIntentDispatcher struct {
	parent    *Module
	parentCtx context.Context

	// startDetector routes (kind, paneID, senderPID) → detector goroutine.
	// Per-dispatcher field (not a package var) so concurrent tests using
	// independent Modules don't race on a global swap during cleanup.
	// Default points to defaultStartProbeIntentDetector; P2-T4 wires a
	// codex-routed implementation in newProbeIntentDispatcher / Init.
	startDetector probeIntentDetectorStarter

	// supportedKinds is the registry of ProbeIntent kinds that have
	// production detector wiring. Lifecycle pre-arm checks membership and
	// fails closed (skip arm + emit observability) for unwired kinds — per
	// audit F6: a provider declaring a new kind without a paired Module.New
	// switch case must surface as a runtime signal rather than silently
	// appearing armed with a noop default detector. nil map = no kinds
	// supported (test default for empty Module unless the test populates).
	supportedKinds map[agentpkg.ProbeIntentKind]struct{}
}

// probeIntentDetectorStarter is the contract for ProbeIntent detector
// goroutines. Implementations MUST honor ctx (return on Done) and MUST
// NOT close the supplied out channel — the dispatcher owns the channel
// lifetime. Implementations MAY emit zero or more Signal values before
// returning.
type probeIntentDetectorStarter func(
	ctx context.Context,
	m *Module,
	kind agentpkg.ProbeIntentKind,
	paneID string,
	senderPID int,
	out chan<- agentpkg.Signal,
)

// activeIntent records one (session, kind) detector arming. Stored in
// m.activeProbeIntents under m.mu. agentType + paneID + senderPID +
// generation form the dedup tuple checked against fresh hook input.
type activeIntent struct {
	agentType  string
	paneID     string
	senderPID  int
	cancel     context.CancelFunc
	generation uint64
}

// lifecyclePlan records the work that must happen OUTSIDE m.mu after
// applyIntentLifecycle finishes its critical section. Cancelling old
// detectors and starting new goroutines is intentionally deferred so
// applyIntentLifecycle never blocks on goroutine schedule while holding
// m.mu (avoids long lock holds during detector startup).
//
// stopOld carries the full observability context for the case-3 / case-5
// teardown so emitStopObservability can run after Unlock without
// re-reading map state.
type lifecyclePlan struct {
	stopOld         *stopMeta
	startCtx        context.Context
	paneID          string
	senderPID       int
	generation      uint64
	unsupportedKind bool
	kind            agentpkg.ProbeIntentKind
}

// stopMeta carries the metadata needed to cancel + emit observability for
// a stopped active intent. Returned by stopActiveIntentInLock so callers
// can defer the cancel + emit work until after they release m.mu.
//
// Per audit F5/F7: every cancel path (lifecycle case 3/5, reconcile,
// stopAll, consumeSignals teardown) MUST emit metric + dev log + trace.
// Centralizing the emission contract through stopMeta + emitStopObservability
// eliminates the previously-distributed observability gaps.
type stopMeta struct {
	cancel     context.CancelFunc
	agentType  string
	kind       agentpkg.ProbeIntentKind
	paneID     string
	generation uint64
	reason     string
}

// newProbeIntentDispatcher constructs a dispatcher tied to the given
// Module. parentCtx is initialized to context.Background; callers may
// rotate it via setParentCtx for tests or future Stop()-driven cancel
// roots. Per W6-3 P1-T4 contract.
func newProbeIntentDispatcher(parent *Module) *probeIntentDispatcher {
	return &probeIntentDispatcher{
		parent:        parent,
		parentCtx:     context.Background(),
		startDetector: defaultStartProbeIntentDetector,
	}
}

// setParentCtx swaps the root context that future detector goroutines
// inherit from. Existing armed detectors keep their original ctx (their
// cancel() is recorded in activeIntent). Test seam — production call
// sites do not rotate parentCtx.
func (d *probeIntentDispatcher) setParentCtx(ctx context.Context) {
	d.parentCtx = ctx
}

// applyStatus is the single entry point for ProbeIntent lifecycle
// transitions. Called from:
//
//   - manageActivityWatch (P1-T5): live hook path, when a hook flips
//     currentStatus.
//   - replayStatus (P1-T6): daemon-restart hydrate.
//   - consumeSignals teardown (round 5 P1): probe-applied transitions
//     re-run lifecycle so the active entry teardown path stays unified.
//
// Per spec §5.4: reconcile session-wide active-set against the new
// (agentType, declaredKinds) tuple BEFORE running the per-intent
// lifecycle so cross-provider switches don't leave stranded entries.
func (d *probeIntentDispatcher) applyStatus(session, agentType string, newStatus agentpkg.Status) {
	provider, ok := d.parent.registry.Get(agentType)
	if !ok {
		// Unknown agent → reconcile clears every active entry for the
		// session (declaredKinds=nil sentinel) and returns. Per round 4 P1:
		// without this, switching session top agent away from codex would
		// leave codex active entries stranded with their detectors still
		// polling.
		d.reconcileSessionActive(session, agentType, nil)
		return
	}
	intents := probeIntentsOf(provider)

	declaredKinds := make(map[agentpkg.ProbeIntentKind]struct{}, len(intents))
	for _, intent := range intents {
		declaredKinds[intent.Kind] = struct{}{}
	}
	d.reconcileSessionActive(session, agentType, declaredKinds)

	for _, intent := range intents {
		d.applyIntentLifecycle(session, agentType, newStatus, intent)
	}
}

// stopActiveIntentInLock removes activeProbeIntents[session][kind] when
// either expectGeneration==0 (caller doesn't care which generation) OR
// cur.generation matches expectGeneration. Returns ok=true with full
// stopMeta only when an entry was removed.
//
// CALLER MUST hold m.mu. The returned cancel ctx is invoked AFTER Unlock
// (followed by emitStopObservability) so detector goroutines that
// re-acquire m.mu during shutdown do not deadlock.
//
// Generation guard (audit F2): consumeSignals teardown after applied=true
// passes its emitting generation; if a concurrent rearm has already
// installed gen N+1, the mismatch causes ok=false and the newer detector
// stays intact.
func (d *probeIntentDispatcher) stopActiveIntentInLock(
	session string,
	kind agentpkg.ProbeIntentKind,
	expectGeneration uint64,
	reason string,
) (stopMeta, bool) {
	perSession := d.parent.activeProbeIntents[session]
	cur, ok := perSession[kind]
	if !ok {
		return stopMeta{}, false
	}
	if expectGeneration != 0 && cur.generation != expectGeneration {
		return stopMeta{}, false
	}
	delete(perSession, kind)
	if len(perSession) == 0 {
		delete(d.parent.activeProbeIntents, session)
	}
	return stopMeta{
		cancel:     cur.cancel,
		agentType:  cur.agentType,
		kind:       kind,
		paneID:     cur.paneID,
		generation: cur.generation,
		reason:     reason,
	}, true
}

// emitStopObservability records counter + dev log + trace for an intent
// teardown. Per audit F5/F7: every stop path must emit these three
// surfaces — single helper enforces the contract.
func (d *probeIntentDispatcher) emitStopObservability(session string, meta stopMeta) {
	agentpkg.MetricProbeIntentStopped.Add(1)
	if isDevMode() {
		log.Printf("[probe-intent] stop session=%s agent=%s kind=%s generation=%d reason=%s",
			session, meta.agentType, meta.kind, meta.generation, meta.reason)
	}
	if d.parent.traceSink != nil {
		d.parent.traceSink.AppendProbeIntent(probeIntentTraceArgs{
			TmuxSession: session,
			PaneID:      meta.paneID,
			AgentType:   meta.agentType,
			Kind:        string(meta.kind),
			Decision:    "stop",
			Reason:      meta.reason,
			Payload: map[string]any{
				"generation": meta.generation,
			},
		})
	}
}

// reconcileSessionActive cancels + deletes active entries that no longer
// apply to the (newAgentType, declaredKinds) pair. Called from applyStatus
// before the per-intent lifecycle loop.
//
// declaredKinds=nil is the sentinel for "drop everything" (unknown agent
// or provider has no ProbeIntents); otherwise an entry survives only when
// its agentType matches AND its kind is in declaredKinds.
//
// Per round 4 P1 (spec §5.4): stranded detectors still pass
// makeProbeIntentStaleCheck because their entry is still in the active
// map; this reconcile is the only place that drops the entry on cross-
// provider transitions.
func (d *probeIntentDispatcher) reconcileSessionActive(
	session string,
	newAgentType string,
	declaredKinds map[agentpkg.ProbeIntentKind]struct{},
) {
	var stops []stopMeta

	d.parent.mu.Lock()
	perSession := d.parent.activeProbeIntents[session]
	if perSession != nil {
		var staleKinds []agentpkg.ProbeIntentKind
		for kind, cur := range perSession {
			stale := declaredKinds == nil ||
				cur.agentType != newAgentType
			if !stale {
				if _, ok := declaredKinds[kind]; !ok {
					stale = true
				}
			}
			if stale {
				staleKinds = append(staleKinds, kind)
			}
		}
		// Map mutation outside the iterator to avoid Go's "for range over
		// map being mutated" undefined ordering on subsequent iterations.
		for _, kind := range staleKinds {
			meta, ok := d.stopActiveIntentInLock(session, kind, 0, "reconcile-cross-provider")
			if ok {
				stops = append(stops, meta)
			}
		}
	}
	d.parent.mu.Unlock()

	for _, meta := range stops {
		meta.cancel()
		d.emitStopObservability(session, meta)
	}
}

// applyIntentLifecycle decides whether to start / stop / re-arm the
// detector for (session, intent.Kind) based on:
//
//   - shouldActive: newStatus ∈ intent.OnEntryStatus
//   - wasActive: m.activeProbeIntents[session][intent.Kind] is set
//   - target match: existing entry's (agentType, paneID, senderPID) ==
//     fresh top-frame (paneID, senderPID) for this agentType
//
// The five cases (per spec §5.4 + round 3 P1):
//
//  1. !shouldActive && !wasActive → noop
//  2. !shouldActive &&  wasActive → cancel + delete
//  3.  shouldActive && !wasActive (frame ok) → record + arm
//  4.  shouldActive &&  wasActive (target match) → noop
//  5.  shouldActive &&  wasActive (target mismatch) → cancel + record + arm
//
// Edge cases (per spec §5.4 lines 717-739):
//
//   - shouldActive but currentStatus has drifted between snapshot and
//     this call (by2z79ouc ATK-2 / replay race): if wasActive, tear down;
//     don't arm.
//   - shouldActive but lookupTopFrameForSessionLocked misses: if
//     wasActive, tear down (target unverifiable); don't arm.
//
// Per spec §5.4: lifecycle decision runs entirely inside one m.mu
// critical section; cancel + start work runs after Unlock.
func (d *probeIntentDispatcher) applyIntentLifecycle(
	session, agentType string,
	newStatus agentpkg.Status,
	intent agentpkg.ProbeIntent,
) {
	shouldActive := slices.Contains(intent.OnEntryStatus, newStatus)

	d.parent.mu.Lock()
	var plan lifecyclePlan
	perSession := d.parent.activeProbeIntents[session]
	cur, wasActive := perSession[intent.Kind]

	switch {
	case !shouldActive && !wasActive:
		// case 1: noop
	case !shouldActive && wasActive:
		// case 3: stop only
		if meta, ok := d.stopActiveIntentInLock(session, intent.Kind, 0, "lifecycle-not-shouldActive"); ok {
			plan.stopOld = &meta
		}
	case shouldActive:
		// case 2 / 4 / 5: lookup live currentStatus + top frame.
		// Per spec §5.4 by2z79ouc ATK-2: status may have changed between
		// the snapshot taken by caller and this lock; re-validate against
		// live currentStatus inside m.mu.
		curStatus, hasStatus := d.parent.currentStatus[session]
		if !hasStatus || !slices.Contains(intent.OnEntryStatus, curStatus) {
			if wasActive {
				if meta, ok := d.stopActiveIntentInLock(session, intent.Kind, 0, "lifecycle-status-drift"); ok {
					plan.stopOld = &meta
				}
			}
			break
		}
		paneID, senderPID, hasFrame := d.parent.lookupTopFrameForSessionLocked(session)
		if !hasFrame || paneID == "" || senderPID == 0 {
			if wasActive {
				if meta, ok := d.stopActiveIntentInLock(session, intent.Kind, 0, "lifecycle-frame-miss"); ok {
					plan.stopOld = &meta
				}
			}
			break
		}
		targetMatches := wasActive &&
			cur.agentType == agentType &&
			cur.paneID == paneID &&
			cur.senderPID == senderPID
		if wasActive && targetMatches {
			// case 4: already armed correctly
			break
		}
		// case 2 (!wasActive) or case 5 (target mismatch).
		// F6 fail-closed: refuse to arm an unsupported kind. A future
		// provider might declare a new ProbeIntentKind without the matching
		// Module.New switch case; emit observability + skip arm rather
		// than silently parking on the noop default detector.
		if _, supported := d.supportedKinds[intent.Kind]; !supported {
			plan.unsupportedKind = true
			if wasActive {
				if meta, ok := d.stopActiveIntentInLock(session, intent.Kind, 0, "lifecycle-unsupported-kind"); ok {
					plan.stopOld = &meta
				}
			}
			break
		}
		if wasActive {
			if meta, ok := d.stopActiveIntentInLock(session, intent.Kind, 0, "lifecycle-target-mismatch"); ok {
				plan.stopOld = &meta
			}
		}
		generation := d.parent.nextProbeIntentGeneration()
		ctx, cancel := context.WithCancel(d.parentCtx)
		perSession = d.parent.activeProbeIntents[session]
		if perSession == nil {
			perSession = make(map[agentpkg.ProbeIntentKind]activeIntent)
			d.parent.activeProbeIntents[session] = perSession
		}
		perSession[intent.Kind] = activeIntent{
			agentType:  agentType,
			paneID:     paneID,
			senderPID:  senderPID,
			cancel:     cancel,
			generation: generation,
		}
		plan.startCtx = ctx
		plan.paneID = paneID
		plan.senderPID = senderPID
		plan.generation = generation
	}
	d.parent.mu.Unlock()

	// Execute plan outside m.mu.
	if plan.stopOld != nil {
		plan.stopOld.cancel()
		d.emitStopObservability(session, *plan.stopOld)
	}
	if plan.unsupportedKind {
		agentpkg.MetricProbeIntentUnsupportedKind.Add(1)
		if isDevMode() {
			log.Printf("[probe-intent] unsupported_kind session=%s agent=%s kind=%s reason=skip-arm",
				session, agentType, intent.Kind)
		}
		if d.parent.traceSink != nil {
			d.parent.traceSink.AppendProbeIntent(probeIntentTraceArgs{
				TmuxSession: session,
				AgentType:   agentType,
				Kind:        string(intent.Kind),
				Decision:    "drop",
				Reason:      "unsupported-kind",
			})
		}
	}
	if plan.startCtx != nil {
		out := make(chan agentpkg.Signal, 1)
		go func() {
			d.startDetector(plan.startCtx, d.parent, intent.Kind, plan.paneID, plan.senderPID, out)
			close(out)
		}()
		go d.consumeSignals(plan.startCtx, session, agentType, intent, plan.generation, out)
		agentpkg.MetricProbeIntentStarted.Add(1)
		if isDevMode() {
			log.Printf("[probe-intent] start session=%s agent=%s kind=%s pane=%s pid=%d generation=%d",
				session, agentType, intent.Kind, plan.paneID, plan.senderPID, plan.generation)
		}
		d.parent.traceSink.AppendProbeIntent(probeIntentTraceArgs{
			TmuxSession: session,
			PaneID:      plan.paneID,
			AgentType:   agentType,
			Kind:        string(intent.Kind),
			Decision:    "start",
			Reason:      "lifecycle-applyStatus",
			Payload: map[string]any{
				"pane_id":    plan.paneID,
				"sender_pid": plan.senderPID,
				"generation": plan.generation,
			},
		})
	}
}

// consumeSignals reads detector emissions, runs them through the shared
// applyProbeGuards pipeline, and tears down the active entry on a
// successful probe-applied transition.
//
// Per round 5 P1 (spec §5.4 lines 805-815): a probe that successfully
// flips status to error/clear means OnEntryStatus no longer holds; the
// detector goroutine has already exited (or is about to via ctx
// cancel from the upstream re-armed lifecycle), but the active entry
// would otherwise leak. Re-running applyStatus with the freshly applied
// status walks lifecycle case 3 (active && !shouldActive) → cancel +
// delete.
func (d *probeIntentDispatcher) consumeSignals(
	_ context.Context,
	session, agentType string,
	intent agentpkg.ProbeIntent,
	generation uint64,
	in <-chan agentpkg.Signal,
) {
	appliedAny := false
	for sig := range in {
		agentpkg.MetricProbeIntentSignalEmitted.Add(1)
		applied, appliedStatus := applyProbeGuards(d.parent, probeGuardArgs{
			Session:    session,
			AgentType:  agentType,
			Reason:     "probe-intent:" + string(intent.Kind),
			Signal:     sig,
			Mapping:    intent.OnSignal,
			StaleCheck: makeProbeIntentStaleCheck(session, intent.Kind, agentType, generation),
			OnDrop:     probeIntentOnDropForSession(session, intent.Kind),
		})
		if !applied {
			continue
		}
		appliedAny = true
		// Per spec §5.4 round 5 P1: applied=true means OnEntryStatus no
		// longer holds (currentStatus already flipped to error/clear inside
		// applyProbeGuards step 4). Tear down the active entry through the
		// generation-scoped helper so a concurrent rearm (gen N+1 from a
		// different hook) survives our cleanup — audit F2.
		//
		// Ordering: emit signal observability (counter / log / trace) BEFORE
		// teardown so test waitFor predicates keying off active-entry
		// presence see the signal log as a happens-before guarantee rather
		// than a race against the teardown's stop log.
		//
		// appliedStatus comes from applyProbeGuards' return (P2-T6): a
		// re-read of currentStatus[session] races with concurrent SessionEnd
		// hooks that may delete the entry before we acquire m.mu.
		agentpkg.MetricProbeIntentApplied.Add(1)
		if isDevMode() {
			log.Printf("[probe-intent] signal session=%s kind=%s PaneAlive=%v newStatus=%s applied=true",
				session, intent.Kind, sig.PaneAlive, appliedStatus)
		}
		if d.parent.traceSink != nil {
			d.parent.traceSink.AppendProbeIntent(probeIntentTraceArgs{
				TmuxSession: session,
				PaneID:      sig.PaneID,
				AgentType:   agentType,
				Kind:        string(intent.Kind),
				Decision:    "signal",
				Reason:      "applied",
				Payload: map[string]any{
					"pane_alive": sig.PaneAlive,
					"sender_pid": sig.SenderPID,
					"new_status": string(appliedStatus),
					"generation": generation,
				},
			})
		}
		// F2 fix: generation-scoped teardown. If a concurrent hook rearmed
		// gen N+1 between detector emit and now, stopActiveIntentInLock
		// returns ok=false (cur.generation != generation) and the newer
		// detector is preserved. Previously we re-ran applyStatus(error)
		// which routed through case-3 cleanup and cancelled whatever entry
		// was present — including the newer rearmed gen — leaving the
		// session running but undetected.
		d.parent.mu.Lock()
		teardown, ok := d.stopActiveIntentInLock(session, intent.Kind, generation, "applied:"+string(appliedStatus))
		d.parent.mu.Unlock()
		if ok {
			teardown.cancel()
			d.emitStopObservability(session, teardown)
		}
	}
	// F1 fix: detector exited (one-shot post-emit) but the loop exited
	// without an applied signal — graceWindow drop, transition gate, or
	// other guard rejected the only emission. Without this teardown the
	// active entry stays around with no live detector; case-4 (target
	// match) would short-circuit the next status change → re-arm never
	// fires → codex stays Running indefinitely after a missed crash.
	// Generation-scoped so a concurrent rearm survives.
	if !appliedAny {
		d.parent.mu.Lock()
		teardown, ok := d.stopActiveIntentInLock(session, intent.Kind, generation, "detector-exited-no-effect")
		d.parent.mu.Unlock()
		if ok {
			teardown.cancel()
			d.emitStopObservability(session, teardown)
		}
	}
}

// makeProbeIntentStaleCheck returns the StaleCheck closure used by
// applyProbeGuards on the ProbeIntent path. Per spec §5.4: re-checks
// active-entry presence + agentType + generation. Caller (applyProbeGuards)
// invokes this while holding m.mu, so the read is lock-free.
//
// generation guard: detector goroutine that was cancelled then a new
// detector armed (case 5 / replay race) — old detector's queued signal
// still in-flight will hit a different generation in the active map and
// drop. Two-tier dedup with agentType (cross-provider switch) +
// generation (same provider re-arm).
func makeProbeIntentStaleCheck(
	session string,
	kind agentpkg.ProbeIntentKind,
	agentType string,
	generation uint64,
) func(*Module) bool {
	return func(m *Module) bool {
		intents, ok := m.activeProbeIntents[session]
		if !ok {
			return false
		}
		cur, ok := intents[kind]
		if !ok {
			return false
		}
		return cur.agentType == agentType && cur.generation == generation
	}
}

// probeIntentsOf returns the provider's declared ProbeIntents, or nil
// when the provider does not implement ProbeIntentProvider. Per spec
// §3.1: implementing the interface is optional.
func probeIntentsOf(p agentpkg.AgentProvider) []agentpkg.ProbeIntent {
	if pip, ok := p.(agentpkg.ProbeIntentProvider); ok {
		return pip.ProbeIntents()
	}
	return nil
}

// replayStatus walks every session that has a currentStatus entry and
// re-runs applyStatus so ProbeIntent detectors arm against post-replay
// state. Called from Module.Start after replayFromDB + startSweep so a
// daemon restart inherits the same gating behavior as a live status
// transition.
//
// Per spec §6.3 / §6.4: snapshot is taken under m.mu via
// snapshotStatuses; for each session, applyStatus runs outside m.mu.
// applyIntentLifecycle re-reads currentStatus + lookupTopFrameForSessionLocked
// inside its own m.mu critical section, so any hook arriving between
// snapshot and lifecycle observes consistent state — race fully closed.
//
// Per spec §6.4 stale-frame race: if the snapshot says Running but the
// top frame.pid is already dead (codex crashed before daemon restart),
// the production codex detector polls and emits a Signal on its first
// IsPidAlive call → applyProbeGuards broadcasts error → consumeSignals
// re-runs applyStatus(error) which tears down the entry. This is the
// canonical issue #698 fix: daemon restart correctly discovers the
// missing process and flips lights without waiting for the next hook.
func (d *probeIntentDispatcher) replayStatus() {
	snapshot := d.parent.snapshotStatuses()
	for session, entry := range snapshot {
		d.applyStatus(session, entry.agentType, entry.status)
	}
}

// stopAll cancels every active ProbeIntent detector and clears the map.
// Called from Module.Stop. Locks m.mu so no concurrent applyStatus can
// observe a partial map.
//
// CancelFuncs are collected under m.mu and invoked outside the lock so
// detector goroutines that may briefly attempt re-acquisition through
// applyProbeGuards do not deadlock.
func (d *probeIntentDispatcher) stopAll() {
	type stopAllRecord struct {
		session string
		meta    stopMeta
	}
	var stops []stopAllRecord

	d.parent.mu.Lock()
	for session, perSession := range d.parent.activeProbeIntents {
		for kind, cur := range perSession {
			stops = append(stops, stopAllRecord{
				session: session,
				meta: stopMeta{
					cancel:     cur.cancel,
					agentType:  cur.agentType,
					kind:       kind,
					paneID:     cur.paneID,
					generation: cur.generation,
					reason:     "module-stop",
				},
			})
		}
	}
	d.parent.activeProbeIntents = make(map[string]map[agentpkg.ProbeIntentKind]activeIntent)
	d.parent.mu.Unlock()

	for _, rec := range stops {
		rec.meta.cancel()
		d.emitStopObservability(rec.session, rec.meta)
	}
}

// defaultStartProbeIntentDetector is the P1-T4 stub: blocks on ctx and
// never emits. Existence of the goroutine still proves dispatcher
// lifecycle (ctx cancel triggers exit + close(out) by the caller). P2-T4
// supplies a codex-routed starter via probeIntentDispatcher.startDetector
// instead of a package-global swap, so concurrent dispatcher tests stay
// isolated.
func defaultStartProbeIntentDetector(
	ctx context.Context,
	_ *Module,
	_ agentpkg.ProbeIntentKind,
	_ string,
	_ int,
	_ chan<- agentpkg.Signal,
) {
	<-ctx.Done()
}

