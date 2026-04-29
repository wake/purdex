package agent

import (
	"context"
	"slices"

	agentpkg "github.com/wake/purdex/internal/agent"
)

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
type lifecyclePlan struct {
	cancelOld  context.CancelFunc
	startCtx   context.Context
	paneID     string
	senderPID  int
	generation uint64
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
	var toCancel []context.CancelFunc

	d.parent.mu.Lock()
	perSession := d.parent.activeProbeIntents[session]
	if perSession != nil {
		for kind, cur := range perSession {
			stale := declaredKinds == nil ||
				cur.agentType != newAgentType
			if !stale {
				if _, ok := declaredKinds[kind]; !ok {
					stale = true
				}
			}
			if stale {
				toCancel = append(toCancel, cur.cancel)
				delete(perSession, kind)
			}
		}
		if len(perSession) == 0 {
			delete(d.parent.activeProbeIntents, session)
		}
	}
	d.parent.mu.Unlock()

	for _, cancel := range toCancel {
		cancel()
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
		plan.cancelOld = cur.cancel
		delete(perSession, intent.Kind)
		if len(perSession) == 0 {
			delete(d.parent.activeProbeIntents, session)
		}
	case shouldActive:
		// case 2 / 4 / 5: lookup live currentStatus + top frame.
		// Per spec §5.4 by2z79ouc ATK-2: status may have changed between
		// the snapshot taken by caller and this lock; re-validate against
		// live currentStatus inside m.mu.
		curStatus, hasStatus := d.parent.currentStatus[session]
		if !hasStatus || !slices.Contains(intent.OnEntryStatus, curStatus) {
			if wasActive {
				plan.cancelOld = cur.cancel
				delete(perSession, intent.Kind)
				if len(perSession) == 0 {
					delete(d.parent.activeProbeIntents, session)
				}
			}
			break
		}
		paneID, senderPID, hasFrame := d.parent.lookupTopFrameForSessionLocked(session)
		if !hasFrame || paneID == "" || senderPID == 0 {
			if wasActive {
				plan.cancelOld = cur.cancel
				delete(perSession, intent.Kind)
				if len(perSession) == 0 {
					delete(d.parent.activeProbeIntents, session)
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
		// case 2 (!wasActive) or case 5 (target mismatch)
		if wasActive {
			plan.cancelOld = cur.cancel
		}
		generation := d.parent.nextProbeIntentGeneration()
		ctx, cancel := context.WithCancel(d.parentCtx)
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
	if plan.cancelOld != nil {
		plan.cancelOld()
	}
	if plan.startCtx != nil {
		out := make(chan agentpkg.Signal, 1)
		go func() {
			d.startDetector(plan.startCtx, d.parent, intent.Kind, plan.paneID, plan.senderPID, out)
			close(out)
		}()
		go d.consumeSignals(plan.startCtx, session, agentType, intent, plan.generation, out)
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
	for sig := range in {
		applied := applyProbeGuards(d.parent, probeGuardArgs{
			Session:    session,
			AgentType:  agentType,
			Reason:     "probe-intent:" + string(intent.Kind),
			Signal:     sig,
			Mapping:    intent.OnSignal,
			StaleCheck: makeProbeIntentStaleCheck(session, intent.Kind, agentType, generation),
		})
		if !applied {
			continue
		}
		d.parent.mu.Lock()
		appliedStatus := d.parent.currentStatus[session]
		d.parent.mu.Unlock()
		d.applyStatus(session, agentType, appliedStatus)
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

// stopAll cancels every active ProbeIntent detector and clears the map.
// Called from Module.Stop. Locks m.mu so no concurrent applyStatus can
// observe a partial map.
//
// CancelFuncs are collected under m.mu and invoked outside the lock so
// detector goroutines that may briefly attempt re-acquisition through
// applyProbeGuards do not deadlock.
func (d *probeIntentDispatcher) stopAll() {
	var toCancel []context.CancelFunc

	d.parent.mu.Lock()
	for _, perSession := range d.parent.activeProbeIntents {
		for _, cur := range perSession {
			toCancel = append(toCancel, cur.cancel)
		}
	}
	d.parent.activeProbeIntents = make(map[string]map[agentpkg.ProbeIntentKind]activeIntent)
	d.parent.mu.Unlock()

	for _, cancel := range toCancel {
		cancel()
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

