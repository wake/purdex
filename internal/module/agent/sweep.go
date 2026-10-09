package agent

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

var (
	sweepInterval = 2 * time.Second
	sweepOnceFn   = func(m *Module) { _ = m.sweepOnce() }
	// nowFn is the time-seam used by sweep broadcast timestamps
	// (canonicalize / prune / afterFrameCleared). Tests override it to
	// produce deterministic broadcastTs values without depending on
	// time.Now's wall clock.
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
	procs := m.sweepProcsFor(frames)
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
		startTime, err := procs.startTime(frame.PID, frame.ProcessStartTime)
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
		m.canonicalizePane(paneID, broadcastTs, procs)
		m.pruneDeadProxyRefs(paneID, broadcastTs, procs)
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
func (m *Module) canonicalizePane(paneID string, broadcastTs int64, procs *sweepProcs) {
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
		actualStart, sterr := procs.startTime(candidate.PID, candidate.ProcessStartTime)
		if sterr != nil || actualStart != candidate.ProcessStartTime {
			continue
		}
		ancestor, found := m.findCanonicalAncestorIn(procs, candidate, framesByPID)
		if !found {
			continue
		}
		// Review F2 (round 2 defender) — owned-state classification
		// happens AFTER ancestor lookup so we can branch on whether
		// the parent already claims this candidate via a matching
		// proxy ref.
		//
		// Four cases:
		//
		//  parent has ref + owned state    → silent skip (already
		//                                   canonical at projection
		//                                   layer; nothing to do; no
		//                                   broadcast spam every tick)
		//  parent has ref + no owned state → delete-only path (the
		//                                   row was a partial leftover
		//                                   from a successful hot-path
		//                                   attach + failed delete;
		//                                   sweep finishes the cleanup)
		//  parent missing ref + owned state    → attach-only recovery
		//                                       (the F2 case; closes
		//                                       round 2 defender high.
		//                                       Without this, an owned-
		//                                       state child whose hot
		//                                       path failed to attach
		//                                       would never converge —
		//                                       projection_dedup needs
		//                                       a proxy ref on the
		//                                       parent to hide the
		//                                       child)
		//  parent missing ref + no owned state → attach + delete (main
		//                                       canonicalization path)
		parentHasMatchingProxy := subagentsContainProxySender(ancestor.Subagents, candidate.PID, candidate.ProcessStartTime)
		ownedState := candidateHasOwnedStateIn(procs.startTime, candidate)
		if parentHasMatchingProxy && ownedState {
			continue
		}
		parentStored := ancestor
		if !parentHasMatchingProxy {
			ref := agentpkg.SubagentRef{
				ID:              fmt.Sprintf("proxy:%s:%d:%s", candidate.AgentType, candidate.PID, candidate.ProcessStartTime),
				Type:            candidate.AgentType,
				StartedAt:       broadcastTs,
				SourcePID:       candidate.PID,
				SourceStartTime: candidate.ProcessStartTime,
				IsProxy:         true,
			}
			attached, ps, aerr := m.attachProxyRefWithRetry(ancestor, ref, broadcastTs)
			if aerr != nil || !attached {
				continue
			}
			parentStored = ps
		}
		if ownedState {
			// Review F2 — deliberate partial: attach succeeded (or was
			// already there), but the candidate carries owned native
			// state we must not lose. Skip delete; projection_dedup
			// hides + merges this child's Subagents into the parent
			// projection at read time.
			agentpkg.MetricPartialCanonicalizationCreated.Add(1)
			canonicalizedAny = true
			anyAncestor = parentStored
			continue
		}
		// Review F1 + F4 (round 2 attacker / round 3 closure) —
		// revalidate ancestor identity BEFORE delete. findCanonicalAncestor
		// only proves the ancestor was live at classification time; the
		// ancestor PID may die between then and this point. Without
		// this gate, we would delete the live candidate row into a
		// dead parent (whose next sweep tick clears it along with all
		// its Subagents, severing the only DB link to the live
		// candidate). Hoisted to the common pre-delete point so BOTH
		// the attach-then-delete branch (parent missing ref) AND the
		// delete-only branch (parent already has matching ref via a
		// prior partial recovery) share the same protection. Round 3
		// caught the original F1 patch missing this delete-only path.
		//
		// On revalidation failure: leave the proxy ref attached (the
		// dead parent is cleared on the next sweep tick along with all
		// its Subagents) and skip delete. Mirrors the "no rollback on
		// partial" Hybrid B+ rule.
		if !isPidAliveFn(parentStored.PID) {
			agentpkg.MetricPartialCanonicalizationCreated.Add(1)
			canonicalizedAny = true
			anyAncestor = parentStored
			continue
		}
		actualAncestorStart, ancestorErr := procs.startTime(parentStored.PID, parentStored.ProcessStartTime)
		if ancestorErr != nil || actualAncestorStart != parentStored.ProcessStartTime {
			agentpkg.MetricPartialCanonicalizationCreated.Add(1)
			canonicalizedAny = true
			anyAncestor = parentStored
			continue
		}
		deleted, _ := m.frames.DeleteIfUnchanged(candidate.FrameID, candidate.LastSeenAt)
		if deleted {
			m.forgetHookBackground(candidate.FrameID)
			m.forgetHookEdge(candidate.FrameID)
		}
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
	if canonicalizedAny {
		m.broadcastProxyCanonicalized(anyAncestor)
	}
}

// broadcastProxyCanonicalized emits a "hook" broadcast with reason=
// sweep:proxy_canonicalized after canonicalizePane attached at least
// one proxy ref + deleted at least one standalone child in a pane.
// Mirrors broadcastProxyPruned so SPA + m.subagents / m.currentStatus
// stay in sync without waiting for an unrelated hook.
//
// Best-effort: errors swallowed (next sweep tick re-evaluates).
// Per-pane (not per-attach) so multiple folds in the same pane
// coalesce into one broadcast — matches broadcastProxyPruned's
// granularity. PR-3.5b §2.4.
func (m *Module) broadcastProxyCanonicalized(reference store.Frame) {
	sessionName, code := m.resolvePaneSession(reference.PaneID)
	// Read, synced and sent inside the emit slot; a failed read does nothing.
	m.emitSession(kindSweep, code, sessionName, func(p *SessionProjection) (agentpkg.NormalizedEvent, bool) {
		return buildProjectionNormalized(p, reference.AgentType, "sweep:proxy_canonicalized", nowFn().UnixNano(), agentpkg.DeriveResult{}), true
	})
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
	return m.findCanonicalAncestorIn(nil, candidate, framesByPID)
}

// findCanonicalAncestorIn is findCanonicalAncestor over a sweep's process view: each step of the PPID walk is answered
// from the tick's table (no fork), and only a pid the table lacks is read singly.
func (m *Module) findCanonicalAncestorIn(procs *sweepProcs, candidate store.Frame, framesByPID map[int]store.Frame) (store.Frame, bool) {
	ppid, err := procs.ppid(candidate.PID)
	if err != nil {
		return store.Frame{}, false
	}
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
				actualStart, sterr := procs.startTime(ancestor.PID, ancestor.ProcessStartTime)
				if sterr == nil && actualStart == ancestor.ProcessStartTime {
					return ancestor, true
				}
			}
			// Ancestor matched in pane but failed identity gate — keep
			// walking; a deeper ancestor might still match.
		}
		parent, err := procs.ppid(ppid)
		if err != nil {
			return store.Frame{}, false
		}
		if parent == ppid {
			return store.Frame{}, false
		}
		ppid = parent
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
// Mirrors the afterFrameCleared broadcast that pid_dead / pid_reused already
// emit. Per-pane (not per-detach) so multiple stale refs in the same pane
// coalesce into one broadcast — matches afterFrameCleared's per-frame
// granularity.
func (m *Module) pruneDeadProxyRefs(paneID string, broadcastTs int64, procs *sweepProcs) {
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
				actualStart, sterr := procs.startTime(ref.SourcePID, ref.SourceStartTime)
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
	// Read, synced and sent inside the emit slot; a failed read does nothing.
	m.emitSession(kindSweep, code, sessionName, func(p *SessionProjection) (agentpkg.NormalizedEvent, bool) {
		return buildProjectionNormalized(p, reference.AgentType, "sweep:proxy_pruned", nowFn().UnixNano(), agentpkg.DeriveResult{}), true
	})
}

