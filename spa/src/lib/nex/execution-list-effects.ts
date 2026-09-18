// spa/src/lib/nex/execution-list-effects.ts — the effectful half of the
// per-host execution list (P-C spec §4.3): subscriber refcount, the one
// site-wide SSE per host, its lane reservation, the debounced refetch and the
// guards that decide whether a list answer may still be committed. Where
// the rendered cache lives is the caller's business (`useExecutionListStore`),
// reached through the small `ListSink`.
import { listExecutions } from './nex-api'
import { openNexSse, type NexSseHandle, type NexSseStatus } from './nex-sse'
import { fingerprintOf } from './nex-host-effects'
import { subscriptionSlots } from './subscription-slots'
import { sanitizeExecutionsPage } from './validate-executions'
import { NexApiError, type ExecutionSummary } from './types'
import { isNexReady } from '../../components/hosts/nex/nex-ready'
import { useNexHostStore } from '../../stores/useNexHostStore'

/** Trailing debounce applied to an SSE-triggered refetch (spec §4.4.3). */
export const LIST_REFRESH_DEBOUNCE_MS = 500

export type HostListPhase = 'idle' | 'loading' | 'ready' | 'error'

export interface HostListCache {
  items: ExecutionSummary[]
  phase: HostListPhase
  error: string | null
  /** Last durable seq seen on the site stream; replayed as Last-Event-ID. */
  lastSeq: number | null
  /** Bumped on every committed refresh; consumers key their own follow-up queries on it. */
  refreshRevision: number
}

export type HostListCaches = Record<string, HostListCache>

/** Where the caches are kept. `set` receives the current map and returns the next (same reference = no change). */
export interface ListSink {
  get: () => HostListCaches
  set: (update: (byHost: HostListCaches) => HostListCaches) => void
}

interface HostListRuntime {
  subscribers: Set<string>
  generation: number
  sse: NexSseHandle | null
  reserved: boolean
  debounce: ReturnType<typeof setTimeout> | null
  fetchToken: number
}

export interface ExecutionListEffects {
  subscribe: (hostId: string) => () => void
  refetch: (hostId: string) => void
  clearHost: (hostId: string) => void
  open: (hostId: string) => void
  close: (hostId: string, opts: { dropCache: boolean }) => void
  /** Hosts that currently have at least one subscriber. */
  subscribedHosts: () => string[]
  resetForTests: () => void
}

export const emptyListCache = (refreshRevision = 0): HostListCache =>
  ({ items: [], phase: 'idle', error: null, lastSeq: null, refreshRevision })

const errorText = (err: unknown): string =>
  err instanceof NexApiError ? err.code : err instanceof Error ? err.message : String(err)

const infoReady = (hostId: string): boolean =>
  isNexReady(useNexHostStore.getState().byHost[hostId]?.info)

