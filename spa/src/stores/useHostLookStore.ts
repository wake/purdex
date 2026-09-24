// spa/src/stores/useHostLookStore.ts — the workbench's host looks (host ownership spec §4.1; plan H2c-1).
//
// `{ looks: { [wireId]: { name?, colors?, color?, icon?, iconWeight? } } }`. The keys are WIRE ids in the store
// itself (`d1_…` for a host whose daemon is known, else that host's local id) — the builder and the applier pass
// them through verbatim, with no local↔wire mapping (spec §4.1); which local host a key means is decided by the
// reader (the selector, H2c-2). So to this module a key is an opaque string: it imports no store, and it keeps
// a key no host of this device claims (another device's daemon, a legacy local id) exactly as it arrived.
//
// Projected in `settings` (`purdex-host-looks.looks`, PROJECTIONS.settings) and registered with `syncManager`.
// Option A (plan §0.5): no `null` tombstone — an entry's absent field IS the value (no colour / default icon).
//
// H2c-1 only creates, persists, syncs and applies the store; nothing reads it yet (the selector switches in H2c-2).
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'
import { sanitizeHostConfig } from '../lib/host-color'
// Type-only: erased at runtime, so importing this module never loads `useHostStore`.
import type { HostConfig } from './useHostStore'
import type { IconWeight } from '../types/tab'

/** One host's look in this workbench. Every field optional; absent = none / default (option A). */
export interface HostLookEntry {
  name?: string
  colors?: HostConfig['colors']
  /** Legacy single colour, as on `HostConfig` (read-only there, spec D10). */
  color?: string
  icon?: string
  iconWeight?: IconWeight
}

/** A re-key step: the entry under `from` moves to `to` (H2c-2's pass). */
export type HostLookMove = readonly [from: string, to: string]

interface HostLookState {
  looks: Record<string, HostLookEntry>
  /** Replaces the entry under `key` with a sanitised copy of `entry`. */
  putLook: (key: string, entry: HostLookEntry) => void
  /** `fn(current)` → the new entry (sanitised); `undefined` removes it; the same object back writes nothing. */
  patchLook: (key: string, fn: (current: HostLookEntry | undefined) => HostLookEntry | undefined) => void
  /** Writes each entry whose key has none yet (skip-if-present); one write, none when nothing is absent. */
  putLooksIfAbsent: (entries: Record<string, HostLookEntry>) => void
  /** Moves `from` → `to` per step: an existing `to` wins and `from` is dropped; a missing `from` is a no-op. */
  rekey: (moves: readonly HostLookMove[]) => void
}

// Assigning `obj['__proto__'] = v` rewrites the prototype instead of adding a key — never copied.
const FORBIDDEN_KEY = '__proto__'
const LOOK_FIELDS = ['colors', 'color', 'icon', 'iconWeight'] as const

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/**
 * The five look fields of an untrusted entry, each validated as `HostConfig`'s own are (`sanitizeHostConfig`:
 * `#rrggbb`, the `colors` set shape, a catalog icon, a known weight); a non-string name and every other field
 * dropped; `null` dropped on every field (option A has no tombstone). `null` for a value that is not a record.
 */
export function sanitizeHostLookEntry(raw: unknown): HostLookEntry | null {
  if (!isRecord(raw)) return null
  const picked: Record<string, unknown> = {}
  if (typeof raw.name === 'string') picked.name = raw.name
  for (const field of LOOK_FIELDS) {
    if (Object.hasOwn(raw, field) && raw[field] !== undefined) picked[field] = raw[field]
  }
  // `sanitizeHostConfig` judges present keys only; none of the host-only ones (daemonId, syncAliases) is present.
  return sanitizeHostConfig(picked as unknown as HostConfig) as HostLookEntry
}

/** The whole record, sanitised: own keys verbatim but `__proto__`; an entry that is not a record dropped. */
export function sanitizeHostLooks(raw: unknown): Record<string, HostLookEntry> {
  const out: Record<string, HostLookEntry> = {}
  if (!isRecord(raw)) return out
  for (const key of Object.keys(raw)) {
    if (key === FORBIDDEN_KEY) continue
    const entry = sanitizeHostLookEntry(raw[key])
    if (entry !== null) out[key] = entry
  }
  return out
}

export const useHostLookStore = create<HostLookState>()(
  persist(
    (set) => ({
      looks: {},

      putLook: (key, entry) =>
        set((state) => {
          const clean = sanitizeHostLookEntry(entry)
          if (key === FORBIDDEN_KEY || clean === null) return state
          return { looks: { ...state.looks, [key]: clean } }
        }),

      patchLook: (key, fn) =>
        set((state) => {
          if (key === FORBIDDEN_KEY) return state
          const current = Object.hasOwn(state.looks, key) ? state.looks[key] : undefined
          const next = fn(current)
          if (next === current) return state
          if (next === undefined) {
            const { [key]: _gone, ...rest } = state.looks
            void _gone
            return { looks: rest }
          }
          const clean = sanitizeHostLookEntry(next)
          if (clean === null) return state
          return { looks: { ...state.looks, [key]: clean } }
        }),

      putLooksIfAbsent: (entries) =>
        set((state) => {
          let looks: Record<string, HostLookEntry> | null = null
          for (const key of Object.keys(entries)) {
            if (key === FORBIDDEN_KEY || Object.hasOwn(state.looks, key)) continue
            const clean = sanitizeHostLookEntry(entries[key])
            if (clean === null) continue
            looks ??= { ...state.looks }
            looks[key] = clean
          }
          return looks === null ? state : { looks }
        }),

      rekey: (moves) =>
        set((state) => {
          let looks: Record<string, HostLookEntry> | null = null
          for (const [from, to] of moves) {
            const cur = looks ?? state.looks
            if (from === to || to === FORBIDDEN_KEY || !Object.hasOwn(cur, from)) continue
            looks ??= { ...state.looks }
            if (!Object.hasOwn(looks, to)) looks[to] = looks[from]
            delete looks[from]
          }
          return looks === null ? state : { looks }
        }),
    }),
    {
      name: STORAGE_KEYS.HOST_LOOKS,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({ looks: state.looks }),
      // Every arrival — this device's storage, another window, a `settings` apply (apply-to-stores rehydrates after
      // its write) — passes through here: the record is rebuilt from sanitised entries.
      // Nothing stored (`persisted` undefined) keeps memory as it is, as zustand's default merge does.
      merge: (persisted, current) => (isRecord(persisted) ? { ...current, looks: sanitizeHostLooks(persisted.looks) } : current),
    },
  ),
)

syncManager.register(STORAGE_KEYS.HOST_LOOKS, useHostLookStore)
