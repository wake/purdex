// spa/src/lib/task-groups/root-cache.ts — a session's lineage root, kept on this device (task groups TG-2r; spec §5). A manual task group stores roots, so a relay keeps the membership; the daemon lists the
// root of every live session in `GET /api/team/unattended` (`team.relay_quota.v1`), and what it listed stays here so an offline host still answers.
//   - `rootOf` is a pure cache read: a session that was never listed is `null` (nothing is guessed, never its own id).
//   - `resolveRoot` is the cache's answer if it has one; otherwise it reads that host through the shared reader, caches the page and answers; `null` when the read fails, the host lacks the capability, or the page does
//     not list the session. The daemon read for a root lives here and in session-roots-lifecycle.ts, nowhere else.
// Keys are this device's host ids: the cache is device-local and never goes into the SOT or the sync.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage } from '../storage'
import { STORAGE_KEYS } from '../storage/keys'
import { readFirstPage } from '../team/unattended-reader'
import { hostIdentityNow } from '../team/quota-host'
import type { SessionQuota } from '../team/types'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { selectDaemonIdMismatch, useHostStore } from '../../stores/useHostStore'

type RootRows = readonly (Pick<SessionQuota, 'session_id' | 'root_session_id'> & { address?: string })[]

interface RootCacheState {
  /** host id → agent session id → the root of its chain. */
  roots: Record<string, Record<string, string>>
  /** host id → agent session id → the peer address the quota row carried when it was last listed (the personal panel's persona; kept after the session ends, when no row lists it). */
  addresses: Record<string, Record<string, string>>
  /** Adds what a read listed. Earlier entries stay (a session's root never changes, and a session that ended is not listed any more). */
  ingest: (hostId: string, rows: RootRows) => void
  dropHost: (hostId: string) => void
  /** Keeps the entries `keep` says yes to; a host left with none is dropped. */
  prune: (keep: (hostId: string, sessionId: string) => boolean) => void
}

/** The persisted value read as a cache: anything that is not a string → string map is dropped. */
function sanitize(persisted: unknown, field: 'roots' | 'addresses' = 'roots'): Record<string, Record<string, string>> {
  const raw = (persisted as Record<string, unknown> | null | undefined)?.[field]
  const out: Record<string, Record<string, string>> = {}
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return out
  for (const [hostId, map] of Object.entries(raw)) {
    if (typeof map !== 'object' || map === null || Array.isArray(map)) continue
    const kept: Record<string, string> = {}
    for (const [id, root] of Object.entries(map)) if (typeof root === 'string' && root !== '') kept[id] = root
    if (Object.keys(kept).length > 0) out[hostId] = kept
  }
  return out
}

export const useRootCacheStore = create<RootCacheState>()(
  persist(
    (set) => ({
      roots: {},
      addresses: {},
      ingest: (hostId, rows) =>
        set((state) => {
          const cur = state.roots[hostId] ?? {}
          const curAddr = state.addresses[hostId] ?? {}
          let next: Record<string, string> | null = null
          let nextAddr: Record<string, string> | null = null
          for (const r of rows) {
            if (typeof r.session_id !== 'string' || typeof r.root_session_id !== 'string' || r.root_session_id === '') continue
            if (cur[r.session_id] !== r.root_session_id) {
              next ??= { ...cur }
              next[r.session_id] = r.root_session_id
            }
            if (typeof r.address === 'string' && r.address !== '' && curAddr[r.session_id] !== r.address) {
              nextAddr ??= { ...curAddr }
              nextAddr[r.session_id] = r.address
            }
          }
          if (next === null && nextAddr === null) return state
          return {
            ...(next === null ? null : { roots: { ...state.roots, [hostId]: next } }),
            ...(nextAddr === null ? null : { addresses: { ...state.addresses, [hostId]: nextAddr } }),
          }
        }),
      dropHost: (hostId) =>
        set((state) => {
          if (state.roots[hostId] === undefined && state.addresses[hostId] === undefined) return state
          const roots = { ...state.roots }
          const addresses = { ...state.addresses }
          delete roots[hostId]
          delete addresses[hostId]
          return { roots, addresses }
        }),
      prune: (keep) =>
        set((state) => {
          let changed = false
          const narrow = (src: Record<string, Record<string, string>>) => {
            const out: Record<string, Record<string, string>> = {}
            for (const [hostId, map] of Object.entries(src)) {
              const kept = Object.fromEntries(Object.entries(map).filter(([id]) => keep(hostId, id)))
              if (Object.keys(kept).length !== Object.keys(map).length) changed = true
              if (Object.keys(kept).length > 0) out[hostId] = kept
            }
            return out
          }
          const roots = narrow(state.roots)
          const addresses = narrow(state.addresses)
          return changed ? { roots, addresses } : state
        }),
    }),
    {
      name: STORAGE_KEYS.TASK_GROUP_ROOTS,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({ roots: state.roots, addresses: state.addresses }),
      merge: (persisted, current) => ({ ...current, roots: sanitize(persisted), addresses: sanitize(persisted, 'addresses') }),
    },
  ),
)

/** Whether the host may be asked for roots: its daemon lists `team.relay_quota.v1` (the quota rows carry them) and answers as the daemon it is stored as. */
export function rootsSupported(hostId: string): boolean {
  if (selectDaemonIdMismatch(useHostStore.getState(), hostId) !== undefined) return false
  return useUnattendedStore.getState().byHost[hostId]?.quotaSupport === 'yes'
}

/** The cached root of an agent session on a host, or null. A pure read. */
export function rootOf(hostId: string, sessionId: string): string | null {
  return useRootCacheStore.getState().roots[hostId]?.[sessionId] ?? null
}

/** The peer address the daemon last listed for an agent session (a pure read; null when no read ever listed one). It stays after the session ends. */
export function addressOf(hostId: string, sessionId: string): string | null {
  return useRootCacheStore.getState().addresses[hostId]?.[sessionId] ?? null
}

/** The root of an agent session: the cache's answer, else what the host's daemon lists now (cached); null when neither knows. */
export async function resolveRoot(hostId: string, sessionId: string): Promise<string | null> {
  const cached = rootOf(hostId, sessionId)
  if (cached !== null) return cached
  if (!rootsSupported(hostId)) return null
  const identity = hostIdentityNow(hostId) // the daemon asked: a page that comes back after the host was re-pointed is the old daemon's and must not be cached under the id
  try {
    const view = await readFirstPage(hostId)
    if (identity !== hostIdentityNow(hostId) || !rootsSupported(hostId)) return null
    if (view.quotas !== undefined && !view.quotasFailed) useRootCacheStore.getState().ingest(hostId, view.quotas)
  } catch {
    return null
  }
  return rootOf(hostId, sessionId)
}
