import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'
import type { QuickCommandSlotId } from '../lib/quick-command-slots'
import {
  type QuickCommand,
  type QuickCommandData,
  getBindingTargets,
  mergePersistedQuickCommandState,
} from '../lib/quick-command-bindings'

interface QuickCommandState extends QuickCommandData {
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
