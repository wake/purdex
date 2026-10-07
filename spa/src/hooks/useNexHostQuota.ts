// spa/src/hooks/useNexHostQuota.ts — the Nexen host's quota (`GET /v1/host` → quota), through the same
// `fetchNexHost` the cost panel uses. Fetched on mount / host change and re-read every few minutes while mounted;
// errors and a non-finite reading are "no quota" (the status bar hides the segments), never an error surface.
import { useEffect, useState } from 'react'
import { fetchNexHost } from '../lib/nex/nex-api'
import type { NexQuota } from '../lib/nex/types'

export const NEX_QUOTA_REFRESH_MS = 5 * 60 * 1000

export function useNexHostQuota(hostId: string): { quota: NexQuota | null; fetchedAt: number } {
  const [state, setState] = useState<{ hostId: string; quota: NexQuota | null; fetchedAt: number } | null>(null)
  useEffect(() => {
    if (!hostId) return
    let cancelled = false
    const load = () => {
      fetchNexHost(hostId)
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
