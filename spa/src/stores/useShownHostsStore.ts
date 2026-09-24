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
// H2d-1 only creates, persists, syncs, applies and re-keys the store; nothing reads it for UI yet.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

/** The persisted value: exactly the wire ids of the shown hosts. */
export interface ShownHosts {
  ids: string[]
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

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

export const useShownHostsStore = create<ShownHostsState>()(
  persist(
    (set) => ({
      ids: [],

      // A no-op returns the same state: no persist, no notify.
      show: (id) => set((state) => (state.ids.includes(id) ? state : { ids: [...state.ids, id] })),

      hide: (id) => set((state) => (state.ids.includes(id) ? { ids: state.ids.filter((x) => x !== id) } : state)),

      toggle: (id) =>
        set((state) => ({ ids: state.ids.includes(id) ? state.ids.filter((x) => x !== id) : [...state.ids, id] })),

      rekey: (moves) =>
        set((state) => {
          let ids: string[] | null = null
          for (const [from, to] of moves) {
            const cur: string[] = ids ?? state.ids
            if (from === to || !cur.includes(from)) continue
            ids = cur.includes(to) ? cur.filter((id) => id !== from) : cur.map((id) => (id === from ? to : id))
          }
          return ids === null ? state : { ids }
        }),
    }),
    {
      name: STORAGE_KEYS.SHOWN_HOSTS,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({ ids: state.ids }),
      // Every arrival — this device's storage, another window, a `settings` apply (apply-to-stores rehydrates after
      // its write) — passes through here. Only `ids` is read (a legacy `all` key is ignored and never written back);
      // nothing stored keeps memory as it is.
      merge: (persisted, current) => (isRecord(persisted) ? { ...current, ids: sanitizeShownIds(persisted.ids) } : current),
    },
  ),
)

syncManager.register(STORAGE_KEYS.SHOWN_HOSTS, useShownHostsStore)
