// spa/src/stores/useExecutionStore.ts — per-(host, execution) view state for
// Nexen executions (spec §4.2.4). Holds data only: no sockets, no timers —
// those live in the P-B.2 hooks so HMR / StrictMode double-mount can never
// leak a connection through the store. Successor of the Stream-mode store
// that P-D.3 removed.
import { create } from 'zustand'
import { subscribeWithSelector } from 'zustand/middleware'
import { compositeKey } from '../lib/composite-key'
import { applyDurableEvent, applyTasksSnapshot, defaultExecutionState, type ExecutionState } from '../lib/nex/event-reducer'
import { applyTransientFrame } from '../lib/nex/partial'
import { applyPreludePage as reducePreludePage, defaultPreludeState, preludeFailed as failPrelude, preludeLoading as loadingPrelude } from '../lib/nex/prelude'
import type { PreludePage } from '../lib/nex/prelude-wire'
import type { ExecutionSummary, NexEvent, WorkerTasksSnapshot } from '../lib/nex/types'

export function executionKey(hostId: string, executionId: string): string {
  return compositeKey(hostId, executionId)
}

/** Host ids may contain ':'; execution ids (exc_…) never do — split on the last one. */
export function splitExecutionKey(key: string): { hostId: string; executionId: string } {
  const i = key.lastIndexOf(':')
  return i < 0 ? { hostId: '', executionId: key } : { hostId: key.slice(0, i), executionId: key.slice(i + 1) }
}

interface ExecutionStore {
  executions: Record<string, ExecutionState>
  /**
   * Apply a summary fetched by the P-B.2 hook. `asOfSeq` is the store's
   * `lastSeq` the hook read *before* issuing the fetch: if a newer durable
   * event has landed by the time the response arrives (`lastSeq > asOfSeq`),
   * the fetched summary is already stale — it stays `summaryStale` so the
   * hook refetches again, AND the existing `summary` object is left
   * untouched (an older fetch must never overwrite fresher local state,
   * e.g. `execution.terminal` arriving mid-flight of a running-state
   * fetch), except when there is no existing summary yet: a first fetch
   * still populates it (still marked stale, so the hook refetches). Callers
   * that don't track a cursor may omit `asOfSeq`, which always adopts the
   * summary and clears `summaryStale` (today's behaviour).
   */
  setSummary: (hostId: string, executionId: string, summary: ExecutionSummary | null, asOfSeq?: number, gen?: number) => void
  /**
   * Merge a locally known summary change (the exit result) and bump `summaryGen`, so a summary
   * fetch that started before it (`gen` older) is dropped when it lands. The SSE path is untouched.
   */
  applySummaryPatch: (hostId: string, executionId: string, change: Partial<ExecutionSummary>) => void
  applyEvents: (hostId: string, executionId: string, events: NexEvent[]) => void
  /**
   * Fold a batch of transient SSE frames (no id / no cursor: `stream_event`,
   * `stream_snapshot`, `lease.renewed`, …) into the partial assembly in ONE
   * `set()` (spec §4.3). The hook coalesces deltas per animation frame and
   * hands them over here. A batch that changes nothing (e.g. only
   * `lease.renewed`) returns the same state object, so `patch` materialises
   * no entry for an execution nobody has otherwise touched.
   */
  applyTransient: (hostId: string, executionId: string, frames: { kind: string; payload: Record<string, unknown> }[]) => void
  /** Merge a `/tasks` snapshot into the task table (nexen #83 correction; see `applyTasksSnapshot`). */
  applyTasksSnapshot: (hostId: string, executionId: string, snapshot: WorkerTasksSnapshot) => void
  setHistoryLoaded: (hostId: string, executionId: string, v: boolean) => void
  setSse: (hostId: string, executionId: string, status: ExecutionState['sse'], err?: string | null) => void
  setLease: (hostId: string, executionId: string, lease: ExecutionState['lease']) => void
  setLeaseError: (hostId: string, executionId: string, err: ExecutionState['leaseError']) => void
  setPendingSend: (hostId: string, executionId: string, v: boolean) => void
  /** The box lock: this pane's send POST is unresolved and not yet accepted (see ExecutionState.sendLocked). */
  setSendLocked: (hostId: string, executionId: string, v: boolean) => void
  setPendingLocal: (hostId: string, executionId: string, local: ExecutionState['pendingLocal']) => void
  setSendError: (hostId: string, executionId: string, err: ExecutionState['sendError']) => void
  setLastTurn: (hostId: string, executionId: string, turn: ExecutionState['lastTurn']) => void
  /** Worker prelude (spec §5.2): request `request` is in flight — also the lock two panes share. */
  preludeLoading: (hostId: string, executionId: string, request: number) => void
  /** No-op unless `request` is the one in flight. */
  applyPreludePage: (hostId: string, executionId: string, page: PreludePage, sentBefore: string | null, request: number) => void
  /** No-op unless `request` is the one in flight. */
  preludeFailed: (hostId: string, executionId: string, message: string, request: number) => void
  /** The prelude stopped on a client limit (loadAll's page cap): an error with Retry. No-op while a request is in flight. */
  preludeHalted: (hostId: string, executionId: string, message: string) => void
  /** Back to idle, which reloads from the first page (spec §5.2: a cursor rejected as foreign). */
  resetPrelude: (hostId: string, executionId: string) => void
  clearExecution: (hostId: string, executionId: string) => void
  clearHost: (hostId: string) => void
}

