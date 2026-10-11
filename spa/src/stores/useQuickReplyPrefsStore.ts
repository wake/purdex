// spa/src/stores/useQuickReplyPrefsStore.ts — the Mac App's quick-reply switches (quick-replies v2 spec §3, QR-2b).
//
// `{ rowEnabled: boolean (default true), disabled: { [wireHostId]: string[] } }` — off-lists, so an item added later is on by default, and an id
// that is no longer in its host's list is ignored. `rowEnabled` is app-wide (the whole row); `disabled` is per host.
//
// The keys of `disabled` are WIRE ids in the store itself (`d1_…` for a host whose daemon is known, else that host's local id until the re-resolve
// pass re-keys it), exactly like `purdex-host-looks.looks`: a local host id is random per Mac, so a local-id key would not name the same host on
// another Mac. The builder and the applier pass them through verbatim (no local↔wire mapping), and a key naming a host this Mac lacks round-trips
// untouched. To this module a key is an opaque string: it imports no store; the readers resolve a host to its key (`lookKeyOf`).
//
// Projected in `settings` (`purdex-quick-reply-prefs.{rowEnabled,disabled}`, PROJECTIONS.settings) and registered with `syncManager`. The daemon
// never holds these switches: the Mac keeps them in its Profile, the iPhone in its App, and the two switch independently.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

/** A re-key step: the entry under `from` moves to `to` (the re-resolve pass). */
export type QuickReplyPrefsMove = readonly [from: string, to: string]

interface QuickReplyPrefsState {
  rowEnabled: boolean
  disabled: Record<string, string[]>
  setRowEnabled: (on: boolean) => void
  /** Switches one item of one host off (`off`) or back on; the same state back writes nothing. */
  setItemDisabled: (key: string, itemId: string, off: boolean) => void
  /** Pruning, after a PUT 200: `key`'s off-list becomes its intersection with `keepIds`; an emptied list is removed. */
  prune: (key: string, keepIds: readonly string[]) => void
  /** Moves `from` → `to` per step: an existing `to` wins and `from` is dropped; a missing `from` is a no-op. */
  rekey: (moves: readonly QuickReplyPrefsMove[]) => void
}

// Assigning `obj['__proto__'] = v` rewrites the prototype instead of adding a key — never copied.
const FORBIDDEN_KEY = '__proto__'

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/** The strings of `raw`, deduped keeping the first, in order; `[]` for anything that is not an array. */
export function sanitizeDisabledIds(raw: unknown): string[] {
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

/** The whole record, sanitised: own keys verbatim but `__proto__`; a non-list value dropped; an emptied list dropped (an off-list of nothing is no entry). */
export function sanitizeDisabled(raw: unknown): Record<string, string[]> {
  const out: Record<string, string[]> = {}
  if (!isRecord(raw)) return out
  for (const key of Object.keys(raw)) {
    if (key === FORBIDDEN_KEY) continue
    const ids = sanitizeDisabledIds(raw[key])
    if (ids.length > 0) out[key] = ids
  }
  return out
}

export const useQuickReplyPrefsStore = create<QuickReplyPrefsState>()(
  persist(
    (set) => ({
      rowEnabled: true,
      disabled: {},

      setRowEnabled: (on) => set((state) => (state.rowEnabled === on ? state : { rowEnabled: on })),

      setItemDisabled: (key, itemId, off) =>
        set((state) => {
          if (key === FORBIDDEN_KEY) return state
          const cur = Object.hasOwn(state.disabled, key) ? state.disabled[key] : []
          if (cur.includes(itemId) === off) return state
          const next = off ? [...cur, itemId] : cur.filter((id) => id !== itemId)
          if (next.length > 0) return { disabled: { ...state.disabled, [key]: next } }
          const { [key]: _gone, ...rest } = state.disabled
          void _gone
          return { disabled: rest }
        }),

      prune: (key, keepIds) =>
        set((state) => {
          if (!Object.hasOwn(state.disabled, key)) return state
          const cur = state.disabled[key]
          const keep = new Set(keepIds)
          const next = cur.filter((id) => keep.has(id))
          if (next.length === cur.length) return state
          if (next.length > 0) return { disabled: { ...state.disabled, [key]: next } }
          const { [key]: _gone, ...rest } = state.disabled
          void _gone
          return { disabled: rest }
        }),

      rekey: (moves) =>
        set((state) => {
          let disabled: Record<string, string[]> | null = null
          for (const [from, to] of moves) {
            const cur = disabled ?? state.disabled
            if (from === to || to === FORBIDDEN_KEY || !Object.hasOwn(cur, from)) continue
            disabled ??= { ...state.disabled }
            if (!Object.hasOwn(disabled, to)) disabled[to] = disabled[from]
            delete disabled[from]
          }
          return disabled === null ? state : { disabled }
        }),
    }),
    {
      name: STORAGE_KEYS.QUICK_REPLY_PREFS,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({ rowEnabled: state.rowEnabled, disabled: state.disabled }),
      // Every arrival — this device's storage, another window, a `settings` apply (apply-to-stores rehydrates after its write) — passes through
      // here: unknown fields are dropped, a non-boolean `rowEnabled` keeps the default, ids are strings, duplicates collapse.
      // Nothing stored (`persisted` undefined) keeps memory as it is, as zustand's default merge does.
      merge: (persisted, current) => {
        if (!isRecord(persisted)) return current
        return {
          ...current,
          rowEnabled: typeof persisted.rowEnabled === 'boolean' ? persisted.rowEnabled : true,
          disabled: sanitizeDisabled(persisted.disabled),
        }
      },
    },
  ),
)

syncManager.register(STORAGE_KEYS.QUICK_REPLY_PREFS, useQuickReplyPrefsStore)
