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
//
// FOR A UI, IN ANY WINDOW (P3 plan Task 2; the machinery is sync-status.ts).
// `subscribeProfileSync` + `profileSyncSnapshot()` — a cached object whose
// identity changes only with its content, which is what `useSyncExternalStore`
// asks for; `requestSyncNow()` / `requestResolve()` — executed here when this
// window leads, handed to the leader through `localStorage` when it does not.
// This file's part is `changed()`: called wherever something `profileSyncState()`
// reads may have moved — a problem, the executor's `onStatus` (no longer
// swallowed), the lease, a block, the master. The status channel lives exactly
// as long as a master mode, one per window; without a master `changed()` copies
// this window's own view into memory and touches nothing else (THE IRON RULE).
import { getClientId, isClientIdPersisted } from '../client-identity'
import { effectiveDeviceName } from '../device-name'
import { ensureDefaultDeviceName, useDeviceNameStore } from '../../stores/useDeviceNameStore'
import { useHostStore } from '../../stores/useHostStore'
import { useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import { isMasterPair, isSyncDirection, selectMaster, useProfileStore } from '../../stores/useProfileStore'
import type { SyncDirection } from '../../stores/useProfileStore'
import { deleteAttachment, putAttachment } from './api'
import { startCollector, watchUnsyncedStores } from './collector'
import { createExecutor } from './executor'
import type { Executor, ExecutorStatus, SectionLock } from './executor'
import { contendForLeadership, leaderWindowId, readLeaderLease } from './leader'
import type { Leadership } from './leader'
import { subscribeProfileEvents } from './profile-ws-dispatch'
import { readMasterWorld } from './master-world'
import { clearSectionStore } from './section-store'
import { copyMasterAsSlave, deleteSlave, promoteToMaster, renameSlave, saveScreenAsSlave, switchActiveProfile } from './switch-active'
import type { CopyResult, PromoteResult, SwitchResult } from './switch-active'
import { __resetSyncStatusForTest, masterTagOf, openStatusChannel, setLocalSnapshot } from './sync-status'
import type { StatusChannel } from './sync-status'
import { STORAGE_KEYS } from '../storage/keys'

export { profileSyncSnapshot, subscribeProfileSync } from './sync-status'
export type { ProfileSyncSnapshot } from './sync-status'

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
  /** Nothing syncs, in any window (see `enterMasterMode`): the master host's ip / port is not the one the
   *  profile was attached at · the attachment answered 404, i.e. the profile is not on the daemon any more ·
   *  an `attachMaster` is in progress somewhere (transient: lifted by its outcome, or by its expiry). */
  blocked: 'master-endpoint-changed' | 'profile-gone' | 'suspended' | null
  /** Null in a follower and without a master: only the leader knows. With `blocked: 'profile-gone'` there is
   *  no executor to ask, and it reads what the executor's own `profileGone` reads: `locked:reset`, no sections. */
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
  /** Local profiles ("slaves") — P3b ships without a UI; the real-machine acceptance drives switch-active.ts from here. */
  profiles: ProfilesDebug
}

export interface ProfilesDebug {
  list(): { active: string; slaves: { id: string; name: string; onScreen: boolean }[] }
  switch(id: string): Promise<SwitchResult>
  copyMaster(name: string): CopyResult
  saveScreen(name: string): CopyResult
  promote(id: string, demotedName: string): PromoteResult
  rename(id: string, name: string): ReturnType<typeof renameSlave>
  remove(id: string): ReturnType<typeof deleteSlave>
  /** A summary of `readMasterWorld()` — never the world itself. */
  world(): { settled: true; onScreen: boolean; workspaces: string[] } | { settled: false; reason: string }
}

declare global {
  interface Window {
    __purdexProfileSync?: ProfileSyncDebug
  }
}

