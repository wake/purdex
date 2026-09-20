// spa/src/lib/profile/sync-status.ts — what Profile Sync is doing, for a UI in
// ANY window, and the way back: the two things a user can ask of the driver
// (P3 plan, P3a › Task 2). start.ts wires it; this file knows nothing of the
// executor, the lease or the stores — it is handed functions.
//
// THE PROBLEM. The driver runs in ONE window, the leader (leader.ts); most
// windows are followers, and in a follower `profileSyncState()` has no status and
// `syncNow()` / `resolve()` have no executor to reach. And `profileSyncState()`
// builds a fresh object on every call, which `useSyncExternalStore` answers with
// an endless render loop.
//
// TWO PARTS, WITH DIFFERENT LIFETIMES
//   the snapshot   module state: one cached `ProfileSyncSnapshot` and a set of
//                  listeners. Lives as long as the realm, because a React
//                  component subscribes once and masters come and go. It is
//                  memory only — no storage, no timer, no event listener — so it
//                  costs a user without a master nothing (start.ts, THE IRON RULE).
//                  Its identity changes only when its CONTENT did (a JSON
//                  signature; at most 50 problems, a handful of sections).
//   the channel    `openStatusChannel()` … `close()`. start.ts opens one per
//                  master mode, in every window. Everything that touches
//                  `localStorage`, the `storage` event or a timer is in here, and
//                  all of it is gone after `close()`.
//
//   purdex-profile-status     →  {at, leader: <windowId>, status, blocked, problems}
//   purdex-profile-cmd:<id>   →  {kind: 'syncNow', at}
//                                {kind: 'resolve', section, keep, conflict, at}
//
// THE LEADER PUBLISHES, FOLLOWERS READ. The leader writes its state on change —
// trailing throttle, one write per `STATUS_THROTTLE_MS`: the executor reports
// every section of a reindex one by one, and every write is a `storage` event in
// every other window. A follower's snapshot is the published record, marked
// `remote: true`; `master` and `leader: false` stay its own (both are facts about
// THIS window). Not through `browserStorage` / `syncManager`, for the reason
// leader.ts gives: nobody's store needs to hear about it.
//   A leader with nothing new writes nothing, so an old record is normal. It is
// `stale` only when it is older than `STATUS_STALE_MS` AND nobody holds the
// lease: then nobody is there who would have corrected it. A follower finds that
// out by a timer of its own — the lease running out is not an event.
//   The record names its writer, so a new leader always publishes, identical
// content or not. And a record that vanishes under a sitting leader (a window
// that heard late about a detach clears it — see `close`) is published again.
//
// COMMANDS: ONE KEY PER COMMAND. An array in one key is read-modify-write, and
// localStorage has no compare-and-swap: two followers both read `[]` and one
// command is lost; the leader clears the array a follower has just appended to.
// With a key each there is nothing to merge — a follower only ever ADDS its own
// key, the leader only ever REMOVES the one it has dealt with. Pinned in the
// test "TWO followers send before the leader looks".
//   - the leader hears of a command through the `storage` event, and SCANS the
//     prefix whenever it has just become the leader: an event fires once, in the
//     windows that were there, and whoever led then may be gone.
//   - older than `COMMAND_TTL_MS` → removed unexecuted: a "Sync now" pressed half
//     a minute ago, while nobody led, is not something to act out on the user
//     now. Followers never remove anything; the next leader does.
//   - executed twice (two leaders in a hand-over both see the key; an event and a
//     scan) is harmless: `syncNow` is idempotent and a `resolve` of a section
//     that is no longer locked is a no-op in the reducer (sync-state, `resolved`).
//     Within ONE leader it does not happen: the key is read at execution time and
//     an event about a key that is gone is dropped.
//
// A `resolve` IS BOUND TO THE CONFLICT THE USER WAS LOOKING AT. It carries the
// pair the UI showed — `{localHash, sot: {rev, hash}}`, the reducer's own
// `SectionConflict`, which the executor publishes in `ExecutorStatus.conflicts` —
// and is executed only if the section's pair is STILL that one, all three values.
// `sot.rev` alone is not enough: the local side can change under an unchanged
// revision, and "keep local" would push content nobody confirmed. A lock without a
// pair (`locked:reset`, `locked:invalid`) is resolved with `null`, and only while
// there still is none. The same check guards the leader's own window: its UI
// renders a snapshot too. A refused resolve is dropped, never kept for later.
//
// WHAT IS LEFT. "Read the pair, then resolve" is two steps in one synchronous
// turn of one window — atomic, unlike the lease. Across windows: the follower's
// view is as old as the last publish (≤ 250 ms plus the event), which is exactly
// what the binding is for. A command written under one master is cleared with it
// (`close(true)`); one that slips through a re-attach is a `syncNow` (harmless) or
// a resolve bound to hashes of content that would have to recur.
import { STORAGE_KEYS } from '../storage/keys'
import type { ProfileSyncState } from './start'
import type { SectionConflict } from './sync-state'

