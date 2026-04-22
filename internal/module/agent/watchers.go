package agent

import (
	"context"
	"time"

	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/module/agent/observation"
)

// watcherKey scopes a Module-managed probe Watcher to (sessionName, kind).
// sessionName (tmux session name) is used because probe targets are already
// keyed by tmux target; the module code translates to session code only
// when submitting the observation.
type watcherKey struct {
	sessionName string
	kind        probe.Kind
}

// registerWatcher stores w under (sessionName, kind). A previous watcher
// under the same key is Stop()'d so we never leak goroutines across
// replacements. Safe to call concurrently.
func (m *Module) registerWatcher(sessionName string, kind probe.Kind, w probe.Watcher) {
	m.watchersMu.Lock()
	if m.watchers == nil {
		m.watchers = make(map[watcherKey]probe.Watcher)
	}
	key := watcherKey{sessionName: sessionName, kind: kind}
	prev, ok := m.watchers[key]
	m.watchers[key] = w
	m.watchersMu.Unlock()
	if ok && prev != nil {
		prev.Stop()
	}
}

// takeWatcher removes and returns the watcher registered for (sessionName,
// kind). The caller is responsible for calling Stop(). Returns (nil, false)
// when no watcher was registered.
func (m *Module) takeWatcher(sessionName string, kind probe.Kind) (probe.Watcher, bool) {
	m.watchersMu.Lock()
	defer m.watchersMu.Unlock()
	key := watcherKey{sessionName: sessionName, kind: kind}
	w, ok := m.watchers[key]
	if !ok {
		return nil, false
	}
	delete(m.watchers, key)
	return w, true
}

// stopWatchersForSession stops every watcher registered under sessionName.
// Used on session teardown so no probe goroutine outlives the session.
// Returns the number of watchers actually stopped.
func (m *Module) stopWatchersForSession(sessionName string) int {
	m.watchersMu.Lock()
	keys := make([]watcherKey, 0, len(m.watchers))
	for k := range m.watchers {
		if k.sessionName == sessionName {
			keys = append(keys, k)
		}
	}
	victims := make([]probe.Watcher, 0, len(keys))
	for _, k := range keys {
		if w := m.watchers[k]; w != nil {
			victims = append(victims, w)
		}
		delete(m.watchers, k)
	}
	m.watchersMu.Unlock()

	for _, w := range victims {
		w.Stop()
	}
	return len(victims)
}

// stopAllWatchers stops every Module-managed Watcher.
func (m *Module) stopAllWatchers() {
	m.watchersMu.Lock()
	victims := make([]probe.Watcher, 0, len(m.watchers))
	for _, w := range m.watchers {
		if w != nil {
			victims = append(victims, w)
		}
	}
	m.watchers = nil
	m.watchersMu.Unlock()

	for _, w := range victims {
		w.Stop()
	}
}

// probeOnOutcome returns a probe.Callback that, on every Watcher firing,
// builds a probe Observation via buildProbeObservation and submits it to
// the Arbitrator input channel through Module.SubmitObservation.
//
// Identity triple resolution: probe targets are tmux sessions, which may
// host multiple panes. We select the TopFrame of the matching
// SessionProjection so the observation carries a self-consistent
// (pane_id, pid, start_time). If no frame exists for the session the
// callback drops the event silently.
func (m *Module) probeOnOutcome(sessionName, agentType string, kind probe.Kind) probe.Callback {
	return func(outcome probe.Outcome, token string, evidence []probe.EvidenceRef) {
		if m.arbitrator == nil {
			return
		}
		sessionCode := m.resolveSessionCode(sessionName)
		if sessionCode == "" {
			return
		}
		projection, err := m.projectionForSession(sessionName)
		if err != nil || projection == nil || projection.TopFrame == nil {
			return
		}
		frame := projection.TopFrame

		observedGen := m.arbitrator.CurrentGeneration(sessionCode)
		probeID := extractProbeID(evidence)

		obs := buildProbeObservation(
			sessionCode,
			frame.PaneID,
			int64(frame.PID),
			frame.ProcessStartTime,
			agentType,
			observedGen,
			probeID,
			token,
			string(kind),
			string(outcome),
			time.Now(),
		)

		obs.Evidence = mergeProbeEvidence(obs.Evidence, evidence)

		m.SubmitObservation(obs)
	}
}

