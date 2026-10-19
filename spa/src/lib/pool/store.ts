// spa/src/lib/pool/store.ts — the App's picture of the resource pool (plan "WWD-7 design", data flow). `GET /api/pool/hosts` is read from
// the first connected daemon (host order) that announces `pool.v1`, every 5 s. A failure follows the error table (`classify`): the next
// daemon, the same one again, or a back-off. While the window is hidden nothing is read; turning visible reads at once, then polls again.
// Nothing here is persisted. A host the pool cannot read is not an error here: `poolHostView` falls back to the fleet.
import { create } from 'zustand'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { fetchPoolHosts } from './api'
import { backoffMs, busyDelayMs, classify, type PoolError, type PoolErrorCode } from './errors'
import type { PoolHost } from './types'

export const POLL_MS = 5000
const POOL_CAPABILITY = 'pool.v1'
/** `busy` shows its text only after this many in a row. */
const BUSY_SHOWN_AT = 3

export interface PoolReadFailure { code: PoolErrorCode; since: number }

interface PoolState {
  hosts: PoolHost[]
  /** The daemon the hosts were last read from; null while none answers or none announces `pool.v1`. */
  sourceHostId: string | null
  /** No connected daemon announces `pool.v1`: the old UI stays, with one notice. */
  oldDaemon: boolean
  /** When each pool host (by alias) was last read live (its facts' time), kept across failures for the "since" lines. */
  lastLiveAt: Record<string, number>
  error: PoolReadFailure | null

  /** One read cycle; resolves to the wait before the next. An answer that arrives after `stop()` or a visibility change (a newer generation) is dropped. */
  readOnce: () => Promise<number>
  start: () => void
  stop: () => void
  reset: () => void
}

const initial = { hosts: [], sourceHostId: null, oldDaemon: false, lastLiveAt: {}, error: null }

/** The connected daemons announcing `pool.v1`, in the App's host order. The Nex cache keeps a daemon's capabilities after it drops, so the connection is checked here. */
function poolSources(): string[] {
  const { byHost } = useNexHostStore.getState()
  const { hostOrder, runtime } = useHostStore.getState()
  return hostOrder.filter((id) => runtime[id]?.status === 'connected' && byHost[id]?.daemonCapabilities?.includes(POOL_CAPABILITY))
}

const errorOf = (e: unknown): PoolError => (e as { poolError?: PoolError })?.poolError ?? classify(null, null)

export const usePoolStore = create<PoolState>((set, get) => {
  let failures = 0 // consecutive back-off failures
  let busy = 0 // consecutive `busy`
  let timer: ReturnType<typeof setTimeout> | null = null
  let gen = 0
  let running = false

  const clearTimer = () => { if (timer !== null) { clearTimeout(timer); timer = null } }
  const hidden = () => typeof document !== 'undefined' && document.visibilityState === 'hidden'

  function applyHosts(sourceHostId: string, hosts: PoolHost[]) {
    const lastLiveAt = { ...get().lastLiveAt }
    for (const h of hosts) if (h.reachable && h.facts) lastLiveAt[h.alias] = h.facts.at
    failures = 0
    busy = 0
    set({ hosts, sourceHostId, oldDaemon: false, lastLiveAt, error: null })
  }

  function fail(code: PoolErrorCode) {
    const prev = get().error
    set({ error: { code, since: prev?.code === code ? prev.since : Date.now() } })
  }

  async function readOnce(): Promise<number> {
    const token = gen // a stop() or a visibility change bumps `gen`: whatever this read learns after that is old news
    const sources = poolSources()
    if (sources.length === 0) {
      set({ hosts: [], sourceHostId: null, oldDaemon: true, error: null })
      return POLL_MS
    }
    let last: PoolError = classify(null, null)
    for (const id of sources) {
      try {
        const hosts = await fetchPoolHosts(id)
        if (token !== gen) return POLL_MS
        applyHosts(id, hosts)
        return POLL_MS
      } catch (e) {
        if (token !== gen) return POLL_MS
        last = errorOf(e)
        if (last.action === 'next_source') continue
        break
      }
    }
    if (last.action === 'next_source') {
      // every daemon failed: the pool shows nothing, so each host falls back to the fleet until one answers
      failures = 0
      busy = 0
      set({ hosts: [], sourceHostId: null, oldDaemon: false })
      fail(last.code)
      return POLL_MS
    }
    if (last.action === 'retry_same') {
      busy += 1
      if (busy >= BUSY_SHOWN_AT) fail(last.code)
      return busyDelayMs(Math.random())
    }
    busy = 0
    if (last.action === 'backoff') {
      fail(last.code)
      const wait = backoffMs(failures)
      failures += 1
      return wait
    }
    return POLL_MS
  }

  async function tick(g: number) {
    if (g !== gen) return
    const delay = await get().readOnce()
    if (g !== gen) return
    timer = setTimeout(() => { void tick(g) }, delay)
  }

  function onVisibility() {
    clearTimer()
    gen += 1
    if (!hidden()) void tick(gen)
  }

  return {
    ...initial,
    readOnce,
    start() {
      if (running) return
      running = true
      document.addEventListener('visibilitychange', onVisibility)
      gen += 1
      if (!hidden()) void tick(gen)
    },
    stop() {
      if (!running) return
      running = false
      document.removeEventListener('visibilitychange', onVisibility)
      clearTimer()
      gen += 1
    },
    reset() {
      failures = 0
      busy = 0
      set({ ...initial })
    },
  }
})
