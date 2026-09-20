// spa/src/lib/profile/start.ts — where Profile Sync is wired up (spec §4.4;
// P2b plan Task 11). The parts — collector, executor, lease, WS events — know
// nothing of each other; this file connects them and owns their lifetimes.
//
// THE IRON RULE
//   A user who has set no master gets the app they had before this feature
//   existed. `startProfileSync()` subscribes to `useProfileStore` and, while
//   there is no master, does nothing else: it subscribes to NO OTHER store,
//   computes no hash, neither reads nor writes the lease, sends no request,
//   schedules no timer, opens no BroadcastChannel. Pinned by
//   start.ironrule.test.ts with nothing mocked.
//   (`useProfileStore` itself is an ordinary persisted, `syncManager`-registered
//   store, like the eighteen others on main: its storage key and its entry in
//   the registry of syncManager's singleton channel exist for every user. That
//   is what any store costs; it is not part of the "nothing" above.)
//
// LIFETIMES (each inside the previous one)
//   started      `startProfileSync()` … its returned stop. One subscription to
//                `useProfileStore` — a synced store, so an attach or detach made
//                in another window arrives here as a rehydrate.
//   master mode  a master is set. EVERY window: `watchUnsyncedStores()` and a
//                contender for the lease. A master that changes (host OR
//                profile) ends this mode and starts a new one.
//                It also watches the master host's endpoint: `ip` / `port`
//                edited in place → BLOCKED, no driver in any window, until the
//                old value is back, or `attachMaster` / `detachMaster`; a new
//                `token` alone → the same daemon, the driver is rebuilt.
//   leading      this window holds the lease (and the mode is not blocked). ONLY here: executor, WS
//                subscription, collector, host watcher. Losing the lease
//                disposes all four and the window is a follower again.
//
//   Building a leader is asynchronous (`primeAll`), tearing one down is not.
//   Every leader has its own `disposed` flag, checked after every `await`: an
//   attach immediately followed by a detach leaves nothing alive. (One flag per
//   instance rather than one global generation counter: the same guarantee, and
//   a late continuation of instance N cannot be confused by N+2 looking like N.)
//
// WHAT THIS FILE OWES THE EXECUTOR (its header, "WHAT THE START LAYER OWES")
//   - `onReconnected()` whenever the master host is connected — THE FIRST TIME
//     INCLUDED — and only after `primeAll()` has given every section a
//     `currentHash`. The host watcher is installed after `primeAll()` too, and
//     the status is read at that moment, so a connect that happened meanwhile is
//     not lost and is not announced early.
//   - the attachment: `putAttachment` on attach and on every (re)connect (it
//     refreshes `lastSeen`), `deleteAttachment` on detach. THE ATTACHMENT COMES
//     FIRST: `onReconnected()` is called only after the daemon has confirmed it,
//     and until then the executor is told the host is unreachable — an executor
//     that was never announced still reindexes by itself as soon as the collector
//     reports (that is how a section asks for its index), so "not announced" is
//     not a gate; `isReachable()` is, because every request asks it first.
//
// `autoSync` off → on: the executor decides only when something pumps it, and
// its only entries that pump every section are `onReconnected()` and
// `syncNow()`. `syncNow()` is the lighter one (no `reconnected` event, so no
// index is marked stale); its "decide as if autoSync were on" is, at that
// moment, simply true.
//
// THE DIRECTION OF AN ATTACH (spec §4.9, decision 10). A client that attaches
// has agreed with the SOT on nothing, so wherever both sides hold something the
// state machine can only lock and ask. The user answers ONCE, at attach: `push`
// or `pull`. `attachMaster` stores it next to the master
// (`useProfileStore.pendingDirection` — persisted and synced, because the first
// reconciliation may span a reload and may be run by another window's leader);
// the executor reads it live (`initialDirection`) and says when everything has
// settled (`onInitialSettled`), at which point it is cleared here and conflicts
// are the user's again. Never cleared by a timeout — see executor.ts.
//
// EVERY ATTACH IS A NEW ONE — to the master already set as well. `attachMaster`
// clears the bases and `setMaster` bumps `attachGeneration`; this file watches
// that counter and rebuilds the whole master mode when it moves, in every
// window. One executor therefore sees at most one first reconciliation, which is
// what lets it refuse a direction that turns up later as stale (executor.ts).
//
// `attachMaster` / `detachMaster` are THE way in and out — P3's wizard calls
// them; the dev hook is a thin layer over them. They run one at a time.
import { getClientId, isClientIdPersisted } from '../client-identity'
import { effectiveDeviceName, useDeviceStateStore } from '../../stores/useDeviceStateStore'
import { useHostStore } from '../../stores/useHostStore'
import { isMasterPair, isSyncDirection, selectMaster, useProfileStore } from '../../stores/useProfileStore'
import type { SyncDirection } from '../../stores/useProfileStore'
import { deleteAttachment, putAttachment } from './api'
import { startCollector, watchUnsyncedStores } from './collector'
import { createExecutor } from './executor'
import type { Executor, ExecutorStatus } from './executor'
import { contendForLeadership } from './leader'
import type { Leadership } from './leader'
import { subscribeProfileEvents } from './profile-ws-dispatch'
import { clearSectionStore } from './section-store'

