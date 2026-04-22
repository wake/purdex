package arbitrator

import (
	"testing"

	"github.com/wake/purdex/internal/module/agent/observation"
)

// TestAdmissionPriority_Committed verifies that Phase=committed observations
// map to the higher AdmissionCommitted priority.
func TestAdmissionPriority_Committed(t *testing.T) {
	obs := observation.Observation{Phase: observation.PhaseCommitted}
	if got := admissionPriority(obs); got != AdmissionCommitted {
		t.Fatalf("admissionPriority(PhaseCommitted) = %v, want %v", got, AdmissionCommitted)
	}
}

// TestAdmissionPriority_Proposed verifies that Phase=proposed observations
// map to AdmissionProposed.
func TestAdmissionPriority_Proposed(t *testing.T) {
	obs := observation.Observation{Phase: observation.PhaseProposed}
	if got := admissionPriority(obs); got != AdmissionProposed {
		t.Fatalf("admissionPriority(PhaseProposed) = %v, want %v", got, AdmissionProposed)
	}
}

// TestAdmissionPriority_Rejected_Proposed verifies that Phase=rejected does
// NOT get promoted to committed — it stays at the proposed admission tier.
func TestAdmissionPriority_Rejected_Proposed(t *testing.T) {
	obs := observation.Observation{Phase: observation.PhaseRejected}
	if got := admissionPriority(obs); got != AdmissionProposed {
		t.Fatalf("admissionPriority(PhaseRejected) = %v, want %v", got, AdmissionProposed)
	}
}

// TestDropPriority_AllMatrix verifies the 7 canonical (SourceKind, ObsPhase)
// combinations map to the drop-priority table in plan D10.2.
func TestDropPriority_AllMatrix(t *testing.T) {
	cases := []struct {
		name   string
		source observation.SourceKind
		phase  observation.ObsPhase
		want   int
	}{
		{"hook_committed", observation.SourceHook, observation.PhaseCommitted, 0},
		{"probe_committed", observation.SourceProbe, observation.PhaseCommitted, 1},
		{"hook_proposed", observation.SourceHook, observation.PhaseProposed, 2},
		{"probe_proposed", observation.SourceProbe, observation.PhaseProposed, 3},
		{"sweep_proposed", observation.SourceSweep, observation.PhaseProposed, 4},
		{"synthetic_proposed", observation.SourceSynthetic, observation.PhaseProposed, 5},
		{"reconcile_proposed", observation.SourceReconcile, observation.PhaseProposed, 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dropPriority(c.source, c.phase)
			if got != c.want {
				t.Fatalf("dropPriority(%s, %s) = %d, want %d", c.source, c.phase, got, c.want)
			}
		})
	}
}

// TestDropPriority_RejectedPhase_TreatedAsProposedTier verifies that a
// rejected-phase observation falls back to the same tier as its proposed
// counterpart (rejected never outranks committed).
func TestDropPriority_RejectedPhase_TreatedAsProposedTier(t *testing.T) {
	cases := []struct {
		name   string
		source observation.SourceKind
		want   int
	}{
		{"hook_rejected_as_proposed", observation.SourceHook, 2},
		{"probe_rejected_as_proposed", observation.SourceProbe, 3},
		{"sweep_rejected_as_proposed", observation.SourceSweep, 4},
		{"synthetic_rejected_as_proposed", observation.SourceSynthetic, 5},
		{"reconcile_rejected_as_proposed", observation.SourceReconcile, 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dropPriority(c.source, observation.PhaseRejected)
			if got != c.want {
				t.Fatalf("dropPriority(%s, rejected) = %d, want %d (same tier as proposed)", c.source, got, c.want)
			}
		})
	}
}

// TestDropPriority_UnknownSource_FallsBackToProposed verifies that an
// unknown SourceKind does not panic and lands at the lowest-priority
// (highest DropPriority number) bucket so stray records don't get unfairly
// retained.
func TestDropPriority_UnknownSource_FallsBackToProposed(t *testing.T) {
	var unknown observation.SourceKind = "mystery"

	committed := dropPriority(unknown, observation.PhaseCommitted)
	proposed := dropPriority(unknown, observation.PhaseProposed)
	rejected := dropPriority(unknown, observation.PhaseRejected)

	// All unknown-source records should be ≥ the highest defined proposed
	// tier (reconcile_proposed = 6) so they never outrank a known record.
	if committed < 6 || proposed < 6 || rejected < 6 {
		t.Fatalf("unknown source should fall back to ≥6 (committed=%d proposed=%d rejected=%d)", committed, proposed, rejected)
	}
}

// TestReasonCodes_NotEmpty verifies every sentinel reason code is a
// non-empty string constant.
func TestReasonCodes_NotEmpty(t *testing.T) {
	codes := map[string]string{
		"ReasonStaleGeneration":               ReasonStaleGeneration,
		"ReasonUnauthorizedGenerationBump":    ReasonUnauthorizedGenerationBump,
		"ReasonStaleWatcher":                  ReasonStaleWatcher,
		"ReasonDuplicateObservation":          ReasonDuplicateObservation,
		"ReasonActorEnded":                    ReasonActorEnded,
		"ReasonPidTreeUnresolvable":           ReasonPidTreeUnresolvable,
		"ReasonPendingEvicted":                ReasonPendingEvicted,
		"ReasonHookStormDropped":              ReasonHookStormDropped,
		"ReasonReconcileStaleNoted":           ReasonReconcileStaleNoted,
		"ReasonSessionRestartCleared":         ReasonSessionRestartCleared,
		"ReasonAuthoritativeNotSupportedPhase1": ReasonAuthoritativeNotSupportedPhase1,
	}
	for name, val := range codes {
		if val == "" {
			t.Errorf("%s is empty", name)
		}
	}
}

// TestReasonCodes_Unique verifies all sentinel reason-code string values are
// distinct — accidental duplicates would collapse trace categories.
func TestReasonCodes_Unique(t *testing.T) {
	codes := []string{
		ReasonStaleGeneration,
		ReasonUnauthorizedGenerationBump,
		ReasonStaleWatcher,
		ReasonDuplicateObservation,
		ReasonActorEnded,
		ReasonPidTreeUnresolvable,
		ReasonPendingEvicted,
		ReasonHookStormDropped,
		ReasonReconcileStaleNoted,
		ReasonSessionRestartCleared,
		ReasonAuthoritativeNotSupportedPhase1,
	}
	seen := make(map[string]int, len(codes))
	for i, c := range codes {
		if prev, ok := seen[c]; ok {
			t.Errorf("duplicate reason-code value %q at indices %d and %d", c, prev, i)
			continue
		}
		seen[c] = i
	}
	if len(seen) != len(codes) {
		t.Fatalf("expected %d unique reason codes, got %d", len(codes), len(seen))
	}
}

// TestApplyOutcome_DistinctValues verifies the four applyOutcome enum
// values are pairwise distinct.
func TestApplyOutcome_DistinctValues(t *testing.T) {
	vals := []applyOutcome{
		outcomeAccepted,
		outcomeRejected,
		outcomePendingStash,
		outcomeReplacedPrimary,
	}
	seen := make(map[applyOutcome]struct{}, len(vals))
	for _, v := range vals {
		if _, ok := seen[v]; ok {
			t.Fatalf("duplicate applyOutcome value %d", v)
		}
		seen[v] = struct{}{}
	}
}
