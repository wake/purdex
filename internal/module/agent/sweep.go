package agent

import (
	"context"
	"encoding/json"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

// frameIdleThreshold is how long a frame can sit without a LastSeenAt refresh
// before sweepOnce marks it idle and attempts an optimistic DELETE. Hook
// traffic continuously bumps LastSeenAt, so a live session will never cross
// this threshold; crossing it is a strong signal the agent process has
// silently exited without emitting SessionEnd (e.g. SIGKILL / crash).
const frameIdleThreshold = 1 * time.Hour

var (
	sweepInterval = 2 * time.Second
	sweepOnceFn   = func(m *Module) { _ = m.sweepOnce() }
	// nowFn is the time-seam used by idle_timeout checks. Tests override to
	// simulate time-travel without having to fabricate past LastSeenAt values
	// that interact awkwardly with the SQLite column types.
	nowFn = time.Now
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
	survivors := make([]store.Frame, 0, len(frames))
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
			continue
		}
		// Idle timeout: alive process + identity-verified, but LastSeenAt
		// hasn't been refreshed by any hook for frameIdleThreshold. Use
		// DeleteIfUnchanged (optimistic concurrency) so a concurrent hook
		// Upsert that just refreshed the row is not clobbered by our stale
		// baseline.
		if nowFn().UnixNano()-frame.LastSeenAt > frameIdleThreshold.Nanoseconds() {
			deleted, err := m.frames.DeleteIfUnchanged(frame.FrameID, frame.LastSeenAt)
			if err != nil {
				return err
			}
			if !deleted {
				// Concurrent refresh raced us — the row is still live.
				// Not an error; skip this frame this round.
				survivors = append(survivors, frame)
				continue
			}
			if err := m.afterFrameCleared(frame, "idle_timeout"); err != nil {
				return err
			}
			continue
		}
		survivors = append(survivors, frame)
	}
	// Phase 3.5 §4.3 — pruneDeadProxyRefs (lifted from PR-3.5b into
	// PR-3.5a per v8 L1 fix). Detach IsProxy SubagentRefs whose source
	// process is gone or has been replaced (PID reuse). Without this,
	// any hot-path SessionEnd that skipped the detach (storage error,
	// daemon crash mid-handler, removeProxyRefForSender exhaustion)
	// leaves a stale proxy ref permanently lit on the parent —
	// projection_dedup cannot hide it because there is no standalone
	// child frame to hide behind.
	panes := uniquePaneIDs(survivors)
	broadcastTs := nowFn().UnixNano()
	for _, paneID := range panes {
		m.pruneDeadProxyRefs(paneID, broadcastTs)
	}
	return nil
}

// uniquePaneIDs collects distinct pane IDs from a frame slice in the order
// they first appear. Used by sweepOnce to drive the pruneDeadProxyRefs pass
// without re-listing per-pane.
func uniquePaneIDs(frames []store.Frame) []string {
	seen := make(map[string]struct{}, len(frames))
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		if _, ok := seen[f.PaneID]; ok {
			continue
		}
		seen[f.PaneID] = struct{}{}
		out = append(out, f.PaneID)
	}
	return out
}

// pruneDeadProxyRefs detaches IsProxy SubagentRefs from every frame in the
// pane whose source process is gone or has been replaced (PID reuse). The
// hot-path SessionEnd handler is now detach-first + propagate (frame_ops.go,
// v8 L1), but a daemon crash mid-handler — or a removeProxyRefForSender
// retry exhaustion that the caller logs and continues past — can still
// leave a stale IsProxy ref on a parent. Without this sweep pass that ref
// would never be reaped: projection_dedup can't hide it because the
// standalone child it claimed is gone, so the parent shows a permanent
// lit dot.
//
// Errors from detachProxyRefWithRetry are logged via the metric increment
// failing (no-op) and otherwise swallowed — the next sweep tick (2s) gets
// another shot, consistent with sweepOnce's other best-effort passes.
func (m *Module) pruneDeadProxyRefs(paneID string, broadcastTs int64) {
	if m.frames == nil {
		return
	}
	frames, err := m.frames.ListByPane(paneID)
	if err != nil {
		return
	}
	for _, frame := range frames {
		for _, ref := range frame.Subagents {
			if !ref.IsProxy {
				continue
			}
			if isPidAliveFn(ref.SourcePID) {
				actualStart, sterr := processStartTimeFn(ref.SourcePID)
				if sterr == nil && actualStart == ref.SourceStartTime {
					// Source still alive + identity-verified — keep.
					continue
				}
			}
			// Source dead, identity unreadable, or PID reused → detach.
			detached, _, derr := m.detachProxyRefWithRetry(frame, ref.SourcePID, ref.SourceStartTime, broadcastTs)
			if derr == nil && detached {
				agentpkg.MetricSweepPrunedProxy.Add(1)
			}
		}
	}
}

// clearFrame is the eager delete path used for pid_dead / pid_reused sweeps
// (and any other call site that wants an unconditional frame removal).
// For the new idle_timeout path, sweepOnce calls DeleteIfUnchanged directly
// and then afterFrameCleared for the shared cleanup work.
func (m *Module) clearFrame(frame store.Frame, reason string) error {
	if m.frames == nil {
		return nil
	}
	if err := m.frames.Delete(frame.FrameID); err != nil {
		return err
	}
	return m.afterFrameCleared(frame, reason)
}

// afterFrameCleared handles the post-delete side effects shared by every
// sweep reason: legacy agent_events cleanup, in-memory projection sync,
// orphan Activity watcher stop (bug fix: previously only pid_dead/pid_reused
// paths forgot to call StopWatch; now centralized), and WS broadcast.
func (m *Module) afterFrameCleared(frame store.Frame, reason string) error {
	sessionName, code := m.resolvePaneSession(frame.PaneID)
	if sessionName != "" && m.events != nil {
		if err := m.events.Delete(sessionName); err != nil {
			return err
		}
	}
	projection, err := m.projectionForSession(sessionName)
	if err != nil {
		return err
	}

	var hadWatcher bool
	m.mu.Lock()
	if sessionName != "" {
		syncProjectionState(m.currentStatus, m.subagents, sessionName, projection)
		if projection == nil || projection.TopFrame == nil {
			_, hadWatcher = m.activeWatchers[sessionName]
			delete(m.activeWatchers, sessionName)
		}
	}
	m.mu.Unlock()
	if hadWatcher && m.prober != nil {
		m.prober.StopWatch(sessionName + ":")
	}

	if code == "" || m.core == nil {
		return nil
	}
	normalized := buildProjectionNormalized(projection, frame.AgentType, "sweep:"+reason, nowFn().UnixNano(), agentpkg.DeriveResult{})
	payload, _ := json.Marshal(normalized)
	m.core.Events.Broadcast(code, "hook", string(payload))
	return nil
}