export interface Master {
  hostId: string
  profileId: string
}

export interface ProfileSyncProblem {
  kind: string
  section?: string
  detail: string
  at: number
}

export interface ProfileSyncState {
  master: Master | null
  /** This window holds the lease right now. */
  leader: boolean
  /** The master host's ip / port is not the one the profile was attached at: nothing syncs (see `enterMasterMode`). */
  blocked: 'master-endpoint-changed' | null
  /** Null in a follower and without a master: only the leader knows. */
  status: ExecutorStatus | null
  /** The latest `PROBLEM_BUFFER_SIZE`, oldest first. */
  problems: ProfileSyncProblem[]
}

export type AttachResult = { ok: true } | { ok: false; reason: string }

/** Only in `import.meta.env.DEV`, as `window.__purdexProfileSync`. */
export interface ProfileSyncDebug {
  attach(hostId: string, profileId: string, direction: SyncDirection): Promise<AttachResult>
  detach(): Promise<void>
  state(): ProfileSyncState
  syncNow(): void
  resolve(section: string, keep: 'local' | 'sot'): void
}

declare global {
  interface Window {
    __purdexProfileSync?: ProfileSyncDebug
  }
}

export const PROBLEM_BUFFER_SIZE = 50
export const ATTACH_RETRY_BASE_MS = 2_000
export const ATTACH_RETRY_CAP_MS = 30_000

// === Problems ===

const problems: ProfileSyncProblem[] = []
/** `kind` + section already warned about; forgotten when the master changes. */
const warned = new Set<string>()

function reportProblem(p: { kind: string; section?: string; detail: string }): void {
  problems.push({ kind: p.kind, ...(p.section !== undefined ? { section: p.section } : {}), detail: p.detail, at: Date.now() })
  if (problems.length > PROBLEM_BUFFER_SIZE) problems.splice(0, problems.length - PROBLEM_BUFFER_SIZE)
  const id = `${p.kind}\u0000${p.section ?? ''}`
  if (warned.has(id)) return
  warned.add(id)
  console.warn(`[profile-sync] ${p.kind}${p.section !== undefined ? ` (${p.section})` : ''}: ${p.detail}`)
}

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

// === The leader ===

function isCurrentMaster(master: Master): boolean {
  return sameMaster(selectMaster(useProfileStore.getState()), master)
}

interface Leader {
  executor: Executor
  status(): ExecutorStatus
  dispose(): void
}