export const PROBLEM_BUFFER_SIZE = 50
export const ATTACH_RETRY_BASE_MS = 2_000
export const ATTACH_RETRY_CAP_MS = 30_000
/**
 * How long ONE stretch of an attach keeps every driver still. Each stretch covers exactly one request
 * (15 s timeout) and is strictly longer than it: the attachment PUT, then — only when the master changes —
 * the DELETE of the old attachment, which gets a fresh stretch before it starts (the first began at most
 * 15 s earlier, so it cannot have run out). An attach waiting in this window's queue suspended in its call;
 * the attach working in front of it refreshes that suspension at each of its own stretches, and the queued
 * one refreshes it again when its turn comes — so no gap opens however many are queued. A detach in the
 * queue clears the master, and with it everything there was to hold still.
 */
export const ATTACH_SUSPEND_MS = 30_000

/** Epoch ms. `startProfileSync({ now })` replaces it. */
let clock: () => number = () => Date.now()

function isSuspended(): boolean {
  const suspension = useProfileStore.getState().suspension
  return suspension !== null && clock() < suspension.until
}

/**
 * The suspension as `localStorage` holds it RIGHT NOW — not as this window's
 * store remembers it. Another window's attach reaches this store through a
 * BroadcastChannel message and a rehydrate, i.e. some turns later; the storage
 * it persisted to is shared synchronously, like the lease. The leader's
 * `isReachable()` asks this before every request (executor.ts, `request`), so an
 * unexpired suspension set anywhere stops the next request here, and closes a
 * flight that was already open.
 *   WHAT IS LEFT, stated plainly: between two renderer processes `localStorage`
 * is not instantaneous either (Chromium propagates it eventually — usually
 * within milliseconds); "read: not suspended → send" is a read followed by an
 * act, not an atomic step; and bytes that have been sent cannot be called back
 * (disposing the executor aborts the request, the daemon may have applied it).
 * The window is no longer a broadcast and a rehydrate, nor "whatever the old
 * driver had decided on": it is one synchronous read, the same grade as the lease.
 *   Unreadable or malformed = not suspended: what the record IS, is the store's
 * sanitiser's business, and a broken record has no master to sync for anyway.
 */
function suspendedInStorage(): boolean {
  const until = storedControl()?.suspension?.until
  return typeof until === 'number' && Number.isFinite(until) && clock() < until
}

/** The control plane as `localStorage` holds it right now (the persist envelope's `state`), or null: absent, unreadable. */
function storedControl(): { masterHostId?: unknown; masterProfileId?: unknown; attachGeneration?: unknown; suspension?: { until?: unknown } | null } | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEYS.PROFILE)
    if (raw === null) return null
    const state = (JSON.parse(raw) as { state?: unknown }).state
    return typeof state === 'object' && state !== null ? (state as ReturnType<typeof storedControl>) : null
  } catch {
    return null
  }
}

/** `attachGeneration` as storage holds it; this window's memory only when storage cannot be read. */
function storedGeneration(): number {
  const g = storedControl()?.attachGeneration
  return typeof g === 'number' && Number.isSafeInteger(g) ? g : useProfileStore.getState().attachGeneration
}

/** The owner of one attach's suspension (see `useProfileStore`, WHY `suspension`). */
function newToken(): string {
  const bytes = new Uint8Array(16)
  try {
    crypto.getRandomValues(bytes)
  } catch {
    for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256)
  }
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

// === Problems ===

const problems: ProfileSyncProblem[] = []
/** `kind` + section already warned about; forgotten when the master changes. */
const warned = new Set<string>()

function reportProblem(p: { kind: string; section?: string; detail: string }): void {
  problems.push({ kind: p.kind, ...(p.section !== undefined ? { section: p.section } : {}), detail: p.detail, at: Date.now() })
  if (problems.length > PROBLEM_BUFFER_SIZE) problems.splice(0, problems.length - PROBLEM_BUFFER_SIZE)
  changed()
  const id = `${p.kind}\u0000${p.section ?? ''}`
  if (warned.has(id)) return
  warned.add(id)
  console.warn(`[profile-sync] ${p.kind}${p.section !== undefined ? ` (${p.section})` : ''}: ${p.detail}`)
}

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

/**
 * What an attachment PUT says about this client. The device name's default (Electron's hostname, else the
 * user agent) is resolved on demand, and this is the demand: only ever on a path that has, or is about to
 * have, a master — a user without one never gets here (THE IRON RULE). `ensureDefaultDeviceName()` is
 * idempotent, single-flight and never throws; on a failure the name falls back and the next PUT retries.
 */
