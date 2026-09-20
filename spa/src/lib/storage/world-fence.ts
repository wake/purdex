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
// WHAT IS LEFT, stated plainly: `localStorage` is not transactional. Between
// "read the side key" and "write the store" another window can still raise the
// fence and write; the fence narrows the window from "a whole sequence of three
// broadcasts and rehydrates, with every subscriber of the app running in
// between" to ONE synchronous read-then-write. And between two renderer
// processes `localStorage` itself is only eventually shared (start.ts,
// `suspendedInStorage`, says the same of the suspension).
import { createJSONStorage } from 'zustand/middleware'
import type { PersistStorage, StorageValue } from 'zustand/middleware'
import { browserStorage } from './browser-backend'
import { STORAGE_KEYS } from './keys'

/** Same backend as `purdexStorage`, so everything that is let through behaves as it always did. */
const inner = createJSONStorage<unknown>(() => browserStorage) as PersistStorage<unknown>

const DECIMAL = /^(0|[1-9][0-9]*)$/

/** The fence as `localStorage` holds it right now; 0 when absent, unreadable or not a decimal safe integer. */
export function readWorldEpochFence(): number {
  try {
    const raw = localStorage.getItem(STORAGE_KEYS.WORLD_EPOCH)
    if (raw === null || !DECIMAL.test(raw)) return 0
    const n = Number(raw)
    return Number.isSafeInteger(n) ? n : 0
  } catch {
    return 0
  }
}

/**
 * Raises the fence to `epoch` — the FIRST write of a switch / promote — and
 * returns how to take that back: the key's previous bytes (or its absence),
 * restored only while the key still holds what this call wrote, and only once.
 * A rollback must lower the fence BEFORE it puts the stores back, or those very
 * writes (they carry the old epoch) would be dropped. Never lowers a fence that
 * is already higher. Throws when storage refuses the write; lowering never throws.
 * Not announced: the side key is no store, and every reader reads storage.
 */
export function raiseWorldEpochFence(epoch: number): () => void {
  if (readWorldEpochFence() >= epoch) return () => {}
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

function epochOf(value: StorageValue<unknown>): unknown {
  const state = value.state
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
