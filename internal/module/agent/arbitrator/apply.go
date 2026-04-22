package arbitrator

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/module/agent/arbmode"
	"github.com/wake/purdex/internal/module/agent/observation"
	"github.com/wake/purdex/internal/store"
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

// primaryActorPrefix gates the divergence writer: only observations whose
// Proposal.ActorKey.ActorID begins with this prefix are projected onto the
// legacy Frame schema. Subagent / proxy actors need Phase-2 frame schema
// fields to project faithfully; see D5.2 / Non-Goals.
const primaryActorPrefix = "primary:"

// reasonRoleTerminalUnresolved is the ReasonCode emitted when a hook or
// probe reports role_resolution == RoleTerminalUnresolved — the role
// cannot be determined and retry is pointless. The observation is dropped
// with a trace but never added to pending.
const reasonRoleTerminalUnresolved = "RoleTerminalUnresolved"

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

// DivergencesWriter is the narrow contract for writing divergence rows from
// the apply pipeline. Only the primary-actor passthrough branch exercises
// it; tests provide fakes that capture inserts for assertion.
type DivergencesWriter interface {
	Insert(row store.FrameDivergence) (int64, error)
}

// LegacyFramesView is the read-side projection of FramesStore the apply
// pipeline needs to compare a proposal against the existing legacy frame.
// Only GetByIdentity is used; future PR-2 work may widen the interface.
type LegacyFramesView interface {
	GetByIdentity(paneID string, pid int, startTime string) (*store.Frame, error)
}

