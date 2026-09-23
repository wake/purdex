// spa/src/components/settings/profile/useSotProfiles.ts — the profiles a host's daemon holds (`GET
// /api/profiles`), for Settings › Profile (the host's profiles list and the wizard's host step): fetched on mount, on
// a host change and on `reload()`. Never polled: the list is as old as the last look, and every action that
// depends on it is checked again by the daemon (a delete answers 409 `attached`).
//
// A LIST IS OF AN ADDRESS, not only of a host id: the same id may be moved to another machine in another window
// (PR #1340 review). So the list carries the endpoint it was asked at (`endpointOfHost`), a move is a new fetch, and until it answers the old address's list is not shown as this one's.
// THE REQUEST ITSELF IS PINNED to that endpoint (`expectEndpoint`, PR #1340 re-review): the address can move A→B
// between the render and the effect and back to A before anything renders again — unpinned, B's list would be
// labelled A. A refusal (`endpoint-changed`) is no answer about the list: it is dropped, and the list asked again.
// A HOST IN NO STORE has no address to pin to: it is not asked at all (`view: null`, as with no host) — nothing is
// ever sent unpinned, so nothing listed can be acted on unpinned.
import { useCallback, useEffect, useState } from 'react'
import { useHostStore } from '../../../stores/useHostStore'
import { endpointOfHost } from '../../../stores/useProfileStore'
import { listProfiles } from '../../../lib/profile/api'
import type { FailureReason, ProfileIndexEntry } from '../../../lib/profile/api'

export type SotProfilesView =
  | { kind: 'loading' }
  /** `reason`: the failure's class — what a caller SAYS (the wizard); `message` is the transport's own text. */
  | { kind: 'error'; message: string; reason: FailureReason | 'thrown' }
  /** `endpoint`: where the host was when it was asked — and the request was pinned to. */
  | { kind: 'rows'; rows: ProfileIndexEntry[]; endpoint: string }

type Settled = { hostId: string; at: string } & Exclude<SotProfilesView, { kind: 'loading' }>

/** `hostId` null (no master attached), or a host in no store (no address to pin to) → no request, `view: null`. */
export function useSotProfiles(hostId: string | null): { view: SotProfilesView | null; reload: () => void } {
  const [nonce, setNonce] = useState(0)
  const [result, setResult] = useState<Settled | null>(null)
  const reload = useCallback(() => setNonce((n) => n + 1), [])
  const endpoint = useHostStore((s) => {
    const host = hostId === null ? undefined : s.hosts[hostId]
    return host === undefined ? null : endpointOfHost(host)
  })

  useEffect(() => {
    if (hostId === null || endpoint === null) return
    // `cancelled` drops an answer that lands after the host or its address changed (or the page closed).
    let cancelled = false
    listProfiles(hostId, { expectEndpoint: endpoint }).then(
      (r) => {
        if (cancelled) return
        // The host was not at `endpoint` when the request left: nothing was asked. Not an error of the list — ask again.
        if (r.kind === 'failed' && r.reason === 'endpoint-changed') return setNonce((n) => n + 1)
        setResult(r.kind === 'ok' ? { hostId, at: endpoint, kind: 'rows', rows: r.value, endpoint } : { hostId, at: endpoint, kind: 'error', message: r.message, reason: r.reason })
      },
      (e: unknown) => {
        if (!cancelled) setResult({ hostId, at: endpoint, kind: 'error', message: e instanceof Error ? e.message : String(e), reason: 'thrown' })
      },
    )
    return () => {
      cancelled = true
    }
  }, [hostId, endpoint, nonce])

  if (hostId === null || endpoint === null) return { view: null, reload }
  // Another host's (or another address's) answer is stale → loading. This one's is kept while a refetch runs, so a
  // refresh does not flash.
  if (result === null || result.hostId !== hostId || result.at !== endpoint) return { view: { kind: 'loading' }, reload }
  return { view: result, reload }
}