function lead(master: Master, leadership: Leadership): Leader {
  const { hostId, profileId } = master
  let disposed = false
  let lastStatus: ExecutorStatus | null = null
  let unwatchHost: (() => void) | null = null
  /** The daemon has confirmed this client's attachment on the CURRENT connection. */
  let attached = false
  let attachFailures = 0
  let round = 0
  let retry: ReturnType<typeof setTimeout> | null = null
  const connected = (): boolean => useHostStore.getState().runtime[hostId]?.status === 'connected'

  const executor = createExecutor({
    hostId,
    profileId,
    isLeader: () => leadership.isLeader(),
    isReachable: () => attached && connected(),
    autoSync: () => useProfileStore.getState().autoSync,
    // Only while the store's master is still THIS one: a direction belongs to the attach that gave it.
    initialDirection: () => (disposed || !isCurrentMaster(master) ? null : useProfileStore.getState().pendingDirection),
    onInitialSettled: () => {
      if (!disposed && isCurrentMaster(master)) useProfileStore.getState().clearPendingDirection()
    },
    onProblem: reportProblem,
    onStatus: (s) => {
      if (!disposed) lastStatus = s
    },
  })
  const unsubscribeWs = subscribeProfileEvents((e) => executor.onRemoteEvent(e))
  const collector = startCollector({ onSection: (r) => executor.onSection(r), onProblem: reportProblem })

  /**
   * The master host is connected: FIRST the attachment, and only once the daemon
   * has confirmed it, the executor. Until then `isReachable()` is false, which
   * is the one thing every request of the executor asks first — so nothing is
   * listed, pulled or pushed for a profile the daemon does not yet know this
   * client is attached to (and would let another client delete meanwhile).
   * A failure is retried, 2 s doubling to a 30 s cap; every new connection starts
   * a new round, and the answer of an older round is dropped.
   */
  const announce = (): void => {
    const mine = ++round
    attached = false
    attachFailures = 0
    cancelRetry()
    void attach(mine)
  }

  const cancelRetry = (): void => {
    if (retry !== null) clearTimeout(retry)
    retry = null
  }

  const attach = async (mine: number): Promise<void> => {
    if (!isClientIdPersisted()) {
      // Reload-proof or nothing: no attachment is written — and without one, nothing syncs.
      reportProblem({ kind: 'client-id-not-persisted', detail: 'no attachment, no sync: this client id would not survive a reload' })
      return
    }
    let failure: string | null = null
    try {
      const body = { clientId: getClientId(), deviceName: effectiveDeviceName(useDeviceStateStore.getState()) }
      const r = await putAttachment(hostId, profileId, body)
      if (r.kind === 'failed') failure = `${r.reason}: ${r.message}`
    } catch (e) {
      failure = message(e)
    }
    if (disposed || mine !== round || !connected()) return
    if (failure === null) {
      attached = true
      attachFailures = 0
      executor.onReconnected()
      return
    }
    const ms = Math.min(ATTACH_RETRY_BASE_MS * 2 ** attachFailures, ATTACH_RETRY_CAP_MS)
    attachFailures += 1
    reportProblem({ kind: 'attachment-failed', detail: `${failure}; nothing syncs until it succeeds, retry in ${ms} ms` })
    retry = setTimeout(() => {
      retry = null
      if (!disposed && mine === round && connected()) void attach(mine)
    }, ms)
  }

  void (async () => {
    try {
      await collector.primeAll()
    } catch (e) {
      if (!disposed) reportProblem({ kind: 'prime-failed', detail: message(e) })
    }
    if (disposed) return
    unwatchHost = useHostStore.subscribe((next, prev) => {
      if (disposed) return
      if (prev.hosts[hostId] !== undefined && next.hosts[hostId] === undefined) {
        // Not a detach: that is the user's decision. The executor stops by itself on `unknown-host`.
        reportProblem({ kind: 'master-host-removed', detail: `host ${hostId} is no longer in the host list` })
      }
      const is = next.runtime[hostId]?.status === 'connected'
      const was = prev.runtime[hostId]?.status === 'connected'
      if (is && !was) announce()
      if (!is && was) {
        // the attachment is confirmed per connection; whatever is out or pending belongs to the old one
        round += 1
        attached = false
        cancelRetry()
      }
    })
    if (connected()) announce()
  })()

  return {
    executor,
    status: () => lastStatus ?? executor.status(),
    dispose() {
      if (disposed) return
      disposed = true
      cancelRetry()
      unwatchHost?.()
      unwatchHost = null
      unsubscribeWs()
      collector.stop()
      executor.dispose()
    },
  }
}

// === Master mode ===

interface MasterMode {
  master: Master
  /** `useProfileStore.attachGeneration` when this mode was entered. */
  generation: number
  isLeader(): boolean
  /** The master host was re-pointed in place: no driver, whoever holds the lease. */
  blocked(): boolean
  leader(): Leader | null
  end(): void
}

/** Where the master's daemon is, and the credential for it. Null = the host is not in the store. */
function endpointOf(hostId: string): { at: string; token: string } | null {
  const host = useHostStore.getState().hosts[hostId]
  return host === undefined ? null : { at: `${host.ip}:${host.port}`, token: host.token ?? '' }
}

