// spa/src/hooks/useHostExecutions.ts — a component's window onto the shared
// per-host execution list (P-C spec §4.3): subscribing keeps the host's one
// site-wide stream open for as long as the component is mounted; the rows,
// phase and error come from `useExecutionListStore`.
import { useCallback, useEffect } from 'react'
import { useExecutionListStore, type HostListPhase } from '../stores/useExecutionListStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import type { ExecutionSummary } from '../lib/nex/types'

export interface HostExecutions {
  items: ExecutionSummary[]
  phase: HostListPhase
  error: string | null
  /** The list hit the page cap; the newest rows may be missing. */
  truncated: boolean
  refetch: () => void
  /** Bumped by the store on every completed refresh attempt (success or failure); key follow-up queries on it. */
  refreshRevision: number
  /** Bumped by an archive-membership delta and by every committed reconcile (#1866 §4.7); the archived query keys on it. */
  archivedRevision: number
}

export interface HostExecutionsOptions {
  /** `false` keeps the component off the store entirely (no ensure, no subscription) while still reading the cache. Default `true`. */
  enabled?: boolean
}

const EMPTY: ExecutionSummary[] = []

export function useHostExecutions(hostId: string, { enabled = true }: HostExecutionsOptions = {}): HostExecutions {
  useEffect(() => {
    if (!enabled) return
    void useNexHostStore.getState().ensure(hostId)
    return useExecutionListStore.getState().subscribe(hostId)
  }, [hostId, enabled])

  const cache = useExecutionListStore((s) => s.byHost[hostId])
  const refetch = useCallback(() => useExecutionListStore.getState().refetch(hostId), [hostId])

  return {
    items: cache?.items ?? EMPTY,
    phase: cache?.phase ?? 'idle',
    error: cache?.error ?? null,
    truncated: cache?.truncated ?? false,
    refetch,
    refreshRevision: cache?.refreshRevision ?? 0,
    archivedRevision: cache?.archivedRevision ?? 0,
  }
}
