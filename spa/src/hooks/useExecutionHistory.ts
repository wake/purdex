// spa/src/hooks/useExecutionHistory.ts — one host's full execution history (archived included) for the Exited tab.
// Keeps its own state: the shared list store stays live-only (`include_archived=false`). It only watches that
// store's `refreshRevision` to know when to refetch.
import { useCallback, useEffect, useRef, useState } from 'react'
import { listAllExecutions } from '../lib/nex/list-all-executions'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import type { ExecutionSummary } from '../lib/nex/types'

export interface ExecutionHistory {
  items: ExecutionSummary[]
  phase: 'loading' | 'ready' | 'error'
  error: string | null
  truncated: boolean
  refetch: () => void
}

interface State { hostId: string; items: ExecutionSummary[]; phase: ExecutionHistory['phase']; error: string | null; truncated: boolean }

const fresh = (hostId: string): State => ({ hostId, items: [], phase: 'loading', error: null, truncated: false })

export function useExecutionHistory(hostId: string): ExecutionHistory {
  const revision = useExecutionListStore((s) => s.byHost[hostId]?.refreshRevision ?? 0)
  const [state, setState] = useState<State>(() => fresh(hostId))
  const token = useRef(0)

  const run = useCallback(() => {
    const mine = ++token.current
    const isCurrent = () => token.current === mine
    listAllExecutions(hostId, { includeArchived: true }, isCurrent)
      .then((result) => {
        if (!result || !isCurrent()) return
        setState({ hostId, items: result.items, phase: 'ready', error: null, truncated: result.truncated })
      })
      .catch((err: unknown) => {
        if (!isCurrent()) return
        const error = err instanceof Error ? err.message : String(err)
        setState((s) => ({ ...(s.hostId === hostId ? s : fresh(hostId)), phase: 'error', error }))
      })
  }, [hostId])

  useEffect(() => {
    run()
    // Any later change of host / revision supersedes this walk; unmount drops it too.
    return () => { token.current += 1 }
  }, [run, revision])

  // A different host's rows never show for this one, even for the render before its effect runs.
  const cur = state.hostId === hostId ? state : fresh(hostId)
  return { items: cur.items, phase: cur.phase, error: cur.error, truncated: cur.truncated, refetch: run }
}