function enterMasterMode(master: Master, generation: number): MasterMode {
  let ended = false
  let leader: Leader | null = null
  // The endpoint the bases (and the attachment, the schema lock, `profileGone`)
  // belong to. `api.ts` resolves the address from the host store on every
  // request, so an edit of the master host IN PLACE would send the next request,
  // with the old daemon's CAS bases, to whatever answers at the new address.
  let home = endpointOf(master.hostId)
  let blocked = false
  warned.clear()
  const unwatchUnsynced = watchUnsyncedStores()
  const leadership = contendForLeadership()

  const follow = (): void => {
    leader?.dispose()
    leader = null
  }
  const apply = (isLeader: boolean): void => {
    if (ended) return
    if (!isLeader || blocked) follow()
    else if (leader === null) leader = lead(master, leadership)
  }
  const unsubscribe = leadership.onChange(apply)

  // Only a LOCAL edit can do this: a `hosts` payload from the SOT that re-points
  // the master's own host is refused by apply-to-stores.
  const unwatchEndpoint = useHostStore.subscribe((next, prev) => {
    if (ended || next.hosts[master.hostId] === prev.hosts[master.hostId]) return
    const now = endpointOf(master.hostId)
    if (now === null) return // removed: the leader reports it; coming back is judged against `home`
    if (home === null) {
      home = now
      return
    }
    if (now.at !== home.at) {
      // ip or port: nobody can tell whether this is the same daemon. Stop. Do not
      // detach (the user's setting) and do not drop the bases (a typo may be
      // corrected): the ways out are the old value, `attachMaster`, `detachMaster`.
      if (blocked) return
      blocked = true
      follow()
      reportProblem({
        kind: 'master-endpoint-changed',
        detail: `host ${master.hostId} now points at ${now.at}, the profile was attached at ${home.at}; nothing syncs until it is attached again, detached, or the address is put back`,
      })
      return
    }
    const rotated = now.token !== home.token
    if (!blocked && !rotated) return
    // Back where the bases belong, or the same daemon with a new token: a new
    // driver on the bases there are — a reconnect, in effect.
    home = now
    blocked = false
    follow()
    apply(leadership.isLeader())
  })

  // `onChange` does not replay: ask once.
  if (leadership.isLeader()) apply(true)

  return {
    master,
    generation,
    isLeader: () => !ended && leadership.isLeader(),
    blocked: () => !ended && blocked,
    leader: () => leader,
    end() {
      if (ended) return
      ended = true
      unsubscribe()
      unwatchEndpoint()
      follow()
      leadership.stop()
      unwatchUnsynced()
    },
  }
}

// === Start ===

let mode: MasterMode | null = null

function sameMaster(a: Master | null, b: Master | null): boolean {
  if (a === null || b === null) return a === b
  return a.hostId === b.hostId && a.profileId === b.profileId
}

export function profileSyncState(): ProfileSyncState {
  return {
    master: selectMaster(useProfileStore.getState()),
    leader: mode?.isLeader() ?? false,
    blocked: mode?.blocked() === true ? 'master-endpoint-changed' : null,
    status: mode?.leader()?.status() ?? null,
    problems: problems.map((p) => ({ ...p })),
  }
}

/**
 * App lifetime, called from main.tsx. Without a master this is one subscription
 * to `useProfileStore` and nothing else (THE IRON RULE above).
 */
export function startProfileSync(): () => void {
  let stopped = false
  const sync = (master: Master | null, generation: number): void => {
    const same = sameMaster(master, mode?.master ?? null)
    if (same && (mode === null || mode.generation === generation)) return
    // Same master, new generation = `attachMaster` was called again, here or in
    // another window: a new first reconciliation, from cleared bases, with a new
    // driver. `attachMaster` cleared them where it ran; the window that WRITES
    // them — the leader, possibly this one and not that one — clears them again
    // once its writer is down, so that nothing it persisted in between survives.
    // A follower must not: by now the leader may have written the new ones.
    const wasLeader = same && mode !== null && mode.isLeader()
    mode?.end()
    if (wasLeader && master !== null) clearSectionStore(master.profileId)
    mode = master === null ? null : enterMasterMode(master, generation)
  }

  const unsubscribe = useProfileStore.subscribe((next, prev) => {
    if (stopped) return
    sync(selectMaster(next), next.attachGeneration)
    // Nothing pumps the executor when a preference changes: do it here.
    if (next.autoSync && !prev.autoSync) mode?.leader()?.executor.syncNow()
  })
  sync(selectMaster(useProfileStore.getState()), useProfileStore.getState().attachGeneration)

  if (import.meta.env.DEV) {
    window.__purdexProfileSync = {
      attach: attachMaster,
      detach: detachMaster,
      state: profileSyncState,
      syncNow: () => mode?.leader()?.executor.syncNow(),
      resolve: (section, keep) => mode?.leader()?.executor.resolve(section, keep),
    }
  }

  return () => {
    if (stopped) return
    stopped = true
    unsubscribe()
    mode?.end()
    mode = null
    if (import.meta.env.DEV) delete window.__purdexProfileSync
  }
}

