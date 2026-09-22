// spa/src/lib/rebuild/refresh-sessions.ts — reconcile a host against a list
// read AFTER something changed the panes on screen (#1255 SPA spec §3.2; #1309 +
// #1310 spec §3.1; daemon contract docs/specs/2026-09-23-session-list-fresh-spec.md).
//
// Session reconciliation only ever looks at the tabs on screen, and a host
// pushes a `sessions` frame only when its sessions change. So panes that come on
// screen — or are re-pointed — by a write of this window (a switch, a profile
// apply of `workspaces` / `tabs.*`, a rebuild, a batch) stay un-reconciled until
// that host's next frame, which may never come. Every such write runs under the
// operation lock, so its release is the trigger (`reconcileAfterLockRelease`):
// every host with a live, versioned connection is asked for a fresh list and
// reconciled with it — the same `reconcileHostSessions` a WS frame runs (session
// store, revive snapshot, revive pass, probes). The fetch is sent after the
// write landed, so its list is evidence for the panes the write put there.
//
// A list is evidence only when the daemon vouches for it: an unversioned answer
// (old daemon) is dropped, and a versioned one only applies when it is newer
// than anything already reconciled for the host (`decide`, session-version.ts).
// It is also dropped when, while it was on the way, the world changed again (the
// world-epoch fence moved — a switch or promote in ANY window), the attach gate
// closed, the host left `hostOrder` / changed its endpoint — or the operation
// lock was taken again (the next holder may be mid-write; ITS release starts the
// next refresh). That lock fence holds for every refresh, the recovery one of a
// WS frame whose reconciliation threw included (codex adversarial, PR #1330):
// a recovery the lock stopped is owed, and the release re-sends it
// (`needsRecovery`) — that frame may have been the one meant to open the gate.
//
// For the same reason — no further push is coming — a failed fetch or a
// reconciliation that threw is retried a few times (`refreshHost`), each try
// re-checking what the refresh was started for.
//
// Only the window that wrote fetches (#1255 spec §3.6): the tab tree it writes
// reaches the other windows through the rehydrate that brought them the write.
import { useHostStore } from '../../stores/useHostStore'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { listSessionsFresh, type FreshSessions } from '../host-api'
import { readWorldEpochFence } from '../storage/world-fence'
import { canAttachTerminal } from './attach-gate'
import { reconcileHostSessions } from './reconcile-host'
import { runRevivePass } from './revive'
import { currentConn, decide, heldVersion, note, raiseBarrier } from './session-version'

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
   * true: the refresh of a lock release — it starts only while the attach gate
   * is open and ends when it closes. false: a RECOVERY refresh (a WS frame whose
   * reconciliation threw), which may start with the gate still closed — that
   * frame was supposed to open it — and is held to its connection instead
   * (`conn` unchanged, answer included). A recovery the lock fence stops is
   * owed to the next release (`needsRecovery`).
   */
  requireGate: boolean
  /**
   * `currentLockGen()` when the refresh started, with the lock free. Every
   * attempt and every answer also needs the lock free and not taken since
   * (codex plan review #1; for recovery, codex adversarial on PR #1330): an
   * answer read before the next holder's write must not be reconciled against
   * it.
   */
  lockGen: number
}

/**
 * Bumped on every acquire of the operation lock (the hook's lock observer calls
 * `operationLockAcquired`). A refresh started by a release carries the value of
 * that moment: a different one means another holder came and maybe wrote.
 */
let lockGen = 0

export function currentLockGen(): number {
  return lockGen
}

/**
 * Hosts whose recovery refresh the operation lock stopped — asked for while the
 * lock was held, or in flight / waiting to retry when it was taken. The frame
 * that failed may have been the one meant to open the gate, and with the gate
 * closed a release would only run the revive pass: so a release recovers these
 * hosts instead (`reconcileAfterLockRelease`). Cleared when a recovery starts
 * for the host, when one ends by anything but the lock, and on entry teardown.
 */
const needsRecovery = new Set<string>()

