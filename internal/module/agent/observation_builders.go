package agent

import (
	"time"

	"github.com/google/uuid"

	"github.com/wake/purdex/internal/module/agent/observation"
	"github.com/wake/purdex/internal/store"
)

// observation_builders.go defines three pure helper builders that convert
// hook / probe / sweep inputs into observation.Observation values. They are
// side-effect free and do no I/O; callers (wired in Stage 3: handler.go /
// module.go / sweep.go) are responsible for resolving any state (e.g. the
// current generation or process start_time from FramesStore) and passing it
// in.
//
// Design contract (pr-1b-1c §D1.4 / §D2.3 / §D3 / §D4.1):
//   - Every builder mints a fresh provisional UUID for Observation.TraceID so
//     observation.Builder.Build validation never trips on empty trace_id; the
//     Arbitrator adopts the hook-produced seed for (session, generation) only
//     on SessionStart via AdoptTraceID.
//   - Every builder emits the identity triple {pid, pane_id, start_time} in
//     Evidence (required by §D5.2 divergence writer) plus the role_resolution
//     state constant (required by §D4.1 apply-layer routing).
//   - Builders are pure functions — no store reads, no clock reads beyond the
//     caller-supplied now.

// hookRetryableReasons are verify decisions that allow the apply pipeline to
// route the observation into the pending window for a retry. A later
// observation with RoleResolved may promote the actor.
var hookRetryableReasons = map[string]struct{}{
	"pane_unresolvable": {},
	"sender_uncertain":  {},
	"identify_mismatch": {},
}

// buildHookObservation constructs a SourceHook observation.
//
// Callers must pre-compute:
//   - observedGeneration: SessionStart -> CurrentGeneration(sessionCode)+1;
//     other events -> CurrentGeneration(sessionCode).
//   - startTime: frame.ProcessStartTime resolved from req or FramesStore;
//     pass "" if completely unresolvable — builder maps it to "unknown" so
//     evidence always carries a non-empty string for the divergence writer.
//   - accepted / rejectReason from verifyEventFn.
func buildHookObservation(
	sessionCode string,
	paneID string,
	pid int64,
	startTime string,
	senderPID int64,
	agentType string,
	eventName string,
	observedGeneration int64,
	accepted bool,
	rejectReason string,
	now time.Time,
) observation.Observation {
	actorKey := observation.ActorKey{
		SessionID:  sessionCode,
		Generation: observedGeneration,
		ActorID:    "primary:" + agentType,
	}

	// Lifecycle hint — hooks only drive SessionStart -> active and
	// SessionEnd -> ended; other event names defer to downstream projection.
	proposal := observation.StateProposal{ActorKey: actorKey}
	switch eventName {
	case "SessionStart":
		proposal.SuggestStatus = "active"
	case "SessionEnd":
		proposal.SuggestStatus = "ended"
		proposal.EndLifecycle = true
		proposal.EndReason = "session_end"
	}

	phase := observation.PhaseCommitted
	if !accepted {
		phase = observation.PhaseProposed
	}

	// Hook evidence contract per §D1.4: identity triple + event_name, plus
	// sender_pid when distinct from pid (aids multi-sender debugging), plus
	// role_resolution enum (replaces legacy verify_reason string matching at
	// apply layer — verify_reason itself is retained for trace debugging when
	// the observation was rejected).
	startEvidence := startTime
	if startEvidence == "" {
		startEvidence = "unknown"
	}

	evidence := make([]observation.EvidenceRef, 0, 8)
	evidence = append(evidence,
		observation.EvidenceRef{Key: "pid", Value: pid},
		observation.EvidenceRef{Key: "pane_id", Value: paneID},
		observation.EvidenceRef{Key: "start_time", Value: startEvidence},
		observation.EvidenceRef{Key: "event_name", Value: eventName},
	)
	if senderPID != pid {
		evidence = append(evidence, observation.EvidenceRef{Key: "sender_pid", Value: senderPID})
	}

	role := observation.RoleResolved
	if !accepted {
		if _, ok := hookRetryableReasons[rejectReason]; ok {
			role = observation.RoleRetryableUnresolved
		} else {
			role = observation.RoleTerminalUnresolved
		}
		evidence = append(evidence, observation.EvidenceRef{Key: "verify_reason", Value: rejectReason})
	}
	evidence = append(evidence, observation.EvidenceRef{
		Key:   observation.EvidenceKeyRoleResolution,
		Value: role,
	})

	return observation.Observation{
		TraceID:            uuid.NewString(),
		SessionID:          sessionCode,
		ObservedGeneration: observedGeneration,
		SourceKind:         observation.SourceHook,
		Action:             eventName,
		Phase:              phase,
		Proposal:           proposal,
		Evidence:           evidence,
		ObservedAt:         now,
		Seq:                now.UnixNano(),
	}
}

