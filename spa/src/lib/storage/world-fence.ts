// spa/src/lib/storage/world-fence.ts — the epoch fence: a window that still holds
// an OLD tab world cannot write it over a newer one (Profile Sync spec §4.1; P3b).
//
// WHAT WENT WRONG WITHOUT IT (found on real hardware, two windows). A switch
// writes three persisted stores — local profiles, tabs, workspaces — under one
// new `worldEpoch`, and every other window catches up store by store, a broadcast
// and a rehydrate each. Between the `tabs` and the `workspaces` rehydrate such a
// window holds the new tabs and the OLD workspaces; any subscriber that answers
// the new tabs with a workspace action (route sync does) makes zustand persist
// write that whole old workspace store back to storage — over the switch. The
// window then rehydrates its own stale write, stays `epoch-mismatch` for good,
// cannot switch back (`unsettled`), and overwrites the disk again with every
// write it makes. The epoch barrier of lib/profile/master-world.ts kept the SOT
// clean through all of it; the user was stuck and the device's storage wrong.
//
// THE FENCE. One side key (`STORAGE_KEYS.WORLD_EPOCH`, a decimal integer). A
// switch / promote raises it to the new epoch FIRST, before any of the three
// stores is written. The three stores persist through `fencedWorldStorage`, which
// looks at the state it is handed BEFORE it is stringified:
//
//   state.worldEpoch <  fence → NOT WRITTEN, and the store is rehydrated
//   state.worldEpoch >= fence → written, exactly as `purdexStorage` writes it
//                               (unchanged value: no write; a write: one
//                               `syncManager.notify`)
//
// No side key, or one that is not a decimal safe integer, is a fence of 0, and no
// epoch is below 0: FOR A USER WHO NEVER SWITCHED A PROFILE NOTHING CHANGES — the
// same bytes are written, and the side key never comes into being (THE IRON RULE,
// lib/profile/start.ts; pinned by the tests). A state whose `worldEpoch` is no
// number at all (storage junk — the two live stores have no `merge`) is written
// too: nothing says it is OLDER, and dropping every write of such a window would
// lose its tabs at the next reload, where today it only never settles.
//
// A DROPPED WRITE IS A DROPPED ACTION, ON PURPOSE. It acted on a world this
// device no longer shows; the rehydrate that follows (one per store per turn, a
// microtask later — never inside the caller's `set`) puts the current world in
// memory and React re-runs on that. A write dropped BY that rehydrate (a
// `migrate` that re-persists) asks for no second one: no loop.
//
// AN EPOCH BELONGS TO ONE OPERATION (`nextWorldEpoch`). With `epoch + 1`, two
// windows at epoch n that switched at the same time BOTH wrote n + 1: the second
// found the fence already there, and their six store writes interleaved into a
// pointer of one world over the tabs of another — all under one epoch, with no
// higher one ever coming to retire it. So an epoch is the clock in microseconds
// plus a random 0–999, and never below (fence + 1) or (any store's epoch + 1):
// still a safe integer, still strictly increasing on this device, and two
// operations share one only when two windows draw the same number in the same
// millisecond — which is then the old behaviour, not a new one. And
// `raiseWorldEpochFence` FAILS when the fence is already at or above its target.
// Of two overlapping operations the one with the higher epoch wins, whole: from
// the moment its fence is up, every store write of the other is below it —
// dropped, and answered with a rehydrate of the winner's world; the loser sees
// that the fence is no longer its own and says `superseded`
// (lib/profile/switch-active.ts). The loser's `lower()` does nothing: the key no
// longer holds what it wrote.
//
// AN EPOCH HAS A CEILING (`MAX_WORLD_EPOCH`, 8e15: below 2^53, and the clock in
// microseconds stays under it until the year 2223). Only corrupt storage can hold
// more — but a fence at `Number.MAX_SAFE_INTEGER` would refuse every raise for
// ever, and a store up there would leave no epoch above it. So anything beyond
// the ceiling is JUNK, like a string or a NaN: the fence reads as 0, a store's
// epoch is no lower bound for the next one, and the door
// (lib/profile/master-world.ts) reads such a store as unsettled — with one way
// out, a switch (`junk-epoch`, there). What a raise ACCEPTS is any safe integer:
// a bound of exactly the ceiling yields ceiling + 1, which is written, reads as
// junk from then on, and is healed by the same way out — no value is a dead end.
//
// WHAT IS LEFT, stated plainly: `localStorage` is not transactional. Between
// "read the side key" and "write the store" another window can still raise the
// fence and write; the fence narrows the window from "a whole sequence of three
// broadcasts and rehydrates, with every subscriber of the app running in
// between" to ONE synchronous read-then-write. And between two renderer
// processes `localStorage` itself is only eventually shared (start.ts,
// `suspendedInStorage`, says the same of the suspension).
//   THE SAME HOLDS FOR RAISING THE FENCE ITSELF, and that one has a name. `raise`
// is read → check → write, and `localStorage` has no compare-and-swap: window A
// reads the fence and finds it below its epoch; window B raises a HIGHER one and
// starts writing its stores; A writes its LOWER epoch over B's. From then on the
// store writes of both pass the fence and land interleaved. Two windows have to
// switch inside the same few microseconds — no hand does that — but it cannot be
// closed on `localStorage` alone, and no further protocol is invented for it
// here. WHERE THERE IS A REAL MUTEX IT IS USED: world-lock.ts runs the block of
// a switch / promote under a Web Lock, and the window is shut. Where there is
// none (no secure context: a dev build over plain http) the consequence is this,
// pinned by a test: the three stores ON SCREEN may end up mixed — one window's
// pointer, the other's tabs. Both windows then rehydrate into a world whose
// epochs or ids disagree: UNSETTLED — nothing is reported to the SOT, nothing is
// applied, a switch is refused; silent, never wrong, and visible
// (`world-unsettled`). THE PARKED WORLDS ARE NOT TOUCHED BY IT: they live in ONE
// key, written in one `setItem`, so they are one window's version, whole. What
// can be lost is the arrangement of the world that was on screen in the losing
// window; the way back is a reload, which reads whatever storage holds.
import { createJSONStorage } from 'zustand/middleware'
import type { PersistStorage, StorageValue } from 'zustand/middleware'
import { browserStorage } from './browser-backend'
import { STORAGE_KEYS } from './keys'

