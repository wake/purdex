package arbitrator

import (
	"time"

	"github.com/wake/purdex/internal/module/agent/arbmode"
	"github.com/wake/purdex/internal/module/agent/observation"
)

// actionSessionStart is the canonical Action string a hook Observation uses
// to signal the start of a new agent session. Only hooks may carry this
// Action — Task 6's generation gate rejects any non-hook source that tries
// to advance the generation via this Action.
const actionSessionStart = "SessionStart"

// reasonSourcePriorityLower is the ReasonCode emitted when an incoming
// Observation comes from a lower-priority source than the last accepted one
// for the same actor (spec §3.4.1 #5). It is package-local because Task 3's
// types.go does not define a sentinel for this case and the code strings
// themselves are the stable contract.
const reasonSourcePriorityLower = "SourcePriorityLower"

// reasonSessionGenerationAdvanced is emitted on the synthetic boundary trace
// when applySessionStart successfully advances a session's generation.
const actionSessionGenerationAdvanced = "session.generation_advanced"

// endReasonReplacedByNewPrimary is the canonical EndReason for synthetic
// observations that end a displaced primary actor (spec §3.4.1 #6 example).
const endReasonReplacedByNewPrimary = "replaced_by_new_primary"

// ArbmodeView is the narrow subset of arbmode.Manager that the apply
// pipeline needs. Task 8 wires *arbmode.Manager directly; tests supply a
// fake that records ApplyAtSessionStart calls.
type ArbmodeView interface {
	// Snapshot returns the current + pending mode + env-lock flag. apply
	// consults Current to decide passthrough vs authoritative branching.
	Snapshot() arbmode.Snapshot
	// ApplyAtSessionStart promotes the most recently published mode into
	// current; called from applySessionStart per D5b step 7.
	ApplyAtSessionStart()
}

// applyDeps is the full dependency bundle for the apply pipeline. Task 7's
// Arbitrator struct holds and constructs one; tests build one directly with
// hand-rolled fakes.
//
// All fields are required unless noted; nil fields cause panics on first use
// (apply() is single-owner and never invoked before Arbitrator wiring).
type applyDeps struct {
	frames          *frameState
	pending         *pendingStore
	idem            *idemCache
	stormGuard      *hookStormGuard
	minter          observation.TraceIDMinter
	arbmode         ArbmodeView
	traceSubmit     TraceSubmitter
	now             func() time.Time
	pendingDeadline time.Duration
	perSessionCap   int
	perEntryObsCap  int
}

// apply runs the full 9-step reducer on obs. The pipeline is single-owner
// (the Arbitrator goroutine); apply must never be invoked concurrently.
//
// Each step returns early (via trace emission) when it rejects or stashes
// the observation; step 9 is reached only when all prior gates accept.
// D12 hook-storm is a pre-gate — step 0 — so the storm does not inflate
// idem / pending state for a session that has already blown its budget.
func (d *applyDeps) apply(obs observation.Observation) {
	now := d.now()

	// Step 0 — D12 hook-storm pre-gate. Only applies to hook-sourced obs;
	// probes/sweeps/synthetic/reconcile have their own rate characteristics.
	if obs.SourceKind == observation.SourceHook && d.stormGuard != nil &&
		d.stormGuard.ShouldDrop(obs.SessionID, now) {
		d.emitRejectedTrace(obs, ReasonHookStormDropped, "per-session hook rate exceeded")
		return
	}

	// Step 1 — Generation gate. SessionStart is the only hook Action that
	// may advance the generation; the helper handles side-effects + returns.
	if !d.checkGenerationGate(obs) {
		return
	}

	// Step 2 — Watcher identity (probe-only).
	if !d.checkWatcher(obs) {
		return
	}

	// Step 3 — Idempotency.
	if !d.checkIdempotency(obs) {
		return
	}

	// Step 4 — Pending routing. Sweep obs targeting a still-pending actor
	// stash onto the pending entry without falling through to apply.
	if d.routeToPendingIfSweepStashing(obs) {
		return
	}

	// Step 5 — Source priority.
	if !d.checkSourcePriority(obs) {
		return
	}

	// Step 6 — Monotone lifecycle.
	if !d.checkMonotoneLifecycle(obs) {
		return
	}

	// Step 7 — Single-primary invariant. Emits synthetic end-of-lifecycle
	// trace for any displaced primary and updates frameState directly.
	d.enforceSinglePrimaryInvariant(obs)

	// Step 8 — Mode branch (passthrough advances frameState lineage only;
	// authoritative fail-closes per P2-2).
	if !d.applyModeBranch(obs) {
		return
	}

	// Step 9 — Emit accepted trace.
	d.emitAcceptedTrace(obs)
}

