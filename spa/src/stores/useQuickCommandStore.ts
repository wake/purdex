import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'
import { QUICK_COMMAND_SLOTS, type QuickCommandSlotId } from '../lib/quick-command-slots'

const VALID_SLOT_IDS = new Set<string>(Object.values(QUICK_COMMAND_SLOTS))

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
  getBoundCommands: (mountTarget: QuickCommandSlotId, hostId: string) => QuickCommand[]
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
 * Rules:
 *  - Top-level value must be a plain object (rejects arrays, null, primitives).
 *  - Reject `__proto__` / `constructor` / `prototype` keys (prototype pollution).
 *  - Drop entries whose value is not an array.
 *  - Within each array, keep only strings present in `QUICK_COMMAND_SLOTS`
 *    (whitelist; rejects typos like `'workspace.action'` and forward-incompat
 *    slot ids — if a future version adds slots, sanitizer ships in that PR).
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
      if (typeof t === 'string' && VALID_SLOT_IDS.has(t)) cleaned.push(t as QuickCommandSlotId)
    }
    if (cleaned.length > 0) out[cmdId] = cleaned
  }
  return out
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
        // Iterate the capability list (stable, sync-safe order) — NOT
        // Object.keys(bindings) which has unpredictable post-sync order
        // (spec §4.4). Dangling binding entries (commandId without a matching
        // capability) are skipped here as well as cleared at removeCommand.
        const cmds = get().getCommands(hostId)
        const { bindings } = get()
        return cmds.filter((c) => bindings[c.id]?.includes(mountTarget))
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
      merge: (persisted, current) => {
        // AR-1: sanitize hydrated bindings BEFORE first read. global / byHost
        // come from a known-shape v1 payload — keep as-is. Only bindings is
        // newly added in v2 and must be clamped against hostile payloads.
        const p = persisted as { global?: unknown; byHost?: unknown; bindings?: unknown } | null | undefined
        return {
          ...current,
          global: Array.isArray(p?.global) ? (p?.global as QuickCommand[]) : current.global,
          byHost: typeof p?.byHost === 'object' && p?.byHost !== null && !Array.isArray(p?.byHost)
            ? (p?.byHost as Record<string, QuickCommand[]>)
            : current.byHost,
          bindings: sanitizeBindings(p?.bindings),
        }
      },
    },
  ),
)

syncManager.register(STORAGE_KEYS.QUICK_COMMANDS, useQuickCommandStore)