/** One publish per this many ms, trailing: the value written is the one current when the timer fires. */
export const STATUS_THROTTLE_MS = 250
/** A published record OLDER than this, with nobody holding the lease, is `stale`. */
export const STATUS_STALE_MS = 10_000
/** A command OLDER than this is removed unexecuted. */
export const COMMAND_TTL_MS = 30_000

const STATUS_KEY = STORAGE_KEYS.PROFILE_STATUS
const COMMAND_PREFIX = STORAGE_KEYS.PROFILE_COMMAND_PREFIX

export interface ProfileSyncSnapshot extends ProfileSyncState {
  /** `status`, `blocked` and `problems` are the LEADER's, read from its published record: this window follows. */
  remote: boolean
  /** Only with `remote`: the record is older than `STATUS_STALE_MS` and nobody holds the lease. */
  stale: boolean
}

/** What the leader writes to `purdex-profile-status`. */
interface PublishedStatus {
  at: number
  /** The writer's window id (leader.ts). */
  leader: string
  status: ProfileSyncState['status']
  blocked: ProfileSyncState['blocked']
  problems: ProfileSyncState['problems']
}

type Command =
  | { kind: 'syncNow'; at: number }
  | { kind: 'resolve'; section: string; keep: 'local' | 'sot'; conflict: SectionConflict | null; at: number }

export interface StatusChannelDeps {
  /** Epoch ms. */
  now(): number
  /** leader.ts, `leaderWindowId()`. */
  windowId: string
  /** THIS window's view: start.ts, `profileSyncState()`. `leader` decides which side of the channel this window is on. */
  local(): ProfileSyncState
  /** Somebody — any window — holds an unexpired lease right now. */
  leaseLive(): boolean
  /** Asked only while `local().leader`. */
  syncNow(): void
  resolve(section: string, keep: 'local' | 'sot'): void
  /** The section's open conflict pair as the executor holds it NOW; null = none (not locked, or a lock without a pair). */
  conflictOf(section: string): SectionConflict | null
}

export interface StatusChannel {
  /** Something in `local()` may have changed. Cheap, and safe to call too often. */
  refresh(): void
  requestSyncNow(): void
  /** `conflict`: the pair the user was shown for this section (`snapshot.status.conflicts[section]`), or null if it had none. */
  requestResolve(section: string, keep: 'local' | 'sot', conflict: SectionConflict | null): void
  /** `clear`: the master is gone or replaced — the status and every command go with it. Otherwise they are the other windows' business. */
  close(clear: boolean): void
}

// === The snapshot ===

const NOTHING: ProfileSyncSnapshot = { master: null, leader: false, blocked: null, status: null, problems: [], remote: false, stale: false }

let snapshot: ProfileSyncSnapshot = NOTHING
let signature = JSON.stringify(NOTHING)
const listeners = new Set<() => void>()

function setSnapshot(next: ProfileSyncSnapshot): void {
  const sig = JSON.stringify(next)
  if (sig === signature) return
  signature = sig
  snapshot = next
  for (const fn of [...listeners]) {
    try {
      fn()
    } catch (err) {
      console.error('[profile/sync-status] listener threw', err)
    }
  }
}

/** For `useSyncExternalStore`: the same object until something in it changed. */
export function profileSyncSnapshot(): ProfileSyncSnapshot {
  return snapshot
}

export function subscribeProfileSync(fn: () => void): () => void {
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
  }
}

/** Without a channel (no master): this window's own view is all there is. Memory only. */
export function setLocalSnapshot(local: ProfileSyncState): void {
  setSnapshot({ ...local, remote: false, stale: false })
}