// extractProbeID pulls the probe_id tag the probe watcher minted. Returns
// "" when not found; buildProbeObservation tolerates the empty string.
func extractProbeID(ev []probe.EvidenceRef) string {
	for _, e := range ev {
		if e.Key == "probe_id" {
			if s, ok := e.Value.(string); ok {
				return s
			}
		}
	}
	return ""
}

// mergeProbeEvidence appends non-duplicate probe.EvidenceRef entries onto
// the builder's observation.EvidenceRef slice. Duplicate keys are skipped
// so the builder's canonical identity+role emission cannot be shadowed.
func mergeProbeEvidence(existing []observation.EvidenceRef, extras []probe.EvidenceRef) []observation.EvidenceRef {
	if len(extras) == 0 {
		return existing
	}
	seen := make(map[string]struct{}, len(existing))
	for _, e := range existing {
		seen[e.Key] = struct{}{}
	}
	out := existing
	for _, ex := range extras {
		if _, ok := seen[ex.Key]; ok {
			continue
		}
		out = append(out, observation.EvidenceRef{Key: ex.Key, Value: ex.Value})
		seen[ex.Key] = struct{}{}
	}
	return out
}

// getWatchersContext returns the context all Module-managed watchers are
// parented to. Created lazily so tests bypassing Start can still construct
// watchers.
func (m *Module) getWatchersContext() context.Context {
	m.watchersMu.Lock()
	defer m.watchersMu.Unlock()
	if m.watchersCtx == nil {
		m.watchersCtx, m.watchersCancel = context.WithCancel(context.Background())
	}
	return m.watchersCtx
}

// cancelWatchersContext cancels every Module-managed watcher's parent
// context.
func (m *Module) cancelWatchersContext() {
	m.watchersMu.Lock()
	cancel := m.watchersCancel
	m.watchersCancel = nil
	m.watchersCtx = nil
	m.watchersMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// startLivenessWatcher spins up a liveness probe Watcher for the given
// session and registers it under the Module's watcher map.
func (m *Module) startLivenessWatcher(sessionName, agentType string) probe.Watcher {
	if m.prober == nil {
		return nil
	}
	ctx := m.getWatchersContext()
	w := probe.StartLiveness(ctx, probe.Spec{
		Target:    sessionName + ":",
		AgentType: agentType,
	}, m.prober.IsAliveFor, m.probeOnOutcome(sessionName, agentType, probe.KindLiveness))
	m.registerWatcher(sessionName, probe.KindLiveness, w)
	return w
}

// startActivityWatcher spins up an activity probe Watcher.
func (m *Module) startActivityWatcher(sessionName, agentType string) probe.Watcher {
	if m.prober == nil {
		return nil
	}
	ctx := m.getWatchersContext()
	w := probe.StartActivity(ctx, probe.Spec{
		Target:    sessionName + ":",
		AgentType: agentType,
	}, m.prober.StartWatch, m.prober.StopWatch, m.probeOnOutcome(sessionName, agentType, probe.KindActivity))
	m.registerWatcher(sessionName, probe.KindActivity, w)
	return w
}

// startReadinessWatcher spins up a readiness probe Watcher.
func (m *Module) startReadinessWatcher(sessionName, agentType string) probe.Watcher {
	if m.prober == nil {
		return nil
	}
	ctx := m.getWatchersContext()
	w := probe.StartReadiness(ctx, probe.Spec{
		Target:    sessionName + ":",
		AgentType: agentType,
	}, m.prober.CheckReadiness, m.probeOnOutcome(sessionName, agentType, probe.KindReadiness))
	m.registerWatcher(sessionName, probe.KindReadiness, w)
	return w
}
