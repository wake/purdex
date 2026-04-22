// Package observation defines the core data model for the Lights observation
// pipeline. It is intentionally free of business logic and side-effects; all
// construction and validation lives in builder.go (Task 2), and the
// TraceIDRegistry lives in trace_id.go (Task 3).
package observation

import (
	"fmt"
	"time"
)

// SourceKind identifies the origin of an Observation.
type SourceKind string

const (
	// SourceHook is produced by a tmux hook callback.
	SourceHook SourceKind = "hook"
	// SourceProbe is produced by a periodic probe.
	SourceProbe SourceKind = "probe"
	// SourceSweep is produced by a background sweep pass.
	SourceSweep SourceKind = "sweep"
	// SourceReconcile is produced by a reconcile loop (observer only).
	SourceReconcile SourceKind = "reconcile"
	// SourceSynthetic is produced by internal inference (e.g. timeout).
	SourceSynthetic SourceKind = "synthetic"
)

// IsValid reports whether sk is one of the five defined SourceKind values.
func (sk SourceKind) IsValid() bool {
	switch sk {
	case SourceHook, SourceProbe, SourceSweep, SourceReconcile, SourceSynthetic:
		return true
	}
	return false
}

// ObsPhase represents the lifecycle phase of an Observation.
type ObsPhase string

const (
	// PhaseProposed means the Observation has been submitted but not yet
	// accepted by the Arbitrator.
	PhaseProposed ObsPhase = "proposed"
	// PhaseCommitted means the Arbitrator has applied the Observation's
	// proposal to the frame.
	PhaseCommitted ObsPhase = "committed"
	// PhaseRejected means the Arbitrator rejected the Observation's proposal.
	PhaseRejected ObsPhase = "rejected"
)

// IsValid reports whether p is one of the three defined ObsPhase values.
func (p ObsPhase) IsValid() bool {
	switch p {
	case PhaseProposed, PhaseCommitted, PhaseRejected:
		return true
	}
	return false
}

// StateProposal captures what an Observation proposes should happen to a
// specific actor (identified by ActorKey).
type StateProposal struct {
	ActorKey      ActorKey `json:"actor_key"`
	SuggestStatus string   `json:"suggest_status"`
	EndLifecycle  bool     `json:"end_lifecycle"`
	EndReason     string   `json:"end_reason"`
}

// Branch is one possible outcome within a DecisionPort.
type Branch struct {
	ID        string `json:"id"`
	Condition string `json:"condition"`
	Outcome   string `json:"outcome"` // matched | rejected | skipped
}

// DecisionPort records the inputs, branches, and selected outcome for a single
// decision point within an Observation. Supports goal #4: flow-chart tracing.
//
// A single Observation may contain 0–16 DecisionPorts. More than 16 is a
// programming error (builder enforces the cap in Task 2).
type DecisionPort struct {
	PortID    string        `json:"port_id"`
	InputRefs []EvidenceRef `json:"input_refs"`
	Branches  []Branch      `json:"branches"`
	Selected  string        `json:"selected"`
	Reason    string        `json:"reason"`
}

// Observation is the top-level data model for the Lights observation pipeline.
// Field order follows spec §3.3 line 208-225.
//
// Observations are produced by hooks, probes, sweeps, reconcile loops, or
// synthetic internal inference, and are consumed by the Arbitrator (PR-1b-1b).
type Observation struct {
	TraceID            string         `json:"trace_id"`
	SpanID             string         `json:"span_id"`
	ParentSpanID       string         `json:"parent_span_id"`
	SessionID          string         `json:"session_id"`
	ObservedGeneration int64          `json:"observed_generation"`
	SourceKind         SourceKind     `json:"source_kind"`
	WatcherToken       string         `json:"watcher_token"`
	Action             string         `json:"action"`
	Phase              ObsPhase       `json:"phase"`
	Proposal           StateProposal  `json:"proposal"`
	ReasonCode         string         `json:"reason_code"`
	ReasonText         string         `json:"reason_text"`
	DecisionPorts      []DecisionPort `json:"decision_ports"`
	Evidence           []EvidenceRef  `json:"evidence"`
	ObservedAt         time.Time      `json:"observed_at"`
	Seq                int64          `json:"seq"`
}

// String returns a concise, human-readable summary of the Observation. It is
// safe to call on a zero value and must not panic.
func (o Observation) String() string {
	return fmt.Sprintf("Observation{trace_id:%s session_id:%s action:%s phase:%s}",
		o.TraceID, o.SessionID, o.Action, o.Phase)
}