// probeOutcomeSuggestStatus maps a probe outcome to a legacy projection
// status hint. Outcomes not in the table fall through to empty SuggestStatus,
// letting downstream projection decide.
func probeOutcomeSuggestStatus(outcome string) string {
	switch outcome {
	case "dead":
		return "ended"
	case "active":
		return "active"
	case "idle":
		return "waiting"
	case "ready":
		return "ready"
	case "error":
		return "error"
	}
	return ""
}

// buildProbeObservation constructs a SourceProbe observation.
//
// Callers must pre-resolve observedGeneration (probes never advance gen;
// pass CurrentGeneration(sessionCode)) and the identity triple.
func buildProbeObservation(
	sessionCode string,
	paneID string,
	pid int64,
	startTime string,
	agentType string,
	observedGeneration int64,
	probeID string,
	probeToken string,
	probeKind string,
	probeOutcome string,
	now time.Time,
) observation.Observation {
	actorKey := observation.ActorKey{
		SessionID:  sessionCode,
		Generation: observedGeneration,
		ActorID:    "primary:" + agentType,
	}
	proposal := observation.StateProposal{
		ActorKey:      actorKey,
		SuggestStatus: probeOutcomeSuggestStatus(probeOutcome),
	}
	if probeOutcome == "dead" {
		proposal.EndLifecycle = true
		proposal.EndReason = "probe_dead"
	}

	// Probe role default = Resolved; identify_mismatch (or any outcome that
	// explicitly names identity trouble) shifts to Retryable so apply routes
	// through pending.
	role := observation.RoleResolved
	if probeOutcome == "identify_mismatch" || probeOutcome == "sender_uncertain" {
		role = observation.RoleRetryableUnresolved
	}

	evidence := []observation.EvidenceRef{
		{Key: "pid", Value: pid},
		{Key: "pane_id", Value: paneID},
		{Key: "start_time", Value: startTime},
		{Key: "probe_id", Value: probeID},
		{Key: "probe_kind", Value: probeKind},
		{Key: "probe_outcome", Value: probeOutcome},
		{Key: observation.EvidenceKeyRoleResolution, Value: role},
	}

	return observation.Observation{
		TraceID:            uuid.NewString(),
		SessionID:          sessionCode,
		ObservedGeneration: observedGeneration,
		SourceKind:         observation.SourceProbe,
		WatcherToken:       probeToken,
		Action:             "probe." + probeKind + "." + probeOutcome,
		Phase:              observation.PhaseProposed,
		Proposal:           proposal,
		Evidence:           evidence,
		ObservedAt:         now,
		Seq:                now.UnixNano(),
	}
}

// buildSweepObservation constructs a SourceSweep observation.
//
// sweep never advances generation; callers pass the current
// CurrentGeneration(frame.SessionCode) — since Frame does not carry a
// SessionCode field, the sweep wiring in Stage 3 is expected to resolve it
// via the pane->session mapping and pass observedGeneration in.
//
// If frame.ProcessStartTime == "" the builder tags the observation
// RoleTerminalUnresolved — sweep has nothing to verify against and the
// observation is dropped by apply.
func buildSweepObservation(
	frame store.Frame,
	reason string,
	observedGeneration int64,
	now time.Time,
) observation.Observation {
	// Frame.PID is int; preserve value as int64 so evidence always emits
	// int64 regardless of frame field type (divergence writer type-asserts
	// to int64).
	pid := int64(frame.PID)

	actorKey := observation.ActorKey{
		// SessionID intentionally left empty here — Frame has no session
		// identity; caller wiring in Stage 3 (sweep.go) may attach the
		// resolved session after lookup. The actor key collapses to zero
		// ActorKey if ActorID is empty (frame has AgentType==""), which the
		// observation.Builder validation tolerates.
		Generation: observedGeneration,
		ActorID:    "primary:" + frame.AgentType,
	}
	proposal := observation.StateProposal{
		ActorKey:      actorKey,
		SuggestStatus: "ended",
		EndLifecycle:  true,
		EndReason:     reason,
	}

	role := observation.RoleResolved
	if frame.ProcessStartTime == "" {
		role = observation.RoleTerminalUnresolved
	}

	evidence := []observation.EvidenceRef{
		{Key: "pid", Value: pid},
		{Key: "pane_id", Value: frame.PaneID},
		{Key: "start_time", Value: frame.ProcessStartTime},
		{Key: "reason", Value: reason},
		{Key: "frame_id", Value: frame.FrameID},
		{Key: observation.EvidenceKeyRoleResolution, Value: role},
	}

	return observation.Observation{
		TraceID:            uuid.NewString(),
		ObservedGeneration: observedGeneration,
		SourceKind:         observation.SourceSweep,
		Action:             "sweep.frame_cleared",
		Phase:              observation.PhaseProposed,
		Proposal:           proposal,
		Evidence:           evidence,
		ObservedAt:         now,
		Seq:                now.UnixNano(),
	}
}
