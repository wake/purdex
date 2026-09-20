// spa/src/lib/profile/start.ts — where Profile Sync is wired up (spec §4.4;
// P2b plan Task 11). The parts — collector, executor, lease, WS events — know
// nothing of each other; this file connects them and owns their lifetimes.
//
// THE IRON RULE
//   A user who has set no master gets the app they had before this feature
//   existed. `startProfileSync()` subscribes to `useProfileStore` and, while
//   there is no master, does NOTHING else: no other subscription, no hash, no
//   lease read or write, no request, no timer. Pinned by start.ironrule.test.ts
//   with nothing mocked.
//
// LIFETIMES (each inside the previous one)
//   started      `startProfileSync()` … its returned stop. One subscription to
//                `useProfileStore` — a synced store, so an attach or detach made
//                in another window arrives here as a rehydrate.
//   master mode  a master is set. EVERY window: `watchUnsyncedStores()` and a
//                contender for the lease. A master that changes (host OR
//                profile) ends this mode and starts a new one.
//   leading      this window holds the lease. ONLY here: executor, WS
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
//     refreshes `lastSeen`), `deleteAttachment` on detach.
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
  const connected = (): boolean => useHostStore.getState().runtime[hostId]?.status === 'connected'

  const executor = createExecutor({
    hostId,
    profileId,
    isLeader: () => leadership.isLeader(),
    isReachable: connected,
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

  /** The master host is connected: refresh the attachment, tell the executor. */
  const announce = (): void => {
    if (isClientIdPersisted()) {
      const body = { clientId: getClientId(), deviceName: effectiveDeviceName(useDeviceStateStore.getState()) }
      putAttachment(hostId, profileId, body).then(
        (r) => {
          if (!disposed && r.kind === 'failed') {
            reportProblem({ kind: 'attachment-refresh-failed', detail: `${r.reason}: ${r.message}` })
          }
        },
        (e: unknown) => {
          if (!disposed) reportProblem({ kind: 'attachment-refresh-failed', detail: message(e) })
        },
      )
    } else {
      reportProblem({
        kind: 'client-id-not-persisted',
        detail: 'attachment not refreshed: this client id would not survive a reload',
      })
    }
    executor.onReconnected()
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
    })
    if (connected()) announce()
  })()

  return {
    executor,
    status: () => lastStatus ?? executor.status(),
    dispose() {
      if (disposed) return
      disposed = true
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
  leader(): Leader | null
  end(): void
}

function enterMasterMode(master: Master, generation: number): MasterMode {
  let ended = false
  let leader: Leader | null = null
  warned.clear()
  const unwatchUnsynced = watchUnsyncedStores()
  const leadership = contendForLeadership()

  const follow = (): void => {
    leader?.dispose()
    leader = null
  }
  const apply = (isLeader: boolean): void => {
    if (ended) return
    if (!isLeader) follow()
    else if (leader === null) leader = lead(master, leadership)
  }
  const unsubscribe = leadership.onChange(apply)
  // `onChange` does not replay: ask once.
  if (leadership.isLeader()) apply(true)

  return {
    master,
    generation,
    isLeader: () => !ended && leadership.isLeader(),
    leader: () => leader,
    end() {
      if (ended) return
      ended = true
      unsubscribe()
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
