// spa/src/stores/useExecutionStore.ts — per-(host, execution) view state for
// Nexen executions (spec §4.2.4). Holds data only: no sockets, no timers —
// those live in the P-B.2 hooks so HMR / StrictMode double-mount can never
// leak a connection through the store. Successor of useStreamStore, which
// P-D removes.
import { create } from 'zustand'
import { subscribeWithSelector } from 'zustand/middleware'
import { compositeKey } from '../lib/composite-key'
import { applyDurableEvent, defaultExecutionState, type ExecutionState } from '../lib/nex/event-reducer'
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
  setSummary: (hostId: string, executionId: string, summary: ExecutionSummary | null) => void
  applyEvents: (hostId: string, executionId: string, events: NexEvent[]) => void
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

    setSummary: (h, e, summary) => patch(h, e, (c) => ({ ...c, summary, summaryStale: false })),

    applyEvents: (h, e, events) => patch(h, e, (c) => events.reduce(applyDurableEvent, c)),

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