// checkGenerationGate guards against stale-generation observations and
// routes SessionStart through applySessionStart. Returns true when apply
// should proceed to step 2.
func (d *applyDeps) checkGenerationGate(obs observation.Observation) bool {
	sg := d.frames.getOrCreateSession(obs.SessionID)
	switch {
	case obs.ObservedGeneration < sg.Generation:
		d.emitRejectedTrace(obs, ReasonStaleGeneration, "observation from older generation")
		return false
	case obs.ObservedGeneration == sg.Generation:
		return true
	default: // obs.ObservedGeneration > sg.Generation
		if obs.SourceKind == observation.SourceHook && obs.Action == actionSessionStart {
			d.applySessionStart(obs)
			// SessionStart is fully handled by the helper; do not fall
			// through to step 2.
			return false
		}
		d.emitRejectedTrace(obs, ReasonUnauthorizedGenerationBump, "only hook.SessionStart may advance generation")
		return false
	}
}

// applySessionStart is the D5b boundary helper. It bumps the session's
// generation, ends old-gen actors, clears stale pending observations,
// rotates the trace_id watermark, consumes any published arbmode pending,
// and emits a synthetic boundary trace.
func (d *applyDeps) applySessionStart(obs observation.Observation) {
	sid := obs.SessionID
	newGen := obs.ObservedGeneration
	sg := d.frames.getOrCreateSession(sid)
	oldGen := sg.Generation
	now := d.now()

	// Steps 2-3 — end old-gen actors (clears WatcherTokens in the process).
	endedActors := d.frames.forceEndOldGenActors(sid, oldGen, now)

	// Step 4 — clear pending entries older than newGen; emit one trace per
	// stashed observation so downstream can reconcile the dropped data.
	clearedPending := d.pending.clearSessionGenerationPending(sid, newGen, func(entry *PendingEntry, _ string) {
		for _, pObs := range entry.Observations {
			d.emitRejectedTrace(pObs, ReasonSessionRestartCleared, "session restart cleared pending observation")
		}
	})

	// Step 1 — bump generation after stashing oldGen side-effects.
	sg.Generation = newGen

	// Step 5 — mint the new generation's trace_id.
	d.minter.Mint(sid, newGen)

	// Step 6 — prune any trace_id entries for generations below newGen.
	d.minter.PruneSessionBefore(sid, newGen)

	// Step 7 — consume the latest published arbmode target.
	d.arbmode.ApplyAtSessionStart()

	// Step 8 — emit synthetic boundary trace.
	d.emitBoundaryTrace(obs, newGen, len(endedActors), clearedPending)
}

// checkWatcher verifies a probe observation's WatcherToken matches the
// token the Arbitrator last rotated for its (actor, probe_id) pair.
// Hook/sweep/synthetic/reconcile sources bypass this gate entirely.
func (d *applyDeps) checkWatcher(obs observation.Observation) bool {
	if obs.SourceKind != observation.SourceProbe {
		return true
	}
	actor, ok := d.frames.actor(obs.Proposal.ActorKey)
	if !ok || len(actor.WatcherTokens) == 0 {
		d.emitRejectedTrace(obs, ReasonStaleWatcher, "no known watcher token for probe's actor")
		return false
	}
	probeID := extractProbeID(obs)
	expected, hasToken := actor.WatcherTokens[probeID]
	if !hasToken || expected != obs.WatcherToken {
		d.emitRejectedTrace(obs, ReasonStaleWatcher, "watcher token mismatch")
		return false
	}
	return true
}

