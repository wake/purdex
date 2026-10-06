// spa/src/hooks/useEntityStints.ts — conversation entity spec §10.3: the
// conversation's earlier stints and each one's transcript boundary.
// ONE consumer per pane (ExecutionView); the boundary cache is this hook's
// ref, so two panes fetch independently, at most once each.
import { useEffect, useMemo, useRef, useState } from 'react'
import { fetchExecutionPrelude } from '../lib/nex/nex-api'
import { listAllExecutions } from '../lib/nex/list-all-executions'
import { orderStints, pickRecentStints, type Stint } from '../lib/nex/entity-stints'
import { selectSessionFilter, useNexHostStore } from '../stores/useNexHostStore'
import type { ExecutionSummary } from '../lib/nex/types'

export type StintsStatus = 'idle' | 'loading' | 'ok' | 'unavailable'
const EMPTY: Stint[] = []

/**
 * Lists ALL the entity's stints (includeArchived, cursor walked to the end:
 * the newest come last), then fetches one boundary per recent stint. Any
 * partial walk (truncated, stuck, or rows dropped as malformed) or failed
 * walk is 'unavailable': no attribution beats a wrong one (§10.6).
 */
export function useEntityStints(hostId: string, summary: ExecutionSummary | null, enabled: boolean): { stints: Stint[]; status: StintsStatus } {
  const sessionFilter = useNexHostStore(selectSessionFilter(hostId))
  const sessionId = summary ? (summary.resume_session_id || summary.session_id || '') : ''
  const currentId = summary?.id ?? ''
  const active = enabled && sessionId !== '' && currentId !== ''
  const tuple = `${hostId}\n${sessionId}\n${currentId}\n${sessionFilter ? 'f' : 'l'}`
  const cache = useRef(new Map<string, number>())
  const [result, setResult] = useState<{ tuple: string; status: 'ok' | 'unavailable'; stints: Stint[] } | null>(null)

  useEffect(() => {
    if (!active) return
    let cancelled = false
    const isCurrent = () => !cancelled
    void (async () => {
      let rows: ExecutionSummary[]
      try {
        const listed = await listAllExecutions(
          hostId,
          sessionFilter ? { includeArchived: true, sessionId } : { includeArchived: true, labels: { 'purdex.session_id': sessionId } },
          isCurrent,
        )
        if (!listed || cancelled) return
        if (listed.truncated || listed.stuck || listed.dropped > 0) { setResult({ tuple, status: 'unavailable', stints: EMPTY }); return }
        rows = listed.items
      } catch {
        if (!cancelled) setResult({ tuple, status: 'unavailable', stints: EMPTY })
        return
      }
      const withBoundary = await Promise.all(pickRecentStints(rows, currentId).map(async (r) => {
        const base = { id: r.id, created_at: r.created_at, summary: r }
        // A row without a resume id began the transcript itself.
        if (!r.resume_session_id) return { ...base, boundary: 0 as number | null }
        const key = `${hostId}:${r.id}`
        const hit = cache.current.get(key)
        if (hit !== undefined) return { ...base, boundary: hit as number | null }
        try {
          const page = await fetchExecutionPrelude(hostId, r.id, { limit: 1 })
          if (page.state === 'ok' && page.totalBytes !== null) {
            cache.current.set(key, page.totalBytes)
            return { ...base, boundary: page.totalBytes as number | null }
          }
        } catch { /* drops this stint only */ }
        return { ...base, boundary: null as number | null }
      }))
      if (cancelled) return
      setResult({ tuple, status: 'ok', stints: orderStints(withBoundary, currentId) })
    })()
    return () => { cancelled = true }
  }, [active, tuple, hostId, sessionId, currentId, sessionFilter])

  return useMemo(() => {
    if (!active) return { stints: EMPTY, status: 'idle' as const }
    if (!result || result.tuple !== tuple) return { stints: EMPTY, status: 'loading' as const }
    return { stints: result.stints, status: result.status }
  }, [active, result, tuple])
}
