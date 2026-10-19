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

/**
 * Is it known that the daemons are older than the pool? Only when a connected daemon's capabilities have been read and none announces
 * `pool.v1` — and none does anywhere, a disconnected one included (it may simply be down). Startup, a drop and an unprobed host are "not known".
 */
function knownOld(): boolean {
  const { byHost } = useNexHostStore.getState()
  const { hostOrder, runtime } = useHostStore.getState()
  const announces = (id: string) => byHost[id]?.daemonCapabilities?.includes(POOL_CAPABILITY) === true
  if (hostOrder.some(announces)) return false
  // `daemonCapabilities` is absent until `/api/info` answered, whatever Nexen's phase is.
  return hostOrder.some((id) => runtime[id]?.status === 'connected' && byHost[id]?.daemonCapabilities !== undefined)
}

const errorOf = (e: unknown): PoolError => (e as { poolError?: PoolError })?.poolError ?? classify(null, null)

export const usePoolStore = create<PoolState>((set, get) => {
  let failures = 0 // consecutive back-off failures
  let pinned: string | null = null // the source a busy or back-off answer came from
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

  function forgetPin() {
    pinned = null
    failures = 0
    busy = 0
  }

  function fail(code: PoolErrorCode) {
    const prev = get().error
    set({ error: { code, since: prev?.code === code ? prev.since : Date.now() } })
  }

  async function readOnce(): Promise<number> {
    const token = gen // a stop() or a visibility change bumps `gen`: whatever this read learns after that is old news
    const sources = poolSources()
    // the retry state belongs to the source that earned it: a source that is gone takes it along
    if (sources.length === 0 || (pinned && !sources.includes(pinned))) forgetPin()
    if (sources.length === 0) {
      set({ hosts: [], sourceHostId: null, oldDaemon: knownOld(), error: null })
      return POLL_MS
    }
    let last: PoolError = classify(null, null)
    let failedOn: string | null = null
    // a source that answered busy or failed is read again on its own until it recovers; if it then says next_source, the rest follow
    const ordered = pinned && sources.includes(pinned) ? [pinned, ...sources.filter((id) => id !== pinned)] : sources
    for (const id of ordered) {
      try {
        const hosts = await fetchPoolHosts(id)
        if (token !== gen) return POLL_MS
        pinned = null
        applyHosts(id, hosts)
        return POLL_MS
      } catch (e) {
        if (token !== gen) return POLL_MS
        last = errorOf(e)
        if (last.action === 'next_source') { forgetPin(); continue }
        failedOn = id
        break
      }
    }
    if (last.action === 'next_source') {
      // every daemon failed: the pool shows nothing, so each host falls back to the fleet until one answers
      pinned = null
      failures = 0
      busy = 0
      set({ hosts: [], sourceHostId: null, oldDaemon: false })
      fail(last.code)
      return POLL_MS
    }
    if (last.action === 'retry_same') {
      pinned = failedOn
      busy += 1
      if (busy >= BUSY_SHOWN_AT) fail(last.code)
      return busyDelayMs(Math.random())
    }
    busy = 0
    if (last.action === 'backoff') {
      pinned = failedOn
      fail(last.code)
      const wait = backoffMs(failures)
      failures += 1
      return wait
    }
    // read_only, log, none, no_chart: nothing to retry differently, but the failure is published and replaces an older one
    pinned = null
    fail(last.code)
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
      pinned = null
      busy = 0
      set({ ...initial })
    },
  }
})