async function attachmentBody(): Promise<{ clientId: string; deviceName: string }> {
  await ensureDefaultDeviceName()
  return { clientId: getClientId(), deviceName: effectiveDeviceName(useDeviceNameStore.getState()) }
}

// === The leader ===

function isCurrentMaster(master: Master): boolean {
  return sameMaster(selectMaster(useProfileStore.getState()), master)
}

interface Leader {
  executor: Executor
  status(): ExecutorStatus
  /** The section's lock as the executor holds it NOW — asked of the executor, not of the last status it announced. Null = not locked. */
  lock(section: string): SectionLock | null
  dispose(): void
}

function lead(master: Master, leadership: Leadership, onProfileGone: (detail: string) => void): Leader {
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
    isReachable: () => attached && connected() && !suspendedInStorage(),
    autoSync: () => useProfileStore.getState().autoSync,
    // Only while the store's master is still THIS one: a direction belongs to the attach that gave it.
    initialDirection: () => (disposed || !isCurrentMaster(master) ? null : useProfileStore.getState().pendingDirection),
    onInitialSettled: () => {
      if (!disposed && isCurrentMaster(master)) useProfileStore.getState().clearPendingDirection()
    },
    onProblem: reportProblem,
    onStatus: (s) => {
      if (disposed) return
      lastStatus = s
      changed()
    },
  })
  const unsubscribeWs = subscribeProfileEvents((e) => executor.onRemoteEvent(e))
  // `world-unsettled` (collector.ts) arrives through `onProblem` like every other problem of the collector's; it is
  // timed on this file's clock. Neither the collector nor master-world.ts has a timer for it (THE IRON RULE's test
  // counts timers after a detach), and master-world.ts is subscribed to by the collector only — so only under a master.
  const collector = startCollector({ onSection: (r) => executor.onSection(r), onProblem: reportProblem, now: () => clock() })

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
    let notFound = false
    try {
      const body = await attachmentBody()
      if (disposed || mine !== round || !connected()) return // the name took a moment: nothing goes out for a round that is over
      const r = await putAttachment(hostId, profileId, body)
      if (r.kind === 'failed') {
        failure = `${r.reason}: ${r.message}`
        notFound = r.reason === 'not-found'
      }
    } catch (e) {
      failure = message(e)
    }
    if (disposed || mine !== round || !connected()) return
    if (notFound) {
      // 404 on the attachment = the profile is not on this daemon. The executor
      // would say `profileGone` — but it is never started without an attachment,
      // so the start layer says it (the caller disposes this leader). No retry:
      // a profile that is made again gets a new id.
      onProfileGone(failure ?? 'not-found')
      return
    }
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
    lock: (section) => executor.status().locks[section] ?? null,
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
  /** No driver, whoever holds the lease: the master host was re-pointed in place, or the profile is gone. */
  blocked(): ProfileSyncState['blocked']
  /** `suspension` moved: take the driver down, or bring it back. */
  reapply(): void
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
  // The bases (and the attachment, the schema lock, `profileGone`) belong to the
  // daemon at `useProfileStore.masterEndpoint` — STORED at attach, so that a
  // reload cannot mistake an edited address for the original. `api.ts` resolves
  // the address from the host store on every request, so an edit of the master
  // host IN PLACE would send the next request, with the old daemon's CAS bases,
  // to whatever answers at the new address. (A master always has one: without it
  // `selectMaster` says there is no master. Null here would still read as foreign.)
  const foreign = (at: string): boolean => at !== useProfileStore.getState().masterEndpoint
  const here = endpointOf(master.hostId)
  let token = here?.token ?? null
  let blocked = here !== null && foreign(here.at)
  /** The attachment answered 404. Final for this mode: only `attachMaster` / `detachMaster` (a new mode) end it. */
  let gone = false
  warned.clear()
  const blockedProblem = (at: string): void =>
    reportProblem({
      kind: 'master-endpoint-changed',
      detail: `host ${master.hostId} points at ${at}, the profile was attached at ${String(useProfileStore.getState().masterEndpoint)}; nothing syncs until it is attached again, detached, or the address is put back`,
    })
  if (blocked && here !== null) blockedProblem(here.at)
  const unwatchUnsynced = watchUnsyncedStores()
  const leadership = contendForLeadership()

  const follow = (): void => {
    leader?.dispose()
    leader = null
  }
  const apply = (isLeader: boolean): void => {
    if (ended) return
    if (!isLeader || blocked || gone || isSuspended()) follow()
    else if (leader === null) leader = lead(master, leadership, profileGone)
    changed() // the lease, a block or a suspension moved — or a driver now exists to be asked
  }
  const profileGone = (detail: string): void => {
    if (ended || gone) return
    gone = true
    follow()
    // Not a detach and not a wipe: what to do with a master that is gone is the user's call (P3's wizard).
    reportProblem({ kind: 'profile-gone', detail: `profile ${master.profileId} is not on host ${master.hostId} any more (${detail}); nothing was applied and nothing was dropped` })
  }
  const unsubscribe = leadership.onChange(apply)

  // Only a LOCAL edit can do this: a `hosts` payload from the SOT that re-points
  // the master's own host is refused by apply-to-stores.
  const unwatchEndpoint = useHostStore.subscribe((next, prev) => {
    if (ended || next.hosts[master.hostId] === prev.hosts[master.hostId]) return
    const now = endpointOf(master.hostId)
    if (now === null) return // removed: the leader reports it; coming back is judged like any edit
    if (foreign(now.at)) {
      // ip or port: nobody can tell whether this is the same daemon. Stop. Do not
      // detach (the user's setting) and do not drop the bases (a typo may be
      // corrected): the ways out are the old value, `attachMaster`, `detachMaster`.
      if (blocked) return
      blocked = true
      follow()
      blockedProblem(now.at)
      return
    }
    const rotated = token !== null && now.token !== token
    token = now.token
    if (!blocked && !rotated) return
    // Back where the bases belong, or the same daemon with a new token: a new
    // driver on the bases there are — a reconnect, in effect.
    blocked = false
    follow()
    apply(leadership.isLeader())
  })

  // SUSPENDED (`useProfileStore.suspension`): an attach is being made, in this
  // window or another. No driver until it is lifted — or until it EXPIRES, which
  // nobody announces: the window that set it may be gone, so the wake-up is a
  // timer of this mode's own.
  let wake: ReturnType<typeof setTimeout> | null = null
  const reapply = (): void => {
    if (wake !== null) clearTimeout(wake)
    wake = null
    if (ended) return
    // Whoever owns it by now: the wake-up looks at the time only.
    const suspension = useProfileStore.getState().suspension
    if (suspension !== null && isSuspended()) wake = setTimeout(reapply, Math.max(1, suspension.until - clock()))
    apply(leadership.isLeader())
  }

  // `onChange` does not replay: ask once.
  reapply()

  return {
    master,
    generation,
    isLeader: () => !ended && leadership.isLeader(),
    blocked: () => (ended ? null : gone ? 'profile-gone' : blocked ? 'master-endpoint-changed' : isSuspended() ? 'suspended' : null),
    reapply,
    leader: () => leader,
    end() {
      if (ended) return
      ended = true
      unsubscribe()
      unwatchEndpoint()
      if (wake !== null) clearTimeout(wake)
      wake = null
      follow()
      leadership.stop()
      unwatchUnsynced()
    },
  }
}

