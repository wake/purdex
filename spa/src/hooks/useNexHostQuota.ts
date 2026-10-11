// spa/src/hooks/useNexHostQuota.ts — the Nexen host's quota (`GET /v1/host` → quota), through the same
// `fetchNexHost` the cost panel uses. Fetched on mount / host change and re-read every few minutes while mounted;
// errors and a non-finite reading are "no quota" (the status bar hides the segments), never an error surface.
import { useEffect, useState } from 'react'
import { fetchNexHost } from '../lib/nex/nex-api'
import type { NexQuota } from '../lib/nex/types'

export const NEX_QUOTA_REFRESH_MS = 5 * 60 * 1000

/** Readers of the same host (the bottom bar and the deck's status row) asking within this window share one request. */
const SHARE_MS = 1000
const shared = new Map<string, { at: number; p: ReturnType<typeof fetchNexHost> }>()
/** Tests only: module state outlives a test. */
export const clearNexQuotaShare = (): void => shared.clear()
function fetchShared(hostId: string): ReturnType<typeof fetchNexHost> {
  const hit = shared.get(hostId)
  if (hit && Date.now() - hit.at < SHARE_MS) return hit.p
  const p = fetchNexHost(hostId)
  shared.set(hostId, { at: Date.now(), p })
  p.catch(() => { if (shared.get(hostId)?.p === p) shared.delete(hostId) })
  return p
}

export function useNexHostQuota(hostId: string): { quota: NexQuota | null; fetchedAt: number } {
  const [state, setState] = useState<{ hostId: string; quota: NexQuota | null; fetchedAt: number } | null>(null)
  useEffect(() => {
    if (!hostId) return
    let cancelled = false
    const load = () => {
      fetchShared(hostId)
        .then((h) => {
          if (cancelled) return
          const q = h.quota
          const valid = !!q && Number.isFinite(q.five_hour_pct) && Number.isFinite(q.seven_day_pct)
          setState({ hostId, quota: valid ? q : null, fetchedAt: Date.now() })
        })
        .catch(() => {})
    }
    load()
    const id = setInterval(load, NEX_QUOTA_REFRESH_MS)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [hostId])
  // Never show host A's quota under host B while B's fetch is pending.
  return state && state.hostId === hostId ? state : { quota: null, fetchedAt: 0 }
}