// clearFrame is the eager delete path used for pid_dead / pid_reused sweeps
// (and any other call site that wants an unconditional frame removal).
//
// The exit envelope (agent-last-state spec, review decision 7) is taken from
// the frame BEFORE the delete — afterwards there is no row to read the session
// id or frame id from — and only for a root frame: a child frame's process
// ending is not the pane's agent ending. Both sweep reasons mean the process
// is gone without a SessionEnd: a quit with no hook (opencode), a crash, a
// kill.
func (m *Module) clearFrame(frame store.Frame, reason string) error {
	if m.frames == nil {
		return nil
	}
	exit := exitForFrame(frame, m.sessionTmuxInstance(), ExitReasonProcessDead, nowFn().UnixMilli())
	exit, claimed, err := m.claimFrameEnd(frame, "", exit)
	if err != nil {
		return err
	}
	if !claimed {
		// Someone else (the SessionEnd hook) ended this frame between the
		// sweep's read and now; that caller owns the end and its broadcast.
		return nil
	}
	return m.afterFrameCleared(frame, reason, exit)
}

// afterFrameCleared handles the post-delete side effects shared by every
// sweep reason: legacy agent_events cleanup, in-memory projection sync,
// orphan Activity watcher stop (bug fix: previously only pid_dead/pid_reused
// paths forgot to call StopWatch; now centralized), and WS broadcast.
//
// exit is the envelope clearFrame took before the delete (nil for a non-root
// frame). It rides on the one broadcast below, so a pane whose session cannot
// be resolved sends nothing at all — as before.
func (m *Module) afterFrameCleared(frame store.Frame, reason string, exit *Exit) error {
	// With an exit in hand (a claimed root frame) a failure below must not
	// lose it: the row is gone, so nothing will ever end this run again
	// (#1381 attacker #3). The cleanup error is logged and still returned,
	// and the broadcast degrades to status clear. Without an exit the old
	// behaviour stands — return before broadcasting anything on a guess.
	var cleanupErr error
	degrade := func(where string, err error) error {
		if exit == nil {
			return err
		}
		logAfterClaim(where, frame.FrameID, err)
		cleanupErr = errors.Join(cleanupErr, err)
		return nil
	}

	sessionName, code := m.resolvePaneSession(frame.PaneID)
	if sessionName != "" && m.events != nil {
		if err := eventsDeleteFn(m, sessionName); err != nil {
			if err := degrade("events.Delete", err); err != nil {
				return err
			}
		}
	}
	projection, err := projectionForSessionFn(m, sessionName)
	projectionKnown := err == nil
	if err != nil {
		if err := degrade("projectionForSession", err); err != nil {
			return err
		}
	}

	var hadWatcher bool
	m.mu.Lock()
	// An unknown projection must not overwrite in-memory state with a guess.
	if sessionName != "" && projectionKnown {
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
		return cleanupErr
	}
	// Issue #717 round-2 race fix: the projection the broadcast carries is
	// read right before broadcasting, and since the emit slot (U1-2b-2) that
	// read happens inside the slot itself, so no other emit can land between
	// it and the send. A hook handler may have created a new frame for this
	// session after the projectionForSession call above (e.g. user kills
	// opencode and immediately runs `opencode` again — the SessionStart hook
	// can land mid-sweep); a stale projection==nil view would overwrite the
	// just-installed running status with clear. Empty result.Status carries
	// StatusClear via the projection==nil branch in buildProjectionNormalized;
	// passing it explicitly documents intent at the callsite.
	var abort error
	m.emitSessionWith(kindSweep, code, sessionName, func(fresh *SessionProjection, ferr error) (agentpkg.NormalizedEvent, bool) {
		if ferr != nil {
			if err := degrade("projectionForSession (re-resolve)", ferr); err != nil {
				abort = err
				return agentpkg.NormalizedEvent{}, false
			}
			fresh = nil // degraded: a nil projection broadcasts status clear
		}
		normalized := buildProjectionNormalized(fresh, frame.AgentType, "sweep:"+reason, nowFn().UnixNano(), agentpkg.DeriveResult{Status: agentpkg.StatusClear})
		attachExit(&normalized, exit)
		return normalized, true
	})
	if abort != nil {
		return abort
	}
	return cleanupErr
}