// === Start ===

let mode: MasterMode | null = null
/** Open exactly while `mode` is: see the header, FOR A UI. */
let channel: StatusChannel | null = null

/** Something `profileSyncState()` reads may have moved. Never more than memory without a master. */
function changed(): void {
  if (channel !== null) channel.refresh()
  else setLocalSnapshot(profileSyncState())
}

function leaseLive(): boolean {
  const lease = readLeaderLease()
  return lease !== null && lease.expiresAt > clock()
}

/** "Sync now", from any window. Without a master: nothing. */
export function requestSyncNow(): void {
  channel?.requestSyncNow()
}

/**
 * Answer a lock, from any window. `lock` is what the user was shown — `snapshot.status.locks[section]`: whoever
 * leads THIS master executes it only if the section's lock is still that one, field by field (sync-status.ts).
 * Without a master: nothing.
 */
export function requestResolve(section: string, keep: 'local' | 'sot', lock: SectionLock): void {
  channel?.requestResolve(section, keep, lock)
}

function sameMaster(a: Master | null, b: Master | null): boolean {
  if (a === null || b === null) return a === b
  return a.hostId === b.hostId && a.profileId === b.profileId
}

export function profileSyncState(): ProfileSyncState {
  const blocked = mode?.blocked() ?? null
  return {
    master: selectMaster(useProfileStore.getState()),
    leader: mode?.isLeader() ?? false,
    blocked,
    status: blocked === 'profile-gone' ? { profile: 'locked:reset', schemaLock: null, sections: {}, locks: {} } : (mode?.leader()?.status() ?? null),
    problems: problems.map((p) => ({ ...p })),
  }
}

