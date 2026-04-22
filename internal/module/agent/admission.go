package agent

import (
	"log"
	"time"

	"github.com/wake/purdex/internal/module/agent/arbitrator"
	"github.com/wake/purdex/internal/module/agent/observation"
)

// committedBlockingRetry is the bounded wait committed-phase observations get
// when arb_in is full (spec §3.5.1, plan D9).
const committedBlockingRetry = 100 * time.Millisecond

// SubmitObservation hands obs to the Arbitrator input channel with the
// admission-priority back-pressure policy from spec §3.5.1 / plan D9:
//
//   - Committed-phase observations get a fast non-blocking attempt; on full
//     channel they fall back to a bounded 100ms blocking retry. If the timer
//     fires, the observation is dropped and lights_arb_in_dropped{priority=
//     committed} is incremented.
//   - Proposed-phase and rejected-phase observations are best-effort: on full
//     channel they are dropped immediately and lights_arb_in_dropped{priority=
//     proposed} is incremented. Rejected is not elevated to committed so a
//     flood of rejects cannot elbow past a healthy committed observation.
//
// The function is a no-op when m.arbitrator is nil (degraded path — e.g.
// traces store missing in New).
func (m *Module) SubmitObservation(obs observation.Observation) {
	if m.arbitrator == nil {
		return
	}
	ch := m.arbitrator.InCh()

	// Fast-path non-blocking attempt for every observation.
	select {
	case ch <- obs:
		return
	default:
	}

	// Slow path: committed gets a bounded blocking retry; everything else drops.
	if obs.Phase == observation.PhaseCommitted {
		timer := time.NewTimer(committedBlockingRetry)
		defer timer.Stop()
		select {
		case ch <- obs:
			return
		case <-timer.C:
			arbitrator.Inc("lights_arb_in_dropped", "priority=committed")
			log.Printf("[agent][arbitrator] arb_in full: committed observation dropped: %s", obs)
			return
		}
	}

	arbitrator.Inc("lights_arb_in_dropped", "priority=proposed")
}
