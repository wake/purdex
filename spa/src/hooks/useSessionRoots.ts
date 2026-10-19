// spa/src/hooks/useSessionRoots.ts — a session's lineage root, for the sidebar's task-group view (name pool v2 spec §7, L10, NA-6b).
//
// The one source is `UnattendedView.quotas` (`GET /api/team/unattended`, `team.relay_quota.v1`): the daemon lists every live session of the host with the root of its chain (`session_id`, `root_session_id`) —
// the formal session → root map, not a borrowed field. A manual task group stores roots, so a relay keeps the membership. The read goes through the shared reader (it joins a read already out, and seeds
// `useRelayQuotaStore.reads`, which this reads from), and only while the view is open:
//   - once when the view opens (a host that has no read yet), and again when a reconnect fenced the host (its read is gone);
//   - once more, debounced, when a live session shows up that the last read did not list — once per session, so a daemon that never lists it is not asked again and again.
// A host whose daemon lacks `task_groups.v1` is never asked (the capability implies the quota rows) and reports no roots. Nothing is guessed: a session whose root is not known is `null` — the display files it
// under 「未分組」, and a WRITE must wait for the real root (callers check `rootsKnown`/`rootOf` before writing).
import { useEffect, useMemo, useRef } from 'react'
import { useRelayQuotaStore } from '../lib/team/relay-quota'
import { readFirstPage } from '../lib/team/unattended-reader'
import { taskGroupsSupported } from '../lib/task-groups/loader'

export const ROOT_REREAD_DEBOUNCE_MS = 800

export interface WantedSession { hostId: string; agentSessionId: string }

export interface SessionRoots {
  /** The lineage root of an agent session id on a host, or null when the last read did not list it (or there was none). */
  rootOf: (hostId: string, agentSessionId: string) => string | null
  /** The host's quota rows have been read (so `rootOf` can tell a session that is not listed from one not asked about yet). */
  rootsKnown: (hostId: string) => boolean
}

export function useSessionRoots(enabled: boolean, wanted: readonly WantedSession[]): SessionRoots {
  const reads = useRelayQuotaStore((s) => s.reads)

  const index = useMemo(() => {
    const byHost = new Map<string, Map<string, string>>()
    for (const [hostId, read] of Object.entries(reads)) {
      if (!read.ok || read.quotas === undefined || read.quotasFailed) continue
      byHost.set(hostId, new Map(read.quotas.map((r) => [r.session_id, r.root_session_id])))
    }
    return byHost
  }, [reads])

  const asked = useRef(new Map<string, Set<string>>()) // per host: the sessions already asked about in this epoch

  // Hosts to read now: wanted, supported, and with no read at all (never read, or fenced by a reconnect). A failed read is a read — it does not loop.
  const unread = useMemo(() => {
    if (!enabled) return ''
    const hosts = new Set<string>()
    for (const w of wanted) if (taskGroupsSupported(w.hostId) && reads[w.hostId] === undefined) hosts.add(w.hostId)
    return [...hosts].sort().join('\n')
  }, [enabled, wanted, reads])
  useEffect(() => {
    if (unread === '') return
    for (const hostId of unread.split('\n')) {
      asked.current.delete(hostId) // a new epoch of the host: what was asked about in the old one is asked again if the new read misses it
      readFirstPage(hostId).catch(() => {}) // a failure lands in the store as the host's read
    }
  }, [unread])

  // Sessions the last read did not list: one more read, debounced, once per session per epoch (what was asked about already is filtered in the effect — a ref is read in effects, not while rendering).
  const unknown = useMemo(() => {
    if (!enabled) return ''
    const keys: string[] = []
    for (const w of wanted) {
      const map = index.get(w.hostId)
      if (!map || !taskGroupsSupported(w.hostId) || map.has(w.agentSessionId)) continue
      keys.push(`${w.hostId}\n${w.agentSessionId}`)
    }
    return keys.sort().join('\n\n')
  }, [enabled, wanted, index])
  useEffect(() => {
    if (unknown === '') return
    const fresh = unknown.split('\n\n').map((key) => key.split('\n') as [string, string]).filter(([hostId, id]) => !asked.current.get(hostId)?.has(id))
    if (fresh.length === 0) return
    const timer = setTimeout(() => {
      const hosts = new Set<string>()
      for (const [hostId, id] of fresh) {
        hosts.add(hostId)
        const set = asked.current.get(hostId) ?? new Set<string>()
        set.add(id)
        asked.current.set(hostId, set)
      }
      for (const hostId of hosts) readFirstPage(hostId).catch(() => {})
    }, ROOT_REREAD_DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [unknown])

  return useMemo<SessionRoots>(() => ({
    rootOf: (hostId, id) => index.get(hostId)?.get(id) ?? null,
    rootsKnown: (hostId) => index.has(hostId),
  }), [index])
}