/**
 * App lifetime, called from main.tsx. Without a master this is one subscription
 * to `useProfileStore` and nothing else (THE IRON RULE above).
 */
export function startProfileSync(opts: { now?: () => number } = {}): () => void {
  let stopped = false
  clock = opts.now ?? (() => Date.now())
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
    // The old master's status and whatever was asked of ITS leader go with it, in every window — and nothing of the
    // next master's, which other windows may be on already (sync-status.ts, `close`).
    // Same master, new generation: a "Sync now" this window's channel still holds for the old one is carried over
    // (sync-status.ts, THE GENERATION HAND-OVER). `same` with a null master never gets here (returned above).
    channel?.close(true, same && master !== null ? masterTagOf(master, generation) : undefined)
    channel = null
    if (wasLeader && master !== null) clearSectionStore(master.profileId)
    mode = master === null ? null : enterMasterMode(master, generation)
    if (master !== null) {
      channel = openStatusChannel({
        now: () => clock(),
        windowId: leaderWindowId(),
        // One channel per (master, generation) — this function's own condition for getting here — so the tag is the
        // channel's for life, and a window that has not heard of a change yet is on another tag than one that has.
        masterTag: masterTagOf(master, generation),
        local: profileSyncState,
        leaseLive,
        syncNow: () => mode?.leader()?.executor.syncNow(),
        resolve: (section, keep) => mode?.leader()?.executor.resolve(section, keep),
        lockOf: (section) => mode?.leader()?.lock(section) ?? null,
      })
    }
    // `enterMasterMode` has called `changed()` already — before `mode` pointed at it. This is the one that counts.
    changed()
  }

  const unsubscribe = useProfileStore.subscribe((next, prev) => {
    if (stopped) return
    sync(selectMaster(next), next.attachGeneration)
    if (next.suspension !== prev.suspension) mode?.reapply()
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
      profiles: {
        list: () => {
          const local = useLocalProfilesStore.getState()
          return { active: local.activeProfileId, slaves: local.slaveOrder.map((id) => ({ id, name: local.slaves[id].name, onScreen: local.slaves[id].world === null })) }
        },
        switch: switchActiveProfile,
        copyMaster: copyMasterAsSlave,
        saveScreen: saveScreenAsSlave,
        promote: promoteToMaster,
        rename: renameSlave,
        remove: deleteSlave,
        world: () => {
          const read = readMasterWorld()
          return read.settled ? { settled: true, onScreen: read.onScreen, workspaces: read.world.workspaces.map((w) => w.name) } : read
        },
      },
    }
  }

  return () => {
    if (stopped) return
    stopped = true
    unsubscribe()
    mode?.end()
    mode = null
    channel?.close(false) // this window lets go; the master is still set, and the keys are the other windows'
    channel = null
    changed()
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
  const refusal = (): AttachResult | null => {
    if (!isSyncDirection(direction)) return { ok: false, reason: 'invalid-direction' }
    if (!isClientIdPersisted()) return { ok: false, reason: 'client-id-not-persisted' }
    if (useHostStore.getState().hosts[hostId] === undefined) return { ok: false, reason: 'unknown-host' }
    if (!isMasterPair(hostId, profileId)) return { ok: false, reason: 'invalid-profile-id' }
    return null
  }
  const refused = refusal()
  if (refused !== null) return Promise.resolve(refused)
  // IN THE CALL, not in the queue below: `serial` is a `.then`, i.e. at least one
  // microtask away, and a write the old driver had already decided on (it sits
  // behind `await shapes()`, or next in its queue) is no further away than that —
  // it was not on the wire when the user attached, and it would be by the time
  // the queue got here. An earlier attach or detach still waiting in the queue is
  // not disturbed by this: each looks after the master itself.
  const hold = holdStill()
  return serial(async (): Promise<AttachResult> => {
    try {
      // what was true in the call may not be by the time its turn comes
      const late = refusal()
      if (late !== null) {
        hold.giveUp()
        return late
      }
      hold.extend() // the wait in the queue is not part of the first request's budget
      return await attachHeld({ hostId, profileId }, direction, hold)
    } finally {
      hold.stop()
    }
  })
}