// applyDeps is the full dependency bundle for the apply pipeline. Task 7's
// Arbitrator struct holds and constructs one; tests build one directly with
// hand-rolled fakes.
//
// All fields are required unless noted; nil fields cause panics on first use
// (apply() is single-owner and never invoked before Arbitrator wiring).
//
// sampler, divergences, legacyFrames are new in PR-1b-1c T5; nil values are
// tolerated (sampler=nil degenerates to always-emit; divergences/legacyFrames
// nil skips the passthrough divergence write path silently). The tolerant
// behaviour lets Stage 3 wiring reach production before every call site is
// updated.
type applyDeps struct {
	frames          *frameState
	pending         *pendingStore
	idem            *idemCache
	stormGuard      *hookStormGuard
	minter          observation.TraceIDMinter
	arbmode         ArbmodeView
	traceSubmit     TraceSubmitter
	sampler         *sampler
	divergences     DivergencesWriter
	legacyFrames    LegacyFramesView
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
// D12 hook-storm is a pre-gate — step 0a — so the storm does not inflate
// idem / pending state for a session that has already blown its budget.
// The authoritative-mode fail-closed gate is also a pre-gate — step 0b —
// so frameState mutations (generation bump, actor end, primary transfer)
// never fire for observations that are going to be rejected anyway.
func (d *applyDeps) apply(obs observation.Observation) {
	now := d.now()

	// Step 0a — D12 hook-storm pre-gate. Only applies to hook-sourced obs;
	// probes/sweeps/synthetic/reconcile have their own rate characteristics.
	if obs.SourceKind == observation.SourceHook && d.stormGuard != nil &&
		d.stormGuard.ShouldDrop(obs.SessionID, now) {
		d.emitRejectedTrace(obs, ReasonHookStormDropped, "per-session hook rate exceeded")
		return
	}

	// Step 0b — Authoritative-mode fail-closed (P2-2). Applied BEFORE any
	// state-mutating step so frameState is not polluted by observations
	// that are ultimately rejected. Phase 2 replaces this with real
	// authoritative mutations; today it's a strict pre-gate.
	if snapshot := d.arbmode.Snapshot(); snapshot.Current == arbmode.ModeAuthoritative {
		d.emitRejectedTrace(obs, ReasonAuthoritativeNotSupportedPhase1, "authoritative mode not supported until Phase 2")
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

	// Step 4 — Pending routing (role-based). Sweep-override keeps stashing
	// sweep proposals onto still-pending entries; role_resolution drives
	// the hook/probe branches.
	if d.routeToPendingIfUnresolved(obs) {
		return
	}

	// Steps 5-9 run for any observation that reached this point.
	d.applyResolvedProposal(obs)
}

// applyResolvedProposal runs steps 5-9 (source priority, monotone lifecycle,
// single-primary, mode branch, trace emit + divergence). It is invoked
// from the main apply() path AND from tryPromoteFromEntry when a pending
// observation is promoted; the pending path has already cleared steps 0-4
// (idempotency + pending routing), so re-running them would double-count.
func (d *applyDeps) applyResolvedProposal(obs observation.Observation) {
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

	// Step 8 — Mode branch (defensive). Authoritative is already rejected
	// at step 0b; the branch here is a belt-and-braces guard against
	// unknown modes slipping past the pre-gate. Passthrough falls through
	// to step 9 (trace emission), but first writes a divergence row if the
	// proposal diverges from the legacy frame (D5.2; primary-only).
	if !d.applyModeBranch(obs) {
		return
	}
	d.writeDivergenceIfAny(obs)

	// Step 9 — Emit accepted trace.
	d.emitAcceptedTrace(obs)
}

// checkGenerationGate guards against stale-generation observations and
// routes SessionStart through applySessionStart. Returns true when apply
// should proceed to step 2.
//
// Reads the current generation through GenerationOf so the RWMutex
// protection covers cross-goroutine readers (Arbitrator.CurrentGeneration
// producers) that may be racing against the apply writer.
func (d *applyDeps) checkGenerationGate(obs observation.Observation) bool {
	current := d.frames.GenerationOf(obs.SessionID)
	switch {
	case obs.ObservedGeneration < current:
		d.emitRejectedTrace(obs, ReasonStaleGeneration, "observation from older generation")
		return false
	case obs.ObservedGeneration == current:
		return true
	default: // obs.ObservedGeneration > current
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
// adopts the hook-provisioned trace_id for the new (session, gen), prunes
// the trace_id watermark, consumes any published arbmode pending, clears
// the sampler's per-session counter, and emits a synthetic boundary trace.
//
// All frameState mutations (read oldGen, end old-gen actors, advance
// generation) go through locked frameState methods. GenerationOf is read
// before the end-old-gen path so the oldGen snapshot is consistent with the
// value that forceEndOldGenActors will filter on.
//
// AdoptTraceID replaces the former Mint call: the hook-side builder mints
// a provisional UUID that propagates into the apply pipeline as
// obs.TraceID; AdoptTraceID binds that seed to the (session, newGen) slot
// so downstream Lookup.Get calls return the same trace_id that Observation.
// TraceID already carries. Replay of the same SessionStart observation is
// idempotent: the registry returns the existing id and discards the seed.
func (d *applyDeps) applySessionStart(obs observation.Observation) {
	sid := obs.SessionID
	newGen := obs.ObservedGeneration
	oldGen := d.frames.GenerationOf(sid)
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
	d.frames.SetGeneration(sid, newGen)

	// Step 5 — adopt the hook-provisioned trace_id for (sid, newGen). The
	// returned id becomes the authoritative tag for every boundary-synthesis
	// event emitted for this new generation. On replay of an identical
	// SessionStart, AdoptTraceID returns the previously-adopted id (seed
	// discarded), preserving chain correlation across retries.
	adoptedTraceID := d.minter.AdoptTraceID(sid, newGen, obs.TraceID)

	// Step 6 — prune any trace_id entries for generations below newGen.
	d.minter.PruneSessionBefore(sid, newGen)

	// Step 7 — consume the latest published arbmode target.
	d.arbmode.ApplyAtSessionStart()

	// Step 7b — reset the sampler's per-session counter so the first sweep
	// of the new generation always emits. No-op when sampler is nil (tests
	// that do not exercise sampling).
	if d.sampler != nil {
		d.sampler.ClearSession(sid)
	}

	// Step 8 — emit synthetic boundary trace stamped with the newly adopted
	// trace_id (identical to obs.TraceID when this is a fresh SessionStart).
	// Empty trace_id would trip validateLightsRow and poison the whole
	// flush batch.
	d.emitBoundaryTrace(obs, newGen, adoptedTraceID, len(endedActors), clearedPending)
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

// routeToPendingIfUnresolved is the PR-1b-1c replacement for the former
// sweep-only routeToPendingIfSweepStashing helper. It keeps the sweep
// override (sweep observations targeting a still-pending actor coalesce
// onto that entry) and adds role-based routing driven by role_resolution
// evidence (D4.1 / D4.2):
//
//   - RoleRetryableUnresolved: stash into pending as first entry or coalesce.
//     No retry scheduler is wired in this PR (Non-Goals); pending entries
//     are driven by flushPendingDue deadline drop + coalesce-on-arrival
//     promote.
//   - RoleTerminalUnresolved: emit a trace-only rejection (reason code
//     "RoleTerminalUnresolved") and drop — never added to pending.
//   - RoleResolved + actor already pending: coalesce onto existing entry
//     and trigger tryPromoteToActorNow to shorten latency (avoid waiting
//     for the reconcile tick).
//   - RoleResolved + actor NOT pending: proceed through the rest of apply.
//   - Missing role_resolution key: malformed observation — fall through
//     to normal apply (not stashed). The builder layer guarantees the
//     key is present for hook/probe/sweep in Stage 3; this branch is a
//     conservative default for legacy test observations.
//
// Returns true when the observation was stashed (or terminally dropped with
// a trace) so apply() can short-circuit steps 5-9.
func (d *applyDeps) routeToPendingIfUnresolved(obs observation.Observation) (stashed bool) {
	// Existing sweep override — keep. Sweep observations can land on a
	// pending entry regardless of role_resolution because sweep semantics
	// are always "pid state suggests end"; the pending entry itself came
	// from a hook/probe that could not resolve the role.
	if obs.SourceKind == observation.SourceSweep && d.pending.isPending(obs.Proposal.ActorKey) {
		d.pending.addPending(obs, d.now(), d.pendingDeadline, d.perSessionCap, d.perEntryObsCap, func(entry *PendingEntry, _ string) {
			for _, pObs := range entry.Observations {
				d.emitRejectedTrace(pObs, ReasonPendingEvicted, "pending evicted due to per-session cap")
			}
		})
		return true
	}

	state, ok := observation.RoleResolutionFromEvidence(obs.Evidence)
	if !ok {
		// No role_resolution key → legacy / malformed; fall through.
		return false
	}

	switch state {
	case observation.RoleRetryableUnresolved:
		d.pending.addPending(obs, d.now(), d.pendingDeadline, d.perSessionCap, d.perEntryObsCap, func(entry *PendingEntry, _ string) {
			for _, pObs := range entry.Observations {
				d.emitRejectedTrace(pObs, ReasonPendingEvicted, "pending evicted due to per-session cap")
			}
		})
		// TODO(retry-scheduler, post-1b-1c): scheduleRetry([100ms, 250ms,
		// 500ms]) — deferred. Promote path is deadline flush +
		// coalesce-on-arrival only.
		return true

	case observation.RoleTerminalUnresolved:
		// Terminal unresolved: emit trace-only rejection and drop.
		d.emitTraceOnly(obs, reasonRoleTerminalUnresolved, "role cannot be resolved (terminal)")
		return true

	case observation.RoleResolved:
		// If the actor is already pending, coalesce + try to promote now so
		// a fresh positive signal short-circuits the deadline wait.
		if d.pending.isPending(obs.Proposal.ActorKey) {
			d.pending.addPending(obs, d.now(), d.pendingDeadline, d.perSessionCap, d.perEntryObsCap, func(entry *PendingEntry, _ string) {
				for _, pObs := range entry.Observations {
					d.emitRejectedTrace(pObs, ReasonPendingEvicted, "pending evicted due to per-session cap")
				}
			})
			_ = d.tryPromoteToActorNow(obs.Proposal.ActorKey)
			return true
		}
		// Not pending — fall through to normal apply.
		return false
	}

	// Unknown state: defensive fall-through.
	return false
}

// tryPromoteFromEntry inspects pendingEntry.Observations for a winning
// positive signal (RoleResolved + meaningful proposal). On success it runs
// apply steps 5-9 on the winner and returns true so the caller can clean
// up the pending entry. See D4.3 for the full contract.
//
// No-signal entries return false; flushPendingDue then emits the deadline
// drop trace. "Resolved but empty proposal" also returns false — an empty
// proposal offers no frame state to project, so promoting it would just
// thrash the actor.
func (d *applyDeps) tryPromoteFromEntry(entry *PendingEntry) bool {
	if entry == nil || len(entry.Observations) == 0 {
		return false
	}
	// Scan newest → oldest for the first RoleResolved observation.
	var winner *observation.Observation
	for i := len(entry.Observations) - 1; i >= 0; i-- {
		obs := &entry.Observations[i]
		if state, ok := observation.RoleResolutionFromEvidence(obs.Evidence); ok && state == observation.RoleResolved {
			winner = obs
			break
		}
	}
	if winner == nil {
		return false
	}
	// Must carry a meaningful proposal — empty proposals cannot drive
	// frame projection.
	if winner.Proposal.SuggestStatus == "" && !winner.Proposal.EndLifecycle {
		return false
	}
	d.applyResolvedProposal(*winner)
	return true
}

// tryPromoteToActorNow is the latency-shortening helper invoked from the
// routeToPendingIfUnresolved RoleResolved branch. It looks up the current
// pending entry for actorKey and calls tryPromoteFromEntry; on success the
// entry is removed so subsequent flushPendingDue ticks skip it.
//
// Returns true when promotion fired + cleanup happened; false when no
// pending entry exists or promotion was declined.
func (d *applyDeps) tryPromoteToActorNow(actorKey observation.ActorKey) bool {
	entry, ok := d.pending.entries[actorKey]
	if !ok {
		return false
	}
	if !d.tryPromoteFromEntry(entry) {
		return false
	}
	delete(d.pending.entries, actorKey)
	return true
}

// emitTraceOnly emits a rejected trace for an observation that has been
// classified as terminal unresolved (or any "drop with note" case we want
// to surface). It is a thin wrapper around emitRejectedTrace with a
// canonical reason code.
func (d *applyDeps) emitTraceOnly(obs observation.Observation, reasonCode, reasonText string) {
	d.emitRejectedTrace(obs, reasonCode, reasonText)
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

// applyModeBranch is a defensive post-mutation mode check.
//
// The authoritative fail-closed gate moved to step 0b of apply() — by the
// time we reach this function, passthrough is the only expected mode. This
// branch stays around to catch unknown / newly-added modes that might
// somehow slip past the pre-gate; it treats anything non-passthrough as
// fail-closed so the reducer can never silently skip Phase-2 wiring.
//
// Passthrough: PR-1b-1b deliberately does NOT write FramesStore /
// divergence / broadcast; that wiring lands in 1b-1c. Return-true here
// means "proceed to step 9 and emit the accept trace".
func (d *applyDeps) applyModeBranch(obs observation.Observation) bool {
	snapshot := d.arbmode.Snapshot()
	if snapshot.Current == arbmode.ModePassthrough {
		return true
	}
	// Authoritative (pre-gate usually catches this) or unknown mode — fail
	// closed defensively.
	d.emitRejectedTrace(obs, ReasonAuthoritativeNotSupportedPhase1, "non-passthrough mode not supported until Phase 2")
	return false
}

// emitAcceptedTrace builds and submits the trace record for an observation
// that passed the full pipeline. In 1b-1b (passthrough only) the record is
// Outcome=skipped because nothing is actually persisted externally; 1b-1c
// will flip outcome semantics when the dual-write lands.
//
// Phase is preserved from the incoming observation (not hard-coded to
// PhaseProposed): a committed observation stays committed end-to-end so the
// priority ring buffer keeps its drop-priority-0/1 protections. Missing
// SpanID is back-filled with a fresh UUID so AppendSteps retries remain
// idempotent (INSERT OR IGNORE keys on step_id).
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

	phase := obs.Phase
	if phase == "" {
		phase = observation.PhaseProposed
	}
	spanID := obs.SpanID
	if spanID == "" {
		spanID = uuid.NewString()
	}

	r := TraceRecord{
		TraceID:            obs.TraceID,
		SpanID:             spanID,
		ParentSpanID:       obs.ParentSpanID,
		SessionID:          obs.SessionID,
		ObservedGeneration: obs.ObservedGeneration,
		SourceKind:         obs.SourceKind,
		Action:             obs.Action,
		Phase:              phase,
		Status:             "success",
		Outcome:            "skipped",
		DecisionPorts:      obs.DecisionPorts,
		Evidence:           obs.Evidence,
		StartedAt:          obs.ObservedAt,
		EndedAt:            obs.ObservedAt,
		Seq:                obs.Seq,
		DropPriority:       dropPriority(obs.SourceKind, phase),
	}
	if !d.sampleAllowEmit(obs.SessionID, obs.SourceKind, phase) {
		return
	}
	d.traceSubmit.Submit(r)
}

// emitRejectedTrace builds and submits a rejected trace record. The
// reasonCode/reasonText are required for downstream operators to classify
// why a given observation never reached frame state.
//
// Missing SpanID is back-filled with a fresh UUID so AppendSteps retries
// remain idempotent (INSERT OR IGNORE keys on step_id).
func (d *applyDeps) emitRejectedTrace(obs observation.Observation, reasonCode, reasonText string) {
	spanID := obs.SpanID
	if spanID == "" {
		spanID = uuid.NewString()
	}
	r := TraceRecord{
		TraceID:            obs.TraceID,
		SpanID:             spanID,
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
	// Rejected traces bypass sampling: they are diagnostic signal and the
	// sweep/synthetic PhaseRejected volume is driven by external events
	// (session restart clears pending, actor-ended rejects, ...) rather
	// than the high-frequency sweep accept path that sampling targets.
	d.traceSubmit.Submit(r)
}

// sampleAllowEmit consults the sampler (if wired) to decide whether a
// trace for (sessionID, sourceKind, phase) should be emitted. Records
// that are dropped bump lights_trace_sampled_dropped with canonical tags.
// When the sampler is nil the call always allows — Stage 3 will wire the
// sampler into Arbitrator construction.
func (d *applyDeps) sampleAllowEmit(sessionID string, sourceKind observation.SourceKind, phase observation.ObsPhase) bool {
	if d.sampler == nil {
		return true
	}
	if d.sampler.ShouldEmit(sessionID, sourceKind, phase) {
		return true
	}
	Inc("lights_trace_sampled_dropped",
		"source="+string(sourceKind),
		"phase="+string(phase),
		"session="+sessionID,
	)
	return false
}

// emitBoundaryTrace submits the synthetic "session.generation_advanced"
// trace record generated by applySessionStart. The Action is canonical so
// downstream tools can filter boundary events out of per-actor views.
//
// traceID must be the newly-minted id for (sessionID, newGen); callers
// (applySessionStart) capture it from minter.Mint's return value rather
// than reusing obs.TraceID, which belongs to the prior generation.
func (d *applyDeps) emitBoundaryTrace(obs observation.Observation, newGen int64, traceID string, endedActors, clearedPending int) {
	now := d.now()
	r := TraceRecord{
		TraceID:            traceID,
		SpanID:             uuid.NewString(),
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
		SpanID:             uuid.NewString(),
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

// writeDivergenceIfAny is the D5.2 passthrough-only helper that compares an
// accepted proposal's projection against the legacy FramesStore frame and
// writes a frame_divergences row when they diverge. It never writes when
// any gate fails (non-primary actor, identity triple missing, legacy frame
// missing); those paths bump per-reason metrics so operators can spot
// missing identity data without silent divergence loss.
//
// Only primary actors are projected in Phase 1 — subagent / proxy
// projection requires the Phase-2 frame schema (Actors JSON). See plan
// D5.2 / Non-Goals.
//
// Divergence write failure is logged via metrics but does NOT fail apply;
// passthrough is an observability layer and must not block the legacy
// path's side-effects.
func (d *applyDeps) writeDivergenceIfAny(obs observation.Observation) {
	if d.divergences == nil || d.legacyFrames == nil {
		// Deps not wired yet (early bring-up); silent no-op until Stage 3.
		return
	}
	if obs.Proposal.ActorKey.ActorID == "" {
		return
	}
	if !strings.HasPrefix(obs.Proposal.ActorKey.ActorID, primaryActorPrefix) {
		Inc("lights_divergence_non_primary_skipped", "actor_kind="+actorKindOf(obs.Proposal.ActorKey.ActorID))
		return
	}

	paneID, panePresent := evidenceString(obs.Evidence, "pane_id")
	pid, pidPresent := evidenceInt64(obs.Evidence, "pid")
	startTime, stPresent := evidenceString(obs.Evidence, "start_time")
	if !panePresent || !pidPresent || !stPresent {
		Inc("lights_divergence_identity_missing", "source="+string(obs.SourceKind))
		return
	}

	legacyFrame, err := d.legacyFrames.GetByIdentity(paneID, int(pid), startTime)
	if err != nil || legacyFrame == nil {
		Inc("lights_divergence_legacy_missing")
		return
	}

	projected := projectProposalToLegacy(obs.Proposal, *legacyFrame)
	if framesMatch(projected, *legacyFrame) {
		Inc("lights_divergence_total", "matched=1")
		return
	}

	_, _ = d.divergences.Insert(store.FrameDivergence{
		SessionID:          obs.SessionID,
		TraceID:            obs.TraceID,
		EventID:            obs.SpanID,
		ObservedGeneration: obs.ObservedGeneration,
		OldStateRef:        mustJSON(legacyFrame),
		ProposalStateRef:   mustJSON(projected),
		DiffSummary:        humanDiff(*legacyFrame, projected),
		Matched:            false,
		ReasonCode:         obs.ReasonCode,
		CreatedAt:          d.now().UnixNano(),
	})
	Inc("lights_divergence_total", "matched=0")
}

// projectProposalToLegacy overlays a StateProposal onto a copy of the
// legacy Frame, producing the "what legacy should look like if we accepted
// this proposal" projection. Only status + end lifecycle fields are
// overlaid in Phase 1; subagent / proxy projection requires Phase-2 frame
// schema changes (D5.2 / Non-Goals).
//
// SuggestStatus is mapped directly to Frame.Status as an agent.Status
// (strings are interchangeable via the Status type alias). EndLifecycle
// triggers a terminal "ended" status.
func projectProposalToLegacy(proposal observation.StateProposal, legacy store.Frame) store.Frame {
	projected := legacy
	if proposal.EndLifecycle {
		projected.Status = agentpkg.Status("ended")
		return projected
	}
	if proposal.SuggestStatus != "" {
		projected.Status = agentpkg.Status(proposal.SuggestStatus)
	}
	return projected
}

// framesMatch reports whether two frames agree on the fields the divergence
// writer cares about (status, end lifecycle derived from ended state).
// Identity fields (pane_id, pid, start_time) are skipped because the
// writer already queried by identity; trace_id / updated_at would create
// spurious mismatches.
func framesMatch(a, b store.Frame) bool {
	return a.Status == b.Status
}

// humanDiff produces a compact, human-readable diff summary for a
// FrameDivergence.DiffSummary column. Phase 1 only surfaces the fields
// framesMatch considers, plus agent_type for context. Output fits on one
// line.
func humanDiff(old, proposed store.Frame) string {
	return fmt.Sprintf("status: %q->%q", string(old.Status), string(proposed.Status))
}

// actorKindOf extracts the actor kind prefix from an ActorID
// ("primary:cc" → "primary", "subagent:task-1" → "subagent",
// "proxy:foo" → "proxy"). Unknown prefixes return "unknown" so metric
// cardinality stays bounded.
func actorKindOf(actorID string) string {
	switch {
	case strings.HasPrefix(actorID, "primary:"):
		return "primary"
	case strings.HasPrefix(actorID, "subagent:"):
		return "subagent"
	case strings.HasPrefix(actorID, "proxy:"):
		return "proxy"
	default:
		return "unknown"
	}
}

// evidenceString scans ev for the first EvidenceRef whose Key matches and
// whose Value is a string. Returns (value, true) on hit; ("", false)
// otherwise — callers use the boolean to drive the identity-triple gate.
func evidenceString(ev []observation.EvidenceRef, key string) (string, bool) {
	for _, e := range ev {
		if e.Key != key {
			continue
		}
		if s, ok := e.Value.(string); ok {
			return s, true
		}
	}
	return "", false
}

// evidenceInt64 scans ev for the first EvidenceRef whose Key matches and
// whose Value is convertible to int64 (int, int32, int64, float64 for
// JSON-round-tripped numerics). Returns (value, true) on hit; (0, false)
// otherwise.
func evidenceInt64(ev []observation.EvidenceRef, key string) (int64, bool) {
	for _, e := range ev {
		if e.Key != key {
			continue
		}
		switch v := e.Value.(type) {
		case int64:
			return v, true
		case int:
			return int64(v), true
		case int32:
			return int64(v), true
		case float64:
			return int64(v), true
		}
	}
	return 0, false
}

// mustJSON marshals v to json.RawMessage; on error it returns the bytes
// "null" so the divergence row's NOT NULL constraint is never violated.
// The error is silently dropped — divergence writes are observability
// only and a marshalling failure should not cascade.
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
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
