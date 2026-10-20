// spa/src/lib/pool/alerts-store.ts — every pool host's alerts (monitoring tabs spec M35, M36; plan MON-10a), read through the pool store's source daemon and the hosts it lists. When it reads:
//   - the source or the host list changed: each host not read yet, once (a new source is a new generation, so the old source's late answers are not written; a host the pool no longer lists is dropped);
//   - the source daemon's `alerts.changed {host}`: that host only; the source's host-events connection opening: all of them;
//   - a source that does not announce `pool.alerts.v1`: all of them every 30 s (the daemon reads the peers on that beat itself); one that does: no timer.
// While the pool cannot be read (no source) the last answers stay and nothing is read, so a number does not fall to zero (W4's spirit). Held open with `retainPoolAlerts()`; `useRunPool` holds it while the sidebar's pool runs.
import { create } from 'zustand'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { fetchHostAlerts, type PoolAlert } from './alerts-api'
import { classify, type PoolError, type PoolErrorCode } from './errors'
import { usePoolStore } from './store'

export const ALERTS_CAPABILITY = 'pool.alerts.v1'
export const ALERTS_POLL_MS = 30_000

export type HostAlertsEntry =
  | { kind: 'ok'; known: boolean; alerts: PoolAlert[]; at: number }
  | { kind: 'error'; code: PoolErrorCode; at: number }

interface AlertsState {
  /** By pool host alias. */
  byHost: Record<string, HostAlertsEntry>
  /** The daemon the entries were read through. */
  source: string | null
  reset: () => void
}

export const useAlertsStore = create<AlertsState>(() => ({ byHost: {}, source: null, reset: () => { useAlertsStore.setState({ byHost: {}, source: null }); gen++; latest.clear() } }))

const errorOf = (e: unknown): PoolError => (e as { poolError?: PoolError })?.poolError ?? classify(null, null)
const announcesAlerts = (id: string | null): boolean => id !== null && useNexHostStore.getState().byHost[id]?.daemonCapabilities?.includes(ALERTS_CAPABILITY) === true

let gen = 0
let mounted = 0
let stopWatching: (() => void) | null = null
let timer: ReturnType<typeof setInterval> | null = null

const listed = (): string[] => usePoolStore.getState().hosts.map((h) => h.alias)

/** The newest read started for each host: an older read that finishes later (an event during the first read, the poll during an event's) must not put its list over the newer one. */
const latest = new Map<string, number>()
let readSeq = 0

async function readHost(source: string, alias: string, token: number): Promise<void> {
  const mine = ++readSeq
  latest.set(alias, mine)
  let entry: HostAlertsEntry
  try {
    const a = await fetchHostAlerts(source, alias)
    entry = { kind: 'ok', known: a.known, alerts: a.alerts, at: Date.now() }
  } catch (e) {
    entry = { kind: 'error', code: errorOf(e).code, at: Date.now() }
  }
  // A newer source, a stop, or a host the pool dropped meanwhile: this answer is old news.
  if (token !== gen || latest.get(alias) !== mine || usePoolStore.getState().sourceHostId !== source || !listed().includes(alias)) return
  useAlertsStore.setState((s) => ({ byHost: { ...s.byHost, [alias]: entry } }))
}

function readAll(source: string): void {
  const token = gen
  for (const alias of listed()) void readHost(source, alias, token)
}

function syncTimer(): void {
  const source = usePoolStore.getState().sourceHostId
  const want = source !== null && !announcesAlerts(source)
  if (want && timer === null) {
    timer = setInterval(() => {
      const src = usePoolStore.getState().sourceHostId
      if (src !== null && !announcesAlerts(src)) readAll(src)
    }, ALERTS_POLL_MS)
  } else if (!want && timer !== null) {
    clearInterval(timer)
    timer = null
  }
}

/** The source or the host list moved (or the store just started): a new source reads everything, the same source reads the hosts it has no entry for yet and drops the ones the pool left. */
function sync(): void {
  const source = usePoolStore.getState().sourceHostId
  syncTimer()
  if (source === null) return // the pool cannot be read: keep what we have
  const sourceChanged = useAlertsStore.getState().source !== source
  const aliases = listed()
  if (sourceChanged) {
    gen++
    useAlertsStore.setState({ source, byHost: Object.fromEntries(Object.entries(useAlertsStore.getState().byHost).filter(([a]) => aliases.includes(a))) })
    readAll(source)
    return
  }
  const have = useAlertsStore.getState().byHost
  const kept = Object.fromEntries(Object.entries(have).filter(([a]) => aliases.includes(a)))
  if (Object.keys(kept).length !== Object.keys(have).length) useAlertsStore.setState({ byHost: kept })
  const token = gen
  for (const alias of aliases) if (!Object.hasOwn(kept, alias)) void readHost(source, alias, token)
}

function startWatching(): void {
  sync()
  const unsubscribes = [
    usePoolStore.subscribe((s, prev) => { if (s.hosts !== prev.hosts || s.sourceHostId !== prev.sourceHostId) sync() }),
    useNexHostStore.subscribe((s, prev) => { if (s.byHost !== prev.byHost) syncTimer() }),
  ]
  stopWatching = () => { for (const u of unsubscribes) u() }
}

/** Holds the alerts' reading open (counted like the pool's poll); the returned function lets go, once. */
export function retainPoolAlerts(): () => void {
  if (mounted++ === 0) startWatching()
  let released = false
  return () => {
    if (released) return
    released = true
    if (--mounted === 0) {
      stopWatching?.()
      stopWatching = null
      if (timer !== null) { clearInterval(timer); timer = null }
      gen++
    }
  }
}

/** `alerts.changed {host}` from `hostId`: that host is read again, when `hostId` is the source and the pool lists it. */
export function alertsEvent(hostId: string, alias: string): void {
  if (mounted === 0) return
  const source = usePoolStore.getState().sourceHostId
  if (source === null || source !== hostId || !listed().includes(alias)) return
  void readHost(source, alias, gen)
}

/** The host-events connection of `hostId` opened (events may have been missed while it was down): everything is read once, when it is the source. */
export function alertsConnectionOpened(hostId: string): void {
  if (mounted === 0) return
  const source = usePoolStore.getState().sourceHostId
  if (source !== null && source === hostId) readAll(source)
}

/** What the guardian still has open (spec M36's second half, MON-11 / S7, S8): 0 until the duty roster is wired in. */
export const dutyOpenCount = (): number => 0

/**
 * The number on the shield and the events tab (M36): alerts still active (`clearedAt` null) at push level, plus what the guardian still has open. Every wwd opens a `silence:<peer>` for the peer it cannot reach,
 * so the same silence key in several hosts' lists is one thing and counts once (88, 10-20); any other key belongs to the host whose list holds it. A host whose read failed counts none.
 */
export function alertCountOf(byHost: Record<string, HostAlertsEntry>): number {
  const open = new Set<string>()
  for (const [alias, e] of Object.entries(byHost)) {
    if (e.kind !== 'ok') continue
    for (const a of e.alerts) {
      if (a.clearedAt !== null || a.level !== 'push') continue
      open.add(a.kind === 'silence' ? a.key : `${alias}\u0000${a.key}`)
    }
  }
  return open.size + dutyOpenCount()
}

export const usePoolAlertCount = (): number => useAlertsStore((s) => alertCountOf(s.byHost))
export const readPoolAlertCount = (): number => alertCountOf(useAlertsStore.getState().byHost)