export function __resetSyncStatusForTest(): void {
  snapshot = NOTHING
  signature = JSON.stringify(NOTHING)
  listeners.clear()
}

// === Parsing: anything that is not exactly the shape is "not there" ===

const BLOCKED: ReadonlyArray<ProfileSyncState['blocked']> = ['master-endpoint-changed', 'profile-gone', 'suspended', null]

function parsePublished(raw: string | null): PublishedStatus | null {
  if (raw === null) return null
  let value: unknown
  try {
    value = JSON.parse(raw)
  } catch {
    return null
  }
  if (typeof value !== 'object' || value === null) return null
  const { at, leader, status, blocked, problems } = value as Record<string, unknown>
  if (typeof at !== 'number' || !Number.isFinite(at)) return null
  if (typeof leader !== 'string' || leader === '') return null
  if (status !== null && typeof status !== 'object') return null
  if (!BLOCKED.includes(blocked as ProfileSyncState['blocked'])) return null
  if (!Array.isArray(problems)) return null
  return { at, leader, status: status as PublishedStatus['status'], blocked: blocked as PublishedStatus['blocked'], problems: problems as PublishedStatus['problems'] }
}

/** `undefined` = damaged. `null` is a value: "the section had no pair". */
function parseConflict(value: unknown): SectionConflict | null | undefined {
  if (value === null) return null
  if (typeof value !== 'object' || value === undefined) return undefined
  const { localHash, sot } = value as Record<string, unknown>
  if (localHash !== null && typeof localHash !== 'string') return undefined
  if (typeof sot !== 'object' || sot === null) return undefined
  const { rev, hash } = sot as Record<string, unknown>
  if (typeof rev !== 'number' || !Number.isSafeInteger(rev)) return undefined
  if (hash !== null && typeof hash !== 'string') return undefined
  return { localHash, sot: { rev, hash } }
}

function parseCommand(raw: string): Command | null {
  let value: unknown
  try {
    value = JSON.parse(raw)
  } catch {
    return null
  }
  if (typeof value !== 'object' || value === null) return null
  const { kind, section, keep, conflict, at } = value as Record<string, unknown>
  if (typeof at !== 'number' || !Number.isFinite(at)) return null
  if (kind === 'syncNow') return { kind, at }
  if (kind !== 'resolve' || typeof section !== 'string' || (keep !== 'local' && keep !== 'sot')) return null
  const pair = parseConflict(conflict)
  return pair === undefined ? null : { kind, section, keep, conflict: pair, at }
}

function sameConflict(a: SectionConflict | null, b: SectionConflict | null): boolean {
  if (a === null || b === null) return a === b
  return a.localHash === b.localHash && a.sot.rev === b.sot.rev && a.sot.hash === b.sot.hash
}

function newCommandId(): string {
  const bytes = new Uint8Array(16)
  try {
    crypto.getRandomValues(bytes)
  } catch {
    for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256)
  }
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

// === Storage: every access is wrapped (leader.ts, "STORAGE THAT DOES NOT WORK") ===

function readItem(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function removeItem(key: string): void {
  try {
    localStorage.removeItem(key)
  } catch {
    /* a status goes stale, a command expires */
  }
}

function commandKeys(): string[] {
  try {
    const keys: string[] = []
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i)
      if (key !== null && key.startsWith(COMMAND_PREFIX)) keys.push(key)
    }
    return keys
  } catch {
    return []
  }
}

// === The channel ===

