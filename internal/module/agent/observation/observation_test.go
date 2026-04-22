package observation_test

import (
	"encoding/json"
	"testing"

	"github.com/wake/purdex/internal/module/agent/observation"
)

func TestSourceKind_ValidValues(t *testing.T) {
	valid := []observation.SourceKind{
		observation.SourceHook,
		observation.SourceProbe,
		observation.SourceSweep,
		observation.SourceReconcile,
		observation.SourceSynthetic,
	}
	for _, sk := range valid {
		if !sk.IsValid() {
			t.Errorf("SourceKind(%q).IsValid() = false; want true", sk)
		}
	}
	if observation.SourceKind("bogus").IsValid() {
		t.Error(`SourceKind("bogus").IsValid() = true; want false`)
	}
	if observation.SourceKind("").IsValid() {
		t.Error(`SourceKind("").IsValid() = true; want false`)
	}
}

func TestObsPhase_ValidValues(t *testing.T) {
	valid := []observation.ObsPhase{
		observation.PhaseProposed,
		observation.PhaseCommitted,
		observation.PhaseRejected,
	}
	for _, p := range valid {
		if !p.IsValid() {
			t.Errorf("ObsPhase(%q).IsValid() = false; want true", p)
		}
	}
	if observation.ObsPhase("bogus").IsValid() {
		t.Error(`ObsPhase("bogus").IsValid() = true; want false`)
	}
	if observation.ObsPhase("").IsValid() {
		t.Error(`ObsPhase("").IsValid() = true; want false`)
	}
}

func TestObservation_ZeroValue_String(t *testing.T) {
	var obs observation.Observation
	got := obs.String()
	want := "Observation{trace_id: session_id: action: phase:}"
	if got != want {
		t.Errorf("Observation{}.String() = %q; want %q", got, want)
	}
}

func TestStateProposal_JSONRoundtrip(t *testing.T) {
	orig := observation.StateProposal{
		ActorKey: observation.ActorKey{
			SessionID:  "sess-abc",
			Generation: 5,
			ActorID:    "primary:main",
		},
		SuggestStatus: "active",
		EndLifecycle:  true,
		EndReason:     "replaced_by_new_primary",
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal StateProposal: %v", err)
	}

	var got observation.StateProposal
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal StateProposal: %v", err)
	}

	if got.ActorKey != orig.ActorKey {
		t.Errorf("ActorKey mismatch: got %+v; want %+v", got.ActorKey, orig.ActorKey)
	}
	if got.SuggestStatus != orig.SuggestStatus {
		t.Errorf("SuggestStatus = %q; want %q", got.SuggestStatus, orig.SuggestStatus)
	}
	if got.EndLifecycle != orig.EndLifecycle {
		t.Errorf("EndLifecycle = %v; want %v", got.EndLifecycle, orig.EndLifecycle)
	}
	if got.EndReason != orig.EndReason {
		t.Errorf("EndReason = %q; want %q", got.EndReason, orig.EndReason)
	}
}

// Covers the bool-zero-value path that JSONRoundtrip (with EndLifecycle=true)
// cannot: without omitempty, false must survive marshal/unmarshal.
func TestStateProposal_ZeroValue(t *testing.T) {
	var orig observation.StateProposal
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal zero StateProposal: %v", err)
	}

	var got observation.StateProposal
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal zero StateProposal: %v", err)
	}

	if got.EndLifecycle {
		t.Errorf("EndLifecycle = true; want false (zero-value must roundtrip)")
	}
	if got.ActorKey != (observation.ActorKey{}) {
		t.Errorf("ActorKey = %+v; want zero value", got.ActorKey)
	}
	if got.SuggestStatus != "" || got.EndReason != "" {
		t.Errorf("non-empty string fields: SuggestStatus=%q EndReason=%q", got.SuggestStatus, got.EndReason)
	}
}

func TestDecisionPort_ZeroValue_JSON(t *testing.T) {
	orig := observation.DecisionPort{}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal DecisionPort{}: %v", err)
	}

	var got observation.DecisionPort
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal DecisionPort{}: %v", err)
	}

	if got.PortID != "" {
		t.Errorf("PortID = %q; want \"\"", got.PortID)
	}
	if len(got.InputRefs) != 0 {
		t.Errorf("InputRefs len = %d; want 0", len(got.InputRefs))
	}
	if len(got.Branches) != 0 {
		t.Errorf("Branches len = %d; want 0", len(got.Branches))
	}
	if got.Selected != "" {
		t.Errorf("Selected = %q; want \"\"", got.Selected)
	}
	if got.Reason != "" {
		t.Errorf("Reason = %q; want \"\"", got.Reason)
	}
}

func TestDecisionPort_InputRefs_EvidenceRef_JSON(t *testing.T) {
	orig := observation.DecisionPort{
		PortID: "verify.ancestor_walk",
		InputRefs: []observation.EvidenceRef{
			{Key: "pid", Value: "123"},
			{Key: "hash", Value: "abc"},
		},
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got observation.DecisionPort
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(got.InputRefs) != 2 {
		t.Fatalf("InputRefs len = %d; want 2", len(got.InputRefs))
	}
	if got.InputRefs[0].Key != "pid" {
		t.Errorf("InputRefs[0].Key = %q; want \"pid\"", got.InputRefs[0].Key)
	}
	if got.InputRefs[1].Key != "hash" {
		t.Errorf("InputRefs[1].Key = %q; want \"hash\"", got.InputRefs[1].Key)
	}
}

func TestBranch_Fields(t *testing.T) {
	orig := observation.Branch{
		ID:        "hit_known_actor",
		Condition: "pid matches existing actor",
		Outcome:   "matched",
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal Branch: %v", err)
	}

	var got observation.Branch
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal Branch: %v", err)
	}

	if got.ID != orig.ID {
		t.Errorf("ID = %q; want %q", got.ID, orig.ID)
	}
	if got.Condition != orig.Condition {
		t.Errorf("Condition = %q; want %q", got.Condition, orig.Condition)
	}
	if got.Outcome != orig.Outcome {
		t.Errorf("Outcome = %q; want %q", got.Outcome, orig.Outcome)
	}
}