/** Same backend as `purdexStorage`, so everything that is let through behaves as it always did. */
const inner = createJSONStorage<unknown>(() => browserStorage) as PersistStorage<unknown>

const DECIMAL = /^(0|[1-9][0-9]*)$/

/** See AN EPOCH HAS A CEILING. */
export const MAX_WORLD_EPOCH = 8e15

/** A value a world epoch can have: a safe integer in [0, MAX_WORLD_EPOCH]. Everything else is storage junk. */
export function isWorldEpoch(v: unknown): v is number {
  return Number.isSafeInteger(v) && (v as number) >= 0 && (v as number) <= MAX_WORLD_EPOCH
}

/**
 * The `worldEpoch` in the record `localStorage` holds for a world store right
 * now, unjudged; undefined: absent, unreadable. For the one question memory
 * cannot answer — is a junk epoch junk ON DISK too, i.e. will a rehydrate not
 * help (master-world.ts, `junk-epoch`). This file is these stores' storage, so
 * the envelope is its to read.
 */
export function persistedWorldEpoch(name: string): unknown {
  try {
    const raw = localStorage.getItem(name)
    if (raw === null) return undefined
    return epochOf(JSON.parse(raw) as StorageValue<unknown>)
  } catch {
    return undefined
  }
}

/** The fence as `localStorage` holds it right now; 0 when absent, unreadable or not the decimal form of a world epoch. */
export function readWorldEpochFence(): number {
  try {
    const raw = localStorage.getItem(STORAGE_KEYS.WORLD_EPOCH)
    if (raw === null || !DECIMAL.test(raw)) return 0
    const n = Number(raw)
    return isWorldEpoch(n) ? n : 0
  } catch {
    return 0
  }
}