/** The versioned, live hosts: gate open AND a versioned list reconciled on this connection. */
function versionedLive(hostId: string): boolean {
  return canAttachTerminal(hostId) && heldVersion(hostId) !== null
}

/**
 * The operation lock went from free to held: the lock generation moves, and
 * every versioned, live host enters barrier (spec §3.1.1, ws-sessions.ts) until
 * the release's refresh for it settles. A host already in barrier keeps it.
 */
export function operationLockAcquired(): void {
  lockGen++
  for (const [hostId, run] of runs) {
    if (run.recovery) needsRecovery.add(hostId) // its answer can no longer apply
  }
  for (const hostId of useHostStore.getState().hostOrder) {
    if (versionedLive(hostId)) raiseBarrier(hostId)
  }
}

function lockFenceHolds(f: RefreshFences): boolean {
  return useRebuildStore.getState().lockedBy === null && lockGen === f.lockGen
}

/** Waits before retry 1, 2 and 3. The first attempt is immediate. */
const RETRY_DELAYS_MS = [1_000, 2_000, 4_000]

/** The one refresh per host that may still act; a newer one replaces it (see `refreshHost`). */
const runs = new Map<string, { cancel: () => void; recovery: boolean }>()

/** 'locked': only the lock fence failed — what a recovery then owes the next release. */
function fencesHold(hostId: string, f: RefreshFences): 'ok' | 'moved' | 'locked' {
  if (readWorldEpochFence() !== f.world) return 'moved'
  if (endpointOf(hostId) !== f.endpoint) return 'moved'
  if (currentConn(hostId) !== f.conn) return 'moved'
  if (f.requireGate && !canAttachTerminal(hostId)) return 'moved'
  return lockFenceHolds(f) ? 'ok' : 'locked'
}

/**
 * One fetch + apply. `retry`: worth another try (the fetch or the
 * reconciliation failed); `locked`: the answer was dropped by the lock fence.
 */
async function attempt(hostId: string, f: RefreshFences, live: () => boolean): Promise<'done' | 'retry' | 'locked'> {
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
  if (!lockFenceHolds(f)) return 'locked'

  const v = { epoch: fresh.epoch, seq: fresh.seq }
  if (decide(hostId, v, { kind: 'fetch', conn: f.conn }) === 'stale') return 'done'
  // Claim before apply (session-version.ts `note`): the reconciliation is not
  // transactional, so a list older than this one must never get in after it —
  // even if it throws half-way. The retry fetches a newer one.
  note(hostId, v)
  try {
    reconcileHostSessions(hostId, fresh.sessions)
  } catch {
    return 'retry'
  }
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
  const recovery = !fences.requireGate
  const run = {
    cancel: () => {
      cancelled = true
      clearTimeout(timer)
      wake?.()
    },
    recovery,
  }
  runs.set(hostId, run)
  if (recovery) needsRecovery.delete(hostId) // this run carries it now
  const live = () => !cancelled

  return (async () => {
    let lockStopped = false
    try {
      for (let i = 0; ; i++) {
        if (!live()) return
        const held = fencesHold(hostId, fences)
        if (held !== 'ok') {
          lockStopped = held === 'locked'
          return
        }
        const r = await attempt(hostId, fences, live)
        if (r !== 'retry') {
          lockStopped = r === 'locked'
          return
        }
        if (i >= RETRY_DELAYS_MS.length) return
        await new Promise<void>((resolve) => {
          wake = resolve
          timer = setTimeout(resolve, RETRY_DELAYS_MS[i])
        })
        wake = undefined
      }
    } finally {
      if (runs.get(hostId) === run) runs.delete(hostId)
      // A recovery stopped by the lock is owed to the next release; one that
      // ended any other way owes nothing. A cancelled one leaves the mark to
      // whoever cancelled it (a newer refresh, or teardown, which clears it).
      if (recovery && live()) {
        if (lockStopped) needsRecovery.add(hostId)
        else needsRecovery.delete(hostId)
      }
    }
  })()
}

