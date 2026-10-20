// spa/src/lib/pool/store.ts — the App's picture of the resource pool (plan "WWD-7 design", data flow). `GET /api/pool/hosts` is read from
// the first connected daemon (host order) that announces `pool.v1`, every 5 s. A failure follows the error table (`classify`): the next
// daemon, the same one again, or a back-off. While the window is hidden nothing is read; turning visible reads at once, then polls again.
// When the source daemon announces `pool.events.v1` (WWD-7a-1c) the poll stops: a read follows each `pool.changed`, the host-events
// connection of the source opening again, and the window turning visible. A failure still retries on its own timer, and a source
// that lost the capability (or another source without it) brings the 5 s poll back.
// Nothing here is persisted. A host the pool cannot read is not an error here: `poolHostState` says `wwd_down`, `host_down` or `unknown`.
import { create } from 'zustand'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { fetchPoolHosts } from './api'
import { backoffMs, busyDelayMs, classify, type PoolError, type PoolErrorCode } from './errors'
import { hostIsOk, LIVE_MS } from './host-state'
import type { PoolHost } from './types'

export const POLL_MS = 5000
const POOL_CAPABILITY = 'pool.v1'
const POOL_EVENTS_CAPABILITY = 'pool.events.v1'
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
  /** When each host's facts were last read (the facts' time), by wwd `host_id` (an alias change does not move it); kept across failures for the "last reading" lines. */
  lastFactsAt: Record<string, number>
  error: PoolReadFailure | null

  /** One read cycle; resolves to the wait before the next. An answer that arrives after `stop()` or a visibility change (a newer generation) is dropped. */
  readOnce: () => Promise<number>
  /** `pool.changed` from `hostId`, or its host-events connection opening: one read when `hostId` is the source and the source drives itself by events. */
  poolEvent: (hostId: string) => void
  start: () => void
  stop: () => void
  reset: () => void
}

const initial = { hosts: [], sourceHostId: null, oldDaemon: false, lastFactsAt: {}, error: null }

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

/** Does this daemon announce `pool.events.v1`? (The daemon declares it only while its watcher runs, so it can come and go.) */
const announcesEvents = (id: string | null): boolean =>
  id !== null && useNexHostStore.getState().byHost[id]?.daemonCapabilities?.includes(POOL_EVENTS_CAPABILITY) === true

const errorOf = (e: unknown): PoolError => (e as { poolError?: PoolError })?.poolError ?? classify(null, null)

