package agent

import (
	"context"
	"encoding/json"
	"fmt"
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
			// Codex round 3 #R2 fix: identity unverifiable for this
			// frame's owner — keep it as a survivor so the pane still
			// enters pruneDeadProxyRefs below. Previously this branch
			// did a bare `continue`, which bypassed the prune pass for
			// every pane whose owner identity read transiently failed,
			// defeating round 2 #O3's per-ref fail-safe at the pane
			// level. Treating the frame as a survivor here is conserva-
			// tive (don't trigger destructive pid_reused cleanup on a
			// read error) and consistent with pruneDeadProxyRefs's own
			// fail-safe, which only detaches refs on CONFIRMED dead /
			// reused source.
			survivors = append(survivors, frame)
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
		// PR-3.5b §2.1 — canonicalize first so newly-attached proxy
		// refs land before pruneDeadProxyRefs validates this pane's
		// proxy refs. Both passes are identity-gated (live + start_time
		// match), so a freshly attached canonical ref is recognized as
		// healthy by prune in the same tick. Reverse order would also
		// work but canonicalize-first is the natural narrative: fix
		// partials, then verify pane health.
		m.canonicalizePane(paneID, broadcastTs)
		m.pruneDeadProxyRefs(paneID, broadcastTs)
	}
	return nil
}

// canonicalizePane folds standalone live cross-type child frames into
// their canonical ancestor's proxy ref within the same pane. Defense-
// in-depth backstop for PR-3.5a hot-path canonicalization paths that
// left a partial state (DeleteIfUnchanged failure / readProcessInfoFn
// transient error / existing-frame SessionStart path that does not run
// self-as-descendant reconcile).
//
// User-visible correctness is already guaranteed by projection dedup
// (PR-3.5a §2.4) — this pass closes the DB-level eventual consistency
// loop in ≤ 2s instead of waiting for the child's own SessionEnd /
// pid_dead / 1h idle timeout.
//
// Single-direction rule (matches hot path): descendant becomes proxy
// of ancestor; ancestor is never reverted to descendant. Identity gate
// applied to BOTH candidate and ancestor — stale (PID-reused) frames
// never participate, leaving them for pid_reused / pid_dead cleanup.
//
// Owned-state guard: candidates carrying native subagent refs or live
// identity-verified IsProxy refs are preserved (folding would lose
// state). See candidateHasOwnedState in frame_ops.go for the full
// classifier shared with the hot path. The round 1 high finding
// (sweep erasing child native state during partial recovery) is
// gated by this guard; IT10n is the regression test.
//
// Best-effort: any storage error / failed gate / failed Upsert / failed
// DeleteIfUnchanged makes this candidate skip; next sweep tick (2s)
// retries. No rollback on partial.
func (m *Module) canonicalizePane(paneID string, broadcastTs int64) {
	if m.frames == nil {
		return
	}
	frames, err := m.frames.ListByPane(paneID)
	if err != nil {
		return
	}
	framesByPID := make(map[int]store.Frame, len(frames))
	for _, frame := range frames {
		framesByPID[frame.PID] = frame
	}
	canonicalizedAny := false
	var anyAncestor store.Frame
	for _, candidate := range frames {
		// Candidate identity gate — stale (dead PID / PID-reuse) frames
		// skip canonicalize; pid_dead / pid_reused passes handle them.
		if !isPidAliveFn(candidate.PID) {
			continue
		}
		actualStart, sterr := processStartTimeFn(candidate.PID)
		if sterr != nil || actualStart != candidate.ProcessStartTime {
			continue
		}
		// PR-3.5b §2.2.1 — protect candidates that own LIVE state.
		// Mirrors hot-path canonicalizeDescendantsAfterUpsert via the
		// shared candidateHasOwnedState helper (frame_ops.go) so any
		// drift between hot-path and sweep is impossible at compile
		// time.
		if candidateHasOwnedState(candidate) {
			continue
		}
		ancestor, found := m.findCanonicalAncestor(candidate, framesByPID)
		if !found {
			continue
		}
		ref := agentpkg.SubagentRef{
			ID:              fmt.Sprintf("proxy:%s:%d:%s", candidate.AgentType, candidate.PID, candidate.ProcessStartTime),
			Type:            candidate.AgentType,
			StartedAt:       broadcastTs,
			SourcePID:       candidate.PID,
			SourceStartTime: candidate.ProcessStartTime,
			IsProxy:         true,
		}
		attached, parentStored, aerr := m.attachProxyRefWithRetry(ancestor, ref, broadcastTs)
		if aerr != nil || !attached {
			continue
		}
		deleted, _ := m.frames.DeleteIfUnchanged(candidate.FrameID, candidate.LastSeenAt)
		if !deleted {
			// Partial — concurrent refresh / hot-path won the race.
			// Next sweep tick re-evaluates. Projection dedup already
			// hides this for SPA. No rollback. Per plan: success =
			// attach + delete both, so MetricSweepCanonicalized is
			// NOT incremented in the partial case.
			continue
		}
		agentpkg.MetricSweepCanonicalized.Add(1)
		canonicalizedAny = true
		anyAncestor = parentStored
	}
	// Broadcast wiring lands in commit 8 (broadcastProxyCanonicalized).
	// Until then this is a no-op except for storage + metric.
	if canonicalizedAny {
		_ = anyAncestor
	}
}