/** One attach's grip on every driver of this client (see `useProfileStore`, WHY `suspension`). */
interface Hold {
  token: string
  /** A fresh 30 s for the suspension in force, if it belongs to this window; never a change of owner. */
  extend(): void
  /** Stops watching. Does not lift anything. */
  stop(): void
  /** The attach failed: lift OUR suspension. A newer attach's stays, and the drivers with it; otherwise the old mode comes back. */
  giveUp(): void
  /**
   * ANOTHER WINDOW's attach or detach overtook this attach: the generation in `localStorage` is not the one
   * this attach was asked in (plus what this window's own queue did since). Windows have separate queues, so
   * nothing else can tell an attach whose PUT is out that the user has meanwhile said "stop" elsewhere — and
   * this window's store may not have heard yet, which is why STORAGE is read, synchronously, like the lease.
   *   What is left: the check is "read, then commit" — not atomic; and `localStorage` is not instantaneous
   * between two renderer processes. The window is one synchronous read and the synchronous commit that
   * follows it, no longer the whole wait for the PUT (up to 15 s).
   */
  superseded(): boolean
  /** This window's own queue moved the generation (a `setMaster` / `clearMaster` of ours): not an overtaking. */
  expected: number
}

/**
 * FROM THE CALL of an attach on (`attachMaster` holds before it queues): every driver of this client stands
 * still — here at once (the store subscription is synchronous), in the other
 * windows as soon as the store reaches them — and stays so WHILE ANY ATTACH IS IN
 * PROGRESS, this one included: if a newer attach (another window's) replaced our
 * suspension and then finished, lifting its own, nobody's is left and we are not
 * done, so it is put back. (The start layer's subscription runs first and may
 * begin a driver on the lifted suspension; it is disposed again in the same turn,
 * before anything of it could reach the network.)
 */
/** The attaches of THIS window that are queued or running. */
const liveHolds = new Set<string>()
const liveAttaches = new Set<Hold>()

/** This window has just moved the generation itself: the attaches waiting behind it expect that. */
function ownGenerationMove(): void {
  for (const hold of liveAttaches) hold.expected += 1
}

function holdStill(): Hold {
  const token = newToken()
  liveHolds.add(token)
  const suspend = (): void => useProfileStore.getState().suspend(token, clock() + ATTACH_SUSPEND_MS)
  suspend()
  let unsubscribe = useProfileStore.subscribe((state) => {
    if (state.suspension === null && selectMaster(state) !== null) suspend()
  })
  const stop = (): void => {
    liveHolds.delete(token)
    liveAttaches.delete(hold)
    unsubscribe()
    unsubscribe = () => undefined
  }
  const hold: Hold = {
    token,
    // read AFTER our own `suspend()` above, which persists the store but does not move the generation
    expected: storedGeneration(),
    superseded: () => storedGeneration() !== hold.expected,
    // Ours — or that of an attach of this window waiting in the queue BEHIND us (it suspended in its call, so
    // it is the newer owner, and its 30 s are running while we work): refreshed under ITS token, so that it
    // cannot run out before its turn. Another window's is left alone.
    extend: () => {
      const current = useProfileStore.getState().suspension
      if (current !== null && liveHolds.has(current.token)) useProfileStore.getState().suspend(current.token, clock() + ATTACH_SUSPEND_MS)
    },
    stop,
    giveUp: () => {
      stop()
      useProfileStore.getState().resume(token)
    },
  }
  liveAttaches.add(hold)
  return hold
}

