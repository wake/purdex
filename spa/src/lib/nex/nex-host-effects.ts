// spa/src/lib/nex/nex-host-effects.ts — the effectful half of the per-host
// Nexen readiness cache (P-C spec §4.1): the `/api/info` + `/v1/capabilities`
// fetch, in-flight dedup, the generation counter and the host-identity
// fingerprint that together decide whether an answer may be committed.
// Entry shapes and transitions come from `nex-host-reducer.ts`; where the
// entries live is the caller's business (`useNexHostStore.ts`), reached
// through the small `EntrySink`.
import { fetchInfo } from '../host-api'
import type { HostInfo } from '../../stores/useHostStore'
import { fetchNexCapabilities } from './nex-api'
import { isNexReady } from '../../components/hosts/nex/nex-ready'
import { hostEndpoint, useHostStore } from '../../stores/useHostStore'
import {
  commitLoaded,
  emptyEntry,
  hostFingerprint,
  isFresh,
  markStale,
  type Loaded,
  type NexHostEntry,
  type RequestToken,
} from './nex-host-reducer'

export type NexHostEntries = Record<string, NexHostEntry>

/** Where the entries are kept. `set` receives the current map and returns the next (same reference = no change). */
export interface EntrySink {
  get: () => NexHostEntries
  set: (update: (byHost: NexHostEntries) => NexHostEntries) => void
}

export interface NexHostEffects {
  ensure: (hostId: string) => Promise<void>
  invalidate: (hostId: string) => Promise<void>
  clearHost: (hostId: string) => void
}

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

function readJson(r: Response, path: string): Promise<HostInfo> {
  return r.ok ? r.json() : Promise.reject(new Error(`${path}: ${r.status}`))
}

/** One fetch round, plus the daemon's `host_id` from `/api/info` (`''` when unknown). */
async function load(hostId: string): Promise<{ loaded: Loaded; observed: string }> {
  let info: Loaded['info']
  let observed = ''
  try {
    const data = await fetchInfo(hostId).then((r) => readJson(r, '/api/info'))
    info = data.nex ?? null
    if (typeof data.host_id === 'string') observed = data.host_id
  } catch (err) {
    return { loaded: { info: null, capabilities: null, error: errorText(err) }, observed }
  }
  return { loaded: await loadCapabilities(hostId, info), observed }
}

async function loadCapabilities(hostId: string, info: Loaded['info']): Promise<Loaded> {
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
 * A host id is not a daemon: between a request leaving and its answer
 * landing, the id can be re-pointed at another address, given another token
 * (a different identity on the same address), removed, or removed and
 * re-added. `hostFetch` would even route an unknown id to a different host.
 * So a request captures the entry's generation and the host identity it
 * went to, and commits only while both still hold — a stale answer is
 * dropped, never written into (or resurrected as) an entry. The same
 * identity is stamped on the entry, so a cached answer is only reused for
 * the daemon it came from.
 */

/** The identity a request for `hostId` goes to (`ip:port:token`), or `null` when the host is unknown. */
export function fingerprintOf(hostId: string): string | null {
  const h = useHostStore.getState().hosts[hostId]
  return h ? hostFingerprint(h) : null
}

let nextGeneration = 1

export function createNexHostEffects(sink: EntrySink): NexHostEffects {
  const inflight = new Map<string, Promise<void>>()

  const dropEntry = (hostId: string) =>
    sink.set((byHost) => {
      if (!(hostId in byHost)) return byHost
      const next = { ...byHost }
      delete next[hostId]
      return next
    })

  const stillCurrent = (hostId: string, token: RequestToken): boolean =>
    sink.get()[hostId]?.generation === token.generation && fingerprintOf(hostId) === token.fingerprint

  async function fetchAndCommit(hostId: string, token: RequestToken): Promise<void> {
    const host = useHostStore.getState().hosts[hostId]
    const endpoint = host ? hostEndpoint(host) : ''
    const { loaded, observed } = await load(hostId)
    if (!stillCurrent(hostId, token)) return
    // The same answer is also this device's daemon-identity check (spec 2026-09-23 D4.3).
    useHostStore.getState().observeDaemonId(hostId, observed, endpoint)
    sink.set((byHost) => ({ ...byHost, [hostId]: commitLoaded(loaded, token, Date.now()) }))
  }

  const ensure = (hostId: string): Promise<void> => {
    // Guarded here and again at commit time: `hostFetch` falls back to
    // another host for an unknown id, so an entry must never come from it.
    const fingerprint = fingerprintOf(hostId)
    if (fingerprint === null) {
      dropEntry(hostId)
      return Promise.resolve()
    }
    const running = inflight.get(hostId)
    if (running) return running
    const existing = sink.get()[hostId]
    if (isFresh(existing, Date.now(), fingerprint)) return Promise.resolve()

    const entry = existing ?? emptyEntry(nextGeneration++, fingerprint)
    if (!existing) sink.set((byHost) => ({ ...byHost, [hostId]: entry }))
    const token: RequestToken = { generation: entry.generation, fingerprint }
    const p = fetchAndCommit(hostId, token).finally(() => {
      if (inflight.get(hostId) === p) inflight.delete(hostId)
    })
    inflight.set(hostId, p)
    return p
  }

  const invalidate = (hostId: string): Promise<void> => {
    inflight.delete(hostId)
    sink.set((byHost) => {
      const cur = byHost[hostId]
      if (!cur) return byHost
      return { ...byHost, [hostId]: markStale(cur, nextGeneration++) }
    })
    return ensure(hostId)
  }

  const clearHost = (hostId: string): void => {
    inflight.delete(hostId)
    dropEntry(hostId)
  }

  return { ensure, invalidate, clearHost }
}