// snapshotTimeout bounds the one process-table read of a sweep tick. It is a var so a test can shorten it.
var snapshotTimeout = 3 * time.Second

// procTable is what a sweep asks of one process-table read: a pid's start text and its parent.
// *agentpkg.ProcessSnapshot is one; tests stage others.
type procTable interface {
	StartTime(pid int) (string, error)
	PPID(pid int) (int, error)
}

// snapshotProcessesFn reads the process table once. A seam: tests replace it.
var snapshotProcessesFn = func(ctx context.Context) (procTable, error) {
	snap, err := agentpkg.SnapshotProcesses(ctx)
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// tableReading is set while a table read started by an earlier tick has not returned: a sysctl cannot be cancelled, so
// a stuck one is abandoned (the tick falls back) and no second one is started until it comes back.
var tableReading atomic.Bool

// sweepProcs answers the process questions of ONE sweep tick. Until #2138 every one of them was its own `ps` fork - a
// start time per frame, a parent per ancestor walked, a start time per proxy ref - about 15-25 forks a second on a busy
// host; now they are answered from a single read of the table. The identity rules are unchanged (alive + start text
// match; a read error keeps the frame): only where the answer comes from differs.
//
//   - A pid the table does not have (it started after the read) is asked of the per-PID reader, and only that pid.
//   - The table's start text only ever CONFIRMS a frame. `ps -o lstart=` prints in the caller's locale and the table
//     builds English text, so where they differ the table says nothing: the answer is the single per-PID read, which is
//     what the sweep always did. A destructive decision (pid_reused, a prune, dropping owned state) is therefore never
//     taken from the table's text alone, and in a non-English locale the sweep costs what it cost before instead of
//     clearing every live frame.
//   - The zero value (no table) asks the per-PID readers for everything, which is also what a failed or abandoned table
//     read leaves.
//
// The table is a point-in-time view taken at the top of the tick, like the owner pass's, and liveness (a signal-0
// probe) is asked live. A pid that exits and is reused between the two within one tick (milliseconds) would keep its
// frame until the next tick, whose fresh table shows the new start text and clears it as pid_reused: the window is
// bounded by one sweep interval and heals itself, and the old per-PID reads had the same shape at a finer grain.
type sweepProcs struct{ table procTable }

// sweepProcsFor reads the table for this tick - only if some frame is there to verify, so an idle daemon forks nothing.
func (m *Module) sweepProcsFor(frames []store.Frame) *sweepProcs {
	p := &sweepProcs{}
	any := false
	for _, f := range frames {
		if f.Verified {
			any = true
			break
		}
	}
	if !any || !tableReading.CompareAndSwap(false, true) {
		return p
	}
	type result struct {
		table procTable
		err   error
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
	defer cancel()
	ch := make(chan result, 1)
	go func() {
		defer tableReading.Store(false)
		table, err := snapshotProcessesFn(ctx)
		ch <- result{table, err}
	}()
	select {
	case r := <-ch:
		if r.err == nil && r.table != nil {
			p.table = r.table
		}
		return p
	case <-ctx.Done():
		return p
	}
}

// startTime is pid's start text, given the text the caller expects (the frame's, the proxy ref's). The table settles it
// only by agreeing; anything else is the per-PID read.
func (p *sweepProcs) startTime(pid int, want string) (string, error) {
	if p == nil {
		return processStartTimeFn(pid)
	}
	if p.table != nil {
		if s, err := p.table.StartTime(pid); err == nil && s == want {
			return s, nil
		}
	}
	return processStartTimeFn(pid)
}

func (p *sweepProcs) ppid(pid int) (int, error) {
	if p != nil && p.table != nil {
		ppid, err := p.table.PPID(pid)
		if err == nil || !errors.Is(err, agentpkg.ErrNotInSnapshot) {
			return ppid, err
		}
	}
	info, err := readProcessInfoFn(pid)
	if err != nil {
		return 0, err
	}
	return info.PPID, nil
}