export const usePoolStore = create<PoolState>((set, get) => {
  let failures = 0 // consecutive back-off failures
  let pinned: string | null = null // the source a busy or back-off answer came from
  let busy = 0 // consecutive `busy`
  let timer: ReturnType<typeof setTimeout> | null = null
  let expiryTimer = false // `timer` is the facts-expiry wait of event mode, not a poll or retry timer: a change to polling must replace it
  let gen = 0
  let running = false
  let inflight = false // a read cycle is under way
  let again = false // an event came during it: read once more right after

  let lastOk = false // the last read cycle was answered by its source (`busy` and the first back-off failures publish no error, but they are failures: their retry timer must stay)
  let readFirst: string | null = null // the first source in host order when the read under way started
  /**
   * The reads are event-driven: the last one was answered by the FIRST connected pool daemon (host order, as a read picks it), it announces
   * `pool.events.v1`, and every host is ok. `liveness_at` moves without a `pool.changed` (events.go), so while any host is not ok the 5 s poll stays (M20).
   */
  const eventDriven = () => {
    const src = get().sourceHostId
    if (!(lastOk && src !== null && announcesEvents(src) && poolSources()[0] === src)) return false
    const now = Date.now()
    return get().hosts.every((h) => hostIsOk(h, now))
  }

  const clearTimer = () => { if (timer !== null) { clearTimeout(timer); timer = null } expiryTimer = false }

  /**
   * Event mode, every host ok: the one read still due is when the earliest facts stop being fresh. A peer can keep saying `ok` while its facts
   * stop advancing and nothing emits `pool.changed` for that, so without it the host would stay ok in the cards' check lines for good. Every
   * ok host has fresh facts (`poolHostState`: at most `MAX_SKEW_MS` ahead, at most `LIVE_MS` old), so the wait is always finite and short.
   */
  const factsExpiryMs = (): number | null => {
    const now = Date.now()
    let soonest: number | null = null
    for (const h of get().hosts) {
      if (!h.facts) continue
      const left = h.facts.at + LIVE_MS + 1 - now
      if (left > 0 && (soonest === null || left < soonest)) soonest = left
    }
    return soonest
  }
  const armExpiry = (g: number) => {
    clearTimer()
    const wait = factsExpiryMs()
    if (wait === null) return
    expiryTimer = true
    timer = setTimeout(() => { timer = null; expiryTimer = false; void tick(g) }, wait)
  }
  const hidden = () => typeof document !== 'undefined' && document.visibilityState === 'hidden'

  function applyHosts(sourceHostId: string, hosts: PoolHost[]) {
    const lastFactsAt = { ...get().lastFactsAt }
    for (const h of hosts) {
      if (!h.reachable || !h.facts) continue
      lastFactsAt[h.host_id] = h.facts.at
    }
    failures = 0
    busy = 0
    lastOk = true
    set({ hosts, sourceHostId, oldDaemon: false, lastFactsAt, error: null })
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
    lastOk = false // until this cycle is answered
    readFirst = poolSources()[0] ?? null
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
    if (g !== gen || inflight) return
    inflight = true
    let delay: number
    try {
      delay = await get().readOnce()
    } finally {
      if (g === gen) inflight = false // a read of an older generation does not clear the newer one's mark
    }
    if (g !== gen) return
    if (again) { again = false; void tick(g); return }
    // no poll timer: the next read comes with an event, a reconnect or the window turning visible (or when the freshest facts expire)
    if (eventDriven()) { armExpiry(g); return }
    timer = setTimeout(() => { timer = null; expiryTimer = false; void tick(g) }, delay) // a fired timer is no timer: onCapabilities reads `timer` to know whether one is pending
  }

  function onVisibility() {
    clearTimer()
    gen += 1
    again = false
    inflight = false
    if (!hidden()) void tick(gen)
  }

  /** The capability (or the source's connection) can change while nothing is read: gone → the poll is back at once, there → the timer is not needed. */
  function onCapabilities() {
    if (!running || hidden()) return
    // a read under way chose its source before this change: when the first source is another one now, read once more after it
    if (inflight) { if (poolSources()[0] !== readFirst) again = true; return }
    if (eventDriven()) armExpiry(gen)
    else if (timer === null || expiryTimer) { clearTimer(); void tick(gen) }
  }
  let unsubscribe: (() => void)[] = []

  return {
    ...initial,
    readOnce,
    poolEvent(hostId) {
      if (!running || hidden() || hostId !== get().sourceHostId) return
      // a read under way has not been answered yet (`lastOk` is false until it is), so an event during it is kept for the next one
      if (inflight) { again = true; return }
      if (!eventDriven()) return
      clearTimer()
      void tick(gen)
    },
    start() {
      if (running) return
      running = true
      document.addEventListener('visibilitychange', onVisibility)
      unsubscribe = [
        useNexHostStore.subscribe((st, prev) => { if (st.byHost !== prev.byHost) onCapabilities() }),
        useHostStore.subscribe((st, prev) => { if (st.runtime !== prev.runtime || st.hostOrder !== prev.hostOrder) onCapabilities() }), // the source dropped
      ]
      gen += 1
      if (!hidden()) void tick(gen)
    },
    stop() {
      if (!running) return
      running = false
      document.removeEventListener('visibilitychange', onVisibility)
      for (const u of unsubscribe) u()
      unsubscribe = []
      clearTimer()
      gen += 1
      again = false
      inflight = false
    },
    reset() {
      lastOk = false
      failures = 0
      pinned = null
      busy = 0
      set({ ...initial })
    },
  }
})
