// spa/src/hooks/useExecutionHistory.ts — one host's full execution history (archived included) for the Exited tab.
// Keeps its own state: the shared list store stays live-only (`include_archived=false`). It only watches that
// store's `refreshRevision` to know when to refetch.
//
// A full archived walk is expensive and refreshes arrive in bursts, so walks are scheduled: at most one in flight,
// a revision that arrives mid-walk sets a dirty flag (exactly one trailing walk), and walk starts are at least
// HISTORY_MIN_INTERVAL_MS apart. A host change cancels and starts at once.
import { useCallback, useEffect, useRef, useState } from 'react'
import { listAllExecutions } from '../lib/nex/list-all-executions'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import type { ExecutionSummary } from '../lib/nex/types'

/** Minimum gap between two history walks' starts. */
export const HISTORY_MIN_INTERVAL_MS = 5_000

export interface ExecutionHistory {
  items: ExecutionSummary[]
  phase: 'loading' | 'ready' | 'error'
  error: string | null
  truncated: boolean
  refetch: () => void
}

interface State { hostId: string; items: ExecutionSummary[]; phase: ExecutionHistory['phase']; error: string | null; truncated: boolean }

const fresh = (hostId: string): State => ({ hostId, items: [], phase: 'loading', error: null, truncated: false })

interface Sched {
  token: number
  inFlight: boolean
  dirty: boolean
  lastStart: number
  timer: ReturnType<typeof setTimeout> | null
  hostId: string
  revision: number
}

export function useExecutionHistory(hostId: string): ExecutionHistory {
  const revision = useExecutionListStore((s) => s.byHost[hostId]?.refreshRevision ?? 0)
  const [state, setState] = useState<State>(() => fresh(hostId))
  const sched = useRef<Sched>({ token: 0, inFlight: false, dirty: false, lastStart: 0, timer: null, hostId, revision })

  // `request` and `start` call each other; the ref keeps one stable pair.
  const api = useRef<{ request: (host: string, throttle: boolean) => void; start: (host: string) => void }>({ request: () => {}, start: () => {} })
  api.current.start = (forHost) => {
    const sc = sched.current
    const mine = sc.token
    const isCurrent = () => sc.token === mine
    sc.inFlight = true
    sc.dirty = false
    sc.lastStart = Date.now()
    const settle = () => {
      sc.inFlight = false
      if (sc.dirty) api.current.request(forHost, true)
    }
    listAllExecutions(forHost, { includeArchived: true }, isCurrent)
      .then((result) => {
        if (!result || !isCurrent()) return
        if (result.stuck) console.warn('nex: execution history cursor repeated', { hostId: forHost, page: result.stuckPage })
        setState({ hostId: forHost, items: result.items, phase: 'ready', error: null, truncated: result.truncated })
        settle()
      })
      .catch((err: unknown) => {
        if (!isCurrent()) return
        const error = err instanceof Error ? err.message : String(err)
        setState((cur) => ({ ...(cur.hostId === forHost ? cur : fresh(forHost)), phase: 'error', error }))
        settle()
      })
  }
  // While a walk runs a request only marks it dirty; otherwise it starts now, or once the window since the last start passes.
  api.current.request = (forHost, throttle) => {
    const sc = sched.current
    if (sc.inFlight) { sc.dirty = true; return }
    if (sc.timer) return
    const wait = throttle ? sc.lastStart + HISTORY_MIN_INTERVAL_MS - Date.now() : 0
    if (wait > 0) {
      const mine = sc.token
      sc.timer = setTimeout(() => {
        sc.timer = null
        if (sc.token === mine) api.current.start(forHost)
      }, wait)
      return
    }
    api.current.start(forHost)
  }

  // Mount and host change: cancel the previous host's walk and timer, start at once.
  useEffect(() => {
    const sc = sched.current
    sc.token += 1
    sc.inFlight = false
    sc.dirty = false
    sc.hostId = hostId
    sc.revision = revision // the new host's own counter is a baseline, not a bump
    if (sc.timer) { clearTimeout(sc.timer); sc.timer = null }
    api.current.start(hostId)
    return () => {
      sc.token += 1
      if (sc.timer) { clearTimeout(sc.timer); sc.timer = null }
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hostId])

  useEffect(() => {
    const sc = sched.current
    if (sc.hostId !== hostId) return
    if (sc.revision === revision) return
    sc.revision = revision
    api.current.request(hostId, true)
  }, [hostId, revision])

  // A retry after an error is loading again (the tab's loading state) until its walk settles.
  const refetch = useCallback(() => {
    setState((cur) => (cur.hostId === hostId && cur.phase === 'error' ? { ...cur, phase: 'loading', error: null } : cur))
    api.current.request(hostId, false)
  }, [hostId])

  // A different host's rows never show for this one, even for the render before its effect runs.
  const cur = state.hostId === hostId ? state : fresh(hostId)
  return { items: cur.items, phase: cur.phase, error: cur.error, truncated: cur.truncated, refetch }
}