// checkIdempotency consults the idemCache. Per Task 4's contract, a
// (false, "") return from idemCache.Check signals that makeIdemKey failed
// (e.g. unmarshalable evidence); apply treats that as a bypass so the
// observation still flows downstream (the cache cannot falsely flag it as
// a duplicate, and dropping silently would hide the event).
func (d *applyDeps) checkIdempotency(obs observation.Observation) bool {
	accepted, reason := d.idem.Check(obs, obs.Seq, d.now())
	if accepted {
		return true
	}
	if reason == "" {
		// Bypass: idemCache could not compute a canonical key. Continue.
		return true
	}
	d.emitRejectedTrace(obs, ReasonDuplicateObservation, reason)
	return false
}

// routeToPendingIfSweepStashing stashes a sweep observation onto an
// existing pending entry, short-circuiting the rest of apply. Non-sweep
// sources always proceed; sweep obs against non-pending actors also
// proceed (they may create a new actor through normal apply).
func (d *applyDeps) routeToPendingIfSweepStashing(obs observation.Observation) (stashed bool) {
	if obs.SourceKind != observation.SourceSweep {
		return false
	}
	if !d.pending.isPending(obs.Proposal.ActorKey) {
		return false
	}
	d.pending.addPending(obs, d.now(), d.pendingDeadline, d.perSessionCap, d.perEntryObsCap, func(entry *PendingEntry, _ string) {
		for _, pObs := range entry.Observations {
			d.emitRejectedTrace(pObs, ReasonPendingEvicted, "pending evicted due to per-session cap")
		}
	})
	return true
}

// checkSourcePriority enforces the source-priority order
// hook > probe > sweep > synthetic > reconcile. A single override clause
// lets probe.error defeat hook.waiting (spec §3.4.1 #5 example).
func (d *applyDeps) checkSourcePriority(obs observation.Observation) bool {
	actor, ok := d.frames.actor(obs.Proposal.ActorKey)
	if !ok || actor.LastAcceptedSource == "" {
		// First observation for this actor — nothing to compare against.
		return true
	}
	incPri := sourceRank(obs.SourceKind)
	lastPri := sourceRank(actor.LastAcceptedSource)
	if incPri <= lastPri {
		return true
	}
	// Incoming is lower priority. The one documented override:
	// probe.error over hook.waiting.
	if obs.SourceKind == observation.SourceProbe &&
		obs.Proposal.SuggestStatus == "error" &&
		actor.LastAcceptedSource == observation.SourceHook &&
		actor.LastAcceptedStatus == "waiting" {
		return true
	}
	d.emitRejectedTrace(obs, reasonSourcePriorityLower, "incoming source priority lower than last accepted")
	return false
}

// sourceRank returns the priority rank for a SourceKind. Lower is better
// (hook=1 .. reconcile=5). Unknown sources fall back to a sentinel
// lower-than-all so they do not accidentally win against known sources.
func sourceRank(k observation.SourceKind) int {
	switch k {
	case observation.SourceHook:
		return 1
	case observation.SourceProbe:
		return 2
	case observation.SourceSweep:
		return 3
	case observation.SourceSynthetic:
		return 4
	case observation.SourceReconcile:
		return 5
	default:
		return 99
	}
}

// checkMonotoneLifecycle blocks observations that would mutate an actor
// after it has already ended. The synthetic "replaced_by_new_primary"
// transition is the only allowed exception.
func (d *applyDeps) checkMonotoneLifecycle(obs observation.Observation) bool {
	actor, ok := d.frames.actor(obs.Proposal.ActorKey)
	if !ok {
		return true
	}
	if actor.EndedAt == nil {
		return true
	}
	if obs.SourceKind == observation.SourceSynthetic &&
		obs.Proposal.EndReason == endReasonReplacedByNewPrimary {
		return true
	}
	d.emitRejectedTrace(obs, ReasonActorEnded, "actor already ended")
	return false
}

