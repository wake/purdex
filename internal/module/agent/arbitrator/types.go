// Package arbitrator houses the Lights Arbitrator — the single-writer that
// consumes Observations from hooks/probes/sweeps/reconcile/synthetic sources
// and applies them to the in-memory frame state (see plan PR-1b-1b).
//
// Task 3 contributes only the shared envelope types, sentinel reason codes,
// and a lightweight in-process metrics counter. The Arbitrator struct and
// goroutine run-loop are wired in later tasks.
package arbitrator

import (
	"context"
	"time"

	"github.com/wake/purdex/internal/module/agent/observation"
)

// AdmissionPriority classifies an Observation's priority for entering the
// Arbitrator input channel (arb_in). Committed observations get a 100ms
// blocking retry; proposed observations are dropped immediately when the
// channel is full (plan D9).
type AdmissionPriority int

const (
	// AdmissionCommitted marks observations whose Phase is committed. They
	// take the high-priority admission path (blocking retry).
	AdmissionCommitted AdmissionPriority = iota
	// AdmissionProposed marks observations whose Phase is proposed or
	// rejected. They take the best-effort admission path (drop on full).
	AdmissionProposed
)

// admissionPriority returns the admission tier for obs.
//
// Only PhaseCommitted promotes to AdmissionCommitted; PhaseProposed and
// PhaseRejected both stay at AdmissionProposed so a rejected observation
// cannot elbow past a healthy committed one.
func admissionPriority(obs observation.Observation) AdmissionPriority {
	if obs.Phase == observation.PhaseCommitted {
		return AdmissionCommitted
	}
	return AdmissionProposed
}

// applyOutcome is the result the apply pipeline produces per step.
// The canonical set is defined by plan §D8 (step composition).
type applyOutcome int

const (
	// outcomeAccepted: proceed to the next apply step.
	outcomeAccepted applyOutcome = iota
	// outcomeRejected: short-circuit the pipeline, emit a rejected trace.
	outcomeRejected
	// outcomePendingStash: observation stashed in the pending window.
	outcomePendingStash
	// outcomeReplacedPrimary: a primary-actor transfer occurred.
	outcomeReplacedPrimary
)

// TraceRecord is the flat Arbitrator-side envelope (one record == one step
// row in agent_trace_steps). It is emitted by the apply pipeline for every
// processed observation and handed to the TraceSubmitter for batching and
// persistence (plan D10.1).
type TraceRecord struct {
	TraceID            string
	SpanID             string
	ParentSpanID       string
	SessionID          string
	ObservedGeneration int64
	SourceKind         observation.SourceKind
	Action             string
	Phase              observation.ObsPhase
	Status             string
	Outcome            string
	ReasonCode         string
	ReasonText         string
	DecisionPorts      []observation.DecisionPort
	Evidence           []observation.EvidenceRef
	StartedAt          time.Time
	EndedAt            time.Time
	Seq                int64
	// DropPriority orders records for the priority ring buffer (D10.2).
	// Lower is higher priority; 0 = hook+committed, 6 = reconcile+proposed.
	DropPriority int
}

// TraceSubmitter is the only way the Arbitrator hands records to the trace
// writer. Submit never blocks — admission (priority-ordered eviction) is
// performed internally. Shutdown flushes any remaining records.
//
// The concrete implementation lives in Task 7.
type TraceSubmitter interface {
	Submit(record TraceRecord)
	Shutdown(ctx context.Context) error
}

// Sentinel ReasonCode constants. Arbitrator emits these on TraceRecord so
// downstream trace readers can categorize rejections with a low-cardinality
// label set. CamelCase string values (never free-form text).
const (
	// ReasonStaleGeneration: observed_generation is older than the
	// frame's current generation.
	ReasonStaleGeneration = "StaleGeneration"
	// ReasonUnauthorizedGenerationBump: a non-SessionStart source tried to
	// advance the generation.
	ReasonUnauthorizedGenerationBump = "UnauthorizedGenerationBump"
	// ReasonStaleWatcher: watcher_token no longer matches the frame's
	// current watcher for the targeted actor.
	ReasonStaleWatcher = "StaleWatcher"
	// ReasonDuplicateObservation: idempotency cache rejected a repeat
	// observation (same canonical key + seq ≤ previous).
	ReasonDuplicateObservation = "DuplicateObservation"
	// ReasonActorEnded: the target actor has already ended.
	ReasonActorEnded = "ActorEnded"
	// ReasonPidTreeUnresolvable: pid-tree resolution failed.
	ReasonPidTreeUnresolvable = "PidTreeUnresolvable"
	// ReasonPendingEvicted: observation evicted from the pending window.
	ReasonPendingEvicted = "PendingEvicted"
	// ReasonHookStormDropped: hook-storm throttle dropped the observation
	// (> 50 hook obs per session within 10ms window).
	ReasonHookStormDropped = "HookStormDropped"
	// ReasonReconcileStaleNoted: reconcile loop recorded a stale entry
	// without applying it.
	ReasonReconcileStaleNoted = "ReconcileStaleNoted"
	// ReasonSessionRestartCleared: an actor was ended because a
	// SessionStart observation bumped the generation.
	ReasonSessionRestartCleared = "SessionRestartCleared"
	// ReasonAuthoritativeNotSupportedPhase1: authoritative arbiter mode is
	// not implemented in Phase-1; observation is rejected without applying.
	ReasonAuthoritativeNotSupportedPhase1 = "AuthoritativeNotSupportedPhase1"
)

// dropPriority returns the ring-buffer drop priority for a record (plan
// D10.2). Lower values are higher priority (retained preferentially).
//
// Canonical table:
//
//	hook      + committed → 0
//	probe     + committed → 1
//	hook      + proposed  → 2
//	probe     + proposed  → 3
//	sweep     + proposed  → 4
//	synthetic + proposed  → 5
//	reconcile + proposed  → 6
//
// Rejected-phase records are treated at the same tier as their proposed
// counterpart for the same source (never elevated to committed). Unknown
// sources fall back to the lowest tier (7) so stray records cannot displace
// known-good ones.
func dropPriority(sourceKind observation.SourceKind, phase observation.ObsPhase) int {
	// Treat rejected as the same tier as proposed.
	effectivePhase := phase
	if effectivePhase == observation.PhaseRejected {
		effectivePhase = observation.PhaseProposed
	}

	switch sourceKind {
	case observation.SourceHook:
		if effectivePhase == observation.PhaseCommitted {
			return 0
		}
		return 2
	case observation.SourceProbe:
		if effectivePhase == observation.PhaseCommitted {
			return 1
		}
		return 3
	case observation.SourceSweep:
		return 4
	case observation.SourceSynthetic:
		return 5
	case observation.SourceReconcile:
		return 6
	default:
		// Unknown source — park below all known tiers.
		return 7
	}
}
