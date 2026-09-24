// spa/src/stores/useShownHostsStore.ts — the hosts shown in a workbench (host ownership spec §1.2 / §4.1; plan H2d-1,
// §0.6 / §0.7).
//
// `{ ids: string[] }` — a plain list, no "all" flag (plan §0.6, the user's rules). `[]` = every host hidden, and that is
// the default: a host added later is hidden, and at ship time every existing host is hidden too — nothing seeds this
// list (§0.7). Every action touches exactly ONE id and carries every other id through untouched, unknown ones
// included: materialising "all" into the ids this device knows is what dropped hosts only another device knows
// (PR #1421 attacker finding).
//
// The ids are WIRE ids in the store itself (`d1_…` for a host whose daemon is known, else that host's local id until
// the re-resolve pass re-keys it) — the builder and the applier pass them through verbatim; which local host an id means
// is decided by the reader (`lib/shown-hosts.ts`). To this module an id is an opaque string: it imports no store.
//
// Projected in `settings` (`purdex-shown-hosts.ids`, PROJECTIONS.settings) and registered with `syncManager`.
//
// PER WORKBENCH (2026-09-25 plan, Design 2): this store holds THE MASTER'S list, on screen or parked; a local workbench's
// list is its `LocalProfile.shownHostIds`. Which one applies is `lib/shown-hosts.ts`'s question.
//
// `relabelStamp` — device-local, persisted, NEVER projected (not in `settings`, not in the collector's payload): the
// `useLocalProfilesStore.relabelCount` this list belongs to. A promote writes the promoted workbench's list here and the
// new count in the same block; a window that has rehydrated one of the two stores but not the other sees them disagree,
// and the reader then trusts neither (fail closed). A record without a stamp — data from before it existed — is stamped
// in `merge` with the count the local-profiles storage holds (the rule of `relabelCountInStorage`, read here directly:
// this module imports no store).
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

/** The persisted value: exactly the wire ids of the shown hosts. */
export interface ShownHosts {
  ids: string[]
  /** The `relabelCount` this list was written under (see the header). */
  relabelStamp: number
}

/** A re-key step: the id `from` becomes `to` (the re-resolve pass). */
export type ShownHostMove = readonly [from: string, to: string]

interface ShownHostsState extends ShownHosts {
  /** Appends `id` when absent. */
  show: (id: string) => void
  /** Removes `id` when present; every other id keeps its place. */
  hide: (id: string) => void
  /** `hide` when listed, else `show`. */
  toggle: (id: string) => void
  /** Per step: `from` is replaced in place by `to`, or dropped when `to` is already listed. */
  rekey: (moves: readonly ShownHostMove[]) => void
}

/** The strings of `raw`, deduped keeping the first, in order; `[]` for anything that is not an array. */
export function sanitizeShownIds(raw: unknown): string[] {
  if (!Array.isArray(raw)) return []
  const seen = new Set<string>()
  const out: string[] = []
  for (const id of raw) {
    if (typeof id !== 'string' || seen.has(id)) continue
    seen.add(id)
    out.push(id)
  }
  return out
}

/** `ids` with each move applied in turn: `from` replaced in place by `to`, or dropped when `to` is already listed.
 *  The same array back when nothing moves. Pure — the store's `rekey`, and a local workbench's own list
 *  (lib/shown-hosts.ts `rekeyShownHosts`), by one rule. */
export function rekeyShownIds(ids: string[], moves: readonly ShownHostMove[]): string[] {
  let out = ids
  for (const [from, to] of moves) {
    if (from === to || !out.includes(from)) continue
    out = out.includes(to) ? out.filter((id) => id !== from) : out.map((id) => (id === from ? to : id))
  }
  return out
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

const isCount = (v: unknown): v is number => Number.isSafeInteger(v) && (v as number) >= 0

/** `relabelCount` as the local-profiles storage holds it — `relabelCountInStorage`'s rule (junk → 0), absent or
 *  unreadable → 0 (what that store then reads as). */
function storedRelabelCount(): number {
  try {
    const raw = localStorage.getItem(STORAGE_KEYS.LOCAL_PROFILES)
    if (raw === null) return 0
    const state = (JSON.parse(raw) as { state?: unknown }).state
    return isRecord(state) && isCount(state.relabelCount) ? state.relabelCount : 0
  } catch {
    return 0
  }
}

export const useShownHostsStore = create<ShownHostsState>()(
  persist(
    (set) => ({
      ids: [],
      relabelStamp: 0,

      // A no-op returns the same state: no persist, no notify.
      show: (id) => set((state) => (state.ids.includes(id) ? state : { ids: [...state.ids, id] })),

      hide: (id) => set((state) => (state.ids.includes(id) ? { ids: state.ids.filter((x) => x !== id) } : state)),

      toggle: (id) =>
        set((state) => ({ ids: state.ids.includes(id) ? state.ids.filter((x) => x !== id) : [...state.ids, id] })),

      rekey: (moves) =>
        set((state) => {
          const ids = rekeyShownIds(state.ids, moves)
          return ids === state.ids ? state : { ids }
        }),
    }),
    {
      name: STORAGE_KEYS.SHOWN_HOSTS,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({ ids: state.ids, relabelStamp: state.relabelStamp }),
      // Every arrival — this device's storage, another window, a `settings` apply (apply-to-stores rehydrates after
      // its write) — passes through here. Only `ids` and `relabelStamp` are read (a legacy `all` key is ignored and
      // never written back); nothing stored keeps memory's ids as they are. A missing or junk stamp is stamped.
      merge: (persisted, current) => {
        const stored = isRecord(persisted) ? persisted : null
        const relabelStamp = stored !== null && isCount(stored.relabelStamp) ? stored.relabelStamp : storedRelabelCount()
        return stored === null ? { ...current, relabelStamp } : { ...current, ids: sanitizeShownIds(stored.ids), relabelStamp }
      },
    },
  ),
)

syncManager.register(STORAGE_KEYS.SHOWN_HOSTS, useShownHostsStore)
