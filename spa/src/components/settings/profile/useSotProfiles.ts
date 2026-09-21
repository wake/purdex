// spa/src/components/settings/profile/useSotProfiles.ts — the profiles a host's daemon holds (`GET
// /api/profiles`), for Settings › Profile: fetched on mount, on a host change and on `reload()`. After
// `useDeviceStateList`. Never polled: the list is as old as the last look, and every action that depends on it
// is checked again by the daemon (a delete answers 409 `attached`).
import { useCallback, useEffect, useState } from 'react'
import { listProfiles } from '../../../lib/profile/api'
import type { ProfileIndexEntry } from '../../../lib/profile/api'

export type SotProfilesView =
  | { kind: 'loading' }
  | { kind: 'error'; message: string }
  | { kind: 'rows'; rows: ProfileIndexEntry[] }

type Settled = { hostId: string } & Exclude<SotProfilesView, { kind: 'loading' }>

/** `hostId` null (no master attached) → no request, `view: null`. */
export function useSotProfiles(hostId: string | null): { view: SotProfilesView | null; reload: () => void } {
  const [nonce, setNonce] = useState(0)
  const [result, setResult] = useState<Settled | null>(null)
  const reload = useCallback(() => setNonce((n) => n + 1), [])

  useEffect(() => {
    if (hostId === null) return
    // `cancelled` drops an answer that lands after the host changed (or the page closed).
    let cancelled = false
    listProfiles(hostId).then(
      (r) => {
        if (cancelled) return
        setResult(r.kind === 'ok' ? { hostId, kind: 'rows', rows: r.value } : { hostId, kind: 'error', message: r.message })
      },
      (e: unknown) => {
        if (!cancelled) setResult({ hostId, kind: 'error', message: e instanceof Error ? e.message : String(e) })
      },
    )
    return () => {
      cancelled = true
    }
  }, [hostId, nonce])

  if (hostId === null) return { view: null, reload }
  // Another host's answer is stale → loading. This host's is kept while a refetch runs, so a refresh does not flash.
  if (result === null || result.hostId !== hostId) return { view: { kind: 'loading' }, reload }
  return { view: result, reload }
}
