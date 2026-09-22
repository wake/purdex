// spa/src/lib/rebuild/refresh-after-switch.ts — after a switch, reconcile the
// world that just came on screen against a list read AFTER the switch (#1255 SPA
// spec §3.2; daemon contract docs/specs/2026-09-23-session-list-fresh-spec.md).
//
// Session reconciliation only ever looks at the tabs on screen, so a world that
// was parked while one of its sessions closed comes back un-reconciled, and with
// no further session change its host never pushes again. The switching window
// therefore asks every host with a live, reconciled connection for a fresh,
// versioned list and reconciles it — the same `reconcileHostSessions` a WS frame
// runs (session store, revive snapshot, revive pass, probes).
//
// A list is evidence only when the daemon vouches for it: an unversioned answer
// (old daemon) is dropped, and a versioned one only applies when it is newer
// than anything already reconciled for the host (`decide`, session-version.ts).
// It is also dropped when, while it was on the way, the world changed again (the
// world-epoch fence moved — a switch or promote in ANY window), the attach gate
// closed, or the host left `hostOrder` / changed its endpoint.
//
// For the same reason — no further push is coming — a failed fetch or a
// reconciliation that threw is retried a few times (`refreshHost`), each try
// re-checking what the refresh was started for.
//
// Only the switching window fetches (spec §3.6): the tab tree it writes reaches
// the other windows through the same rehydrate that brought them the new world.
import { useHostStore } from '../../stores/useHostStore'
import { listSessionsFresh, type FreshSessions } from '../host-api'
import { readWorldEpochFence } from '../storage/world-fence'
import { canAttachTerminal } from './attach-gate'
import { reconcileHostSessions } from './reconcile-host'
import { currentConn, decide, note } from './session-version'

/** `ip:port` of a host still in `hostOrder`; null otherwise. */
function endpointOf(hostId: string): string | null {
  const { hosts, hostOrder } = useHostStore.getState()
  const host = hosts[hostId]
  if (!host || !hostOrder.includes(hostId)) return null
  return `${host.ip}:${host.port}`
}

/**
 * What a refresh was started for. Every attempt re-checks all of it before it
 * fetches, and the answer is checked again before it is applied: when any of
 * it has moved, the refresh ends and nothing is retried — whatever moved it
 * (another switch, a new connection, a removed host) brings its own evidence.
 */
export interface RefreshFences {
  /** `readWorldEpochFence()` when the refresh started. */
  world: number
  /** `currentConn(hostId)` when the refresh started — the connection the fetches are sent on. */
  conn: number
  /** `ip:port` when the refresh started. */
  endpoint: string
  /**
   * The post-switch refresh starts only while the attach gate is open and ends
   * when it closes. A recovery refresh (a WS frame whose reconciliation threw)
   * may start with the gate still closed — that frame was supposed to open it —
   * and is held to its connection instead (`conn` unchanged, answer included).
   */
  requireGate: boolean
}

/** Waits before retry 1, 2 and 3. The first attempt is immediate. */
const RETRY_DELAYS_MS = [1_000, 2_000, 4_000]

/** The one refresh per host that may still act; a newer one replaces it (see `refreshHost`). */
const runs = new Map<string, { cancel: () => void }>()

function fencesHold(hostId: string, f: RefreshFences): boolean {
  if (readWorldEpochFence() !== f.world) return false
  if (endpointOf(hostId) !== f.endpoint) return false
  if (currentConn(hostId) !== f.conn) return false
  return !f.requireGate || canAttachTerminal(hostId)
}

/** One fetch + apply. `retry`: worth another try (the fetch or the reconciliation failed). */
async function attempt(hostId: string, f: RefreshFences, live: () => boolean): Promise<'done' | 'retry'> {
  let fresh: FreshSessions
  try {
    fresh = await listSessionsFresh(hostId)
  } catch {
    return 'retry'
  }
  if (!live()) return 'done' // replaced or cancelled while the fetch was out
  if (fresh.kind !== 'versioned') return 'done' // an old daemon: never evidence, retrying changes nothing

  if (readWorldEpochFence() !== f.world) return 'done'
  if (endpointOf(hostId) !== f.endpoint) return 'done'
  if (f.requireGate ? !canAttachTerminal(hostId) : currentConn(hostId) !== f.conn) return 'done'

  const v = { epoch: fresh.epoch, seq: fresh.seq }
  if (decide(hostId, v, { kind: 'fetch', conn: f.conn }) === 'stale') return 'done'
  try {
    reconcileHostSessions(hostId, fresh.sessions)
  } catch {
    return 'retry' // nothing held moves on a failed reconciliation
  }
  note(hostId, v)
  return 'done'
}

/**
 * Reconcile `hostId` against a fresh, versioned list, retrying a failed fetch
 * or reconciliation up to 3 times (after 1 s, 2 s, 4 s) while `fences` hold.
 * A stale or unversioned answer, or a moved fence, ends it without a retry.
 *
 * ONE refresh per host at a time: a new call REPLACES the running one — its
 * pending retry is cancelled and its in-flight answer dropped. The newer call
 * carries the newer fences and its fetch is read later, so the older answer
 * can only be as new or older than the newer one's.
 *
 * The promise settles when this refresh is over; it exists for tests.
 */
export function refreshHost(hostId: string, fences: RefreshFences): Promise<void> {
  runs.get(hostId)?.cancel()
  let cancelled = false
  let timer: ReturnType<typeof setTimeout> | undefined
  let wake: (() => void) | undefined
  const run = {
    cancel: () => {
      cancelled = true
      clearTimeout(timer)
      wake?.()
    },
  }
  runs.set(hostId, run)
  const live = () => !cancelled

  return (async () => {
    try {
      for (let i = 0; ; i++) {
        if (!live() || !fencesHold(hostId, fences)) return
        if ((await attempt(hostId, fences, live)) === 'done') return
        if (i >= RETRY_DELAYS_MS.length) return
        await new Promise<void>((resolve) => {
          wake = resolve
          timer = setTimeout(resolve, RETRY_DELAYS_MS[i])
        })
        wake = undefined
      }
    } finally {
      if (runs.get(hostId) === run) runs.delete(hostId)
    }
  })()
}

/** Ends `hostId`'s refresh, if any: its pending retry never fires. Called on entry teardown. */
export function cancelSessionRefresh(hostId: string): void {
  runs.get(hostId)?.cancel()
}

export function __resetRefreshForTests(): void {
  for (const run of [...runs.values()]) run.cancel()
  runs.clear()
}

function refreshAfterSwitch(hostId: string): Promise<void> {
  // ONE synchronous step (spec §3.3, last paragraph): the gate and `conn` are
  // read together, so the fetch is sent only while the gate is open AND `conn`
  // names the connection that opened it — the hook moves `conn` before it
  // closes the gate.
  if (!canAttachTerminal(hostId)) return Promise.resolve()
  const conn = currentConn(hostId)
  const world = readWorldEpochFence()
  const endpoint = endpointOf(hostId)
  if (endpoint === null) return Promise.resolve()
  return refreshHost(hostId, { world, conn, endpoint, requireGate: true })
}

/**
 * Fire-and-forget for the caller (`switchActiveProfile`, after both locks are
 * released); the promise only exists for tests. Each host on its own: one
 * host's failure costs no other its refresh.
 */
export function refreshSessionsAfterSwitch(): Promise<void> {
  const hosts = useHostStore.getState().hostOrder
  return Promise.all(hosts.map((hostId) => refreshAfterSwitch(hostId).catch(() => {}))).then(() => {})
}