// findCanonicalAncestor walks descendant's PPID chain looking for a
// cross-type frame in the same pane that is live and identity-verified.
// Caps walk at proxyMaxDepth (5) to bound syscall cost.
//
// Returns (frame, true) on a successful match. Returns (zero, false)
// when:
//   - readProcessInfoFn errors mid-walk (transient — next sweep tick retries)
//   - PPID hits init (<=1) or self-loop (PPID == current PID)
//   - depth exhausted without finding a frame in the pane
//   - a same-type ancestor is encountered (cross-type-only proxy semantics;
//     a same-type ancestor in chain means we're "inside" the same agent
//     tree and won't find a different cross-type ancestor higher up either,
//     by design — mirrors findProxyParent's same-type hard-stop)
//   - the matched ancestor fails identity gate (dead PID / PID-reused)
//
// Note: framesByPID is keyed by PID, not (PID, paneID), but caller passes
// only frames from a single pane (ListByPane), so cross-pane PID
// collision is impossible.
func (m *Module) findCanonicalAncestor(candidate store.Frame, framesByPID map[int]store.Frame) (store.Frame, bool) {
	info, err := readProcessInfoFn(candidate.PID)
	if err != nil {
		return store.Frame{}, false
	}
	ppid := info.PPID
	for depth := 0; depth < proxyMaxDepth; depth++ {
		if ppid <= 1 {
			return store.Frame{}, false
		}
		ancestor, ok := framesByPID[ppid]
		if ok {
			if ancestor.AgentType == candidate.AgentType {
				// Same-type — not a proxy relationship. Hard-stop the
				// walk (mirrors findProxyParent semantics).
				return store.Frame{}, false
			}
			if isPidAliveFn(ancestor.PID) {
				actualStart, sterr := processStartTimeFn(ancestor.PID)
				if sterr == nil && actualStart == ancestor.ProcessStartTime {
					return ancestor, true
				}
			}
			// Ancestor matched in pane but failed identity gate — keep
			// walking; a deeper ancestor might still match.
		}
		ancestorInfo, err := readProcessInfoFn(ppid)
		if err != nil {
			return store.Frame{}, false
		}
		if ancestorInfo.PPID == ppid {
			return store.Frame{}, false
		}
		ppid = ancestorInfo.PPID
	}
	return store.Frame{}, false
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
//
// Codex round 2 #P1 fix: after at least one successful detach in the pane,
// emit a "hook" broadcast with reason=sweep:proxy_pruned so SPA + in-memory
// state (m.subagents / m.currentStatus) reflect the change immediately.
// Mirrors the afterFrameCleared broadcast that pid_dead/pid_reused/idle_timeout
// already emit. Per-pane (not per-detach) so multiple stale refs in the same
// pane coalesce into one broadcast — matches afterFrameCleared's per-frame
// granularity.
func (m *Module) pruneDeadProxyRefs(paneID string, broadcastTs int64) {
	if m.frames == nil {
		return
	}
	frames, err := m.frames.ListByPane(paneID)
	if err != nil {
		return
	}
	detachedAny := false
	var anyOwner store.Frame
	for _, frame := range frames {
		for _, ref := range frame.Subagents {
			if !ref.IsProxy {
				continue
			}
			// Codex round 2 #O3 fix: detach only on CONFIRMED staleness.
			// Read errors from processStartTimeFn (transient /proc
			// failure / platform probe issue) must not destructively
			// reap a possibly-live proxy ref. Fail-safe: keep the ref,
			// retry next sweep tick (2s).
			var shouldPrune bool
			if !isPidAliveFn(ref.SourcePID) {
				shouldPrune = true // confirmed dead source
			} else {
				actualStart, sterr := processStartTimeFn(ref.SourcePID)
				if sterr != nil {
					// Read error → keep, retry next sweep.
					continue
				}
				if actualStart != ref.SourceStartTime {
					shouldPrune = true // confirmed PID reuse
				}
			}
			if !shouldPrune {
				continue // alive + identity-verified
			}
			detached, _, derr := m.detachProxyRefWithRetry(frame, ref.SourcePID, ref.SourceStartTime, broadcastTs)
			if derr == nil && detached {
				agentpkg.MetricSweepPrunedProxy.Add(1)
				if !detachedAny {
					anyOwner = frame
					detachedAny = true
				}
			}
		}
	}
	if detachedAny {
		m.broadcastProxyPruned(anyOwner)
	}
}

// broadcastProxyPruned emits a "hook" broadcast with reason=sweep:proxy_pruned
// after pruneDeadProxyRefs detached at least one stale ref in a pane. Mirrors
// afterFrameCleared's broadcast path so SPA + m.subagents / m.currentStatus
// stay in sync with storage without waiting for an unrelated hook.
//
// Best-effort: errors from projection / broadcast resolution are logged via
// metric (no-op) and otherwise swallowed — the next sweep tick (2s) will
// re-emit if any stale refs remain. Consistent with sweepOnce's other
// best-effort passes. Codex round 2 #P1 fix.
func (m *Module) broadcastProxyPruned(reference store.Frame) {
	sessionName, code := m.resolvePaneSession(reference.PaneID)
	projection, err := m.projectionForSession(sessionName)
	if err != nil {
		return
	}
	if sessionName != "" {
		m.mu.Lock()
		syncProjectionState(m.currentStatus, m.subagents, sessionName, projection)
		m.mu.Unlock()
	}
	if code == "" || m.core == nil {
		return
	}
	normalized := buildProjectionNormalized(projection, reference.AgentType, "sweep:proxy_pruned", nowFn().UnixNano(), agentpkg.DeriveResult{})
	payload, _ := json.Marshal(normalized)
	m.core.Events.Broadcast(code, "hook", string(payload))
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