export function createExecutionListEffects(sink: ListSink): ExecutionListEffects {
  // Rendered state and connection ownership are kept apart: the cache is what
  // a subscriber displays and survives close/reopen (instant re-display),
  // while the runtime — subscriber tokens, SSE handle, timers, the lane
  // reservation — is never rendered and must not be reset by a cache reset
  // (a host identity change wipes the rows but the subscribers still want
  // the next daemon).
  const runtimes = new Map<string, HostListRuntime>()
  let nextToken = 1

  const runtimeOf = (hostId: string): HostListRuntime => {
    let rt = runtimes.get(hostId)
    if (!rt) {
      rt = { subscribers: new Set(), generation: 0, sse: null, reserved: false, debounce: null, fetchToken: 0 }
      runtimes.set(hostId, rt)
    }
    return rt
  }

  const patchCache = (hostId: string, update: (cache: HostListCache) => HostListCache) =>
    sink.set((byHost) => {
      const cur = byHost[hostId]
      if (!cur) return byHost
      const next = update(cur)
      return next === cur ? byHost : { ...byHost, [hostId]: next }
    })

  const ensureCache = (hostId: string) =>
    sink.set((byHost) => (byHost[hostId] ? byHost : { ...byHost, [hostId]: emptyListCache() }))

  function fetch(hostId: string): void {
    const rt = runtimeOf(hostId)
    const generation = rt.generation
    const token = ++rt.fetchToken
    const fingerprint = fingerprintOf(hostId)
    patchCache(hostId, (c) => (c.phase === 'ready' ? c : { ...c, phase: 'loading' }))

    const stillCurrent = () =>
      rt.generation === generation
      && rt.fetchToken === token
      && fingerprintOf(hostId) === fingerprint
      && infoReady(hostId)
      && rt.subscribers.size > 0

    listExecutions(hostId, { includeArchived: false, limit: 100 })
      .then((page) => {
        if (!stillCurrent()) return
        const { items, dropped } = sanitizeExecutionsPage(page)
        if (dropped > 0) console.warn('nex: executions page dropped malformed row(s)', { hostId, dropped })
        patchCache(hostId, (c) => ({ ...c, items, phase: 'ready', error: null, refreshRevision: c.refreshRevision + 1 }))
      })
      .catch((err: unknown) => {
        if (!stillCurrent()) return
        patchCache(hostId, (c) => ({ ...c, phase: 'error', error: errorText(err) }))
      })
  }

  function close(hostId: string, { dropCache }: { dropCache: boolean }): void {
    const rt = runtimeOf(hostId)
    rt.generation += 1
    if (rt.debounce) {
      clearTimeout(rt.debounce)
      rt.debounce = null
    }
    const sse = rt.sse
    rt.sse = null
    sse?.close()
    if (rt.reserved) {
      rt.reserved = false
      subscriptionSlots.unreserve(hostId, 'site-wide')
    }
    patchCache(hostId, (c) => {
      if (dropCache) return emptyListCache(c.refreshRevision)
      return c.phase === 'loading' ? { ...c, phase: 'idle' } : c
    })
  }

  function open(hostId: string): void {
    const rt = runtimeOf(hostId)
    ensureCache(hostId)
    if (rt.sse || !infoReady(hostId)) return
    const generation = rt.generation

    // Reserve before the stream exists: the lane is what keeps a fifth pane
    // SSE from taking the browser connection this stream is about to use.
    rt.reserved = true
    subscriptionSlots.reserve(hostId, 'site-wide')

    const scheduleRefetch = () => {
      if (rt.debounce) clearTimeout(rt.debounce)
      rt.debounce = setTimeout(() => {
        rt.debounce = null
        fetch(hostId)
      }, LIST_REFRESH_DEBOUNCE_MS)
    }

    let prevStatus: NexSseStatus | null = null
    const handle = openNexSse({
      hostId,
      url: '/api/nex/v1/events',
      getLastEventId: () => sink.get()[hostId]?.lastSeq ?? null,
      onFrame: (frame) => {
        if (rt.generation !== generation) return
        if (frame.id != null) {
          const seq = Number(frame.id)
          patchCache(hostId, (c) => (c.lastSeq !== null && c.lastSeq >= seq ? c : { ...c, lastSeq: seq }))
        }
        scheduleRefetch()
      },
      onStatus: (status, err) => {
        if (rt.generation !== generation) return
        if (status === 'closed' && err) {
          close(hostId, { dropCache: false })
          patchCache(hostId, (c) => ({ ...c, phase: 'error', error: errorText(err) }))
          return
        }
        if (status === 'open' && prevStatus === 'reconnecting') scheduleRefetch()
        prevStatus = status
      },
    })
    // openNexSse may report a terminal `closed` synchronously, before it
    // returns (nex-sse.ts: a host missing from the host store is refused
    // without awaiting). That ran `close()` above: the generation moved on,
    // the lane is already released and the cache carries the error. Keeping
    // the (already dead) handle would make `refetch` re-fetch instead of
    // reopen and leave nothing to close on unsubscribe; fetching would
    // commit rows for a stream that is not there.
    if (rt.generation !== generation) {
      handle.close()
      return
    }
    rt.sse = handle
    fetch(hostId)
  }

  const subscribe = (hostId: string): (() => void) => {
    const rt = runtimeOf(hostId)
    const token = `sub-${nextToken++}`
    rt.subscribers.add(token)
    if (rt.subscribers.size === 1) open(hostId)
    else ensureCache(hostId)
    return () => {
      if (!rt.subscribers.delete(token)) return
      if (rt.subscribers.size === 0) close(hostId, { dropCache: false })
    }
  }

  const refetch = (hostId: string): void => {
    const rt = runtimes.get(hostId)
    if (!rt || rt.subscribers.size === 0) return
    if (rt.sse) fetch(hostId)
    else open(hostId)
  }

  const clearHost = (hostId: string): void => {
    close(hostId, { dropCache: true })
    sink.set((byHost) => {
      if (!(hostId in byHost)) return byHost
      const next = { ...byHost }
      delete next[hostId]
      return next
    })
  }

  const subscribedHosts = (): string[] =>
    [...runtimes].filter(([, rt]) => rt.subscribers.size > 0).map(([hostId]) => hostId)

  const resetForTests = (): void => {
    for (const rt of runtimes.values()) {
      if (rt.debounce) clearTimeout(rt.debounce)
    }
    runtimes.clear()
    nextToken = 1
  }

  return { subscribe, refetch, clearHost, open, close, subscribedHosts, resetForTests }
}
