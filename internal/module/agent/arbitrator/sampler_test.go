package arbitrator

import (
	"testing"

	"github.com/wake/purdex/internal/module/agent/observation"
)

// Sampler is the trace-emit reducer inside the apply pipeline. It is owned by
// a single goroutine (the Arbitrator Run loop) and therefore intentionally
// carries no mutex. Tests here assert only the ShouldEmit contract under
// serialized calls — concurrent-access coverage is the apply pipeline's job
// (Stage 2 T5), per D7 of the PR-1b-1c plan.

// TestSampler_Committed_AlwaysEmits verifies that PhaseCommitted always emits
// regardless of source — even sweep/synthetic, which are sampled 1-in-10 when
// proposed. Runs 20 iterations per sampled source to ensure the committed-fast-path
// bypasses the counter entirely.
func TestSampler_Committed_AlwaysEmits(t *testing.T) {
	s := newSampler()
	sources := []observation.SourceKind{
		observation.SourceHook,
		observation.SourceProbe,
		observation.SourceSweep,
		observation.SourceSynthetic,
		observation.SourceReconcile,
	}
	for _, src := range sources {
		for i := 0; i < 20; i++ {
			if !s.ShouldEmit("sess-A", src, observation.PhaseCommitted) {
				t.Errorf("ShouldEmit(sess-A, %q, committed) iter %d = false; want true", src, i)
			}
		}
	}
}

// TestSampler_Proposed_Hook_AlwaysEmits verifies that SourceHook + PhaseProposed
// always emits (hook proposals are not sampled).
func TestSampler_Proposed_Hook_AlwaysEmits(t *testing.T) {
	s := newSampler()
	for i := 0; i < 20; i++ {
		if !s.ShouldEmit("sess-A", observation.SourceHook, observation.PhaseProposed) {
			t.Errorf("ShouldEmit(sess-A, hook, proposed) iter %d = false; want true", i)
		}
	}
}

// TestSampler_Proposed_Probe_AlwaysEmits verifies that SourceProbe + PhaseProposed
// always emits (probe proposals are not sampled).
func TestSampler_Proposed_Probe_AlwaysEmits(t *testing.T) {
	s := newSampler()
	for i := 0; i < 20; i++ {
		if !s.ShouldEmit("sess-A", observation.SourceProbe, observation.PhaseProposed) {
			t.Errorf("ShouldEmit(sess-A, probe, proposed) iter %d = false; want true", i)
		}
	}
}

// TestSampler_Proposed_Sweep_1In10 verifies that SourceSweep + PhaseProposed is
// sampled 1-in-10: the 1st, 11th, 21st ... calls emit; all others drop.
func TestSampler_Proposed_Sweep_1In10(t *testing.T) {
	s := newSampler()
	// Iterations 1..30 — call indices 0..29.
	for i := 0; i < 30; i++ {
		got := s.ShouldEmit("sess-A", observation.SourceSweep, observation.PhaseProposed)
		want := i%10 == 0 // 1st (i=0), 11th (i=10), 21st (i=20) emit
		if got != want {
			t.Errorf("ShouldEmit(sess-A, sweep, proposed) iter %d = %v; want %v", i, got, want)
		}
	}
}

// TestSampler_Proposed_Synthetic_1In10 verifies the same 1-in-10 sampling for
// SourceSynthetic + PhaseProposed.
func TestSampler_Proposed_Synthetic_1In10(t *testing.T) {
	s := newSampler()
	for i := 0; i < 30; i++ {
		got := s.ShouldEmit("sess-A", observation.SourceSynthetic, observation.PhaseProposed)
		want := i%10 == 0
		if got != want {
			t.Errorf("ShouldEmit(sess-A, synthetic, proposed) iter %d = %v; want %v", i, got, want)
		}
	}
}

// TestSampler_CrossSession_IndependentCounters verifies that per-session counters
// do not interfere: session A may have burned 9 sweeps (all dropped after the
// first emit) but session B's first sweep must still emit, since B's counter
// starts from 0.
func TestSampler_CrossSession_IndependentCounters(t *testing.T) {
	s := newSampler()

	// Session A: 9 proposed sweeps.
	// Call 1 (i=0) emits; calls 2..9 (i=1..8) drop.
	for i := 0; i < 9; i++ {
		got := s.ShouldEmit("sess-A", observation.SourceSweep, observation.PhaseProposed)
		want := i == 0
		if got != want {
			t.Errorf("sess-A iter %d = %v; want %v", i, got, want)
		}
	}

	// Session B: first sweep should emit (independent counter).
	if !s.ShouldEmit("sess-B", observation.SourceSweep, observation.PhaseProposed) {
		t.Error("sess-B first sweep = false; want true (per-session counters must be independent)")
	}

	// Session B's second sweep should drop (i=1 → 1%10 != 0).
	if s.ShouldEmit("sess-B", observation.SourceSweep, observation.PhaseProposed) {
		t.Error("sess-B second sweep = true; want false")
	}

	// Session A's 10th call (i=9) should still drop.
	if s.ShouldEmit("sess-A", observation.SourceSweep, observation.PhaseProposed) {
		t.Error("sess-A 10th sweep = true; want false (counter is 9, 9%10 != 0)")
	}

	// Session A's 11th call (i=10) should emit.
	if !s.ShouldEmit("sess-A", observation.SourceSweep, observation.PhaseProposed) {
		t.Error("sess-A 11th sweep = false; want true (counter is 10, 10%10 == 0)")
	}
}

// TestSampler_ClearSession_ResetsCounts verifies that ClearSession reclaims the
// per-session counter map so the next ShouldEmit for that session starts at 0
// (and therefore emits).
func TestSampler_ClearSession_ResetsCounts(t *testing.T) {
	s := newSampler()

	// Burn 5 sweeps on session A.
	// Call 1 (i=0) emits, calls 2..5 drop.
	for i := 0; i < 5; i++ {
		got := s.ShouldEmit("sess-A", observation.SourceSweep, observation.PhaseProposed)
		want := i == 0
		if got != want {
			t.Errorf("pre-clear iter %d = %v; want %v", i, got, want)
		}
	}

	// Clear session A — counter map for sess-A should be reclaimed.
	s.ClearSession("sess-A")

	// Next call on sess-A should emit (counter restarted from 0).
	if !s.ShouldEmit("sess-A", observation.SourceSweep, observation.PhaseProposed) {
		t.Error("post-clear first sweep = false; want true (ClearSession should reset counter)")
	}

	// Second post-clear call should drop (counter is 1).
	if s.ShouldEmit("sess-A", observation.SourceSweep, observation.PhaseProposed) {
		t.Error("post-clear second sweep = true; want false")
	}
}