export const useExecutionStore = create<ExecutionStore>()(subscribeWithSelector((set) => {
  const patch = (hostId: string, executionId: string, fn: (cur: ExecutionState) => ExecutionState) =>
    set((s) => {
      const key = executionKey(hostId, executionId)
      const cur = s.executions[key] ?? defaultExecutionState()
      const next = fn(cur)
      // A reducer no-op (old/NaN seq) must not materialise an empty entry for
      // an execution nobody has otherwise touched; setters always return a
      // fresh object so they still create the entry on first use.
      if (next === cur) return s
      return { executions: { ...s.executions, [key]: next } }
    })

  return {
    executions: {},

    setSummary: (h, e, summary, asOfSeq, gen) =>
      patch(h, e, (c) => {
        if (gen != null && gen < c.summaryGen) return c
        if (asOfSeq == null) return { ...c, summary, summaryStale: false }
        const stale = c.lastSeq > asOfSeq
        return { ...c, summary: stale && c.summary !== null ? c.summary : summary, summaryStale: stale }
      }),

    applySummaryPatch: (h, e, change) =>
      patch(h, e, (c) => (c.summary ? { ...c, summary: { ...c.summary, ...change }, summaryGen: c.summaryGen + 1 } : c)),

    applyEvents: (h, e, events) => patch(h, e, (c) => events.reduce(applyDurableEvent, c)),

    applyTransient: (h, e, frames) =>
      patch(h, e, (c) => frames.reduce((s, f) => applyTransientFrame(s, f.kind, f.payload), c)),

    applyTasksSnapshot: (h, e, snapshot) => patch(h, e, (c) => applyTasksSnapshot(c, snapshot)),

    setHistoryLoaded: (h, e, v) => patch(h, e, (c) => ({ ...c, historyLoaded: v })),

    setSse: (h, e, status, err = null) => patch(h, e, (c) => ({ ...c, sse: status, sseError: err })),

    setLease: (h, e, lease) => patch(h, e, (c) => ({ ...c, lease })),

    setLeaseError: (h, e, err) => patch(h, e, (c) => ({ ...c, leaseError: err })),

    // sendLocked implies pendingSend: ending the turn flag also frees the box.
    setPendingSend: (h, e, v) => patch(h, e, (c) => ({ ...c, pendingSend: v, ...(v ? {} : { sendLocked: false }) })),

    setSendLocked: (h, e, v) => patch(h, e, (c) => ({ ...c, sendLocked: v })),

    setPendingLocal: (h, e, local) => patch(h, e, (c) => ({ ...c, pendingLocal: local })),

    setSendError: (h, e, err) => patch(h, e, (c) => ({ ...c, sendError: err })),

    setLastTurn: (h, e, turn) => patch(h, e, (c) => ({ ...c, lastTurn: turn })),

    preludeLoading: (h, e, r) => patch(h, e, (c) => ({ ...c, prelude: loadingPrelude(c.prelude, r) })),
    applyPreludePage: (h, e, page, sentBefore, r) => patch(h, e, (c) => {
      const prelude = reducePreludePage(c.prelude, page, sentBefore, r)
      return prelude === c.prelude ? c : { ...c, prelude }
    }),
    preludeFailed: (h, e, message, r) => patch(h, e, (c) => {
      const prelude = failPrelude(c.prelude, message, r)
      return prelude === c.prelude ? c : { ...c, prelude }
    }),
    preludeHalted: (h, e, message) => patch(h, e, (c) => (
      c.prelude.status === 'loading' ? c : { ...c, prelude: { ...c.prelude, status: 'error', error: message, request: null } }
    )),
    resetPrelude: (h, e) => patch(h, e, (c) => ({ ...c, prelude: defaultPreludeState() })),

    clearExecution: (h, e) => set((s) => {
      const { [executionKey(h, e)]: _dropped, ...rest } = s.executions
      return { executions: rest }
    }),

    clearHost: (hostId) => set((s) => {
      const executions: Record<string, ExecutionState> = {}
      for (const [k, v] of Object.entries(s.executions)) {
        if (splitExecutionKey(k).hostId !== hostId) executions[k] = v
      }
      return { executions }
    }),
  }
}))

/**
 * The optimistic line's thumbnails (phase E) are owned here, by the store,
 * not by a pane: `pendingLocal` is per-execution state that two panes may
 * show at once, so no pane's unmount may revoke them. An array is revoked
 * exactly once, on the write that drops it — pendingLocal cleared (accepted,
 * failed, the execution or host cleared) or replaced by another array. The
 * same array kept across the delivery update is not a drop.
 */
export function revokeDroppedPreviews(prev: Record<string, ExecutionState>, next: Record<string, ExecutionState>): void {
  if (prev === next || typeof URL.revokeObjectURL !== 'function') return
  for (const key in prev) {
    const old = prev[key].pendingLocal?.attachments
    if (!old || next[key]?.pendingLocal?.attachments === old) continue
    for (const p of old) URL.revokeObjectURL(p.previewUrl)
  }
}

const unsubscribeRevokeDroppedPreviews = useExecutionStore.subscribe((s, prev) => revokeDroppedPreviews(prev.executions, s.executions))

// HMR-dispose so a hot-reload round-trip can't leave a second subscription
// registered against the module-level store: without this, "revoked exactly
// once" would stop being literally true after any edit to this file while
// the dev server is running (each reload's new subscription piles onto the
// old one, and the old one keeps the previous module's closure alive too).
if (import.meta.hot) {
  import.meta.hot.dispose(() => unsubscribeRevokeDroppedPreviews())
}
