package arbitrator

import (
	"github.com/wake/purdex/internal/module/agent/observation"
)

// sampler is the trace-emit reducer used by the apply pipeline to cut the
// volume of low-signal proposed traces (sweep / synthetic) to 1-in-10 per
// session+source, while preserving every committed trace and every hook/probe
// proposed trace.
//
// Single-owner contract: sampler is called only from the apply pipeline, which
// is itself driven by a single goroutine (the Arbitrator Run loop). There is
// NO internal mutex; concurrent use from multiple goroutines is a caller bug
// and will race. See D7 of docs/superpowers/plans/2026-04-22-lights-system/
// pr-1b-1c.md for the broader contract.
type sampler struct {
	// counts[sessionID][sourceKind] — monotonic invocation counter for sampled
	// source kinds (SourceSweep / SourceSynthetic, PhaseProposed only). The
	// modulo-10 decision is made on the value BEFORE increment, so the first
	// call (c = 0) always emits and every 10th call thereafter emits.
	//
	// Counter is per-session so that a chatty session cannot starve other
	// sessions' sweep traces; ClearSession recycles a session's counter when
	// its generation advances.
	counts map[string]map[observation.SourceKind]int
}

// newSampler returns an empty sampler. Caller must ensure single-goroutine
// invocation for the sampler's lifetime.
func newSampler() *sampler {
	return &sampler{
		counts: make(map[string]map[observation.SourceKind]int),
	}
}

// ShouldEmit reports whether a trace for the given (session, source, phase)
// tuple should be emitted by the apply pipeline.
//
// Rules:
//   - PhaseCommitted → always emit (fast path; counter untouched).
//   - PhaseProposed + source != sweep/synthetic → always emit (hook/probe/
//     reconcile proposals are high-signal; counter untouched).
//   - PhaseProposed + sweep/synthetic → emit the 1st, 11th, 21st ... call
//     (c % 10 == 0, evaluated BEFORE increment), drop all others.
//
// Callers serialize all invocations (single-goroutine owner). No locking.
func (s *sampler) ShouldEmit(
	sessionID string,
	source observation.SourceKind,
	phase observation.ObsPhase,
) bool {
	// Committed is always emitted, regardless of source.
	if phase == observation.PhaseCommitted {
		return true
	}
	// Only sweep / synthetic proposals are sampled; everything else emits.
	if source != observation.SourceSweep && source != observation.SourceSynthetic {
		return true
	}

	sessCounts, ok := s.counts[sessionID]
	if !ok {
		sessCounts = make(map[observation.SourceKind]int)
		s.counts[sessionID] = sessCounts
	}
	c := sessCounts[source]
	sessCounts[source] = c + 1
	return c%10 == 0
}

// ClearSession reclaims the per-session counter map for sessionID. Called by
// the SessionStart helper when a session's generation advances, so post-restart
// sweeps start fresh (first sweep always emits). No-op if sessionID is unknown.
func (s *sampler) ClearSession(sessionID string) {
	delete(s.counts, sessionID)
}