/**
 * Ends `hostId`'s refresh, if any: its pending retry never fires; a recovery
 * owed to the next release is forgotten. Called on entry teardown.
 */
export function cancelSessionRefresh(hostId: string): void {
  runs.get(hostId)?.cancel()
  needsRecovery.delete(hostId)
}

export function __resetRefreshForTests(): void {
  for (const run of [...runs.values()]) run.cancel()
  runs.clear()
  needsRecovery.clear()
}

/**
 * A WS `sessions` frame of the current socket failed to reconcile
 * (ws-sessions.ts): refresh from a fresh list on the same connection. The gate
 * is not required — that frame may have been the one meant to open it — but
 * the answer must come back on the connection that is live now, with the
 * operation lock free and not taken since. While the lock is held nothing is
 * fetched: the host is marked and the release recovers it (`needsRecovery`).
 */
export function recoverHostSessions(hostId: string): Promise<void> {
  if (useRebuildStore.getState().lockedBy !== null) {
    needsRecovery.add(hostId)
    return Promise.resolve()
  }
  const endpoint = endpointOf(hostId)
  if (endpoint === null) {
    needsRecovery.delete(hostId)
    return Promise.resolve()
  }
  return refreshHost(hostId, {
    world: readWorldEpochFence(),
    conn: currentConn(hostId),
    endpoint,
    requireGate: false,
    lockGen,
  })
}

function refreshLive(hostId: string, lock: number): Promise<void> {
  // ONE synchronous step (spec §3.3, last paragraph): the gate and `conn` are
  // read together, so the fetch is sent only while the gate is open AND `conn`
  // names the connection that opened it — the hook moves `conn` before it
  // closes the gate.
  if (!canAttachTerminal(hostId)) return Promise.resolve()
  const conn = currentConn(hostId)
  const world = readWorldEpochFence()
  const endpoint = endpointOf(hostId)
  if (endpoint === null) return Promise.resolve()
  return refreshHost(hostId, { world, conn, endpoint, requireGate: true, lockGen: lock })
}

/**
 * The operation lock was released (spec §3.1): per host, on its own — one
 * host's failure costs no other its turn —
 * - a recovery the lock stopped (`needsRecovery`): that recovery, gate open or
 *   not (`recoverHostSessions`, fenced by the lock like any other) — after
 *   today's revive pass when the host is not versioned & live;
 * - versioned & live (the attach gate open AND a versioned list reconciled on
 *   this connection): refresh from a fresh list, fenced by the lock; the
 *   reconciliation it ends in runs the revive pass over that list;
 * - otherwise (gate closed, or an old daemon): today's revive pass over the
 *   list last reconciled — a closed gate waits for its connection's own first
 *   frame, and an old daemon offers nothing better.
 *
 * `onHostSettled(hostId)` runs once that host is done — right away on the
 * revive path, when its refresh is over otherwise, however it ended; the lock
 * observer ends the host's barrier there (ws-sessions.ts,
 * `endSessionsBarrier`).
 *
 * Called synchronously from the lock observer, i.e. inside
 * `releaseOperationLock`'s `set`: nothing here may throw. The promise settles
 * when every host's refresh is over; it exists for tests.
 */
export function reconcileAfterLockRelease(onHostSettled: (hostId: string) => void = () => {}): Promise<void> {
  const gen = lockGen
  const settled = (hostId: string) => {
    try {
      onHostSettled(hostId)
    } catch { /* ignore */ }
  }
  const hosts = useHostStore.getState().hostOrder
  return Promise.all(hosts.map((hostId) => {
    try {
      if (needsRecovery.has(hostId)) {
        if (!versionedLive(hostId)) runRevivePass(hostId)
        return recoverHostSessions(hostId).catch(() => {}).then(() => settled(hostId))
      }
      if (versionedLive(hostId)) return refreshLive(hostId, gen).catch(() => {}).then(() => settled(hostId))
      runRevivePass(hostId)
    } catch { /* ignore */ }
    settled(hostId)
    return Promise.resolve()
  })).then(() => {})
}
