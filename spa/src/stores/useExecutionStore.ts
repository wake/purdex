// spa/src/stores/useExecutionStore.ts — per-(host, execution) view state for
// Nexen executions (spec §4.2.4). Holds data only: no sockets, no timers —
// those live in the P-B.2 hooks so HMR / StrictMode double-mount can never
// leak a connection through the store. Successor of the Stream-mode store
// that P-D.3 removed.
import { create } from 'zustand'
import { subscribeWithSelector } from 'zustand/middleware'
import { compositeKey } from '../lib/composite-key'
import { applyDurableEvent, defaultExecutionState, type ExecutionState } from '../lib/nex/event-reducer'
import { applyTransientFrame } from '../lib/nex/partial'
import type { ExecutionSummary, NexEvent } from '../lib/nex/types'

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
  setSummary: (hostId: string, executionId: string, summary: ExecutionSummary | null, asOfSeq?: number) => void
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
  setHistoryLoaded: (hostId: string, executionId: string, v: boolean) => void
  setSse: (hostId: string, executionId: string, status: ExecutionState['sse'], err?: string | null) => void
  setLease: (hostId: string, executionId: string, lease: ExecutionState['lease']) => void
  setLeaseError: (hostId: string, executionId: string, err: ExecutionState['leaseError']) => void
  setPendingSend: (hostId: string, executionId: string, v: boolean) => void
  setPendingLocal: (hostId: string, executionId: string, local: ExecutionState['pendingLocal']) => void
  setSendError: (hostId: string, executionId: string, err: ExecutionState['sendError']) => void
  setLastTurn: (hostId: string, executionId: string, turn: ExecutionState['lastTurn']) => void
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

    setSummary: (h, e, summary, asOfSeq) =>
      patch(h, e, (c) => {
        if (asOfSeq == null) return { ...c, summary, summaryStale: false }
        const stale = c.lastSeq > asOfSeq
        return { ...c, summary: stale && c.summary !== null ? c.summary : summary, summaryStale: stale }
      }),

    applyEvents: (h, e, events) => patch(h, e, (c) => events.reduce(applyDurableEvent, c)),

    applyTransient: (h, e, frames) =>
      patch(h, e, (c) => frames.reduce((s, f) => applyTransientFrame(s, f.kind, f.payload), c)),

    setHistoryLoaded: (h, e, v) => patch(h, e, (c) => ({ ...c, historyLoaded: v })),

    setSse: (h, e, status, err = null) => patch(h, e, (c) => ({ ...c, sse: status, sseError: err })),

    setLease: (h, e, lease) => patch(h, e, (c) => ({ ...c, lease })),

    setLeaseError: (h, e, err) => patch(h, e, (c) => ({ ...c, leaseError: err })),

    setPendingSend: (h, e, v) => patch(h, e, (c) => ({ ...c, pendingSend: v })),

    setPendingLocal: (h, e, local) => patch(h, e, (c) => ({ ...c, pendingLocal: local })),

    setSendError: (h, e, err) => patch(h, e, (c) => ({ ...c, sendError: err })),

    setLastTurn: (h, e, turn) => patch(h, e, (c) => ({ ...c, lastTurn: turn })),

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
