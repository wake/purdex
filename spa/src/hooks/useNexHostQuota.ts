// spa/src/hooks/useNexHostQuota.ts — the Nexen host's quota (`GET /v1/host` → quota), through the same
// `fetchNexHost` the cost panel uses. One poller per host, shared by every reader (the bottom bar and each worker deck's status row):
// fetched when the first reader mounts, re-read every few minutes while any is mounted, stopped when the last one goes.
// Errors and a non-finite reading are "no quota" (the status bar hides the segments), never an error surface.
import { useCallback, useSyncExternalStore } from 'react'
import { fetchNexHost } from '../lib/nex/nex-api'
import type { NexQuota } from '../lib/nex/types'

export const NEX_QUOTA_REFRESH_MS = 5 * 60 * 1000

type Reading = { quota: NexQuota | null; fetchedAt: number }
const NONE: Reading = { quota: null, fetchedAt: 0 }

interface Poller { reading: Reading; subs: Set<() => void>; timer: ReturnType<typeof setInterval> | null }
const pollers = new Map<string, Poller>()

function load(hostId: string, p: Poller): void {
  fetchNexHost(hostId)
    .then((h) => {
      if (pollers.get(hostId) !== p) return // everyone left while it was on its way
      const q = h.quota
      const valid = !!q && Number.isFinite(q.five_hour_pct) && Number.isFinite(q.seven_day_pct)
      p.reading = { quota: valid ? q : null, fetchedAt: Date.now() }
      for (const cb of [...p.subs]) cb()
    })
    .catch(() => {})
}

function subscribe(hostId: string, cb: () => void): () => void {
  let p = pollers.get(hostId)
  if (!p) {
    const fresh: Poller = { reading: NONE, subs: new Set(), timer: null }
    p = fresh
    pollers.set(hostId, fresh)
    load(hostId, fresh)
    fresh.timer = setInterval(() => load(hostId, fresh), NEX_QUOTA_REFRESH_MS)
  }
  const mine = p
  mine.subs.add(cb)
  return () => {
    mine.subs.delete(cb)
    if (mine.subs.size === 0 && pollers.get(hostId) === mine) {
      if (mine.timer) clearInterval(mine.timer)
      pollers.delete(hostId)
    }
  }
}

/** Tests only: module state outlives a test. */
export const clearNexQuotaShare = (): void => {
  for (const p of pollers.values()) if (p.timer) clearInterval(p.timer)
  pollers.clear()
}

export function useNexHostQuota(hostId: string): Reading {
  // A host's reading is its own: host A's quota is never shown under host B while B's fetch is pending.
  // (a stable subscribe: a new one per render would leave and rejoin the poller on every render)
  const sub = useCallback((cb: () => void) => (hostId ? subscribe(hostId, cb) : () => {}), [hostId])
  return useSyncExternalStore(sub, () => (hostId ? pollers.get(hostId)?.reading ?? NONE : NONE))
}
