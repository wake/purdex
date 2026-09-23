// spa/src/lib/profile/pull-unconfirmed.ts — the notice that a pull was stopped because the SOT's `hosts` moved after
// the user confirmed it (#1366): `{ hostId, profileId, at }`, under its OWN key (`STORAGE_KEYS.PROFILE_PULL_UNCONFIRMED`).
//
// WHY NOT A FIELD OF `useProfileStore` (codex R2 #1). The notice is written by the window whose executor halted —
// at a moment when that window's memory of the control plane may be STALE: another window may just have attached
// anew (new master or `attachId`, its own direction and guard in storage) and this one not have
// rehydrated yet. A persisted zustand store writes its WHOLE partialized state on any `set`, so a notice written
// through it would put the old master, generation, direction and guard back over the other window's attach. Here
// the write is one `localStorage.setItem` of this key alone: nothing of the master, the generation, the direction
// or the guard is ever touched.
//
// DEVICE-LOCAL. Never in the SOT, not registered with syncManager (no BroadcastChannel, no rehydrate): every window
// reads the same key; another window's write reaches a UI through the native `storage` event, this window's own
// through the listeners below. Every read and write is wrapped: storage that throws reads as "no notice" and a
// write that throws answers `false` — the stop that follows the notice happens either way (start.ts).
//
// LIFE: written by the start layer when a guarded pull halts (BEFORE the detach, so it survives it), cleared by
// Dismiss (Settings › Profile, CurrentBlock) and by an attach that succeeds (start.ts, `attachHeld`: the user has
// set sync up anew).
import { STORAGE_KEYS } from '../storage/keys'
import { isMasterPair } from '../../stores/useProfileStore'

const KEY = STORAGE_KEYS.PROFILE_PULL_UNCONFIRMED

/** A pull was stopped because the SOT's `hosts` moved after the user confirmed it (see the header). */
export interface PullUnconfirmed {
  hostId: string
  profileId: string
  at: number
}

/** A well-formed notice, copied (only the three fields), or null. */
function sanitise(v: unknown): PullUnconfirmed | null {
  if (typeof v !== 'object' || v === null || Array.isArray(v)) return null
  const { hostId, profileId, at } = v as Record<string, unknown>
  return isMasterPair(hostId, profileId) && typeof at === 'number' && Number.isFinite(at) ? { hostId: hostId as string, profileId: profileId as string, at } : null
}

function rawValue(): string | null {
  try {
    return localStorage.getItem(KEY)
  } catch {
    return null
  }
}

function parse(raw: string | null): PullUnconfirmed | null {
  if (raw === null) return null
  try {
    return sanitise(JSON.parse(raw))
  } catch {
    return null
  }
}

/** The notice storage holds now, or null (none, junk, unreadable). */
export function readPullUnconfirmed(): PullUnconfirmed | null {
  return parse(rawValue())
}

const listeners = new Set<() => void>()

function notify(): void {
  for (const fn of [...listeners]) fn()
}

/** Malformed, or storage refused it → `false`, nothing written. */
export function writePullUnconfirmed(notice: PullUnconfirmed): boolean {
  const clean = sanitise(notice)
  if (clean === null) return false
  try {
    localStorage.setItem(KEY, JSON.stringify(clean))
  } catch {
    return false
  }
  notify()
  return true
}

/** Dismissed, or a new attach. No notice → nothing is written and nobody is told. */
export function clearPullUnconfirmed(): void {
  if (rawValue() === null) return
  try {
    localStorage.removeItem(KEY)
  } catch {
    return
  }
  notify()
}

function onStorage(e: StorageEvent): void {
  // `key === null`: another window cleared the whole storage.
  if (e.key === KEY || e.key === null) notify()
}

/** For `useSyncExternalStore`. The `storage` listener exists only while someone subscribes. */
export function subscribePullUnconfirmed(fn: () => void): () => void {
  if (listeners.size === 0) window.addEventListener('storage', onStorage)
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
    if (listeners.size === 0) window.removeEventListener('storage', onStorage)
  }
}

let cachedRaw: string | null = null
let cached: PullUnconfirmed | null = null

/** For `useSyncExternalStore`: the same object until the stored value changes. */
export function pullUnconfirmedSnapshot(): PullUnconfirmed | null {
  const raw = rawValue()
  if (raw !== cachedRaw) {
    cachedRaw = raw
    cached = parse(raw)
  }
  return cached
}