export function openStatusChannel(deps: StatusChannelDeps): StatusChannel {
  let closed = false
  /** `local().leader` at the last refresh: false → true is "has just taken the lease". */
  let wasLeader = false
  /** What the record in storage says, as far as THIS leader knows; null = publish whatever comes next. */
  let publishedSignature: string | null = null
  let publishTimer: ReturnType<typeof setTimeout> | null = null
  let staleTimer: ReturnType<typeof setTimeout> | null = null

  const clearPublishTimer = (): void => {
    if (publishTimer !== null) clearTimeout(publishTimer)
    publishTimer = null
  }
  const clearStaleTimer = (): void => {
    if (staleTimer !== null) clearTimeout(staleTimer)
    staleTimer = null
  }

  /** Everything of a record but `at`: what makes two of them "the same". */
  const publishable = (local: ProfileSyncState): string =>
    JSON.stringify({ leader: deps.windowId, status: local.status, blocked: local.blocked, problems: local.problems })

  const publish = (): void => {
    publishTimer = null
    if (closed) return
    const local = deps.local()
    if (!local.leader) return // stood down inside the throttle window: the record is the next leader's
    const record: PublishedStatus = { at: deps.now(), leader: deps.windowId, status: local.status, blocked: local.blocked, problems: local.problems }
    try {
      localStorage.setItem(STATUS_KEY, JSON.stringify(record))
      publishedSignature = publishable(local)
    } catch {
      publishedSignature = null // quota, blocked: the next change tries again
    }
  }

  const accepts = (section: string, conflict: SectionConflict | null): boolean => sameConflict(deps.conflictOf(section), conflict)

  const execute = (command: Command): void => {
    if (command.kind === 'syncNow') deps.syncNow()
    else if (accepts(command.section, command.conflict)) deps.resolve(command.section, command.keep)
  }

  /** Leader only. Reads the key NOW: an event about a key that has been dealt with finds nothing. */
  const take = (key: string): void => {
    const raw = readItem(key)
    if (raw === null) return
    try {
      const command = parseCommand(raw)
      if (command !== null && deps.now() - command.at <= COMMAND_TTL_MS) execute(command)
    } catch (err) {
      console.error('[profile/sync-status] command failed', err)
    } finally {
      removeItem(key)
    }
  }

  const refresh = (): void => {
    if (closed) return
    const local = deps.local()
    if (local.leader) {
      clearStaleTimer()
      setSnapshot({ ...local, remote: false, stale: false })
      if (publishTimer === null && publishable(local) !== publishedSignature) publishTimer = setTimeout(publish, STATUS_THROTTLE_MS)
      if (!wasLeader) {
        wasLeader = true
        for (const key of commandKeys()) take(key)
      }
      return
    }
    wasLeader = false
    publishedSignature = null
    clearPublishTimer()
    clearStaleTimer()
    const record = parsePublished(readItem(STATUS_KEY))
    if (record === null) {
      setSnapshot({ ...local, remote: false, stale: false })
      return
    }
    const age = deps.now() - record.at
    const stale = age > STATUS_STALE_MS && !deps.leaseLive()
    setSnapshot({ master: local.master, leader: false, blocked: record.blocked, status: record.status, problems: record.problems, remote: true, stale })
    // Neither growing old nor a lease running out is an event. Once stale, only a new record changes that — and that IS one.
    if (!stale) staleTimer = setTimeout(refresh, age <= STATUS_STALE_MS ? STATUS_STALE_MS - age + 1 : STATUS_STALE_MS)
  }

  const onStorage = (e: StorageEvent): void => {
    if (closed) return
    if (e.key === null) {
      // localStorage.clear(): the record is gone, and so is every command
      publishedSignature = null
      refresh()
    } else if (e.key === STATUS_KEY) {
      if (e.newValue === null) publishedSignature = null
      refresh()
    } else if (e.key.startsWith(COMMAND_PREFIX)) {
      if (e.newValue !== null && deps.local().leader) take(e.key)
    }
  }

  const send = (command: Command): void => {
    try {
      localStorage.setItem(`${COMMAND_PREFIX}${newCommandId()}`, JSON.stringify(command))
    } catch {
      /* the user presses again */
    }
  }

  const hasWindow = typeof window !== 'undefined'
  if (hasWindow) window.addEventListener('storage', onStorage)

  return {
    refresh,
    requestSyncNow() {
      if (closed) return
      const command: Command = { kind: 'syncNow', at: deps.now() }
      if (deps.local().leader) execute(command)
      else send(command)
    },
    requestResolve(section, keep, conflict) {
      if (closed) return
      const command: Command = { kind: 'resolve', section, keep, conflict, at: deps.now() }
      if (deps.local().leader) execute(command)
      else send(command)
    },
    close(clear) {
      if (closed) return
      closed = true
      clearPublishTimer()
      clearStaleTimer()
      if (hasWindow) window.removeEventListener('storage', onStorage)
      if (!clear) return
      // EVERY window does this when the master goes, not only the leader: removing is idempotent, and the leader
      // may be the window that hears last. A window that hears late may remove the record of the NEXT master's
      // leader — which publishes it again (`onStorage`).
      removeItem(STATUS_KEY)
      for (const key of commandKeys()) removeItem(key)
    },
  }
}