// === Attach / detach ===

/** Attach and detach run one at a time, in the order they were asked for. */
let queue: Promise<unknown> = Promise.resolve()

function serial<T>(fn: () => Promise<T>): Promise<T> {
  const run = queue.then(fn, fn)
  queue = run.catch(() => undefined)
  return run
}

/** Best effort: whatever happens, the caller goes on. */
async function dropAttachment(master: Master): Promise<void> {
  try {
    const r = await deleteAttachment(master.hostId, master.profileId, getClientId())
    if (r.kind === 'failed') reportProblem({ kind: 'detach-failed', detail: `${r.reason}: ${r.message}` })
  } catch (e) {
    reportProblem({ kind: 'detach-failed', detail: message(e) })
  }
}

/**
 * The attachment is written FIRST and the master set only if the daemon took
 * it: a master without an attachment is a profile the daemon would let someone
 * delete under this client (spec acceptance 12).
 *
 * `direction` — which side wins wherever both hold something, until the first
 * reconciliation has settled: `'push'` = this machine overwrites the SOT (and
 * `tabs.*` of workspaces that are not here are deleted from it); `'pull'` = the
 * SOT overwrites this machine.
 *
 * SAFETY — `'pull'` REPLACES THIS MACHINE'S workspaces, tabs, hosts and
 * settings with the SOT's. The user's decision 12 requires that the local state
 * is first saved as a slave profile, and that the user confirms. Slaves arrive
 * with P3, so UNTIL P3'S WIZARD IS WIRED UP THE ONLY CALLER OF THIS PATH IS THE
 * DEV HOOK; P3 must have completed that step BEFORE it calls
 * `attachMaster(…, 'pull')`. Nothing in here does it for the caller.
 */
export function attachMaster(hostId: string, profileId: string, direction: SyncDirection): Promise<AttachResult> {
  return serial(async (): Promise<AttachResult> => {
    if (!isSyncDirection(direction)) return { ok: false, reason: 'invalid-direction' }
    if (!isClientIdPersisted()) return { ok: false, reason: 'client-id-not-persisted' }
    if (useHostStore.getState().hosts[hostId] === undefined) return { ok: false, reason: 'unknown-host' }
    if (!isMasterPair(hostId, profileId)) return { ok: false, reason: 'invalid-profile-id' }

    const next: Master = { hostId, profileId }
    let put: Awaited<ReturnType<typeof putAttachment>>
    try {
      const body = { clientId: getClientId(), deviceName: effectiveDeviceName(useDeviceStateStore.getState()) }
      put = await putAttachment(hostId, profileId, body)
    } catch (e) {
      return { ok: false, reason: message(e) }
    }
    if (put.kind === 'failed') return { ok: false, reason: put.reason }

    const previous = selectMaster(useProfileStore.getState())
    if (previous !== null && !sameMaster(previous, next)) await dropAttachment(previous)
    // EVERY attach starts from no bases, the same master included. With a base
    // still held, a section that is dirty while the SOT has not moved is simply
    // pushed — under `pull` too, the opposite of what was asked; without one it is
    // a conflict, and the direction answers it. No `await` between this and
    // `setMaster`: the old driver cannot write a base in between, and the new one
    // (built by the subscription, on the new `attachGeneration`) is not seeded
    // from the old ones.
    clearSectionStore()
    return useProfileStore.getState().setMaster(hostId, profileId, direction)
      ? { ok: true }
      : { ok: false, reason: 'invalid-profile-id' }
  })
}

/**
 * The user said stop, so it stops — NOW. The master is cleared first, which is
 * synchronous: the subscription above takes the driver down before this function
 * reaches its first `await`, so nothing is pulled or pushed while the daemon is
 * being told (up to a 15 s timeout on a slow or absent host). The bases go with
 * it. Telling the daemon is best effort and comes last: a failure is a problem,
 * recorded, not a reason to stay attached. Nothing after the `await` touches the
 * store — by then the master may be a new one, set by another window.
 */
export function detachMaster(): Promise<void> {
  return serial(async (): Promise<void> => {
    const master = selectMaster(useProfileStore.getState())
    if (master === null) return
    useProfileStore.getState().clearMaster()
    clearSectionStore(master.profileId)
    await dropAttachment(master)
  })
}

export function __resetProfileSyncForTest(): void {
  problems.length = 0
  warned.clear()
  queue = Promise.resolve()
}