// enforceSinglePrimaryInvariant handles the spec §3.4.1 #7 case: when an
// incoming observation would create a second primary in the same session,
// the Arbitrator emits a synthetic "replaced_by_new_primary" end trace for
// the displaced primary and hands the flag over via setPrimary.
func (d *applyDeps) enforceSinglePrimaryInvariant(obs observation.Observation) {
	if !hasEvidenceFlag(obs, "is_primary", true) {
		return
	}
	key := obs.Proposal.ActorKey
	if key == (observation.ActorKey{}) {
		return
	}
	// Ensure the target actor exists so setPrimary has something to
	// promote; reconcile will see the Status on the next tick if apply
	// never wrote LastActivity first (apply step 9 writes it below).
	d.frames.upsertActor(key, func(a *actorSummary) {})
	now := d.now()
	displaced, hasDisplaced := d.frames.findPrimary(obs.SessionID)
	if hasDisplaced && displaced != key {
		d.emitSyntheticReplacedPrimaryTrace(obs, displaced, now)
	}
	d.frames.setPrimary(key, now)
}

// applyModeBranch implements D6 step 8 + P2-2 fail-closed for authoritative.
//
// Passthrough: frameState has already been updated by the preceding steps
// (generation gate, idempotency bookkeeping, potential primary rotation).
// PR-1b-1b deliberately does NOT write FramesStore / divergence / broadcast;
// that wiring lands in 1b-1c. The return-true here means "proceed to step 9
// and emit the accept trace".
//
// Authoritative: fail-closed. Observations are rejected with a stable
// reason_code so operators who set AGENT_ARB_MODE=authoritative see an
// explicit reject rather than a silent half-functional system. Phase 2
// replaces this branch with real frame mutations.
func (d *applyDeps) applyModeBranch(obs observation.Observation) bool {
	snapshot := d.arbmode.Snapshot()
	switch snapshot.Current {
	case arbmode.ModePassthrough:
		return true
	case arbmode.ModeAuthoritative:
		d.emitRejectedTrace(obs, ReasonAuthoritativeNotSupportedPhase1, "authoritative mode not supported until Phase 2")
		return false
	default:
		// Defensive — should be unreachable because arbmode.Manager always
		// publishes a valid mode. Treat as authoritative fail-closed.
		d.emitRejectedTrace(obs, ReasonAuthoritativeNotSupportedPhase1, "unknown arb_mode")
		return false
	}
}

// emitAcceptedTrace builds and submits the trace record for an observation
// that passed the full pipeline. In 1b-1b (passthrough only) the record is
// Phase=proposed + Outcome=skipped because nothing is actually persisted
// externally; 1b-1c will flip this to committed when the dual-write lands.
// It also records the acceptance onto the actor summary so step-5 source
// priority can consult it on future observations.
func (d *applyDeps) emitAcceptedTrace(obs observation.Observation) {
	if obs.Proposal.ActorKey != (observation.ActorKey{}) {
		d.frames.upsertActor(obs.Proposal.ActorKey, func(a *actorSummary) {
			a.LastAcceptedSource = obs.SourceKind
			a.LastAcceptedStatus = obs.Proposal.SuggestStatus
			if !obs.ObservedAt.IsZero() {
				a.LastActivity = obs.ObservedAt
			}
			if obs.Proposal.SuggestStatus != "" {
				a.Status = obs.Proposal.SuggestStatus
			}
			if obs.Proposal.EndLifecycle && a.EndedAt == nil {
				t := obs.ObservedAt
				if t.IsZero() {
					t = d.now()
				}
				a.EndedAt = &t
				a.EndedReason = obs.Proposal.EndReason
				a.Status = "ended"
			}
		})
	}

	r := TraceRecord{
		TraceID:            obs.TraceID,
		SpanID:             obs.SpanID,
		ParentSpanID:       obs.ParentSpanID,
		SessionID:          obs.SessionID,
		ObservedGeneration: obs.ObservedGeneration,
		SourceKind:         obs.SourceKind,
		Action:             obs.Action,
		Phase:              observation.PhaseProposed,
		Status:             "success",
		Outcome:            "skipped",
		DecisionPorts:      obs.DecisionPorts,
		Evidence:           obs.Evidence,
		StartedAt:          obs.ObservedAt,
		EndedAt:            obs.ObservedAt,
		Seq:                obs.Seq,
		DropPriority:       dropPriority(obs.SourceKind, observation.PhaseProposed),
	}
	d.traceSubmit.Submit(r)
}

