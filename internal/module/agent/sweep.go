package agent

import (
	"context"
	"encoding/json"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

var (
	sweepInterval = 2 * time.Second
	sweepOnceFn   = func(m *Module) { _ = m.sweepOnce() }
)

func (m *Module) startSweep() {
	if m.frames == nil || m.sweepCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.sweepCancel = cancel
	m.sweepWG.Add(1)
	go func() {
		defer m.sweepWG.Done()
		ticker := time.NewTicker(sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweepOnceFn(m)
			}
		}
	}()
}

func (m *Module) sweepOnce() error {
	if m.frames == nil {
		return nil
	}
	frames, err := m.frames.ListAll()
	if err != nil {
		return err
	}
	for _, frame := range frames {
		if !frame.Verified {
			continue
		}
		if !isPidAliveFn(frame.PID) {
			if err := m.clearFrame(frame, "pid_dead"); err != nil {
				return err
			}
			continue
		}
		startTime, err := processStartTimeFn(frame.PID)
		if err != nil {
			continue
		}
		if startTime != frame.ProcessStartTime {
			if err := m.clearFrame(frame, "pid_reused"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Module) clearFrame(frame store.Frame, reason string) error {
	if m.frames == nil {
		return nil
	}
	// PR-1b-1c T8 / D3: emit a SourceSweep observation before the legacy
	// clearFrame path runs. buildSweepObservation is pure; caller is
	// responsible for (a) resolving the session code since store.Frame has
	// no SessionCode field, and (b) passing the arbitrator's current
	// generation — sweep never advances gen. On a nil arbitrator (degraded
	// path) or a saturated input channel, SubmitObservation drops
	// silently so the legacy clearFrame sequence below still runs
	// (§D9 fail-open contract).
	sessionName, code := m.resolvePaneSession(frame.PaneID)
	var observedGen int64
	if m.arbitrator != nil {
		observedGen = m.arbitrator.CurrentGeneration(code)
	}
	obs := buildSweepObservation(frame, reason, observedGen, time.Now())
	obs.SessionID = code
	m.SubmitObservation(obs)

	if err := m.frames.Delete(frame.FrameID); err != nil {
		return err
	}
	if sessionName != "" && m.events != nil {
		if err := m.events.Delete(sessionName); err != nil {
			return err
		}
	}
	projection, err := m.projectionForSession(sessionName)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if sessionName != "" {
		syncProjectionState(m.currentStatus, m.subagents, sessionName, projection)
		if projection == nil || projection.TopFrame == nil {
			delete(m.activeWatchers, sessionName)
		}
	}
	m.mu.Unlock()

	if code == "" || m.core == nil {
		return nil
	}
	normalized := buildProjectionNormalized(projection, frame.AgentType, "sweep:"+reason, time.Now().UnixNano(), agentpkg.DeriveResult{})
	payload, _ := json.Marshal(normalized)
	m.core.Events.Broadcast(code, "hook", string(payload))
	return nil
}
