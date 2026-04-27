import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'
import type { QuickCommandSlotId } from '../lib/quick-command-slots'

export interface QuickCommand {
  id: string
  name: string
  command: string
  icon?: string
  category?: string
  hostOnly?: boolean
}

export type Bindings = Record<string /* commandId */, QuickCommandSlotId[]>

interface QuickCommandState {
  global: QuickCommand[]
  byHost: Record<string, QuickCommand[]>
  bindings: Bindings

  addCommand: (cmd: QuickCommand, hostId?: string) => void
  updateCommand: (id: string, patch: Partial<QuickCommand>, hostId?: string) => void
  removeCommand: (id: string, hostId?: string) => void
  getCommands: (hostId: string) => QuickCommand[]

  setBinding: (commandId: string, targets: QuickCommandSlotId[]) => void
  // hostId === null: workspace caller before HostPicker resolves a host;
  // renderer uses state.global as capability order (spec §4.4).
  // hostId !== null: per-host capability list from getCommands.
  getBoundCommands: (mountTarget: QuickCommandSlotId, hostId: string | null) => QuickCommand[]
}

// v2: do NOT pre-seed defaults. Existing users who hydrated v1 defaults
// (`start-cc` / `start-codex`) keep them but bindings = {} so nothing renders
// until the user mounts them via Settings.
const DEFAULT_COMMANDS: QuickCommand[] = []

const UNSAFE_KEYS = new Set(['__proto__', 'constructor', 'prototype'])

/**
 * AR-1 (mirrors `useModuleEnabledStore` codex review #617):
 * Sanitize bindings on rehydrate / sync deserialize. A corrupted payload —
 * hand-edited localStorage, failed migration, hostile sync source — must
 * NEVER let arbitrary string keys silently mount commands into slots.
 *
 * Rules (spec §2.3 — forward-compat with future slot ids):
 *  - Top-level value must be a plain object (rejects arrays, null, primitives).
 *  - Reject `__proto__` / `constructor` / `prototype` command id keys
 *    (prototype pollution).
 *  - Drop entries whose value is not an array.
 *  - Within each array, keep only non-empty strings. NO slot id whitelist —
 *    spec §2.3 explicitly requires accepting unknown slot ids in Phase 1
 *    so cross-version sync (older client pulls newer client's future slot
 *    binding) doesn't lose data; SlotHost simply ignores unknown slot ids
 *    at render time.
 *  - Drop entries whose cleaned array is empty (matches setBinding(id, [])).
 */
export function sanitizeBindings(raw: unknown): Bindings {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return {}
  const out: Bindings = {}
  for (const [cmdId, targets] of Object.entries(raw as Record<string, unknown>)) {
    if (typeof cmdId !== 'string' || cmdId.length === 0) continue
    if (UNSAFE_KEYS.has(cmdId)) continue
    if (!Array.isArray(targets)) continue
    const cleaned: QuickCommandSlotId[] = []
    for (const t of targets) {
      if (typeof t === 'string' && t.length > 0) cleaned.push(t as QuickCommandSlotId)
    }
    if (cleaned.length > 0) out[cmdId] = cleaned
  }
  return out
}

/**
 * Read bindings[cmdId] safely. Prevents inherited-prop attacks where cmdId
 * happens to match Object.prototype methods (toString / valueOf / hasOwnProperty
 * / isPrototypeOf / etc.) — naive `bindings[cmdId]` would resolve to a
 * non-array function and `.includes(slot)` would throw, crashing
 * getBoundCommands for every caller (DoS).
 *
 * Returns undefined unless `bindings` has its OWN property `cmdId` AND that
 * value is an array.
 */
function getBindingTargets(bindings: Bindings, cmdId: string): QuickCommandSlotId[] | undefined {
  if (!Object.prototype.hasOwnProperty.call(bindings, cmdId)) return undefined
  const targets = bindings[cmdId]
  return Array.isArray(targets) ? targets : undefined
}

/**
 * Pure merge function — used by zustand persist `merge` hook AND directly by
 * tests to drive the real hydrate trust boundary with malformed payloads.
 * Exporting this avoids the previous test pattern of using setState as a
 * mock for hydrate, which never actually went through sanitizer.
 */
