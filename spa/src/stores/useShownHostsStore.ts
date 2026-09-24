// spa/src/stores/useShownHostsStore.ts — the hosts a workbench enables (host ownership spec §4.1 / §4.5; plan H2d-1,
// §0.6, §0.21).
//
// `{ all: boolean; ids: string[] }`, both keys always present (plan §0.6, coordinator decision): `all: true` is the
// spec's `ids: null` ("every host"). Encoding it as `null` could not travel — the settings apply rejects a field that
// changes shape class (`null` ↔ array) — and an absent field reads as "not sent", which could never clear another
// device's list. `ids` keeps its unknown ids and its order in BOTH modes.
//
// The ids are WIRE ids in the store itself (`d1_…` for a host whose daemon is known, else that host's local id) — the
// builder and the applier pass them through verbatim; which local host an id means is decided by the reader
// (`lib/shown-hosts.ts`). So to this module an id is an opaque string: it imports no store, and it keeps an id no host
// of this device claims (another device's daemon, a legacy local id) exactly as it arrived. The re-resolve pass moves
// a local id to its `d1_…` once the host's daemonId is known (`rekey`, plan §0.12).
//
// Projected in `settings` (`purdex-shown-hosts.all` / `.ids`, PROJECTIONS.settings) and registered with `syncManager`.
// H2d-1 only creates, persists, syncs, applies and re-keys the store; nothing filters by it yet.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

/** The persisted value: `all: true` = every host enabled; else exactly the wire ids in `ids`. */
export interface ShownHosts {
  all: boolean
  ids: string[]
}

/** A re-key step: the id `from` becomes `to` (the re-resolve pass). */
export type ShownHostMove = readonly [from: string, to: string]

interface ShownHostsState extends ShownHosts {
  /** `all: true`; the list is kept. */
  showAll: () => void
  /** `all: false`, exactly `ids` (strings, deduped keeping the first). */
  setShown: (ids: readonly string[]) => void
  /**
   * Flip one wire id. From `all: true` → `all: false` with every id of `knownWireIds` but `wireId` (the ids already
   * listed — unknown ones included — kept first, in their order); from a list → `wireId` leaves, or is appended.
   */
  toggle: (wireId: string, knownWireIds: readonly string[]) => void
  /** Appends `wireId` when absent; `all` is left as it is. */
  addShown: (wireId: string) => void
  /** Per step: `from` is replaced in place by `to`, or dropped when `to` is already listed. `all` is left alone. */
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
      all: true,
      ids: [],

      showAll: () => set((state) => (state.all ? state : { all: true })),

      setShown: (ids) => set({ all: false, ids: sanitizeShownIds(ids) }),

      toggle: (wireId, knownWireIds) =>
        set((state) => {
          if (state.all) {
            const ids = state.ids.filter((id) => id !== wireId)
            for (const id of knownWireIds) if (id !== wireId && !ids.includes(id)) ids.push(id)
            return { all: false, ids }
          }
          return { ids: state.ids.includes(wireId) ? state.ids.filter((id) => id !== wireId) : [...state.ids, wireId] }
        }),

      addShown: (wireId) => set((state) => (state.ids.includes(wireId) ? state : { ids: [...state.ids, wireId] })),

      rekey: (moves) =>
        set((state) => {
          let ids: string[] | null = null
          for (const [from, to] of moves) {
            const cur: string[] = ids ?? state.ids
            const at = cur.indexOf(from)
            if (from === to || at < 0) continue
            ids = cur.includes(to) ? cur.filter((id) => id !== from) : cur.map((id) => (id === from ? to : id))
          }
          return ids === null ? state : { ids }
        }),
    }),
    {
      name: STORAGE_KEYS.SHOWN_HOSTS,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({ all: state.all, ids: state.ids }),
      // Every arrival — this device's storage, another window, a `settings` apply (apply-to-stores rehydrates after
      // its write) — passes through here. A non-boolean `all` keeps memory's; nothing stored keeps memory as it is.
      merge: (persisted, current) =>
        isRecord(persisted)
          ? { ...current, all: typeof persisted.all === 'boolean' ? persisted.all : current.all, ids: sanitizeShownIds(persisted.ids) }
          : current,
    },
  ),
)

syncManager.register(STORAGE_KEYS.SHOWN_HOSTS, useShownHostsStore)
