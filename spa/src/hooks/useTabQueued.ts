// spa/src/hooks/useTabQueued.ts — the panes of a tab whose agent waits in a host's command queue (watcher-area plan §8).
import { useMemo } from 'react'
import { useFleetStore } from '../lib/fleet/store'
import type { FleetWaiter } from '../lib/fleet/types'
import type { TabAgentPane } from '../lib/agent-lights/tab-aggregate'
import { queuedBySession } from '../lib/watch/queued-sessions'

export interface TabQueued {
  /** Keys of the panes whose agent session has a waiter. */
  keys: ReadonlySet<string>
  /** Every waiter of those panes. */
  waiters: FleetWaiter[]
}

const NONE: TabQueued = { keys: new Set(), waiters: [] }

/**
 * The selector returns a JSON string (a primitive), so a fleet push that changes nothing for this tab does not re-render
 * it, and a Map built per call never leaks into the render.
 */
export function useTabQueued(panes: TabAgentPane[]): TabQueued {
  const sig = useFleetStore((s) => {
    const withSession = panes.filter((p) => p.sessionId)
    if (withSession.length === 0 || !s.snapshot) return ''
    const bySession = queuedBySession(s.snapshot)
    if (bySession.size === 0) return ''
    const hits = withSession.flatMap((p) => {
      const waiters = bySession.get(p.sessionId!.toLowerCase())
      return waiters ? [{ key: p.key, waiters }] : []
    })
    return hits.length > 0 ? JSON.stringify(hits) : ''
  })
  return useMemo(() => {
    if (!sig) return NONE
    const hits = JSON.parse(sig) as { key: string; waiters: FleetWaiter[] }[]
    return { keys: new Set(hits.map((h) => h.key)), waiters: hits.flatMap((h) => h.waiters) }
  }, [sig])
}