export function mergePersistedQuickCommandState(
  persisted: unknown,
  current: QuickCommandState,
): QuickCommandState {
  const p = persisted as
    | { global?: unknown; byHost?: unknown; bindings?: unknown }
    | null
    | undefined
  return {
    ...current,
    global: Array.isArray(p?.global) ? (p?.global as QuickCommand[]) : current.global,
    byHost:
      typeof p?.byHost === 'object' && p?.byHost !== null && !Array.isArray(p?.byHost)
        ? (p?.byHost as Record<string, QuickCommand[]>)
        : current.byHost,
    bindings: sanitizeBindings(p?.bindings),
  }
}

export const useQuickCommandStore = create<QuickCommandState>()(
  persist(
    (set, get) => ({
      global: DEFAULT_COMMANDS,
      byHost: {},
      bindings: {},

      addCommand: (cmd, hostId) =>
        set((state) => {
          if (hostId) {
            const hostCmds = [...(state.byHost[hostId] ?? []).filter((c) => c.id !== cmd.id), cmd]
            return { byHost: { ...state.byHost, [hostId]: hostCmds } }
          }
          // global add: replace if id exists, else append
          const existingIdx = state.global.findIndex((c) => c.id === cmd.id)
          if (existingIdx >= 0) {
            const next = [...state.global]
            next[existingIdx] = cmd
            return { global: next }
          }
          return { global: [...state.global, cmd] }
        }),

      updateCommand: (id, patch, hostId) =>
        set((state) => {
          const update = (cmds: QuickCommand[]) =>
            cmds.map((c) => (c.id === id ? { ...c, ...patch } : c))
          if (hostId) {
            const hostCmds = update(state.byHost[hostId] ?? [])
            return { byHost: { ...state.byHost, [hostId]: hostCmds } }
          }
          return { global: update(state.global) }
        }),

      removeCommand: (id, hostId) =>
        set((state) => {
          if (hostId) {
            const hostCmds = (state.byHost[hostId] ?? []).filter((c) => c.id !== id)
            // per-host removal does NOT touch bindings (binding is a global concept)
            return { byHost: { ...state.byHost, [hostId]: hostCmds } }
          }
          // global removal — also drop the binding entry to avoid dangling refs.
          // getBoundCommands also filters at read-time as a defense-in-depth net.
          const nextBindings = { ...state.bindings }
          delete nextBindings[id]
          return {
            global: state.global.filter((c) => c.id !== id),
            bindings: nextBindings,
          }
        }),

      getCommands: (hostId) => {
        const { global, byHost } = get()
        const hostCmds = byHost[hostId] ?? []
        if (hostCmds.length === 0) return global

        const merged = [
          ...global.map((g) => {
            const override = hostCmds.find((h) => h.id === g.id)
            return override ?? g
          }),
          ...hostCmds.filter((h) => !global.some((g) => g.id === h.id)),
        ]
        return merged
      },

      setBinding: (commandId, targets) =>
        set((state) => {
          const nextBindings = { ...state.bindings }
          if (targets.length === 0) {
            delete nextBindings[commandId]
          } else {
            nextBindings[commandId] = [...targets]
          }
          return { bindings: nextBindings }
        }),

      getBoundCommands: (mountTarget, hostId) => {
        // hostId === null: spec §4.4 — workspace caller before HostPicker
        // resolved a host; render uses state.global as capability order so
        // chip list isn't re-sorted when picker eventually picks a host.
        // Non-null: per-host capability order from getCommands.
        // Iterate the capability list (stable, sync-safe order) — NOT
        // Object.keys(bindings) which has unpredictable post-sync order.
        // Dangling binding entries (commandId without a matching capability)
        // are skipped here as well as cleared at removeCommand.
        const cmds = hostId === null ? get().global : get().getCommands(hostId)
        const { bindings } = get()
        return cmds.filter((c) => {
          const targets = getBindingTargets(bindings, c.id)
          return targets !== undefined && targets.includes(mountTarget)
        })
      },
    }),
    {
      name: STORAGE_KEYS.QUICK_COMMANDS,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({
        global: state.global,
        byHost: state.byHost,
        bindings: state.bindings,
      }),
      merge: (persisted, current) =>
        mergePersistedQuickCommandState(persisted, current as QuickCommandState),
    },
  ),
)

syncManager.register(STORAGE_KEYS.QUICK_COMMANDS, useQuickCommandStore)
