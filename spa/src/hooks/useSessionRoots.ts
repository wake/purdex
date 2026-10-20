// spa/src/hooks/useSessionRoots.ts — a session's lineage root, for the sidebar's task-group view (name pool v2 spec §7, L10, NA-6b; TG-2r). A thin reader: the roots come from the device-local root cache
// (lib/task-groups/root-cache.ts), and the reading itself — once per host, one debounced re-read per missed session, only while the view is on — is the lifecycle's (session-roots-lifecycle.ts), which this
// tells what the view wants. Nothing is guessed: a session whose root is not known is `null` — the display files it under 「未分組」, and a WRITE must wait for the real root (`resolveRoot`).
import { useEffect, useMemo } from 'react'
import { useRelayQuotaStore } from '../lib/team/relay-quota'
import { useRootCacheStore } from '../lib/task-groups/root-cache'
import { setRootsInterest, type WantedSession } from '../lib/task-groups/session-roots-lifecycle'

export { ROOT_REREAD_DEBOUNCE_MS, type WantedSession } from '../lib/task-groups/session-roots-lifecycle'

export interface SessionRoots {
  /** The lineage root of an agent session id on a host, or null when no read ever listed it (or there was none). */
  rootOf: (hostId: string, agentSessionId: string) => string | null
  /** The host's quota rows have been read (so `rootOf` can tell a session that is not listed from one not asked about yet). */
  rootsKnown: (hostId: string) => boolean
  /** The lineage roots of EVERY live session the daemon listed for the host (not only the ones with an open tab), or null when the host has not been read. */
  liveRoots: (hostId: string) => ReadonlySet<string> | null
}

export function useSessionRoots(enabled: boolean, wanted: readonly WantedSession[]): SessionRoots {
  const reads = useRelayQuotaStore((s) => s.reads)
  const cached = useRootCacheStore((s) => s.roots)

  useEffect(() => {
    setRootsInterest(enabled, wanted)
    return () => setRootsInterest(false, [])
  }, [enabled, wanted])

  return useMemo<SessionRoots>(() => {
    const live = (hostId: string): readonly { root_session_id: string }[] | null => {
      const read = reads[hostId]
      return read?.ok && read.quotas !== undefined && !read.quotasFailed ? read.quotas : null
    }
    return {
      rootOf: (hostId, id) => cached[hostId]?.[id] ?? null,
      rootsKnown: (hostId) => live(hostId) !== null,
      liveRoots: (hostId) => { const rows = live(hostId); return rows ? new Set(rows.map((r) => r.root_session_id)) : null },
    }
  }, [reads, cached])
}