// emitRejectedTrace builds and submits a rejected trace record. The
// reasonCode/reasonText are required for downstream operators to classify
// why a given observation never reached frame state.
func (d *applyDeps) emitRejectedTrace(obs observation.Observation, reasonCode, reasonText string) {
	r := TraceRecord{
		TraceID:            obs.TraceID,
		SpanID:             obs.SpanID,
		ParentSpanID:       obs.ParentSpanID,
		SessionID:          obs.SessionID,
		ObservedGeneration: obs.ObservedGeneration,
		SourceKind:         obs.SourceKind,
		Action:             obs.Action,
		Phase:              observation.PhaseRejected,
		Status:             "success",
		Outcome:            "rejected",
		ReasonCode:         reasonCode,
		ReasonText:         reasonText,
		DecisionPorts:      obs.DecisionPorts,
		Evidence:           obs.Evidence,
		StartedAt:          obs.ObservedAt,
		EndedAt:            obs.ObservedAt,
		Seq:                obs.Seq,
		DropPriority:       dropPriority(obs.SourceKind, observation.PhaseRejected),
	}
	d.traceSubmit.Submit(r)
}

// emitBoundaryTrace submits the synthetic "session.generation_advanced"
// trace record generated by applySessionStart. The Action is canonical so
// downstream tools can filter boundary events out of per-actor views.
func (d *applyDeps) emitBoundaryTrace(obs observation.Observation, newGen int64, endedActors, clearedPending int) {
	now := d.now()
	r := TraceRecord{
		TraceID:            obs.TraceID,
		SessionID:          obs.SessionID,
		ObservedGeneration: newGen,
		SourceKind:         observation.SourceSynthetic,
		Action:             actionSessionGenerationAdvanced,
		Phase:              observation.PhaseProposed,
		Status:             "success",
		Outcome:            "emitted",
		ReasonCode:         ReasonSessionRestartCleared,
		ReasonText:         "generation advanced via SessionStart",
		StartedAt:          now,
		EndedAt:            now,
		DropPriority:       dropPriority(observation.SourceSynthetic, observation.PhaseProposed),
		Evidence: []observation.EvidenceRef{
			{Key: "cleared_actors", Value: endedActors},
			{Key: "cleared_pending", Value: clearedPending},
		},
	}
	d.traceSubmit.Submit(r)
}

// emitSyntheticReplacedPrimaryTrace records the synthetic end-of-lifecycle
// trace for a primary actor displaced by a new primary. The trace is
// emitted BEFORE setPrimary mutates the displaced actor, preserving the
// visible "what was the state before this happened" for debugging.
func (d *applyDeps) emitSyntheticReplacedPrimaryTrace(obs observation.Observation, displaced observation.ActorKey, now time.Time) {
	r := TraceRecord{
		TraceID:            obs.TraceID,
		SessionID:          obs.SessionID,
		ObservedGeneration: obs.ObservedGeneration,
		SourceKind:         observation.SourceSynthetic,
		Action:             "actor.end_lifecycle",
		Phase:              observation.PhaseProposed,
		Status:             "success",
		Outcome:            "emitted",
		ReasonCode:         endReasonReplacedByNewPrimary,
		ReasonText:         "primary replaced by new primary in same session",
		StartedAt:          now,
		EndedAt:            now,
		DropPriority:       dropPriority(observation.SourceSynthetic, observation.PhaseProposed),
		Evidence: []observation.EvidenceRef{
			{Key: "displaced_actor_id", Value: displaced.ActorID},
			{Key: "displaced_generation", Value: displaced.Generation},
		},
	}
	d.traceSubmit.Submit(r)
}

// extractProbeID returns the value of the Evidence entry with Key "probe_id"
// (first match wins). Returns "" if no such entry exists or the value is
// not a string.
func extractProbeID(obs observation.Observation) string {
	for _, e := range obs.Evidence {
		if e.Key != "probe_id" {
			continue
		}
		if s, ok := e.Value.(string); ok {
			return s
		}
	}
	return ""
}

// hasEvidenceFlag reports whether obs carries an Evidence entry with Key
// matching `key` and Value equal to `want` (bool compare). Used by the
// single-primary invariant to identify primary-asserting observations.
func hasEvidenceFlag(obs observation.Observation, key string, want bool) bool {
	for _, e := range obs.Evidence {
		if e.Key != key {
			continue
		}
		if b, ok := e.Value.(bool); ok && b == want {
			return true
		}
	}
	return false
}
