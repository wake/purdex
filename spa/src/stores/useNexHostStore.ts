// spa/src/stores/useNexHostStore.ts — per-host cache of "is Nexen ready
// here?" (P-C spec §4.1): `/api/info.nex` plus `GET /v1/capabilities`, one
// truth shared by the Host → Nex page, the Headless NewTab section and the
// hand-off entry points. Not persisted, not synced.
import { create } from 'zustand'
import { fetchInfo, type NexInfo } from '../lib/host-api'
import { fetchNexCapabilities } from '../lib/nex/nex-api'
import type { NexCapabilities } from '../lib/nex/types'
import { isNexReady } from '../components/hosts/nex/nex-ready'
import { useHostStore, type HostInfo } from './useHostStore'

export const NEX_HOST_TTL_MS = 60_000

export type NexHostPhase = 'unknown' | 'loading' | 'ready' | 'disabled' | 'unavailable'

export interface NexHostEntry {
  info: NexInfo | null
  capabilities: NexCapabilities | null
  phase: NexHostPhase
  error: string | null
  fetchedAt: number
  generation: number
}

interface NexHostState {
  byHost: Record<string, NexHostEntry>
  ensure: (hostId: string) => Promise<void>
  invalidate: (hostId: string) => Promise<void>
  clearHost: (hostId: string) => void
}

type Loaded = Pick<NexHostEntry, 'info' | 'capabilities' | 'error'>

/** The one place `phase` is derived, so `ready` can never outlive its inputs. */
function phaseOf({ info, capabilities, error }: Loaded): NexHostPhase {
  if (!info) return 'unavailable'
  if (!info.configured || !info.mounted) return 'disabled'
  if (info.init_error) return 'unavailable'
  if (!isNexReady(info)) return 'unavailable'
  if (error !== null || capabilities === null) return 'unavailable'
  return 'ready'
}

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

function readJson(r: Response, path: string): Promise<HostInfo> {
  return r.ok ? r.json() : Promise.reject(new Error(`${path}: ${r.status}`))
}

async function load(hostId: string): Promise<Loaded> {
  let info: NexInfo | null
  try {
    const data = await fetchInfo(hostId).then((r) => readJson(r, '/api/info'))
    info = data.nex ?? null
  } catch (err) {
    return { info: null, capabilities: null, error: errorText(err) }
  }
  if (!info) return { info, capabilities: null, error: '/api/info: no nex section' }
  if (!info.configured || !info.mounted) return { info, capabilities: null, error: null }
  if (info.init_error) return { info, capabilities: null, error: info.init_error }
  if (!isNexReady(info)) return { info, capabilities: null, error: null }
  try {
    return { info, capabilities: await fetchNexCapabilities(hostId), error: null }
  } catch (err) {
    return { info, capabilities: null, error: errorText(err) }
  }
}

/**
 * A host id is not an endpoint: between a request leaving and its answer
 * landing, the id can be re-pointed at another daemon, removed, or removed
 * and re-added. `hostFetch` would even route an unknown id to a different
 * host. So a request captures the entry's generation and the endpoint it
 * went to, and commits only while both still hold — a stale answer is
 * dropped, never written into (or resurrected as) an entry.
 */
interface RequestToken { generation: number; endpoint: string }

/** The address a request for `hostId` goes to, or `null` when the host is unknown. */
function endpointOf(hostId: string): string | null {
  const h = useHostStore.getState().hosts[hostId]
  return h ? `${h.ip}:${h.port}` : null
}

let nextGeneration = 1
const inflight = new Map<string, Promise<void>>()

function emptyEntry(): NexHostEntry {
  return { info: null, capabilities: null, phase: 'loading', error: null, fetchedAt: 0, generation: nextGeneration++ }
}

function isFresh(entry: NexHostEntry | undefined, now: number): boolean {
  if (!entry || entry.fetchedAt === 0 || entry.phase === 'unavailable') return false
  return now - entry.fetchedAt < NEX_HOST_TTL_MS
}

export const useNexHostStore = create<NexHostState>()((set, get) => {
  const dropEntry = (hostId: string) =>
    set((s) => {
      if (!(hostId in s.byHost)) return s
      const byHost = { ...s.byHost }
      delete byHost[hostId]
      return { byHost }
    })

  const stillCurrent = (hostId: string, token: RequestToken): boolean =>
    get().byHost[hostId]?.generation === token.generation && endpointOf(hostId) === token.endpoint

  async function fetchAndCommit(hostId: string, token: RequestToken): Promise<void> {
    const loaded = await load(hostId)
    if (!stillCurrent(hostId, token)) return
    set((s) => ({
      byHost: {
        ...s.byHost,
        [hostId]: { ...loaded, phase: phaseOf(loaded), fetchedAt: Date.now(), generation: token.generation },
      },
    }))
  }

  return {
    byHost: {},

    ensure: (hostId) => {
      // Guarded here and again at commit time: `hostFetch` falls back to
      // another host for an unknown id, so an entry must never come from it.
      const endpoint = endpointOf(hostId)
      if (endpoint === null) {
        dropEntry(hostId)
        return Promise.resolve()
      }
      const running = inflight.get(hostId)
      if (running) return running
      const existing = get().byHost[hostId]
      if (isFresh(existing, Date.now())) return Promise.resolve()

      const entry = existing ?? emptyEntry()
      if (!existing) set((s) => ({ byHost: { ...s.byHost, [hostId]: entry } }))
      const token: RequestToken = { generation: entry.generation, endpoint }
      const p = fetchAndCommit(hostId, token).finally(() => {
        if (inflight.get(hostId) === p) inflight.delete(hostId)
      })
      inflight.set(hostId, p)
      return p
    },

    invalidate: (hostId) => {
      inflight.delete(hostId)
      set((s) => {
        const cur = s.byHost[hostId]
        if (!cur) return s
        return { byHost: { ...s.byHost, [hostId]: { ...cur, fetchedAt: 0, generation: nextGeneration++ } } }
      })
      return get().ensure(hostId)
    },

    clearHost: (hostId) => {
      inflight.delete(hostId)
      dropEntry(hostId)
    },
  }
})

export function selectReady(hostId: string): (s: Pick<NexHostState, 'byHost'>) => boolean {
  return (s) => s.byHost[hostId]?.phase === 'ready'
}

export function selectHandoffReady(hostId: string): (s: Pick<NexHostState, 'byHost'>) => boolean {
  return (s) => {
    const entry = s.byHost[hostId]
    if (entry?.phase !== 'ready' || !entry.capabilities) return false
    return entry.capabilities.delegate?.resume_session_id === true
      && entry.capabilities.sandbox_profiles.includes('handoff')
  }
}

/**
 * Refetch a host's readiness when its daemon comes (back) online. Same shape as
 * `startPeerCacheInvalidation`: one module-level subscription for the app's
 * lifetime, started from main.tsx; returns its unsubscribe. Only hosts someone
 * has already asked about are refetched — a reconnect is not a reason to poll
 * every daemon's Nexen state.
 */
export function startNexHostInvalidation(): () => void {
  return useHostStore.subscribe((next, prev) => {
    if (next.runtime === prev.runtime) return
    for (const hostId of Object.keys(next.runtime)) {
      const connected = next.runtime[hostId]?.status === 'connected'
      const wasConnected = prev.runtime[hostId]?.status === 'connected'
      if (connected && !wasConnected && useNexHostStore.getState().byHost[hostId]) {
        void useNexHostStore.getState().invalidate(hostId)
      }
    }
  })
}