/**
 * The epoch of the NEXT operation that moves the world (see AN EPOCH BELONGS TO
 * ONE OPERATION). `current`: the epochs the three world stores hold in memory —
 * this file is below the stores in the import graph, so the caller hands them in;
 * one that is no world epoch (storage junk, the ceiling included) is no bound.
 */
export function nextWorldEpoch(current: readonly number[]): number {
  const floor = Math.max(readWorldEpochFence(), ...current.filter(isWorldEpoch))
  return Math.max(Date.now() * 1000 + Math.floor(Math.random() * 1000), floor + 1)
}

/**
 * Raises the fence to `epoch` — the FIRST write of a switch / promote — and
 * returns how to take that back: the key's previous bytes (or its absence),
 * restored only while the key still holds what this call wrote, and only once.
 * A rollback must lower the fence BEFORE it puts the stores back, or those very
 * writes (they carry the old epoch) would be dropped.
 *   `null` — NOT RAISED: the fence is already at or above `epoch` (another
 * operation got there between `nextWorldEpoch` and here), or `epoch` is no
 * positive safe integer. Nothing was written; the caller draws a new epoch or
 * gives up. Throws when storage refuses the write; lowering never throws. Not
 * announced: the side key is no store, and every reader reads storage.
 */
export function raiseWorldEpochFence(epoch: number): (() => void) | null {
  if (!Number.isSafeInteger(epoch) || epoch <= readWorldEpochFence()) return null
  const previous = localStorage.getItem(STORAGE_KEYS.WORLD_EPOCH)
  const mine = String(epoch)
  localStorage.setItem(STORAGE_KEYS.WORLD_EPOCH, mine)
  let lowered = false
  return () => {
    if (lowered) return
    lowered = true
    try {
      if (localStorage.getItem(STORAGE_KEYS.WORLD_EPOCH) !== mine) return // another window has moved it since
      if (previous === null) localStorage.removeItem(STORAGE_KEYS.WORLD_EPOCH)
      else localStorage.setItem(STORAGE_KEYS.WORLD_EPOCH, previous)
    } catch {
      // best effort: a fence left too high drops stale-looking writes and rehydrates — silent, never wrong
    }
  }
}

// === the stores behind the fence ===

interface FencedStore {
  persist: { rehydrate: () => void | Promise<void> }
}

const stores = new Map<string, FencedStore>()
/** Storage keys with a rehydrate queued, and with one running: a drop in either state queues nothing. */
const queued = new Set<string>()
const rehydrating = new Set<string>()

/** Tells the fence which store persists under `name` — the one it rehydrates after dropping a write. */
export function registerFencedStore(name: string, store: FencedStore): void {
  stores.set(name, store)
}

function rehydrateSoon(name: string): void {
  if (queued.has(name) || rehydrating.has(name)) return
  queued.add(name)
  queueMicrotask(() => {
    queued.delete(name)
    const store = stores.get(name)
    if (store === undefined) return
    rehydrating.add(name)
    // `then`: a synchronous throw and a rejection end in the same `catch`.
    void Promise.resolve()
      .then(() => store.persist.rehydrate())
      .catch(() => {
        // the store stays as it was; its next stale write asks again
      })
      .finally(() => rehydrating.delete(name))
  })
}

function epochOf(value: StorageValue<unknown> | null): unknown {
  const state = value?.state
  return typeof state === 'object' && state !== null ? (state as { worldEpoch?: unknown }).worldEpoch : undefined
}

/** The persist storage of the three world stores (`purdex-tabs`, `purdex-workspaces`, `purdex-local-profiles`). */
export const fencedWorldStorage: PersistStorage<unknown> = {
  getItem: (name) => inner.getItem(name),
  setItem: (name, value) => {
    const epoch = epochOf(value)
    if (typeof epoch === 'number' && epoch < readWorldEpochFence()) {
      rehydrateSoon(name)
      return
    }
    return inner.setItem(name, value)
  },
  removeItem: (name) => inner.removeItem(name),
}