async function attachHeld(next: Master, direction: SyncDirection, hold: Hold): Promise<AttachResult> {
  const { hostId, profileId } = next
  const previous = selectMaster(useProfileStore.getState())
  const asYouWere = (reason: string): AttachResult => {
    hold.giveUp() // bases and attachment of the previous master are as they were
    return { ok: false, reason }
  }

  let put: Awaited<ReturnType<typeof putAttachment>>
  try {
    put = await putAttachment(hostId, profileId, await attachmentBody())
  } catch (e) {
    return asYouWere(message(e))
  }
  if (put.kind === 'failed') return asYouWere(put.reason)
  if (hold.superseded()) return standDown(next, hold)
  if (endpointOf(hostId) === null) return asYouWere('unknown-host') // gone while the PUT was out: leave the previous master whole
  if (previous !== null && !sameMaster(previous, next)) {
    // A second request to wait for, so a fresh budget. Each stretch (`ATTACH_SUSPEND_MS`, 30 s) is strictly
    // longer than the ONE request it covers (15 s timeout), and the first stretch cannot have run out before
    // this one starts (it began at most 15 s ago).
    hold.extend()
    await dropAttachment(previous)
    if (hold.superseded()) return standDown(next, hold)
  }
  // The address the attachment was written to: the home of every base from here
  // on. Read after the last `await`, checked before anything is cleared (the host
  // may have left the store meanwhile).
  const at = endpointOf(hostId)
  if (at === null) return asYouWere('unknown-host')
  // EVERY attach starts from no bases, the same master included. With a base
  // still held, a section that is dirty while the SOT has not moved is simply
  // pushed — under `pull` too, the opposite of what was asked; without one it is
  // a conflict, and the direction answers it. No `await` between this and
  // `setMaster`: the old driver cannot write a base in between, and the new one
  // (built by the subscription, on the new `attachGeneration`) is not seeded
  // from the old ones.
  // THE COMMIT. Last look at storage first: no `await` lies between it and `setMaster`.
  if (hold.superseded()) return standDown(next, hold)
  clearSectionStore()
  hold.stop() // `setMaster` lifts our suspension on purpose
  // One write: master, direction, endpoint, a new generation — and OUR suspension lifted (not a newer attach's).
  if (!useProfileStore.getState().setMaster(hostId, profileId, direction, at.at, hold.token)) return asYouWere('invalid-profile-id')
  ownGenerationMove()
  return { ok: true }
}

/**
 * Overtaken by another window (`Hold.superseded`): this attach commits NOTHING — no bases cleared, no master
 * set. This window's store is first brought up to what storage holds (its broadcast may still be on the way;
 * a write made from the stale memory — even lifting our own suspension — would persist the OLD master over
 * the other window's decision), then our suspension is lifted if it is still ours. The attachment this attach
 * has just written is taken down again, best effort, unless it is exactly the one the winner wants: overtaken
 * by a DETACH → nobody wants it; by an attach to the same (host, profile) → theirs now; to another → nobody's.
 */
async function standDown(next: Master, hold: Hold): Promise<AttachResult> {
  await useProfileStore.persist.rehydrate() // synchronous storage: memory is storage before this yields
  hold.giveUp()
  const winner = selectMaster(useProfileStore.getState())
  if (!sameMaster(winner, next)) await dropAttachment(next)
  return { ok: false, reason: 'superseded' }
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
    ownGenerationMove() // an attach of THIS window queued behind us is not "overtaken" by it: the user asked in that order
    clearSectionStore(master.profileId)
    await dropAttachment(master)
  })
}

export function __resetProfileSyncForTest(): void {
  problems.length = 0
  warned.clear()
  queue = Promise.resolve()
  __resetSyncStatusForTest()
}
